/*
 * Copyright (C) 2017 Genymobile
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package relay

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"time"
)

const tcpConnTag = "TCPConnection"

// MTU same value as GnirehtetService.MTU in the client
const MTU = 0x4000

// MaxPayloadSize is 20 bytes for IP headers, 20 bytes for TCP headers
const MaxPayloadSize = MTU - 20 - 20

// TCPState represents the TCP connection state
type TCPState int

const (
	StateSynSent TCPState = iota
	StateSynReceived
	StateEstablished
	StateCloseWait
	StateLastAck
	StateClosing
	StateFinWait1
	StateFinWait2
)

func (s TCPState) String() string {
	switch s {
	case StateSynSent:
		return "SYN_SENT"
	case StateSynReceived:
		return "SYN_RECEIVED"
	case StateEstablished:
		return "ESTABLISHED"
	case StateCloseWait:
		return "CLOSE_WAIT"
	case StateLastAck:
		return "LAST_ACK"
	case StateClosing:
		return "CLOSING"
	case StateFinWait1:
		return "FIN_WAIT_1"
	case StateFinWait2:
		return "FIN_WAIT_2"
	default:
		return "UNKNOWN"
	}
}

func (s TCPState) IsConnected() bool {
	return s != StateSynSent && s != StateSynReceived
}

func (s TCPState) IsClosed() bool {
	return s == StateFinWait1 || s == StateFinWait2 || s == StateClosing || s == StateLastAck
}

// TCPConnection represents a TCP connection (lock-free version using channels)
type TCPConnection struct {
	*BaseConnection
	clientToNetwork *StreamBuffer
	networkToClient *Packetizer
	packetForClient *IPv4Packet

	conn net.Conn

	// State machine state (only accessed in stateLoop, no lock needed)
	state                      *TCPState
	synSequenceNumber          uint32
	sequenceNumber             uint32
	acknowledgementNumber      uint32
	theirAcknowledgementNumber uint32
	finSequenceNumber          *uint32
	finReceived                bool
	clientWindow               int

	// Channels for lock-free communication
	closeChan         chan struct{}
	packetChan        chan *IPv4Packet      // packets from client
	readDataChan      chan []byte           // data read from network
	readErrorChan     chan error            // read errors
	writeRequestChan  chan chan struct{}    // request to write data
	writeResultChan   chan int              // bytes written
	writeErrorChan    chan error            // write errors
	getPacketChan     chan chan *IPv4Packet // request current packet
	nextPacketChan    chan struct{}         // advance to next packet
	canReadQueryChan  chan chan bool        // query if can read
	connectResultChan chan error            // connection establishment result

	// Context for canceling connection attempts
	connectCtx    context.Context
	connectCancel context.CancelFunc
}

// NewTCPConnection creates a new TCP connection
func NewTCPConnection(id *ConnectionId, client *Client, ipv4Header *IPv4Header, tcpHeader *TCPHeader) (*TCPConnection, error) {
	shrinkedTcpHeader := tcpHeader.Copy()
	shrinkedTcpHeader.ShrinkOptions()

	c := &TCPConnection{
		BaseConnection:    NewBaseConnection(id, client),
		clientToNetwork:   NewStreamBuffer(4 * MaxPacketLength),
		closeChan:         make(chan struct{}),
		packetChan:        make(chan *IPv4Packet, 100),
		readDataChan:      make(chan []byte, 10),
		readErrorChan:     make(chan error, 1),
		writeRequestChan:  make(chan chan struct{}, 1),
		writeResultChan:   make(chan int, 1),
		writeErrorChan:    make(chan error, 1),
		getPacketChan:     make(chan chan *IPv4Packet, 1),
		nextPacketChan:    make(chan struct{}, 1),
		canReadQueryChan:  make(chan chan bool, 1),
		connectResultChan: make(chan error, 1),
	}
	// Initialize context (will be replaced when connection attempt starts)
	c.connectCtx, c.connectCancel = context.WithCancel(context.Background())

	c.networkToClient = NewPacketizer(ipv4Header, shrinkedTcpHeader)
	c.networkToClient.GetResponseIPv4Header().SwapSourceAndDestination()
	c.networkToClient.GetResponseTransportHeader().SwapSourceAndDestination()

	// Start state machine loop (serializes all state operations)
	go c.stateLoop()

	// Start connection establishment immediately (like Java/Rust versions)
	// This ensures connection is ready when first SYN packet arrives
	c.createChannelAsync()

	// I/O loops will be started after connection is established
	// readLoop and writeLoop will check if conn is nil

	return c, nil
}

func (c *TCPConnection) createChannel(ctx context.Context) (net.Conn, error) {
	c.LogiSimple(tcpConnTag, "Open")
	dest := c.GetRewrittenDestination()
	// Use DialContext with context for cancellable connection attempts
	// This allows immediate cancellation when connection is closed, matching Java's non-blocking behavior
	dialer := &net.Dialer{
		Timeout: 30 * time.Second, // Maximum timeout, but can be cancelled earlier
	}
	return dialer.DialContext(ctx, "tcp", dest)
}

// createChannelAsync establishes connection in a goroutine (non-blocking)
func (c *TCPConnection) createChannelAsync() {
	// Create a new context for this connection attempt (cancellable)
	ctx, cancel := context.WithCancel(context.Background())

	// Store cancel function so we can cancel it when connection closes
	// Use a channel to safely update connectCancel from stateLoop
	select {
	case <-c.closeChan:
		cancel()
		return
	default:
		// Cancel previous connection attempt if any
		if c.connectCancel != nil {
			c.connectCancel()
		}
		c.connectCtx = ctx
		c.connectCancel = cancel
	}

	go func() {
		defer cancel()

		conn, err := c.createChannel(ctx)
		if err != nil {
			// Check if error is due to context cancellation
			if ctx.Err() == context.Canceled {
				// Connection was cancelled, don't send error
				return
			}
			select {
			case c.connectResultChan <- err:
			case <-c.closeChan:
			}
			return
		}

		// Connection established successfully
		c.conn = conn

		// Start I/O loops now that connection is established
		go c.readLoop()
		go c.writeLoop()

		// Signal success (nil error)
		select {
		case c.connectResultChan <- nil:
		case <-c.closeChan:
		}
	}()
}

// Disconnect closes the connection
func (c *TCPConnection) Disconnect() {
	select {
	case <-c.closeChan:
		// already closed
		return
	default:
		close(c.closeChan)
		c.LogiSimple(tcpConnTag, "Close")
		// Cancel any ongoing connection attempt
		if c.connectCancel != nil {
			c.connectCancel()
		}
		if c.conn != nil {
			_ = c.conn.Close()
		}
	}
}

// IsExpired returns false - TCP connections don't expire externally
func (c *TCPConnection) IsExpired() bool {
	return false
}

// SendToNetwork handles incoming packets from the client (lock-free)
func (c *TCPConnection) SendToNetwork(packet *IPv4Packet) {
	select {
	case c.packetChan <- packet:
		// packet sent to state machine
	case <-c.closeChan:
		// connection closed
		return
	default:
		// channel full, drop packet
		c.LogwSimple(tcpConnTag, "Packet channel full, dropping packet")
	}
}

// stateLoop serializes all state machine operations (lock-free)
func (c *TCPConnection) stateLoop() {
	for {
		select {
		case <-c.closeChan:
			return

		case packet := <-c.packetChan:
			// Handle packet from client
			response, mustClose := c.handlePacket(packet)
			if response != nil {
				c.SendToClient(response)
			}
			if mustClose {
				c.closeConnection()
				return
			}
			c.LogdSimple(tcpConnTag, fmt.Sprintf("current ack=%d", c.acknowledgementNumber))

		case data := <-c.readDataChan:
			// Handle data read from network
			c.processReceiveData(data)

		case err := <-c.readErrorChan:
			// Handle read error
			var response *IPv4Packet
			var mustClose bool
			if errors.Is(err, io.EOF) {
				response = c.eof()
			} else {
				response, mustClose = c.resetConnection()
			}
			if response != nil {
				c.SendToClient(response)
			}
			if mustClose {
				c.closeConnection()
				return
			}

		case err := <-c.connectResultChan:
			// Handle connection establishment result
			if err != nil {
				// Connection failed, reset connection
				c.LogeSimple(tcpConnTag, "Cannot finish connect")
				response, mustClose := c.resetConnection()
				if response != nil {
					c.SendToClient(response)
				}
				if mustClose {
					c.closeConnection()
					return
				}
			} else {
				// Connection established successfully
				c.LogdSimple(tcpConnTag, "Network connection established")
				// Send SYN-ACK if state is already initialized (handleFirstPacket was processed)
				// If state is not initialized yet, handleFirstPacket will check and send SYN-ACK
				if c.state != nil && *c.state == StateSynSent {
					response := c.processConnect()
					if response != nil {
						c.SendToClient(response)
					}
				}
				// If state is nil, handleFirstPacket will check c.conn and send SYN-ACK
			}

		case writeReq := <-c.writeRequestChan:
			// Handle write request - write data from buffer to network
			if c.conn == nil {
				// Connection not established yet, signal empty
				select {
				case writeReq <- struct{}{}:
				case <-c.closeChan:
					return
				}
				continue
			}

			if c.clientToNetwork.IsEmpty() {
				// No data, signal empty
				select {
				case writeReq <- struct{}{}:
				case <-c.closeChan:
					return
				}
				continue
			}

			// Write data from buffer to network (in stateLoop to avoid race)
			c.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			n64, err := c.clientToNetwork.WriteTo(c.conn)
			n := int(n64)

			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Timeout, signal to retry
					select {
					case writeReq <- struct{}{}:
					case <-c.closeChan:
						return
					}
					continue
				}
				// Other error, send to error channel
				select {
				case c.writeErrorChan <- err:
				case <-c.closeChan:
					return
				}
				// Signal write loop to stop
				select {
				case writeReq <- struct{}{}:
				case <-c.closeChan:
					return
				}
				continue
			}

			// Signal success with bytes written
			select {
			case writeReq <- struct{}{}:
			case <-c.closeChan:
				return
			}

			// Update acknowledgementNumber immediately after write (like Java/Rust)
			// This ensures expectedPacket calculation is correct for next packet
			if n > 0 {
				c.acknowledgementNumber += uint32(n)
				c.LogdSimple(tcpConnTag, fmt.Sprintf("%d bytes written to the network socket", n))

				var response *IPv4Packet
				var mustClose bool
				if c.finReceived && c.clientToNetwork.IsEmpty() {
					c.LogdSimple(tcpConnTag, "No more pending data, process the pending FIN")
					response, mustClose = c.doHandleFin()
				} else {
					c.LogdSimple(tcpConnTag, fmt.Sprintf("Sending ACK %s to client", c.numbers()))
					response = c.createEmptyResponsePacket(TCPFlagACK)
				}
				if response != nil {
					c.SendToClient(response)
				}
				if mustClose {
					c.closeConnection()
					return
				}
			}

		case n := <-c.writeResultChan:
			// This channel is no longer used - acknowledgementNumber is updated immediately after write
			// Keep this case to avoid blocking if any code still sends to this channel
			_ = n

		case err := <-c.writeErrorChan:
			// Handle write error
			var rst *IPv4Packet
			var mustClose bool
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				// Retry later - notify write loop
				continue
			} else {
				rst, mustClose = c.resetConnection()
			}
			if rst != nil {
				c.SendToClient(rst)
			}
			if mustClose {
				c.closeConnection()
				return
			}

		case req := <-c.getPacketChan:
			// Handle Get() request (PacketSource interface)
			if c.packetForClient != nil {
				// Update ACK number
				tcpHeader := c.packetForClient.GetTransportHeader().(*TCPHeader)
				tcpHeader.SetAcknowledgementNumber(c.acknowledgementNumber)
				c.packetForClient.ComputeChecksums()
			}
			req <- c.packetForClient

		case <-c.nextPacketChan:
			// Handle Next() request (PacketSource interface)
			if c.packetForClient != nil {
				payloadLen := c.packetForClient.GetPayloadLength()
				c.sequenceNumber += uint32(payloadLen)
				numbers := fmt.Sprintf("(seq=%d, ack=%d)", c.sequenceNumber-uint32(payloadLen), c.acknowledgementNumber)
				c.LogdSimple(tcpConnTag, fmt.Sprintf("Packet (%d bytes) sent to client %s", payloadLen, numbers))
				if IsVerboseEnabled() {
					c.LogvSimple(tcpConnTag, BuildPacketString(c.packetForClient.GetRaw()))
				}
				c.packetForClient = nil
			}

		case req := <-c.canReadQueryChan:
			// Handle can read query
			canRead := c.mayRead()
			select {
			case req <- canRead:
			case <-c.closeChan:
				return
			}
		}
	}
}

// mayRead checks if we can read from network (similar to Rust's may_read)
func (c *TCPConnection) mayRead() bool {
	if c.state == nil {
		return false
	}
	if !c.state.IsConnected() || c.state.IsClosed() {
		return false
	}
	if c.packetForClient != nil {
		// a packet is already pending
		return false
	}
	return c.getRemainingClientWindow() > 0
}

// tempWriter is a helper to capture written data
type tempWriter struct {
	buf []byte
	pos int
}

func (tw *tempWriter) Write(p []byte) (int, error) {
	n := copy(tw.buf[tw.pos:], p)
	tw.pos += n
	return n, nil
}

func (c *TCPConnection) handlePacket(packet *IPv4Packet) (*IPv4Packet, bool) {
	tcpHeader := packet.GetTransportHeader().(*TCPHeader)

	if c.state == nil {
		return c.handleFirstPacket(packet)
	}

	if tcpHeader.IsSyn() {
		return c.handleDuplicateSyn(packet)
	}

	// In SYN_SENT state, we haven't sent SYN-ACK yet, so ignore non-SYN packets
	// (client may send ACK packets while waiting, but we can't process them yet)
	if c.state != nil && *c.state == StateSynSent {
		c.LogdSimple(tcpConnTag, fmt.Sprintf("Ignoring packet in SYN_SENT state (seq=%d, ack=%d, flags=%d); waiting for connection to establish",
			tcpHeader.GetSequenceNumber(), tcpHeader.GetAcknowledgementNumber(), tcpHeader.GetFlags()))
		return nil, false
	}

	packetSequenceNumber := tcpHeader.GetSequenceNumber()
	expectedPacket := c.acknowledgementNumber + uint32(c.clientToNetwork.Size())

	// In SYN_RECEIVED state, allow the first ACK packet (confirming SYN-ACK) even if sequence number is off by 1
	// This can happen if client sent ACK before connection was fully established
	// The ACK should have seq = synSequenceNumber + 1, but client may send seq = synSequenceNumber
	if c.state != nil && *c.state == StateSynReceived &&
		tcpHeader.IsAck() && packet.GetPayloadLength() == 0 &&
		packetSequenceNumber == c.synSequenceNumber && expectedPacket == c.acknowledgementNumber {
		// This is the ACK for SYN-ACK, accept it even though sequence number is synSequenceNumber
		// (should be synSequenceNumber + 1, but client may have sent it before connection was established)
		c.LogdSimple(tcpConnTag, fmt.Sprintf("Accepting ACK for SYN-ACK (seq=%d, ack=%d, flags=%d); expected seq=%d",
			packetSequenceNumber, tcpHeader.GetAcknowledgementNumber(), tcpHeader.GetFlags(), expectedPacket))
		// Don't update acknowledgementNumber - it's already correct (synSequenceNumber + 1)
		// Just accept this packet and continue
	} else if packetSequenceNumber != expectedPacket {
		c.LogwSimple(tcpConnTag, fmt.Sprintf("Ignoring packet %d (acking %d); expecting %d; flags=%d",
			packetSequenceNumber, tcpHeader.GetAcknowledgementNumber(), expectedPacket, tcpHeader.GetFlags()))
		return nil, false
	}

	c.clientWindow = tcpHeader.GetWindow()
	c.theirAcknowledgementNumber = tcpHeader.GetAcknowledgementNumber()

	c.LogdSimple(tcpConnTag, fmt.Sprintf("Receiving expected packet %d (flags = %d)", packetSequenceNumber, tcpHeader.GetFlags()))

	if tcpHeader.IsRst() {
		c.LogdSimple(tcpConnTag, "Reset requested, closing")
		return nil, true
	}

	var response *IPv4Packet
	var mustClose bool

	if tcpHeader.IsAck() {
		c.LogdSimple(tcpConnTag, fmt.Sprintf("Client acked %d", tcpHeader.GetAcknowledgementNumber()))
		c.handleAck(packet)
	}

	if tcpHeader.IsFin() {
		response, mustClose = c.handleFin()
	}

	if c.finSequenceNumber != nil && tcpHeader.GetAcknowledgementNumber() == *c.finSequenceNumber+1 {
		c.LogdSimple(tcpConnTag, "Received ACK of FIN")
		if c.handleFinAck() {
			mustClose = true
		}
	}

	return response, mustClose
}

func (c *TCPConnection) handleFirstPacket(packet *IPv4Packet) (*IPv4Packet, bool) {
	c.LogdSimple(tcpConnTag, "handleFirstPacket()")
	tcpHeader := packet.GetTransportHeader().(*TCPHeader)

	if !tcpHeader.IsSyn() {
		c.LogwSimple(tcpConnTag, fmt.Sprintf("Unexpected first packet %d; acking %d; flags=%d",
			tcpHeader.GetSequenceNumber(), tcpHeader.GetAcknowledgementNumber(), tcpHeader.GetFlags()))
		c.sequenceNumber = tcpHeader.GetAcknowledgementNumber()
		return c.resetConnection()
	}

	theirSequenceNumber := tcpHeader.GetSequenceNumber()
	c.acknowledgementNumber = theirSequenceNumber + 1
	c.synSequenceNumber = theirSequenceNumber

	c.sequenceNumber = rand.Uint32()
	c.LogdSimple(tcpConnTag, fmt.Sprintf("initialized seqNum=%d; ackNum=%d", c.sequenceNumber, c.acknowledgementNumber))
	c.clientWindow = tcpHeader.GetWindow()
	state := StateSynSent
	c.state = &state
	c.LogdSimple(tcpConnTag, fmt.Sprintf("State = %s", state))

	// Connection establishment was already started in NewTCPConnection()
	// (like Java/Rust versions which start connection in constructor)
	// Check if connection is already established, if so, send SYN-ACK immediately
	if c.conn != nil {
		// Connection already established, send SYN-ACK now
		response := c.processConnect()
		return response, false
	}
	// Connection not established yet, it will be sent when connection is established
	// (in connectResultChan handler)
	return nil, false
}

func (c *TCPConnection) handleDuplicateSyn(packet *IPv4Packet) (*IPv4Packet, bool) {
	tcpHeader := packet.GetTransportHeader().(*TCPHeader)
	theirSequenceNumber := tcpHeader.GetSequenceNumber()

	if c.state != nil && *c.state == StateSynSent {
		c.synSequenceNumber = theirSequenceNumber
		c.acknowledgementNumber = theirSequenceNumber + 1
		return nil, false
	}
	if theirSequenceNumber != c.synSequenceNumber {
		return c.resetConnection()
	}
	return nil, false
}

func (c *TCPConnection) handleFin() (*IPv4Packet, bool) {
	c.LogdSimple(tcpConnTag, fmt.Sprintf("Received a FIN from the client %s", c.numbers()))
	c.finReceived = true
	if c.clientToNetwork.IsEmpty() {
		c.LogdSimple(tcpConnTag, "No pending data, process the FIN immediately")
		return c.doHandleFin()
	}
	return nil, false
}

func (c *TCPConnection) doHandleFin() (*IPv4Packet, bool) {
	c.acknowledgementNumber++

	var response *IPv4Packet
	var mustClose bool
	switch *c.state {
	case StateEstablished:
		response = c.createEmptyResponsePacket(TCPFlagFIN | TCPFlagACK)
		fin := c.sequenceNumber
		c.finSequenceNumber = &fin
		c.sequenceNumber++
		state := StateLastAck
		c.state = &state
		c.LogdSimple(tcpConnTag, fmt.Sprintf("State = %s", state))
	case StateFinWait1:
		response = c.createEmptyResponsePacket(TCPFlagACK)
		state := StateClosing
		c.state = &state
		c.LogdSimple(tcpConnTag, fmt.Sprintf("State = %s", state))
	case StateFinWait2:
		response = c.createEmptyResponsePacket(TCPFlagACK)
		mustClose = true
	default:
		c.LogwSimple(tcpConnTag, fmt.Sprintf("Received FIN while state was %s", c.state))
	}
	return response, mustClose
}

func (c *TCPConnection) handleFinAck() bool {
	switch *c.state {
	case StateLastAck, StateClosing:
		return true
	case StateFinWait1:
		state := StateFinWait2
		c.state = &state
		c.LogdSimple(tcpConnTag, fmt.Sprintf("State = %s", state))
	case StateFinWait2:
		// do nothing
	default:
		c.LogwSimple(tcpConnTag, fmt.Sprintf("Received FIN ACK while state was %s", c.state))
	}
	return false
}

func (c *TCPConnection) handleAck(packet *IPv4Packet) {
	c.LogdSimple(tcpConnTag, "handleAck()")

	if *c.state == StateSynReceived {
		state := StateEstablished
		c.state = &state
		c.LogdSimple(tcpConnTag, fmt.Sprintf("State = %s", state))
		return
	}

	if IsVerboseEnabled() {
		c.LogvSimple(tcpConnTag, BuildPacketString(packet.GetRaw()))
	}

	payloadLength := packet.GetPayloadLength()
	if payloadLength == 0 {
		return
	}

	if c.clientToNetwork.Remaining() < payloadLength {
		c.LogwSimple(tcpConnTag, "Not enough space, dropping packet")
		return
	}

	c.clientToNetwork.ReadFrom(packet.GetPayload())
	// Notify write loop that there's data to write
	select {
	case writeReq := <-c.writeRequestChan:
		writeReq <- struct{}{} // signal
	default:
		// No one waiting, that's ok
	}
}

func (c *TCPConnection) processConnect() *IPv4Packet {
	c.LogdSimple(tcpConnTag, "processConnect()")
	c.LogdSimple(tcpConnTag, fmt.Sprintf("SYN_RECEIVED, acking %s", c.numbers()))
	state := StateSynReceived
	c.state = &state
	c.LogdSimple(tcpConnTag, fmt.Sprintf("State = %s", state))
	packet := c.createEmptyResponsePacket(TCPFlagSYN | TCPFlagACK)
	c.sequenceNumber++
	return packet
}

func (c *TCPConnection) resetConnection() (*IPv4Packet, bool) {
	c.LogdSimple(tcpConnTag, "Resetting connection")
	c.state = nil
	packet := c.createEmptyResponsePacket(TCPFlagRST)
	return packet, true
}

func (c *TCPConnection) sendEmptyPacketToClient(flags int) {
	packet := c.createEmptyResponsePacket(flags)
	c.SendToClient(packet)
}

func (c *TCPConnection) createEmptyResponsePacket(flags int) *IPv4Packet {
	c.updateHeaders(flags)
	packet := c.networkToClient.PacketizeEmptyPayload()
	c.LogdSimple(tcpConnTag, fmt.Sprintf("Forging empty response (flags=%d) %s", flags, c.numbers()))
	if IsVerboseEnabled() {
		c.LogdSimple(tcpConnTag, BuildPacketString(packet.GetRaw()))
	}
	if (flags & TCPFlagACK) != 0 {
		c.LogdSimple(tcpConnTag, fmt.Sprintf("Acking %s", c.numbers()))
	}
	return packet
}

func (c *TCPConnection) updateHeaders(flags int) {
	tcpHeader := c.networkToClient.GetResponseTransportHeader().(*TCPHeader)
	tcpHeader.SetFlags(flags)
	tcpHeader.SetSequenceNumber(c.sequenceNumber)
	tcpHeader.SetAcknowledgementNumber(c.acknowledgementNumber)
}

func (c *TCPConnection) numbers() string {
	return fmt.Sprintf("(seq=%d, ack=%d)", c.sequenceNumber, c.acknowledgementNumber)
}

func (c *TCPConnection) getRemainingClientWindow() int {
	remaining := int(c.theirAcknowledgementNumber) + c.clientWindow - int(c.sequenceNumber)
	if remaining < 0 || remaining > c.clientWindow {
		return 0
	}
	return remaining
}

func (c *TCPConnection) eof() *IPv4Packet {
	if c.state == nil {
		return nil
	}

	packet := c.createEmptyResponsePacket(TCPFlagFIN | TCPFlagACK)

	fin := c.sequenceNumber
	c.finSequenceNumber = &fin
	c.sequenceNumber++

	if *c.state == StateCloseWait {
		state := StateLastAck
		c.state = &state
	} else {
		state := StateFinWait1
		c.state = &state
	}
	c.LogdSimple(tcpConnTag, fmt.Sprintf("State = %s", *c.state))
	return packet
}

func (c *TCPConnection) closeConnection() {
	c.Disconnect()
	c.client.GetRouter().Remove(c)
}

// readLoop only handles I/O (lock-free)
func (c *TCPConnection) readLoop() {
	buf := make([]byte, MaxPayloadSize)
	for {
		select {
		case <-c.closeChan:
			return
		default:
		}

		// Wait for connection to be established
		if c.conn == nil {
			time.Sleep(10 * time.Millisecond)
			continue
		}

		// Check if we can read (query state machine)
		canReadReq := make(chan bool, 1)
		select {
		case c.canReadQueryChan <- canReadReq:
			canRead := <-canReadReq
			if !canRead {
				time.Sleep(10 * time.Millisecond)
				continue
			}
		case <-c.closeChan:
			return
		}

		c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := c.conn.Read(buf)

		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}

			select {
			case c.readErrorChan <- err:
			case <-c.closeChan:
			}
			return
		}

		if n > 0 {
			// Send data to state machine
			data := make([]byte, n)
			copy(data, buf[:n])
			select {
			case c.readDataChan <- data:
			case <-c.closeChan:
				return
			}
		}
	}
}

// processReceiveData processes data received from network (called in stateLoop)
func (c *TCPConnection) processReceiveData(data []byte) {
	c.updateHeaders(TCPFlagACK | TCPFlagPSH)

	reader := &byteReader{data: data}
	var err error
	packet, err := c.networkToClient.Packetize(reader, len(data))
	if err != nil || packet == nil {
		finPacket := c.eof()
		if finPacket != nil {
			c.SendToClient(finPacket)
		}
		return
	}

	// Update ACK number in the packet
	tcpHeader := packet.GetTransportHeader().(*TCPHeader)
	tcpHeader.SetAcknowledgementNumber(c.acknowledgementNumber)
	packet.ComputeChecksums()

	// Store packet for client
	c.packetForClient = packet

	// Try to send via Consume (this will call Get() which may block, but that's ok)
	// Get() will send a request to stateLoop via channel, and stateLoop will respond
	// Since we're in stateLoop, we need to handle this carefully
	// Actually, we can't call Consume here because it will block on Get()
	// So we'll just store the packet and let the client pull it via Get() when ready
	// The client's processPending will call Get() and Next() when buffer has space
	// But we need to register this as a pending source
	// For now, let's just try to send directly and handle failure
	if !c.client.SendToClient(packet) {
		// Failed, need to register as pending - but Consume will call Get() which blocks
		// So we need a different approach - let's use a goroutine to call Consume
		go func() {
			c.client.Consume(c)
		}()
	} else {
		// Successfully sent, update sequence number
		payloadLen := packet.GetPayloadLength()
		c.sequenceNumber += uint32(payloadLen)
		c.packetForClient = nil
		c.LogdSimple(tcpConnTag, fmt.Sprintf("Packet (%d bytes) sent to client %s", payloadLen, c.numbers()))
		if IsVerboseEnabled() {
			c.LogvSimple(tcpConnTag, BuildPacketString(packet.GetRaw()))
		}
	}
}

// writeLoop only triggers writes (lock-free)
// Actual writing happens in stateLoop to avoid race conditions
func (c *TCPConnection) writeLoop() {
	writeNotify := make(chan struct{}, 1)

	// Wait for connection to be established
	for c.conn == nil {
		select {
		case <-c.closeChan:
			return
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	// Start by requesting a write
	select {
	case c.writeRequestChan <- writeNotify:
	case <-c.closeChan:
		return
	}

	for {
		select {
		case <-c.closeChan:
			return
		case <-writeNotify:
			// Write completed (or no data), wait a bit and request again
			time.Sleep(10 * time.Millisecond)
			// Request another write
			select {
			case c.writeRequestChan <- writeNotify:
			case <-c.closeChan:
				return
			}
		}
	}
}

// Get returns the current packet for the client (PacketSource interface, lock-free)
func (c *TCPConnection) Get() *IPv4Packet {
	req := make(chan *IPv4Packet, 1)
	select {
	case c.getPacketChan <- req:
		return <-req
	case <-c.closeChan:
		return nil
	}
}

// Next advances past the current packet (PacketSource interface, lock-free)
func (c *TCPConnection) Next() {
	select {
	case c.nextPacketChan <- struct{}{}:
	case <-c.closeChan:
	}
}
