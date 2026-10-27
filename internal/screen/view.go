package screen

import (
	"fmt"
	"net/http"
)

func (s *Session) handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, indexHtml, s.serial)
}

const indexHtml = `<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, user-scalable=no">
<title>adbox screen — %s</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body { height: 100%%; }
  body { background: #111; color: #eee; font-family: system-ui, sans-serif;
         display: flex; flex-direction: column; height: 100vh; height: 100dvh; overflow: hidden; }
  #header { flex: 0 0 auto; width: 100%%; padding: 5px 12px; background: #1a1a1a;
            display: flex; justify-content: space-between; align-items: center;
            font-size: 12px; color: #888; border-bottom: 1px solid #2a2a2a; }
  #status { color: #4caf50; font-variant-numeric: tabular-nums; }
  #viewport { flex: 1 1 auto; min-height: 0; display: flex;
              justify-content: center; align-items: center; width: 100%%; overflow: hidden; }
  /* The canvas keeps the device's aspect ratio and is scaled to FIT the
     viewport, never beyond it. height:auto with max-height is what makes the
     bottom of a tall phone screen visible in a landscape browser window --
     max-width alone leaves the canvas overflowing the bottom. */
  canvas { display: block; max-width: 100%%; max-height: 100%%;
           width: auto; height: auto; object-fit: contain;
           cursor: crosshair; background: #000;
           box-shadow: 0 0 0 1px #2a2a2a; }
</style>
</head>
<body>
<div id="header">
  <span>adbox screen</span>
  <span id="status">connecting...</span>
</div>
<div id="viewport">
  <canvas id="canvas"></canvas>
</div>

<script>
const canvas = document.getElementById('canvas');
const ctx = canvas.getContext('2d');
const status = document.getElementById('status');
let decoder = null;
let videoWidth = 0;
let videoHeight = 0;
let isDown = false;

function initDecoder() {
  if (!('VideoDecoder' in window)) {
    status.innerText = 'Error: WebCodecs not supported in this browser';
    status.style.color = '#f44336';
    return;
  }
  decoder = new VideoDecoder({
    output: (frame) => {
      if (canvas.width !== frame.displayWidth || canvas.height !== frame.displayHeight) {
        // The backing store matches the video pixel size; CSS scales the
        // element to the viewport. Keeping these separate is what lets the
        // image stay crisp and the layout stay responsive.
        canvas.width = frame.displayWidth;
        canvas.height = frame.displayHeight;
        videoWidth = frame.displayWidth;
        videoHeight = frame.displayHeight;
      }
      ctx.drawImage(frame, 0, 0);
      frame.close();
    },
    error: (e) => {
      console.error('decoder error:', e);
      status.innerText = 'Decoder error: ' + e.message;
    }
  });
}
initDecoder();

const wsProto = location.protocol === 'https:' ? 'wss:' : 'ws:';
const ws = new WebSocket(wsProto + '//' + location.host + '/ws');
ws.binaryType = 'arraybuffer';

ws.onopen = () => {
  status.innerText = 'live';
  status.style.color = '#4caf50';
};

ws.onclose = () => {
  status.innerText = 'disconnected';
  status.style.color = '#f44336';
};

let waitingForKey = true;

ws.onmessage = (e) => {
  const data = new Uint8Array(e.data);
  if (!decoder) return;

  // Scan ALL NAL units in this buffer (a buffer can contain [SPS, PPS, IDR])
  let hasSPS = false;
  let hasIDR = false;
  let codecStr = null;

  for (let i = 0; i + 4 < data.length; i++) {
    if (data[i] === 0 && data[i+1] === 0 && (data[i+2] === 1 || (data[i+2] === 0 && data[i+3] === 1))) {
      const offset = (data[i+2] === 1) ? i + 3 : i + 4;
      const nalType = data[offset] & 0x1f;
      if (nalType === 7) {
        hasSPS = true;
        const p = data[offset + 1], c = data[offset + 2], l = data[offset + 3];
        const hex = (n) => n.toString(16).padStart(2, '0').toUpperCase();
        codecStr = 'avc1.' + hex(p) + hex(c) + hex(l);
      }
      if (nalType === 5) {
        hasIDR = true;
      }
    }
  }

  // 1. Configure the decoder on SPS
  if (hasSPS && decoder.state !== 'configured') {
    const codec = codecStr || 'avc1.428020';
    decoder.configure({ codec, optimizeForLatency: true });
    status.innerText = 'live · ' + codec;
    status.style.color = '#4caf50';
    waitingForKey = true; // Chrome demands the very next decode() MUST be a key frame
  }

  if (decoder.state !== 'configured') return;

  // 2. WebCodecs hard requirement: after configure(), the FIRST chunk decoded
  // MUST have type: 'key'. Drop any delta frames that arrive before the keyframe.
  if (waitingForKey) {
    if (!hasIDR) {
      return; // drop delta frames until the first IDR arrives
    }
    waitingForKey = false;
  }

  try {
    decoder.decode(new EncodedVideoChunk({
      type: hasIDR ? 'key' : 'delta',
      timestamp: performance.now() * 1000,
      data: data
    }));
  } catch (err) {
    console.warn('decode error:', err);
  }
};

// Touch / mouse interaction
function sendTouch(action, e) {
  if (videoWidth === 0 || videoHeight === 0 || ws.readyState !== WebSocket.OPEN) return;
  const rect = canvas.getBoundingClientRect();
  const scaleX = videoWidth / rect.width;
  const scaleY = videoHeight / rect.height;
  const clientX = e.clientX !== undefined ? e.clientX : (e.touches && e.touches[0] ? e.touches[0].clientX : 0);
  const clientY = e.clientY !== undefined ? e.clientY : (e.touches && e.touches[0] ? e.touches[0].clientY : 0);
  const x = Math.max(0, Math.min(videoWidth, Math.round((clientX - rect.left) * scaleX)));
  const y = Math.max(0, Math.min(videoHeight, Math.round((clientY - rect.top) * scaleY)));

  // 32-byte scrcpy inject touch packet
  const buf = new ArrayBuffer(32);
  const v = new DataView(buf);
  v.setUint8(0, 2);               // TYPE_INJECT_TOUCH_EVENT
  v.setUint8(1, action);          // 0=DOWN, 1=UP, 2=MOVE
  v.setBigUint64(2, 0n);          // pointerId
  v.setUint32(10, x);
  v.setUint32(14, y);
  v.setUint16(18, videoWidth);
  v.setUint16(20, videoHeight);
  v.setUint16(22, 0xffff);        // pressure 1.0
  v.setUint32(24, 1);             // actionButton
  v.setUint32(28, 1);             // buttons
  ws.send(buf);
}

canvas.addEventListener('mousedown', (e) => { isDown = true; sendTouch(0, e); });
window.addEventListener('mousemove', (e) => { if (isDown) sendTouch(2, e); });
window.addEventListener('mouseup', (e) => { if (isDown) { isDown = false; sendTouch(1, e); } });

canvas.addEventListener('touchstart', (e) => { e.preventDefault(); sendTouch(0, e); }, {passive:false});
canvas.addEventListener('touchmove', (e) => { e.preventDefault(); sendTouch(2, e); }, {passive:false});
canvas.addEventListener('touchend', (e) => { e.preventDefault(); sendTouch(1, e); }, {passive:false});
</script>
</body>
</html>
`
