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
	"sync"
)

const routerTag = "Router"

// Router manages connections for a client
type Router struct {
	client      *Client
	connections []Connection
	mu          sync.RWMutex
}

// NewRouter creates a new Router
func NewRouter(client *Client) *Router {
	return &Router{
		client:      client,
		connections: make([]Connection, 0),
	}
}

// SendToNetwork sends a packet to the appropriate network connection
func (r *Router) SendToNetwork(packet *IPv4Packet) {
	if !packet.IsValid() {
		LogwSimple(routerTag, "Dropping invalid packet")
		if IsVerboseEnabled() {
			LogvSimple(routerTag, BuildPacketString(packet.GetRaw()))
		}
		return
	}

	conn, err := r.getConnection(packet.GetIPv4Header(), packet.GetTransportHeader())
	if err != nil {
		Loge(routerTag, "Cannot create connection, dropping packet", err)
		return
	}

	conn.SendToNetwork(packet)
}

func (r *Router) getConnection(ipv4Header *IPv4Header, transportHeader TransportHeader) (Connection, error) {
	id := ConnectionIdFrom(ipv4Header, transportHeader)

	r.mu.RLock()
	conn := r.find(id)
	r.mu.RUnlock()

	if conn != nil {
		return conn, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	conn = r.find(id)
	if conn != nil {
		return conn, nil
	}

	conn, err := r.createConnection(id, ipv4Header, transportHeader)
	if err != nil {
		return nil, err
	}

	r.connections = append(r.connections, conn)
	return conn, nil
}

func (r *Router) createConnection(id *ConnectionId, ipv4Header *IPv4Header, transportHeader TransportHeader) (Connection, error) {
	protocol := id.GetProtocol()

	switch protocol {
	case ProtocolUDP:
		return NewUDPConnection(id, r.client, ipv4Header, transportHeader.(*UDPHeader))
	case ProtocolTCP:
		return NewTCPConnection(id, r.client, ipv4Header, transportHeader.(*TCPHeader))
	default:
		LogeSimple(routerTag, "Unsupported protocol")
		return nil, nil
	}
}

func (r *Router) find(id *ConnectionId) Connection {
	for _, conn := range r.connections {
		if id.Equals(conn.GetId()) {
			return conn
		}
	}
	return nil
}

// Clear disconnects all connections
func (r *Router) Clear() {
	// Copy connections list and release lock before calling Disconnect()
	// to avoid potential deadlock if Disconnect() needs to acquire other locks
	r.mu.Lock()
	connections := make([]Connection, len(r.connections))
	copy(connections, r.connections)
	r.connections = r.connections[:0]
	r.mu.Unlock()

	// Disconnect all connections outside of lock
	for _, conn := range connections {
		conn.Disconnect()
	}
}

// Remove removes a connection from the router
func (r *Router) Remove(conn Connection) {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i, c := range r.connections {
		if c == conn {
			r.connections = append(r.connections[:i], r.connections[i+1:]...)
			return
		}
	}
}

// CleanExpiredConnections removes expired connections
func (r *Router) CleanExpiredConnections() {
	// Collect expired connections first, then disconnect them outside of lock
	// to avoid potential deadlock
	var expired []Connection
	var indices []int

	r.mu.Lock()
	for i := len(r.connections) - 1; i >= 0; i-- {
		conn := r.connections[i]
		if conn.IsExpired() {
			LogdSimple(routerTag, "Remove expired connection: "+conn.GetId().String())
			expired = append(expired, conn)
			indices = append(indices, i)
		}
	}
	// Remove expired connections from list
	for _, i := range indices {
		r.connections = append(r.connections[:i], r.connections[i+1:]...)
	}
	r.mu.Unlock()

	// Disconnect expired connections outside of lock
	for _, conn := range expired {
		conn.Disconnect()
	}
}
