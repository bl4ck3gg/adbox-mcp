package screen

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestPortal(t *testing.T, token string) (*Portal, *Manager) {
	t.Helper()
	mgr := NewManager()
	return NewPortal(mgr, "127.0.0.1", 0, token), mgr
}

// An empty portal must answer with an empty array rather than null or an error:
// the page does devices.length, and a null there reads as "the portal is broken".
func TestPortalReportsEmptyList(t *testing.T) {
	p, _ := newTestPortal(t, "")

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	p.route(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(raw["devices"]) != "[]" {
		t.Fatalf("devices = %s, want []", raw["devices"])
	}
}

// A card links to a path on the portal, never to a port of its own: the whole
// point of the portal is that there is one address to remember.
func TestPortalDeviceURLIsAPath(t *testing.T) {
	p, mgr := newTestPortal(t, "")
	sess := &Session{serial: "serial-a"}
	sess.running.Store(true)
	mgr.sessions["serial-a"] = sess

	req := httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	rec := httptest.NewRecorder()
	p.route(rec, req)

	var body struct {
		Devices []deviceCard `json:"devices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(body.Devices))
	}
	if got, want := body.Devices[0].URL, "/device/serial-a/"; got != want {
		t.Fatalf("url = %q, want %q", got, want)
	}
}

// Every route is behind the token when one is set, including the device paths:
// a viewer that could be reached without it would make the token decoration.
func TestPortalRequiresToken(t *testing.T) {
	p, mgr := newTestPortal(t, "s3cret")
	sess := &Session{serial: "serial-a"}
	sess.running.Store(true)
	mgr.sessions["serial-a"] = sess

	for _, path := range []string{"/", "/api/devices", "/device/serial-a/", "/device/serial-a/ws"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		p.route(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without token = %d, want 401", path, rec.Code)
		}
	}
}

// The query parameter is accepted, and a bearer header too: the first is what a
// human pastes into a browser, the second is what a script sends.
func TestPortalAcceptsQueryAndBearer(t *testing.T) {
	p, _ := newTestPortal(t, "s3cret")

	req := httptest.NewRequest(http.MethodGet, "/api/devices?token=s3cret", nil)
	rec := httptest.NewRecorder()
	p.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("query token = %d, want 200", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/devices", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	p.route(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("bearer = %d, want 200", rec.Code)
	}
}

// The first authenticated page load leaves the cookie behind, because a browser
// cannot put a header on a WebSocket handshake and the token must not be in every
// URL the page loads.
func TestPortalSetsCookieOnToken(t *testing.T) {
	p, mgr := newTestPortal(t, "s3cret")
	sess := &Session{serial: "serial-a"}
	sess.running.Store(true)
	mgr.sessions["serial-a"] = sess

	req := httptest.NewRequest(http.MethodGet, "/device/serial-a/?token=s3cret", nil)
	rec := httptest.NewRecorder()
	p.route(rec, req)

	var found bool
	for _, c := range rec.Result().Cookies() {
		if c.Name == cookieName && c.Value == "s3cret" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %s cookie set; got %v", cookieName, rec.Result().Cookies())
	}
}

// A wrong token is refused with the same status as no token: the answer must not
// tell a guesser that the shape of the secret was right.
func TestPortalRejectsWrongToken(t *testing.T) {
	p, _ := newTestPortal(t, "s3cret")

	req := httptest.NewRequest(http.MethodGet, "/api/devices?token=wrong", nil)
	rec := httptest.NewRecorder()
	p.route(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d, want 401", rec.Code)
	}
}

// A device that is not streaming answers with a sentence, not a bare 404: the
// caller usually has a stale tab open and needs to know which device is missing.
func TestPortalUnknownDeviceExplainsItself(t *testing.T) {
	p, _ := newTestPortal(t, "")

	req := httptest.NewRequest(http.MethodGet, "/device/ghost/", nil)
	rec := httptest.NewRecorder()
	p.route(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if body := rec.Body.String(); len(body) == 0 {
		t.Fatal("empty body; want a sentence naming the device")
	}
}
