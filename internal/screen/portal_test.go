package screen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A portal with no sessions must still answer, and it must answer with an empty
// list rather than an error: the page a caller bookmarks is the page that has to
// work before any device is streaming, because that is when they open it.
func TestPortalReportsEmptyList(t *testing.T) {
	p := NewPortal(NewManager(), "127.0.0.1", 0)

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	p.handleDevices(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Count   int          `json:"count"`
		Devices []deviceCard `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v (body %q)", err, rec.Body.String())
	}
	if body.Count != 0 || len(body.Devices) != 0 {
		t.Fatalf("count=%d devices=%d, want zero of each", body.Count, len(body.Devices))
	}
}

// The list must be a JSON array even when empty, never null: a page doing
// devices.length would throw on null, and the failure would look like "the
// portal is broken" rather than "no devices".
func TestPortalEmptyListIsArray(t *testing.T) {
	p := NewPortal(NewManager(), "127.0.0.1", 0)

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	p.handleDevices(rec, req)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["devices"]) != "[]" {
		t.Fatalf("devices = %s, want []", raw["devices"])
	}
}

// A viewer URL must be built from the host the page was reached at, not from the
// portal's bind address: a portal bound to 0.0.0.0 is reached by the machine's
// own name, and a card pointing at 0.0.0.0:PORT is a link nobody can click.
func TestPortalCardURLUsesRequestHost(t *testing.T) {
	mgr := NewManager()
	mgr.sessions["serial-a"] = &Session{serial: "serial-a", httpPort: 41888}

	p := NewPortal(mgr, "0.0.0.0", 0)
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Host = "192.168.1.50:18080"
	rec := httptest.NewRecorder()
	p.handleDevices(rec, req)

	var body struct {
		Devices []deviceCard `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(body.Devices))
	}
	want := "http://192.168.1.50:41888/"
	if got := body.Devices[0].URL; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

// A session that has not reached its listener yet reports port 0 and an empty
// URL, which is how the page tells "starting" from "clickable".
func TestPortalPendingSessionHasNoURL(t *testing.T) {
	mgr := NewManager()
	mgr.sessions["serial-b"] = &Session{serial: "serial-b"}

	p := NewPortal(mgr, "127.0.0.1", 0)
	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	p.handleDevices(rec, req)

	var body struct {
		Devices []deviceCard `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got := body.Devices[0].URL; got != "" {
		t.Fatalf("url = %q, want empty for a pending session", got)
	}
	if got := body.Devices[0].Port; got != 0 {
		t.Fatalf("port = %d, want 0", got)
	}
}
