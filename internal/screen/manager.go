package screen

import (
	"sync"

	"adbox-mcp/internal/adb"
)

// Manager keeps one active screen session per device.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{
		sessions: make(map[string]*Session),
	}
}

func (m *Manager) Start(dev *adb.Device, serial string, opts Options) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, exists := m.sessions[serial]; exists {
		old.Close()
		delete(m.sessions, serial)
	}

	s := NewSession(dev, serial, opts)
	// A session that dies on its own is removed from the table, so the next call
	// starts a fresh one instead of writing into a socket that is already gone.
	s.SetOnEnded(func(reason string) {
		m.mu.Lock()
		if cur, ok := m.sessions[serial]; ok && cur == s {
			delete(m.sessions, serial)
		}
		m.mu.Unlock()
	})
	url, err := s.Start()
	if err != nil {
		return "", err
	}
	m.sessions[serial] = s
	return url, nil
}

func (m *Manager) Stop(serial string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, exists := m.sessions[serial]; exists {
		s.Close()
		delete(m.sessions, serial)
		return true
	}
	return false
}

func (m *Manager) Get(serial string) (*Session, bool) {
	m.mu.Lock()
	s, ok := m.sessions[serial]
	if ok && !s.Running() {
		delete(m.sessions, serial)
		m.mu.Unlock()
		return nil, false
	}
	m.mu.Unlock()
	return s, ok
}

// AutoRestart starts a session again if the previous one ended on the device
// side. Used by the viewer so a crashed encoder heals without a human.
func (m *Manager) AutoRestart(dev *adb.Device, serial string, opts Options) (string, error) {
	if _, ok := m.Get(serial); ok {
		if s, _ := m.Get(serial); s != nil && s.Running() {
			return "", nil
		}
	}
	return m.Start(dev, serial, opts)
}
