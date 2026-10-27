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

func (m *Manager) Start(dev *adb.Device, serial string, maxSize, bitRate int) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if old, exists := m.sessions[serial]; exists {
		old.Close()
		delete(m.sessions, serial)
	}

	s := NewSession(dev, serial, maxSize, bitRate)
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
	defer m.mu.Unlock()
	s, ok := m.sessions[serial]
	return s, ok
}
