# Feature gap analysis: mr-robot vs adbox-mcp

Reference: `~/workdir/mr-robot/mr-robot` (Go, 14 MCP tools) plus
`~/workdir/mr-robot/webview-app` (WebView container) and
`~/workdir/mr-robot/mr-robot-agent` (on-device Java).

This file exists so the comparison does not have to be redone. Each entry names
the reference file, so the behaviour can be read rather than guessed.

## Status legend
- **HAVE** — implemented and verified
- **PART** — implemented, weaker than the reference
- **MISS** — not implemented
- **N/A** — deliberately excluded (group control, WebRTC)

---

## 1. MCP tools

Reference: `internal/mcp/tools.go` (14 tools)

| Reference tool | adbox equivalent | Status |
|---|---|---|
| `devices` | `devices_list` / `device_info` | HAVE |
| `endpoints` | `endpoints_add` / `endpoints_list` / `endpoints_remove` | HAVE |
| `shell_exec` | `device_shell` / `device_spawn` | HAVE |
| `display_power` | `device_screen_action power/sleep/wake` | HAVE |
| `scrcpy_audio` | `device_screen_video` (audio auto-discovered) | PART — no explicit audio toggle |
| `tethering` | `device_tether_start` / `device_tether_stop` | PART — no loss-rate control |
| `u2_jsonrpc` | `device_ui_*` + `device_eval` | PART — narrower than raw u2 JSON-RPC |
| `files` | `device_push` (base64 inline) | PART — no MD5 dedupe centre |
| `adb_file` | `device_push` | PART — no separate install step |
| `proxy_get` | **missing** | MISS |
| `forward_http` | **missing** | MISS |
| `webview` | **missing** | MISS |
| `webview_devtools` | **missing** | MISS |
| `settings` | **missing** | MISS |

### Missing tools, in detail

**`proxy_get`** — `internal/api/proxy_handler.go`, `internal/apk/pty_socks5.go`
Returns a SOCKS5 URL that routes through a specific device, so the device's own
network becomes an egress. Two flavours: `direct` (local bridge port) and
`forward` (global gateway, username = UDID). This is what makes a phone usable
as a proxy node — a capability nothing in adbox covers today.

**`forward_http`** — `internal/api/forward_handler.go`
Issues one HTTP request to a port on the device through the agent tunnel,
response capped at 1 MB. Useful for reaching a service the device runs (a
local dev server, a WebView DevTools endpoint) without a persistent forward.

**`webview`** — `internal/api/webview_handler.go` + `webview-app/`
install / start(url) / stop. The APK is a WebView container that calls
`WebView.setWebContentsDebuggingEnabled(true)`, so Chrome DevTools can attach.
Intent extras: `url`, `data_directory`, `js_bridge_name`.
`WebViewActivity.kt` is 132 lines and holds the whole contract.

**`webview_devtools`** — `internal/service/webview_devtools.go` (215 lines)
Scans `/proc/net/unix` for `webview_devtools_remote_*`, forwards each abstract
socket to a host port, and reports `cdp_endpoint` so a CDP client can attach.
Also cleans up stale forwards. This is the bridge between a phone WebView and
`open cdp` in termcp — without it, WebView automation on the device is not
reachable at all.

**`settings`** — `internal/api/settings_handler.go`
get/set server settings. Only `auto_screen_off` today: turn the panel off
automatically when a device connects. Small, but it is the pattern for any
future knob.

---

## 2. Screen mirroring

Reference: `internal/streaming/` (~6 files, scrcpy 2.4 based)

| Capability | Reference | adbox | Status |
|---|---|---|---|
| H.264 video over WebSocket | webrtc.go (WebRTC) | hub.go (WebSocket) | HAVE (different transport by design) |
| Audio capture | `startAudioReader` | `readAudioLoop` | PART — Opus parsed, playback unverified on API 30+ |
| Keyframe on demand | `RequestKeyFrame` | `device_screen_video keyframe` | HAVE |
| Video enable/disable | `SetVideoEnabled` | `device_screen_video enabled` | HAVE |
| Clipboard read | `handleControlStream` | `readControlLoop` | HAVE |
| Clipboard write | `SendControlMessage` | `device_screen_clipboard` | HAVE |
| Rotation geometry | `TYPE_DISPLAY_GEOMETRY` | `readControlLoop` + info frame | PART — canvas resizes, no layout test |
| UHID keyboard/mouse | `UhidManager.java` | `device_screen_uhid` | PART — registers, input unverified |
| Multi-touch / pinch | `injectTouch` pointer table | `InjectTouchPointer` | HAVE |
| Touch / swipe / scroll | `Controller.java` | `device_screen_*` | HAVE |
| Two-finger back | not in reference | `sendBack()` | HAVE (adbox addition) |
| Right-click back | `Controller` KEYCODE_BACK | `sendBack()` | HAVE |
| `ControlOnly` mode | `control_only.go` | **missing** | MISS — control without video |
| Stream supervisor | `stream_supervisor.go` | **missing** | MISS — no auto-restart on encoder death |
| WebRTC / TURN | `webrtc.go`, `turn_server.go` | excluded | N/A — WebSocket chosen deliberately |
| Device pool | `DeviceScrcpyPool` | excluded | N/A — single control only |

### `control_only.go`
Runs scrcpy with `video=false` so only the control channel exists. Costs far
less CPU and battery than a full mirror when all you need is input injection.
Worth having: the encoder is the expensive part and this is the way to skip it.

### `stream_supervisor.go`
Watches a session and restarts it when the encoder dies (app rotation storms,
codec errors, the process being killed). Today a dead session stays dead until
`device_screen_start` is called again.

---

## 3. On-device agent

Reference: `mr-robot-agent/` — Java modules `uia2`, `gnirehtet`, `webview`

| Capability | adbox agent | Status |
|---|---|---|
| UIAutomator2 automation | `adbox-agent` (absorbed from uia2-mcp) | HAVE |
| Reflection evaluator | `Reflect.java` (338 lines) | HAVE — broader than reference |
| gnirehtet VpnService | `TetherController.java` | HAVE |
| WebView container | **missing** | MISS — see `webview` tool above |
| Socks5 server on device | `socks5/Socks5Main.java` | PART — see `proxy_get` |

---

## 4. HTTP API surface

Reference: `internal/api/` (24 handler files)

adbox exposes MCP over stdio only; the reference additionally serves a REST
surface (`/api/devices`, `/api/video`, `/api/webrtc`, `/api/webview`, ...).
That is a deliberate difference — adbox drives everything through MCP tools —
but it means anything the reference did over REST must have an MCP tool here,
which is exactly what the table in section 1 tracks.

---

## 5. Priority

1. **`webview_devtools`** — the only way to reach a phone WebView from CDP.
   Without it, on-device browser automation does not exist.
2. **`proxy_get`** — turns a device into a network egress. Nothing else covers it.
3. **`forward_http`** — small, and the natural companion to `proxy_get`.
4. **`webview`** — install/start/stop; needs the APK built into `embedded/`.
5. **`control_only`** — cheap to add, large power win for input-only sessions.
6. **`stream_supervisor`** — resilience; matters once sessions run for hours.
7. **`settings` / `auto_screen_off`** — smallest, do last.
8. **tethering loss-rate** — weak-network simulation, niche.
