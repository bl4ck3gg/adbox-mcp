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
	"net"
	"sync"
	"time"
)

// IdleTimeout is the timeout for UDP connections
const IdleTimeout = 2 * 60 * time.Second

const udpConnTag = "UDPConnection"

// UDPConnection represents a UDP connection
type UDPConnection struct {
	*BaseConnection
	clientToNetwork *DatagramBuffer
	networkToClient *Packetizer
	conn            *net.UDPConn
	idleSince       time.Time

	// Protects clientToNetwork and idleSince.
	mu sync.Mutex

	closeChan   chan struct{}
	sendNotify  chan struct{}
	disconnectOnce sync.Once
	removeOnce     sync.Once
}

// NewUDPConnection creates a new UDP connection
func NewUDPConnection(id *ConnectionId, client *Client, ipv4Header *IPv4Header, udpHeader *UDPHeader) (*UDPConnection, error) {
	c := &UDPConnection{
		BaseConnection:  NewBaseConnection(id, client),
		clientToNetwork: NewDatagramBuffer(4 * MaxPacketLength),
		closeChan:       make(chan struct{}),
		sendNotify:      make(chan struct{}, 1),
	}

	c.networkToClient = NewPacketizer(ipv4Header, udpHeader)
	c.networkToClient.GetResponseIPv4Header().SwapSourceAndDestination()
	c.networkToClient.GetResponseTransportHeader().SwapSourceAndDestination()

	c.touch()

	conn, err := c.createChannel()
	if err != nil {
		return nil, err
	}
	c.conn = conn

	// Start goroutines for reading and writing
	go c.readLoop()
	go c.writeLoop()

	return c, nil
}

func (c *UDPConnection) createChannel() (*net.UDPConn, error) {
	c.LogiSimple(udpConnTag, "Open")
	dest := c.GetRewrittenDestination()
	addr, err := net.ResolveUDPAddr("udp", dest)
	if err != nil {
		return nil, err
	}
	return net.DialUDP("udp", nil, addr)
}

func (c *UDPConnection) touch() {
	c.mu.Lock()
	c.idleSince = time.Now()
	c.mu.Unlock()
}

// SendToNetwork sends a packet to the network
func (c *UDPConnection) SendToNetwork(packet *IPv4Packet) {
	c.mu.Lock()
	ok := c.clientToNetwork.ReadFrom(packet.GetPayload())
	c.mu.Unlock()
	if !ok {
		c.LogwSimple(udpConnTag, "Cannot send to network, dropping packet")
		return
	}
	c.notifySend()
}

func (c *UDPConnection) notifySend() {
	select {
	case c.sendNotify <- struct{}{}:
	default:
	}
}

// Disconnect closes the connection
func (c *UDPConnection) Disconnect() {
	c.disconnectOnce.Do(func() {
		close(c.closeChan)
		c.LogiSimple(udpConnTag, "Close")
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
}

// IsExpired returns true if the connection has expired
func (c *UDPConnection) IsExpired() bool {
	c.mu.Lock()
	idle := c.idleSince
	c.mu.Unlock()
	return time.Since(idle) >= IdleTimeout
}

func (c *UDPConnection) readLoop() {
	buf := make([]byte, MaxPacketLength)
	for {
		select {
		case <-c.closeChan:
			return
		default:
		}

		c.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := c.conn.Read(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			c.closeConnection()
			return
		}

		if n > 0 {
			c.touch()
			c.processReceive(buf[:n])
		}
	}
}

func (c *UDPConnection) writeLoop() {
	for {
		select {
		case <-c.closeChan:
			return
		case <-c.sendNotify:
		}

		// Flush as much as possible until the datagram buffer becomes empty.
		for {
			select {
			case <-c.closeChan:
				return
			default:
			}

			// Check if buffer is empty (with lock)
			c.mu.Lock()
			if c.clientToNetwork.IsEmpty() {
				c.mu.Unlock()
				break
			}
			c.idleSince = time.Now()
			// Release lock before network I/O to avoid blocking
			c.mu.Unlock()

			// Perform network I/O outside of lock
			_ = c.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
			ok, err := c.clientToNetwork.WriteTo(c.conn)

			if err != nil || !ok {
				c.closeConnection()
				return
			}
		}
	}
}

func (c *UDPConnection) processReceive(data []byte) {
	// Create a simple reader from the data
	reader := &byteReader{data: data}
	packet, err := c.networkToClient.PacketizeAll(reader)
	if err != nil || packet == nil {
		c.closeConnection()
		return
	}
	c.pushToClient(packet)
}

func (c *UDPConnection) pushToClient(packet *IPv4Packet) {
	if !c.SendToClient(packet) {
		c.LogwSimple(udpConnTag, "Cannot send to client, dropping packet")
		return
	}
	c.LogdSimple(udpConnTag, "Packet sent to client")
	if IsVerboseEnabled() {
		c.LogvSimple(udpConnTag, BuildPacketString(packet.GetRaw()))
	}
}

func (c *UDPConnection) closeConnection() {
	c.removeOnce.Do(func() {
		c.Disconnect()
		c.client.GetRouter().Remove(c)
	})
}

// byteReader is a simple reader from a byte slice
type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) Read(p []byte) (int, error) {
	if r.pos >= len(r.data) {
		return 0, nil
	}
	n := copy(p, r.data[r.pos:])
	r.pos += n
	return n, nil
}
