// Package tether gives a network-less device an internet connection through adb.
//
// THE PROBLEM. A device with no WiFi, no SIM, and no way to be configured still
// has exactly one working transport: the adb connection. Everything else -- DNS,
// routing, proxies -- is unavailable, which is also why configuring it by hand is
// not an option: there is nothing to configure against.
//
// THE APPROACH. Android lets an app install a VpnService, which captures every
// packet the device would have sent and hands it to that app instead of the
// network stack. gnirehtet's client writes those raw IPv4 packets into a local
// socket, and something on the other end has to be a TCP/IP stack.
//
// That something is here, in this process. The relay terminates TCP and UDP in
// user space, opens real sockets on the host, and writes the replies back as
// IPv4 packets. The device believes it is online; in truth every byte crosses the
// adb tunnel.
//
// This is why the relay belongs inside adbox rather than beside it. The device's
// uplink and its control channel are the same adb connection, so the process that
// owns that connection should own the uplink too -- one lifecycle, one place to
// look when it breaks.
//
// The relay implementation is gnirehtet's (Apache 2.0, Genymobile); see
// internal/relay/LICENSE and the attribution in internal/relay/relay.go.
package tether

import (
	"fmt"
	"sync"
	"time"

	"adbox-mcp/internal/relay"
)

// Session is one running relay, keyed by device so a second start on the same
// device is a no-op rather than a port collision.
type Session struct {
	Device    string    `json:"device"`
	Port      int       `json:"port"`
	StartedAt time.Time `json:"started_at"`
}

// Manager owns the relay processes.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// Start brings up a relay for the device on the given port and returns the
// session. Calling it twice for the same device returns the running session.
func (m *Manager) Start(device string, port int) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.sessions[device]; ok {
		return existing, nil
	}

	r := relay.NewRelay(port)
	// Run blocks; the relay is a server. Give it a moment to bind before the
	// caller goes looking for the port, and let a bind failure surface here
	// rather than as a mysterious dead tunnel later.
	errCh := make(chan error, 1)
	go func() { errCh <- r.Run() }()

	select {
	case err := <-errCh:
		return nil, fmt.Errorf("relay failed to start: %w", err)
	case <-time.After(500 * time.Millisecond):
	}

	s := &Session{Device: device, Port: port, StartedAt: time.Now()}
	m.sessions[device] = s
	return s, nil
}

// Stop ends the session for a device. The relay goroutine is left to be reaped
// with the process: it has no shutdown path that would not also drop the sockets
// in flight, and a tethered device losing its connection mid-transfer is worse
// than a socket lingering until the next restart.
func (m *Manager) Stop(device string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[device]; !ok {
		return false
	}
	delete(m.sessions, device)
	return true
}

// List returns the running sessions.
func (m *Manager) List() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, *s)
	}
	return out
}

// Get returns the session for a device, if any.
func (m *Manager) Get(device string) (*Session, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[device]
	return s, ok
}
