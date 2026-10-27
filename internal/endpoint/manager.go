package endpoint

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"strings"
	"sync"

	"adbox-mcp/internal/adb"
)

// Endpoint represents a connected ADB daemon endpoint.
type Endpoint struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Host      string `json:"host"`
	Port      int    `json:"port"`
	ProxyURL  string `json:"proxy,omitempty"`
	Status    string `json:"status"` // "ok", "unreachable", "error"
	LastError string `json:"last_error,omitempty"`
}

// DeviceRecord represents an active Android device.
type DeviceRecord struct {
	Serial      string `json:"serial"`
	TransportID int    `json:"transport_id,omitempty"`
	EndpointID  string `json:"endpoint_id"`
	Endpoint    string `json:"endpoint_name"`
	Product     string `json:"product,omitempty"`
	Model       string `json:"model,omitempty"`
	Device      string `json:"device,omitempty"`
	State       string `json:"state"` // "device", "offline", "unauthorized"
}

// Manager manages multiple ADB endpoints and resolves devices across them.
type Manager struct {
	mu        sync.RWMutex
	endpoints map[string]*endpointEntry
	order     []string
}

type endpointEntry struct {
	info   Endpoint
	client *adb.Adb
	stopCh chan struct{}
}

func NewManager() *Manager {
	return &Manager{
		endpoints: make(map[string]*endpointEntry),
		order:     make([]string, 0),
	}
}

func randomHexID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// AddEndpoint parses a spec string like "adb=127.0.0.1:5037,name=remote,proxy=socks5://127.0.0.1:11080"
func (m *Manager) AddEndpoint(spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", fmt.Errorf("empty endpoint specification")
	}

	var adbVal, name, proxyURL string
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		idx := strings.Index(part, "=")
		if idx <= 0 {
			if !strings.Contains(part, "=") && adbVal == "" {
				adbVal = part
			}
			continue
		}
		k := strings.ToLower(strings.TrimSpace(part[:idx]))
		v := strings.TrimSpace(part[idx+1:])
		switch k {
		case "adb":
			adbVal = v
		case "name":
			name = v
		case "proxy":
			proxyURL = v
		}
	}

	if adbVal == "" {
		return "", fmt.Errorf("missing adb= host:port in spec %q", spec)
	}

	host, portStr, err := net.SplitHostPort(adbVal)
	if err != nil {
		host = adbVal
		portStr = "5037"
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return "", fmt.Errorf("invalid port in %q: %v", adbVal, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	id := randomHexID(3) // 6 hex chars
	for {
		if _, exists := m.endpoints[id]; !exists {
			break
		}
		id = randomHexID(3)
	}

	if name == "" {
		name = fmt.Sprintf("%s:%d", host, port)
	}

	cfg := adb.ServerConfig{
		Host: host,
		Port: port,
	}

	// The proxy field carries the transport, and the two transports answer
	// different questions. socks5:// routes through a proxy that is already
	// running. ssh:// opens an SSH session and dials through it, which is how a
	// device that can only dial outward becomes reachable -- nothing needs to
	// listen on the phone's network.
	if proxyURL != "" {
		switch {
		case strings.HasPrefix(proxyURL, "socks5://"):
			dialer, err := adb.NewSOCKS5Dialer(proxyURL)
			if err != nil {
				return "", fmt.Errorf("socks5 dialer: %w", err)
			}
			cfg.Dialer = dialer

		case strings.HasPrefix(proxyURL, "ssh://"):
			sshOpts, err := adb.ParseSSHSpec(proxyURL)
			if err != nil {
				return "", fmt.Errorf("ssh spec: %w", err)
			}
			if sshOpts.KeyPath == "" && sshOpts.Password == "" {
				return "", fmt.Errorf("ssh endpoint needs ?key=<path> or ?password=<pass>: an interactive prompt is not possible here")
			}
			dialer, err := adb.NewSSHDialer(sshOpts)
			if err != nil {
				return "", fmt.Errorf("ssh dialer: %w", err)
			}
			cfg.Dialer = dialer

		default:
			return "", fmt.Errorf("proxy must start with socks5:// or ssh:// (got %q)", proxyURL)
		}
	}

	client, err := adb.NewWithConfig(cfg)
	if err != nil {
		return "", fmt.Errorf("init adb client: %w", err)
	}

	entry := &endpointEntry{
		info: Endpoint{
			ID:       id,
			Name:     name,
			Host:     host,
			Port:     port,
			ProxyURL: proxyURL,
			Status:   "ok",
		},
		client: client,
		stopCh: make(chan struct{}),
	}

	// Verify server reachability immediately
	if _, err := client.ServerVersion(); err != nil {
		entry.info.Status = "unreachable"
		entry.info.LastError = err.Error()
		slog.Warn("endpoint added but currently unreachable", "id", id, "name", name, "err", err)
	}

	m.endpoints[id] = entry
	m.order = append(m.order, id)
	return id, nil
}

// RemoveEndpoint drops an endpoint by ID or host:port
func (m *Manager) RemoveEndpoint(target string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	var targetID string
	if _, ok := m.endpoints[target]; ok {
		targetID = target
	} else {
		for id, e := range m.endpoints {
			if fmt.Sprintf("%s:%d", e.info.Host, e.info.Port) == target || e.info.Name == target {
				targetID = id
				break
			}
		}
	}
	if targetID == "" {
		return false
	}

	close(m.endpoints[targetID].stopCh)
	delete(m.endpoints, targetID)
	newOrder := make([]string, 0, len(m.order)-1)
	for _, id := range m.order {
		if id != targetID {
			newOrder = append(newOrder, id)
		}
	}
	m.order = newOrder
	return true
}

func (m *Manager) ListEndpoints() []Endpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Endpoint, 0, len(m.order))
	for _, id := range m.order {
		if e, ok := m.endpoints[id]; ok {
			out = append(out, e.info)
		}
	}
	return out
}

// ListDevices polls all active endpoints and returns aggregated records.
func (m *Manager) ListDevices() ([]DeviceRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var records []DeviceRecord
	for _, id := range m.order {
		e := m.endpoints[id]
		devs, err := e.client.ListDevices()
		if err != nil {
			e.info.Status = "error"
			e.info.LastError = err.Error()
			continue
		}
		e.info.Status = "ok"
		e.info.LastError = ""
		for _, d := range devs {
			records = append(records, DeviceRecord{
				Serial:      d.Serial,
				TransportID: d.TransportID,
				EndpointID:  id,
				Endpoint:    e.info.Name,
				Product:     d.Product,
				Model:       d.Model,
				Device:      d.DeviceInfo,
				State:       strings.TrimSpace(d.Status),
			})
		}
	}
	return records, nil
}

// ResolveDevice finds the target device and returns its ADB Device handle.
// selector format: "serial", "serial:transport_id", "serial@endpoint", or "serial:transport_id@endpoint"
func (m *Manager) ResolveDevice(selector string) (*adb.Device, *DeviceRecord, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, nil, fmt.Errorf("device selector cannot be empty")
	}

	var endpointID string
	if at := strings.LastIndex(selector, "@"); at >= 0 {
		endpointID = selector[at+1:]
		selector = selector[:at]
	}

	var transportID int
	serial := selector
	if colon := strings.LastIndex(selector, ":"); colon > 0 {
		if t, err := strconv.Atoi(selector[colon+1:]); err == nil && t > 0 {
			transportID = t
			serial = selector[:colon]
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, id := range m.order {
		if endpointID != "" && id != endpointID && m.endpoints[id].info.Name != endpointID {
			continue
		}
		e := m.endpoints[id]
		var desc adb.DeviceDescriptor
		if transportID > 0 {
			desc = adb.DeviceWithTransportID(transportID)
		} else if serial != "" {
			desc = adb.DeviceWithSerial(serial)
		} else {
			desc = adb.AnyDevice()
		}
		dev := e.client.Device(desc)
		// Test presence with state read
		state, err := dev.State()
		if err == nil {
			rec := &DeviceRecord{
				Serial:      serial,
				TransportID: transportID,
				EndpointID:  id,
				Endpoint:    e.info.Name,
				State:       deviceStateName(state),
			}
			return dev, rec, nil
		}
	}

	return nil, nil, fmt.Errorf("device %q not found on any active endpoint", selector)
}

// deviceStateName renders a DeviceState the way adb itself spells it ("online",
// "unauthorized"), not the Go constant name. A caller comparing the string against
// what `adb devices` prints should not have to strip a "State" prefix.
func deviceStateName(s adb.DeviceState) string {
	switch s {
	case adb.StateOnline:
		return "online"
	case adb.StateOffline:
		return "offline"
	case adb.StateUnauthorized:
		return "unauthorized"
	case adb.StateConnecting:
		return "connecting"
	case adb.StateDisconnected:
		return "disconnected"
	default:
		return "unknown"
	}
}
