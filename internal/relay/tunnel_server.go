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
	"sync"
)

const tunnelServerTag = "TunnelServer"

// TunnelServer handles connections from clients
type TunnelServer struct {
	listener net.Listener
	clients  []*Client
	mu       sync.Mutex
	closeChan chan struct{}
}

// NewTunnelServer creates a new TunnelServer
func NewTunnelServer(port int) (*TunnelServer, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}

	ts := &TunnelServer{
		listener:  listener,
		clients:   make([]*Client, 0),
		closeChan: make(chan struct{}),
	}

	return ts, nil
}

// Start starts accepting client connections
func (ts *TunnelServer) Start() {
	go ts.acceptLoop()
}

func (ts *TunnelServer) acceptLoop() {
	for {
		select {
		case <-ts.closeChan:
			return
		default:
		}

		conn, err := ts.listener.Accept()
		if err != nil {
			select {
			case <-ts.closeChan:
				return
			default:
				Loge(tunnelServerTag, "Cannot accept client", err)
				continue
			}
		}

		ts.acceptClient(conn)
	}
}

func (ts *TunnelServer) acceptClient(conn net.Conn) {
	client := NewClient(conn, ts.removeClient)

	ts.mu.Lock()
	ts.clients = append(ts.clients, client)
	ts.mu.Unlock()

	LogiSimple(tunnelServerTag, fmt.Sprintf("Client #%d connected", client.GetId()))
}

func (ts *TunnelServer) removeClient(client *Client) {
	ts.mu.Lock()
	defer ts.mu.Unlock()

	for i, c := range ts.clients {
		if c == client {
			ts.clients = append(ts.clients[:i], ts.clients[i+1:]...)
			break
		}
	}

	LogiSimple(tunnelServerTag, fmt.Sprintf("Client #%d disconnected", client.GetId()))
}

// CleanUp cleans up expired connections for all clients
func (ts *TunnelServer) CleanUp() {
	ts.mu.Lock()
	clients := make([]*Client, len(ts.clients))
	copy(clients, ts.clients)
	ts.mu.Unlock()

	for _, client := range clients {
		client.CleanExpiredConnections()
	}
}

// Close closes the tunnel server
func (ts *TunnelServer) Close() {
	close(ts.closeChan)
	ts.listener.Close()

	ts.mu.Lock()
	for _, client := range ts.clients {
		client.Close()
	}
	ts.clients = ts.clients[:0]
	ts.mu.Unlock()
}
