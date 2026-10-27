package screen

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Portal is the only HTTP listener adbox opens. Every device is a path under it:
//
//	/                      the device list
//	/api/devices           the list as JSON
//	/device/<serial>/      that phone's control page
//	/device/<serial>/ws    its video/audio/control socket
//
// WHY ONE PORT. A listener per session means a port per phone, and a port that
// changes on every restart is a number nobody can keep. Worse, every one of those
// ports is a separate way in: a caller that publishes one viewer to the network
// publishes all of them, and each has to be remembered separately when a token
// arrives. One listener has one address, one token, and one place to check it.
//
// The routing is in-process, not a reverse proxy: /device/<serial>/ calls the
// session's own handler directly, so video never travels through a second socket
// and a slow device cannot hold another device's page.
type Portal struct {
	mgr   *Manager
	host  string
	port  int
	token string // "" disables the check, which is only sane on loopback

	mu   sync.Mutex
	ln   net.Listener
	addr string
}

// NewPortal builds a portal over mgr. host is the bind address (0.0.0.0 to reach
// it from another machine); port 0 asks the OS for one. token guards every route
// when non-empty.
func NewPortal(mgr *Manager, host string, port int, token string) *Portal {
	return &Portal{mgr: mgr, host: host, port: port, token: strings.TrimSpace(token)}
}

// NewToken mints the shared secret used when the caller did not supply one. It is
// 12 hex characters (48 bits): short enough to read off a terminal and retype on a
// phone, long enough that guessing it over a LAN is not a thing.
func NewToken() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		return "adbox"
	}
	return hex.EncodeToString(b)
}

// Start begins listening and returns the URL a browser should open. The URL
// carries the token when one is set, because the first request is the one that
// has to prove itself and a caller reading a log has no other way to know it.
func (p *Portal) Start() (string, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort(p.host, fmt.Sprint(p.port)))
	if err != nil {
		return "", fmt.Errorf("listen portal: %w", err)
	}
	p.mu.Lock()
	p.ln = ln
	p.addr = ln.Addr().String()
	p.mu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("/", p.route)

	go func() {
		if err := http.Serve(ln, mux); err != nil {
			slog.Debug("portal stopped", "err", err)
		}
	}()

	shown := p.host
	if shown == "" || shown == "0.0.0.0" || shown == "::" {
		shown = "127.0.0.1"
	}
	base := fmt.Sprintf("http://%s:%d/", shown, ln.Addr().(*net.TCPAddr).Port)
	full := base
	if p.token != "" {
		full = base + "?token=" + url.QueryEscape(p.token)
	}
	slog.Info("portal started", "url", base, "auth", p.token != "")
	return full, nil
}

// Close stops the listener. It is safe to call more than once.
func (p *Portal) Close() {
	p.mu.Lock()
	ln := p.ln
	p.ln = nil
	p.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
}

// Addr is the address actually bound, or "" before Start.
func (p *Portal) Addr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addr
}

// URL is the address a browser should open, or "" before Start.
func (p *Portal) URL() string {
	addr := p.Addr()
	if addr == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return ""
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return fmt.Sprintf("http://%s:%s/", host, port)
}

// route is the whole surface: authenticate, then dispatch on the path.
func (p *Portal) route(w http.ResponseWriter, r *http.Request) {
	if !p.authorized(r) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="adbox"`)
		http.Error(w, "unauthorized: append ?token=<token> to the URL", http.StatusUnauthorized)
		return
	}
	path := r.URL.Path
	switch {
	case path == "/" || path == "":
		p.handleIndex(w, r)
	case path == "/api/devices":
		p.handleDevices(w, r)
	case strings.HasPrefix(path, "/device/"):
		p.handleDevice(w, r)
	default:
		http.NotFound(w, r)
	}
}

// authorized accepts the token from a query parameter, a bearer header, or the
// cookie the first successful query leaves behind.
//
// THE COOKIE IS THE POINT. A browser cannot set a header on a WebSocket
// handshake, so a query-only scheme would put the token in every URL the page
// loads -- history, referrers, screenshots. The first request carries ?token=,
// the response stores it, and the socket that follows authenticates by cookie.
func (p *Portal) authorized(r *http.Request) bool {
	if p.token == "" {
		return true
	}
	given := r.URL.Query().Get("token")
	if given == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			given = strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
	}
	if given == "" {
		if c, err := r.Cookie(cookieName); err == nil {
			given = c.Value
		}
	}
	if given == "" {
		return false
	}
	// Constant time: a comparison that returns early leaks the prefix, and this
	// is the one secret standing between a LAN and a phone's input.
	return subtle.ConstantTimeCompare([]byte(given), []byte(p.token)) == 1
}

const cookieName = "adbox_token"

// handleDevice routes /device/<serial>/ and /device/<serial>/ws to a session.
func (p *Portal) handleDevice(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/device/")
	serial, tail, _ := strings.Cut(rest, "/")
	if serial == "" {
		http.NotFound(w, r)
		return
	}
	sess, ok := p.mgr.Get(serial)
	if !ok {
		// A page that says which device is missing beats a bare 404: the caller
		// may have an old tab open after the session was stopped.
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("no screen session for " + serial + "; start one with device_screen_start"))
		return
	}

	// The first authenticated request sets the cookie that the WebSocket will use.
	if p.token != "" {
		if r.URL.Query().Get("token") != "" {
			http.SetCookie(w, &http.Cookie{
				Name:     cookieName,
				Value:    p.token,
				Path:     "/",
				HttpOnly: true,
				SameSite: http.SameSiteLaxMode,
			})
		}
	}

	switch strings.TrimSuffix(tail, "/") {
	case "":
		sess.ServeHTTP(w, r)
	case "ws":
		if p.token != "" {
			// The cookie is set above only on a request that carried ?token=; a
			// socket arriving with neither is a browser that went straight to the
			// ws path, which the page never does.
			if _, err := r.Cookie(cookieName); err != nil && r.URL.Query().Get("token") == "" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		sess.ServeWS(w, r)
	default:
		http.NotFound(w, r)
	}
}

// deviceCard is one row of the list.
type deviceCard struct {
	Serial  string `json:"serial"`
	URL     string `json:"url"`
	Video   bool   `json:"video"`
	Audio   bool   `json:"audio"`
	Uhid    bool   `json:"uhid"`
	Running bool   `json:"running"`
}

// snapshot builds the list from the manager. URLs are paths, not absolute
// addresses: the page is served from the same origin as the viewers, so a path is
// both shorter and correct whatever host the browser used to reach it.
func (p *Portal) snapshot() []deviceCard {
	sessions := p.mgr.List()
	cards := make([]deviceCard, 0, len(sessions))
	for _, s := range sessions {
		cards = append(cards, deviceCard{
			Serial:  s.serial,
			URL:     "/device/" + url.PathEscape(s.serial) + "/",
			Video:   s.VideoEnabled(),
			Audio:   s.AudioEnabled(),
			Uhid:    s.uhidOn.Load(),
			Running: s.running.Load(),
		})
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Serial < cards[j].Serial })
	return cards
}

func (p *Portal) handleDevices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count":   len(p.mgr.List()),
		"devices": p.snapshot(),
	})
}

func (p *Portal) handleIndex(w http.ResponseWriter, r *http.Request) {
	if p.token != "" && r.URL.Query().Get("token") != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    p.token,
			Path:     "/",
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(portalHtml))
}

const portalHtml = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>adbox devices</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { background: #0d0d0f; color: #e8e8ea;
         font-family: system-ui, -apple-system, "Segoe UI", sans-serif;
         min-height: 100vh; padding: 28px 20px 48px; }
  header { max-width: 1100px; margin: 0 auto 22px; display: flex;
           align-items: baseline; gap: 14px; flex-wrap: wrap; }
  h1 { font-size: 19px; font-weight: 600; letter-spacing: .01em; }
  .count { font-size: 13px; color: #6b6b73; font-variant-numeric: tabular-nums; }
  .grid { max-width: 1100px; margin: 0 auto; display: grid; gap: 12px;
          grid-template-columns: repeat(auto-fill, minmax(232px, 1fr)); }
  .card { display: block; text-decoration: none; color: inherit;
          background: #17171b; border: 1px solid #26262c; border-radius: 10px;
          padding: 15px 16px 13px; transition: border-color .12s, background .12s; }
  .card:hover { border-color: #3d3d47; background: #1c1c21; }
  .serial { font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
            font-size: 13px; font-weight: 600; margin-bottom: 9px;
            overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .meta { display: flex; gap: 7px; align-items: center; font-size: 11.5px;
          color: #85858f; flex-wrap: wrap; }
  .dot { width: 6px; height: 6px; border-radius: 50%; background: #2ecc71;
         flex: 0 0 auto; }
  .dot.off { background: #4a4a52; }
  .tag { padding: 1px 6px; border-radius: 4px; background: #232329;
         color: #9a9aa4; font-size: 10.5px; letter-spacing: .02em; }
  .tag.on { background: #1e3a29; color: #6fd39a; }
  .empty { max-width: 1100px; margin: 64px auto; text-align: center;
           color: #55555e; font-size: 13.5px; line-height: 1.9; }
  code { font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
         color: #85858f; background: #17171b; padding: 2px 6px; border-radius: 4px; }
</style>
</head>
<body>
<header>
  <h1>adbox devices</h1>
  <span class="count" id="count"></span>
</header>
<div class="grid" id="grid"></div>
<div class="empty" id="empty" style="display:none">
  No device is streaming right now.<br>
  Start one with <code>device_screen_start</code>.
</div>

<script>
const grid = document.getElementById('grid');
const empty = document.getElementById('empty');
const countEl = document.getElementById('count');

function render(devices) {
  countEl.textContent = devices.length ? devices.length + ' streaming' : '';
  if (!devices.length) {
    grid.style.display = 'none';
    empty.style.display = 'block';
    return;
  }
  grid.style.display = 'grid';
  empty.style.display = 'none';
  grid.textContent = '';
  for (const d of devices) {
    const a = document.createElement('a');
    a.className = 'card';
    a.href = d.url;
    const serial = document.createElement('div');
    serial.className = 'serial';
    serial.textContent = d.serial;
    a.appendChild(serial);

    const meta = document.createElement('div');
    meta.className = 'meta';

    const dot = document.createElement('span');
    dot.className = 'dot' + (d.running ? '' : ' off');
    meta.appendChild(dot);

    const pathLabel = document.createElement('span');
    pathLabel.textContent = d.url;
    meta.appendChild(pathLabel);

    for (const [name, on] of [['video', d.video], ['audio', d.audio], ['uhid', d.uhid]]) {
      if (!on) continue;
      const tag = document.createElement('span');
      tag.className = 'tag on';
      tag.textContent = name;
      meta.appendChild(tag);
    }
    a.appendChild(meta);
    grid.appendChild(a);
  }
}

async function refresh() {
  try {
    const res = await fetch('/api/devices', { cache: 'no-store' });
    if (!res.ok) throw new Error(res.status);
    const data = await res.json();
    render(data.devices || []);
  } catch (e) {
    countEl.textContent = 'connection lost';
  }
}

refresh();
setInterval(refresh, 3000);
</script>
</body>
</html>
`
