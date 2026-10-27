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
	"time"
)

const relayTag = "Relay"

// CleaningInterval is the interval between cleaning expired connections
const CleaningInterval = 60 * time.Second

// Relay is the main relay server
type Relay struct {
	port         int
	tunnelServer *TunnelServer
}

// NewRelay creates a new Relay
func NewRelay(port int) *Relay {
	return &Relay{
		port: port,
	}
}

// Run starts the relay server
func (r *Relay) Run() error {
	tunnelServer, err := NewTunnelServer(r.port)
	if err != nil {
		return err
	}
	r.tunnelServer = tunnelServer
	tunnelServer.Start()

	LogiSimple(relayTag, "Relay server started")

	ticker := time.NewTicker(CleaningInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tunnelServer.CleanUp()
		}
	}
}

// Close closes the relay server
func (r *Relay) Close() {
	if r.tunnelServer != nil {
		r.tunnelServer.Close()
	}
}
