package screen

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"sync"
)

// Portal is the front door: one page listing every device that currently has a
// viewer, so a caller does not have to remember a port number per phone.
type Portal struct {
	mgr  *Manager
	host string
	port int

	mu   sync.Mutex
	ln   net.Listener
	addr string
}

func NewPortal(mgr *Manager, host string, port int) *Portal {
	return &Portal{mgr: mgr, host: host, port: port}
}

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
	mux.HandleFunc("/", p.handleIndex)
	mux.HandleFunc("/api/devices", p.handleDevices)
	go func() {
		if err := http.Serve(ln, mux); err != nil {
			slog.Debug("portal stopped", "err", err)
		}
	}()

	shown := p.host
	if shown == "" || shown == "0.0.0.0" || shown == "::" {
		shown = "127.0.0.1"
	}
	url := fmt.Sprintf("http://%s:%d/", shown, ln.Addr().(*net.TCPAddr).Port)
	slog.Info("portal started", "url", url)
	return url, nil
}

func (p *Portal) Close() {
	p.mu.Lock()
	ln := p.ln
	p.ln = nil
	p.mu.Unlock()
	if ln != nil {
		_ = ln.Close()
	}
}

func (p *Portal) Addr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.addr
}

type deviceCard struct {
	Serial  string `json:"serial"`
	Port    int    `json:"port"`
	URL     string `json:"url"`
	Video   bool   `json:"video"`
	Audio   bool   `json:"audio"`
	Uhid    bool   `json:"uhid"`
	Running bool   `json:"running"`
}

func (p *Portal) snapshot(r *http.Request) []deviceCard {
	sessions := p.mgr.List()
	cards := make([]deviceCard, 0, len(sessions))
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	for _, s := range sessions {
		port := s.Port()
		card := deviceCard{
			Serial:  s.serial,
			Port:    port,
			Video:   s.VideoEnabled(),
			Audio:   s.AudioEnabled(),
			Uhid:    s.uhidOn.Load(),
			Running: s.running.Load(),
		}
		if port > 0 {
			card.URL = fmt.Sprintf("http://%s:%d/", host, port)
		}
		cards = append(cards, card)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].Serial < cards[j].Serial })
	return cards
}

func (p *Portal) handleDevices(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"count":   len(p.mgr.List()),
		"devices": p.snapshot(r),
	})
}

func (p *Portal) handleIndex(w http.ResponseWriter, r *http.Request) {
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
  .card.pending { opacity: .55; pointer-events: none; }
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
    a.className = 'card' + (d.port ? '' : ' pending');
    a.href = d.port ? d.url : '#';
    a.target = '_blank';
    a.rel = 'noopener';

    const serial = document.createElement('div');
    serial.className = 'serial';
    serial.textContent = d.serial;
    a.appendChild(serial);

    const meta = document.createElement('div');
    meta.className = 'meta';

    const dot = document.createElement('span');
    dot.className = 'dot' + (d.running ? '' : ' off');
    meta.appendChild(dot);

    const portLabel = document.createElement('span');
    portLabel.textContent = d.port ? ':' + d.port : 'starting';
    meta.appendChild(portLabel);

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
