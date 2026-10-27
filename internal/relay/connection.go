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
	"fmt"
	"net"
)

// 10.0.2.2 must be forwarded to localhost
const localhostForward = 0x0a000202

// Connection interface represents a network connection
type Connection interface {
	GetId() *ConnectionId
	SendToNetwork(packet *IPv4Packet)
	Disconnect()
	IsExpired() bool
}

// PacketSource is a source that may produce packets
type PacketSource interface {
	Get() *IPv4Packet
	Next()
}

// BaseConnection provides common functionality for connections
type BaseConnection struct {
	id     *ConnectionId
	client *Client
}

// NewBaseConnection creates a new BaseConnection
func NewBaseConnection(id *ConnectionId, client *Client) *BaseConnection {
	return &BaseConnection{
		id:     id,
		client: client,
	}
}

// GetId returns the connection ID
func (c *BaseConnection) GetId() *ConnectionId {
	return c.id
}

// Close disconnects and removes from router
func (c *BaseConnection) Close(conn Connection) {
	conn.Disconnect()
	c.client.GetRouter().Remove(conn)
}

// Consume passes a packet source to the client
func (c *BaseConnection) Consume(source PacketSource) {
	c.client.Consume(source)
}

// SendToClient sends a packet to the client
func (c *BaseConnection) SendToClient(packet *IPv4Packet) bool {
	return c.client.SendToClient(packet)
}

// GetRewrittenAddress returns the destination address, rewriting localhost if needed
func GetRewrittenAddress(ip uint32) net.IP {
	if ip == localhostForward {
		return net.IPv4(127, 0, 0, 1)
	}
	return ToInetAddress(ip)
}

// GetRewrittenDestination returns the destination, rewritten to localhost if it was 10.0.2.2
func (c *BaseConnection) GetRewrittenDestination() string {
	destIp := c.id.GetDestinationIp()
	port := c.id.GetDestinationPort()
	ip := GetRewrittenAddress(destIp)
	return fmt.Sprintf("%s:%d", ip.String(), port)
}

// Logging helpers
func (c *BaseConnection) Logv(tag, message string, err error) {
	Logv(tag, fmt.Sprintf("%s %s", c.id, message), err)
}

func (c *BaseConnection) LogvSimple(tag, message string) {
	c.Logv(tag, message, nil)
}

func (c *BaseConnection) Logd(tag, message string, err error) {
	Logd(tag, fmt.Sprintf("%s %s", c.id, message), err)
}

func (c *BaseConnection) LogdSimple(tag, message string) {
	c.Logd(tag, message, nil)
}

func (c *BaseConnection) Logi(tag, message string, err error) {
	Logi(tag, fmt.Sprintf("%s %s", c.id, message), err)
}

func (c *BaseConnection) LogiSimple(tag, message string) {
	c.Logi(tag, message, nil)
}

func (c *BaseConnection) Logw(tag, message string, err error) {
	Logw(tag, fmt.Sprintf("%s %s", c.id, message), err)
}

func (c *BaseConnection) LogwSimple(tag, message string) {
	c.Logw(tag, message, nil)
}

func (c *BaseConnection) Loge(tag, message string, err error) {
	Loge(tag, fmt.Sprintf("%s %s", c.id, message), err)
}

func (c *BaseConnection) LogeSimple(tag, message string) {
	c.Loge(tag, message, nil)
}
