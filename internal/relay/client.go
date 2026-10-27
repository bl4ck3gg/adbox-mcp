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
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

const clientTag = "Client"

var nextClientId int32 = 0

// CloseListener is called when a client is closed
type CloseListener func(*Client)

// Client represents a connected client
type Client struct {
	id                   int
	conn                 net.Conn
	closeListener        CloseListener
	clientToNetwork      *IPv4PacketBuffer
	networkToClient      *StreamBuffer
	router               *Router
	pendingPacketSources []PacketSource
	closed               bool
	mu                   sync.Mutex
	closeChan            chan struct{}
	writeNotify          chan struct{}
}

// NewClient creates a new Client
func NewClient(conn net.Conn, closeListener CloseListener) *Client {
	id := int(atomic.AddInt32(&nextClientId, 1) - 1)

	c := &Client{
		id:                   id,
		conn:                 conn,
		closeListener:        closeListener,
		clientToNetwork:      NewIPv4PacketBuffer(),
		networkToClient:      NewStreamBuffer(16 * MaxPacketLength),
		pendingPacketSources: make([]PacketSource, 0),
		closeChan:            make(chan struct{}),
		writeNotify:          make(chan struct{}, 1),
	}

	c.router = NewRouter(c)

	// Send client ID first
	go c.sendId()

	return c
}

// GetId returns the client ID
func (c *Client) GetId() int {
	return c.id
}

// GetRouter returns the router for this client
func (c *Client) GetRouter() *Router {
	return c.router
}

func (c *Client) sendId() {
	idBuf := make([]byte, 4)
	binary.BigEndian.PutUint32(idBuf, uint32(c.id))

	_, err := c.conn.Write(idBuf)
	if err != nil {
		Loge(clientTag, fmt.Sprintf("Cannot write client id #%d", c.id), err)
		c.Close()
		return
	}

	LogdSimple(clientTag, fmt.Sprintf("Client id #%d sent to client", c.id))

	// Start main loops after ID is sent
	go c.readLoop()
	go c.writeLoop()
}

func (c *Client) readLoop() {
	for {
		select {
		case <-c.closeChan:
			return
		default:
		}

		n, err := c.clientToNetwork.ReadFrom(c.conn)
		if err != nil {
			if err != io.EOF {
				Loge(clientTag, "Cannot read", err)
			}
			c.Close()
			return
		}

		c.pushToNetwork()
		_ = n // n is only used to detect errors (handled above)
	}
}

func (c *Client) pushToNetwork() {
	for {
		packet := c.clientToNetwork.AsIPv4Packet()
		if packet == nil {
			break
		}
		c.router.SendToNetwork(packet)
		c.clientToNetwork.Next()
	}
}

func (c *Client) writeLoop() {
	for {
		select {
		case <-c.closeChan:
			return
		case <-c.writeNotify:
		}

		// Flush as much as possible until the buffer becomes empty.
		for {
			select {
			case <-c.closeChan:
				return
			default:
			}

			c.mu.Lock()
			if c.networkToClient.IsEmpty() {
				c.mu.Unlock()
				break
			}

			_, err := c.networkToClient.WriteTo(c.conn)
			c.mu.Unlock()

			if err != nil {
				Loge(clientTag, "Cannot write", err)
				c.Close()
				return
			}

			c.processPending()
		}
	}
}

func (c *Client) notifyWrite() {
	select {
	case c.writeNotify <- struct{}{}:
	default:
	}
}

// SendToClient sends a packet to the client
func (c *Client) SendToClient(packet *IPv4Packet) bool {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return false
	}

	if c.networkToClient.Remaining() < packet.GetRawLength() {
		LogwSimple(clientTag, "Client buffer full")
		c.mu.Unlock()
		return false
	}

	c.networkToClient.ReadFrom(packet.GetRaw())
	c.mu.Unlock()
	c.notifyWrite()
	return true
}

// Consume consumes packets from a packet source
func (c *Client) Consume(source PacketSource) {
	packet := source.Get()
	if c.SendToClient(packet) {
		source.Next()
		return
	}

	c.mu.Lock()
	c.pendingPacketSources = append(c.pendingPacketSources, source)
	c.mu.Unlock()
}

func (c *Client) processPending() {
	c.mu.Lock()
	defer c.mu.Unlock()

	remaining := c.pendingPacketSources[:0]
	for _, source := range c.pendingPacketSources {
		packet := source.Get()
		if c.sendToClientLocked(packet) {
			source.Next()
			LogdSimple(clientTag, fmt.Sprintf("Pending packet sent to client (%d)", packet.GetRawLength()))
		} else {
			LogwSimple(clientTag, fmt.Sprintf("Pending packet not sent to client (%d), client buffer full again", packet.GetRawLength()))
			remaining = append(remaining, source)
		}
	}
	c.pendingPacketSources = remaining
}

func (c *Client) sendToClientLocked(packet *IPv4Packet) bool {
	if c.networkToClient.Remaining() < packet.GetRawLength() {
		return false
	}
	c.networkToClient.ReadFrom(packet.GetRaw())
	return true
}

// Close closes the client connection
func (c *Client) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.closeChan)
	c.mu.Unlock()

	c.conn.Close()
	c.router.Clear()
	c.closeListener(c)
}

// CleanExpiredConnections cleans up expired connections
func (c *Client) CleanExpiredConnections() {
	c.router.CleanExpiredConnections()
}
