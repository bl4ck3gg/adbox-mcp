package screen

import (
	"fmt"
	"net/http"
)

func (s *Session) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, indexHtml, s.serial)
}

const indexHtml = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, user-scalable=no, viewport-fit=cover">
<title>adbox screen</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; -webkit-tap-highlight-color: transparent; }
  html, body { height: 100%%; }
  body { background: #0d0d0f; color: #e8e8ea; font-family: system-ui, -apple-system, sans-serif;
         display: flex; height: 100vh; height: 100dvh; overflow: hidden; }

  /* Left rail: a vertical strip of icon buttons beside the screen, the way a
     remote control sits next to a TV. A bottom bar wastes vertical space, which
     is exactly what a tall phone image needs. */
  #rail { flex: 0 0 auto; width: 52px; display: flex; flex-direction: column; gap: 4px;
          padding: 6px 5px; background: #17171a; border-right: 1px solid #26262b;
          overflow-y: auto; overflow-x: hidden; scrollbar-width: none; }
  #rail::-webkit-scrollbar { display: none; }

  #rail button { width: 42px; height: 38px; display: flex; align-items: center; justify-content: center;
                 background: #24242a; color: #d8d8dd; border: 1px solid #33333b; border-radius: 7px;
                 font-size: 16px; line-height: 1; cursor: pointer; font-family: inherit;
                 flex: 0 0 auto; position: relative; }
  #rail button:hover { background: #2e2e36; border-color: #45454f; }
  #rail button:active { background: #3a3a44; transform: scale(0.94); }
  #rail button.on { background: #2a4a2e; border-color: #4caf50; color: #a5d6a7; }

  /* Tooltip on hover, since the buttons are icon-only. */
  #rail button::after { content: attr(data-tip); position: absolute; left: 46px; top: 50%%;
                        transform: translateY(-50%%); background: #000; color: #eee;
                        padding: 3px 7px; border-radius: 4px; font-size: 11px; white-space: nowrap;
                        opacity: 0; pointer-events: none; transition: opacity .12s; z-index: 10;
                        border: 1px solid #33333b; }
  #rail button:hover::after { opacity: 1; }

  #rail .sep { height: 1px; background: #2a2a30; margin: 3px 2px; flex: 0 0 auto; }

  #main { flex: 1 1 auto; min-width: 0; display: flex; flex-direction: column; }

  #header { flex: 0 0 auto; display: flex; align-items: center; gap: 10px;
            padding: 4px 10px; background: #17171a; border-bottom: 1px solid #26262b; font-size: 12px; }
  #name { color: #9a9aa2; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  #status { margin-left: auto; color: #4caf50; font-variant-numeric: tabular-nums; white-space: nowrap; }
  #status.bad { color: #f44336; }
  #status.warn { color: #ff9800; }

  #viewport { flex: 1 1 auto; min-height: 0; display: flex; justify-content: center;
              align-items: center; overflow: hidden; padding: 4px; background: #000; }

  /* Both max-width AND max-height with auto width/height: the canvas keeps the
     device aspect ratio and fits whichever dimension is tighter. */
  canvas { display: block; max-width: 100%%; max-height: 100%%; width: auto; height: auto;
           background: #000; cursor: crosshair; }

  #msg { position: fixed; bottom: 10px; left: 62px; background: #000c; color: #ddd;
         padding: 5px 10px; border-radius: 5px; font-size: 11px; opacity: 0;
         transition: opacity .2s; pointer-events: none; border: 1px solid #33333b; }
  #msg.show { opacity: 1; }

  @media (max-width: 480px) {
    #rail { width: 46px; padding: 5px 4px; }
    #rail button { width: 38px; height: 34px; font-size: 15px; }
  }
</style>
</head>
<body>
<div id="rail">
  <button data-tip="Back"            data-key="4">◀</button>
  <button data-tip="Home"            data-key="3">●</button>
  <button data-tip="Recents"         data-key="187">■</button>
  <div class="sep"></div>
  <button data-tip="Volume up"       data-key="24">＋</button>
  <button data-tip="Volume down"     data-key="25">－</button>
  <button id="btn-power"  data-tip="Panel power (device keeps running)">⏻</button>
  <div class="sep"></div>
  <button id="btn-rotate"   data-tip="Rotate display">⟳</button>
  <button id="btn-notif"    data-tip="Notification panel">☰</button>
  <button id="btn-settings" data-tip="Quick settings">⚙</button>
  <button id="btn-collapse" data-tip="Collapse panels">✕</button>
  <div class="sep"></div>
  <button id="btn-kbd"  class="on" data-tip="Keyboard → device (on)">⌨</button>
  <button id="btn-clip" data-tip="Copy device clipboard here">📋</button>
  <button id="btn-wake" data-tip="Wake screen">☀</button>
</div>

<div id="main">
  <div id="header">
    <span id="name">%s</span>
    <span id="status" class="warn">connecting…</span>
  </div>
  <div id="viewport"><canvas id="canvas" tabindex="0"></canvas></div>
</div>

<div id="msg"><textarea id="kbd" style="position:absolute;left:-9999px;top:0;width:1px;height:1px;opacity:0"></textarea>

<script>
const canvas  = document.getElementById('canvas');
const ctx     = canvas.getContext('2d', { alpha: false });
const statusEl= document.getElementById('status');
const msgEl   = document.getElementById('msg');
const kbd     = document.getElementById('kbd');

let decoder = null;
let videoWidth = 0, videoHeight = 0;
let waitingForKey = true;
let keyboardMode = true;
let panelOn = true;
let ws = null;

// Control message types, mirroring protocol.go. Duplicated here because the page
// has no build step; both sides must stay in step.
const MSG_KEYCODE = 0, MSG_TEXT = 1, MSG_TOUCH = 2, MSG_SCROLL = 3, MSG_BACK_WAKE = 4;
const MSG_EXPAND_NOTIF = 5, MSG_EXPAND_SETTINGS = 6, MSG_COLLAPSE = 7;
const MSG_GET_CLIP = 8, MSG_SET_CLIP = 9, MSG_POWER = 10, MSG_ROTATE = 11, MSG_RESET_VIDEO = 17;

const ACTION_DOWN = 0, ACTION_UP = 1, ACTION_MOVE = 2;
const BTN_NONE = 0, BTN_PRIMARY = 1;

function setStatus(text, cls) {
  statusEl.textContent = text;
  statusEl.className = cls || '';
}
function flash(text) {
  msgEl.textContent = text;
  msgEl.classList.add('show');
  clearTimeout(flash._t);
  flash._t = setTimeout(() => msgEl.classList.remove('show'), 2200);
}

// ---- video ----------------------------------------------------------------

function initDecoder() {
  if (!('VideoDecoder' in window)) {
    setStatus('WebCodecs unsupported', 'bad');
    return false;
  }
  decoder = new VideoDecoder({
    output: (frame) => {
      if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
        // Backing store follows the video pixels; CSS scales the element. Keeping
        // the two separate is what makes the image crisp and the layout responsive.
        canvas.width  = frame.displayWidth;
        canvas.height = frame.displayHeight;
        videoWidth  = frame.displayWidth;
        videoHeight = frame.displayHeight;
      }
      ctx.drawImage(frame, 0, 0);
      frame.close();
    },
    error: (e) => setStatus('decode error', 'bad'),
  });
  return true;
}
initDecoder();

function connect() {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  ws = new WebSocket(proto + '//' + location.host + '/ws');
  ws.binaryType = 'arraybuffer';

  ws.onopen = () => setStatus('live', '');
  ws.onclose = () => {
    setStatus('reconnecting…', 'warn');
    setTimeout(connect, 1000);          // the session outlives any one tab
  };
  ws.onerror = () => setStatus('socket error', 'bad');

  ws.onmessage = (e) => {
    const data = new Uint8Array(e.data);
    if (!decoder) return;

    // A buffer may hold several NAL units: a keyframe arrives as SPS+PPS+IDR in
    // one piece. Scan all of them, not just the first.
    let hasSPS = false, hasIDR = false, codecStr = null;
    for (let i = 0; i + 4 < data.length; i++) {
      if (data[i] === 0 && data[i+1] === 0 && (data[i+2] === 1 || (data[i+2] === 0 && data[i+3] === 1))) {
        const off = (data[i+2] === 1) ? i + 3 : i + 4;
        const nal = data[off] & 0x1f;
        if (nal === 7) {
          hasSPS = true;
          const hex = (n) => n.toString(16).padStart(2, '0').toUpperCase();
          codecStr = 'avc1.' + hex(data[off+1]) + hex(data[off+2]) + hex(data[off+3]);
        }
        if (nal === 5) hasIDR = true;
      }
    }

    if (hasSPS && decoder.state !== 'configured') {
      decoder.configure({ codec: codecStr || 'avc1.42E01E', optimizeForLatency: true });
      waitingForKey = true;
    }
    if (decoder.state !== 'configured') return;

    // WebCodecs requires the first chunk after configure() to be a keyframe. A
    // delta here makes it reject the frame and stay broken for the whole session,
    // which looks exactly like a stream that never started.
    if (waitingForKey) {
      if (!hasIDR) return;
      waitingForKey = false;
    }

    try {
      decoder.decode(new EncodedVideoChunk({
        type: hasIDR ? 'key' : 'delta',
        timestamp: performance.now() * 1000,
        data,
      }));
    } catch (_) {}
  };
}
connect();

// ---- control helpers ------------------------------------------------------

function send(bytes) {
  if (ws && ws.readyState === WebSocket.OPEN) ws.send(new Uint8Array(bytes));
}
function u32(view, off, v) { view.setUint32(off, v, false); }
function u16(view, off, v) { view.setUint16(off, v, false); }

function sendKeycode(keycode) {
  for (const action of [0, 1]) {
    const b = new ArrayBuffer(14), v = new DataView(b);
    v.setUint8(0, MSG_KEYCODE); v.setUint8(1, action);
    u32(v, 2, keycode); u32(v, 6, 0); u32(v, 10, 0);
    send(b);
  }
}

function sendText(text) {
  const enc = new TextEncoder().encode(text.slice(0, 300));
  const b = new ArrayBuffer(5 + enc.length), v = new DataView(b);
  v.setUint8(0, MSG_TEXT); u32(v, 1, enc.length);
  new Uint8Array(b, 5).set(enc);
  send(b);
}

function sendTouch(action, x, y) {
  if (!videoWidth) return;
  const buttons = action === ACTION_UP ? BTN_NONE : BTN_PRIMARY;
  const b = new ArrayBuffer(32), v = new DataView(b);
  v.setUint8(0, MSG_TOUCH); v.setUint8(1, action);
  v.setBigUint64(2, 0n, false);
  u32(v, 10, x); u32(v, 14, y);
  u16(v, 18, videoWidth); u16(v, 20, videoHeight);
  u16(v, 22, 0xffff);           // pressure 1.0 in u16 fixed point
  u32(v, 24, buttons); u32(v, 28, buttons);
  send(b);
}

function sendScroll(x, y, h, v) {
  if (!videoWidth) return;
  const b = new ArrayBuffer(21), dv = new DataView(b);
  dv.setUint8(0, MSG_SCROLL);
  u32(dv, 1, x); u32(dv, 5, y);
  u16(dv, 9, videoWidth); u16(dv, 11, videoHeight);
  dv.setInt16(13, Math.round(h * 16), false);
  dv.setInt16(15, Math.round(v * 16), false);
  u32(dv, 17, 0);
  send(b);
}

function sendSimple(type) { send(new Uint8Array([type])); }
function sendClipboard(text, paste) {
  const enc = new TextEncoder().encode(text);
  const b = new ArrayBuffer(14 + enc.length), v = new DataView(b);
  v.setUint8(0, MSG_SET_CLIP);
  v.setBigUint64(1, BigInt(Date.now()), false);
  v.setUint8(9, paste ? 1 : 0);
  u32(v, 10, enc.length);
  new Uint8Array(b, 14).set(enc);
  send(b);
}

// ---- pointer input --------------------------------------------------------

// Convert a browser pointer position to video pixels. Both dimensions are
// scaled independently by the CSS box, so each needs its own ratio.
function toVideo(ev) {
  const r = canvas.getBoundingClientRect();
  const cx = (ev.clientX !== undefined) ? ev.clientX : ev.touches[0].clientX;
  const cy = (ev.clientY !== undefined) ? ev.clientY : ev.touches[0].clientY;
  return {
    x: Math.max(0, Math.min(videoWidth  - 1, Math.round((cx - r.left) * videoWidth  / r.width))),
    y: Math.max(0, Math.min(videoHeight - 1, Math.round((cy - r.top)  * videoHeight / r.height))),
  };
}

let pointerDown = false;

canvas.addEventListener('mousedown', (e) => { e.preventDefault(); pointerDown = true; const p = toVideo(e); sendTouch(ACTION_DOWN, p.x, p.y); });
canvas.addEventListener('mousemove', (e) => { if (!pointerDown) return; const p = toVideo(e); sendTouch(ACTION_MOVE, p.x, p.y); });
window.addEventListener('mouseup',  (e) => { if (!pointerDown) return; pointerDown = false; const p = toVideo(e); sendTouch(ACTION_UP, p.x, p.y); });

canvas.addEventListener('touchstart', (e) => { e.preventDefault(); const p = toVideo(e); sendTouch(ACTION_DOWN, p.x, p.y); }, {passive:false});
canvas.addEventListener('touchmove',  (e) => { e.preventDefault(); const p = toVideo(e); sendTouch(ACTION_MOVE, p.x, p.y); }, {passive:false});
canvas.addEventListener('touchend',   (e) => { e.preventDefault(); const t = e.changedTouches[0]; const r = canvas.getBoundingClientRect();
  sendTouch(ACTION_UP,
    Math.max(0, Math.min(videoWidth-1,  Math.round((t.clientX - r.left) * videoWidth  / r.width))),
    Math.max(0, Math.min(videoHeight-1, Math.round((t.clientY - r.top)  * videoHeight / r.height)))); }, {passive:false});

// Wheel scrolls the device rather than the page.
canvas.addEventListener('wheel', (e) => {
  e.preventDefault();
  const p = toVideo(e);
  const dy = e.deltaY > 0 ? 1 : -1;
  const dx = e.deltaX > 0 ? 1 : (e.deltaX < 0 ? -1 : 0);
  sendScroll(p.x, p.y, dx, dy);
}, {passive:false});

// ---- keyboard -------------------------------------------------------------

// Printable characters go through inject-text, which handles any Unicode and the
// device's own IME. Only the keys that have no text equivalent use keycodes.
const SPECIAL_KEYS = {
  'Enter': 66, 'Backspace': 67, 'Tab': 61, 'Escape': 111,
  'ArrowUp': 19, 'ArrowDown': 20, 'ArrowLeft': 21, 'ArrowRight': 22,
  'Delete': 112, 'Home': 122, 'End': 123, 'PageUp': 92, 'PageDown': 93,
  ' ': 62,
};

window.addEventListener('keydown', (e) => {
  if (!keyboardMode) return;
  if (e.target === kbd) return;           // the off-screen field handles its own
  if (e.metaKey || e.ctrlKey && e.key !== 'v') return;

  if (SPECIAL_KEYS[e.key] !== undefined) {
    e.preventDefault();
    sendKeycode(SPECIAL_KEYS[e.key]);
    return;
  }
  if (e.key.length === 1) {
    e.preventDefault();
    sendText(e.key);
  }
});

// Ctrl/Cmd+V bridges the host clipboard into the device.
window.addEventListener('paste', (e) => {
  const text = (e.clipboardData || window.clipboardData).getData('text');
  if (text) { e.preventDefault(); sendClipboard(text, false); flash('clipboard → device'); }
});

// ---- toolbar --------------------------------------------------------------

document.querySelectorAll('button[data-key]').forEach((btn) => {
  btn.onclick = () => sendKeycode(parseInt(btn.dataset.key, 10));
});

document.getElementById('btn-power').onclick = () => {
  panelOn = !panelOn;
  send(new Uint8Array([MSG_POWER, panelOn ? 1 : 0]));
  flash(panelOn ? 'panel on' : 'panel off (device keeps running)');
};

document.getElementById('btn-rotate').onclick  = () => sendSimple(MSG_ROTATE);
document.getElementById('btn-notif').onclick   = () => sendSimple(MSG_EXPAND_NOTIF);
document.getElementById('btn-settings').onclick= () => sendSimple(MSG_EXPAND_SETTINGS);
document.getElementById('btn-collapse').onclick= () => sendSimple(MSG_COLLAPSE);

document.getElementById('btn-kbd').onclick = (e) => {
  keyboardMode = !keyboardMode;
  e.currentTarget.classList.toggle('on', keyboardMode);
  flash(keyboardMode ? 'keyboard → device' : 'keyboard captured by page');
};

document.getElementById('btn-wake').onclick = () => {
  send(new Uint8Array([MSG_POWER, 1]));
  panelOn = true;
  flash('panel on');
};

document.getElementById('btn-clip').onclick = () => {
  send(new Uint8Array([MSG_GET_CLIP, 0]));
  flash('requested device clipboard');
};

// Keep focus so keystrokes reach the handler rather than the page chrome.
window.addEventListener('click', (e) => {
  if (e.target.tagName !== 'BUTTON') canvas.focus();
});
</script>
</body>
</html>
`
