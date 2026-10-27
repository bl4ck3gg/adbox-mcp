# screen — known gaps

State: streaming works, 13 control buttons live, MCP tools pass. ~60% of a
complete screen feature. What follows is what is missing, roughly by how much
it would be noticed.

## Missing outright

- **Audio.** The server is launched with `audio=false` and the audio socket is
  never connected. Enabling it means: a third socket accept, an AAC stream on
  the same WebSocket with a type tag, and WebCodecs `AudioDecoder` plus an
  `AudioContext` on the page. Android 10+ only.

- **Multi-device.** `Manager` keys sessions by serial and the viewer serves one
  device per port. A rack view needs a multiplexed endpoint and a device picker.

- **Reconnect from the device side.** If scrcpy dies (encoder crash, app
  rotation storm), the session is dead until `device_screen_start` is called
  again. The reference implementation supervised and restarted.

- **Input while the screen is off.** `SetDisplayPower(false)` keeps the panel
  dark and uiautomator working, but touches are dropped by the device because
  the window is not interactive. Needs a wake-then-act, or UHID.

- **UHID (types 12/13/14).** Physical-keyboard and mouse emulation. The protocol
  is implemented nowhere yet; `ParseDeviceEvent` also ignores UHID output.

## Present but unverified

- **`device_screen_text` on a real IME.** Verified that the packet is accepted;
  not verified that Chinese text lands correctly in an app with a soft keyboard
  open.
- **Scroll direction and magnitude.** Sign conventions differ between devices;
  only one Note 9 was tried.
- **Rotation.** The packet is sent and the geometry message is parsed, but the
  canvas does not resize on rotation — the video's own resolution change is what
  currently rescales it, and the two can disagree mid-rotation.
- **Clipboard read-back.** `device_screen_clipboard` without text returns what
  the device last pushed. On Android 10 the server only pushes on change, so a
  freshly attached viewer often sees an empty string until the clipboard changes.

## Quality

- **No touch multi-pointer.** Only pointerId 0. Pinch-zoom needs a pointer table
  on both sides.
- **No frame pacing.** Frames are forwarded as they arrive; a burst after a stall
  plays back at whatever speed the network delivers. A small jitter buffer would
  smooth it.
- **No keyframe request on packet loss.** WebSocket is reliable so this rarely
  bites, but a viewer that falls behind stays behind until the next IDR
  (`i-frame-interval=1` bounds that at one second).
- **The page duplicates the protocol constants.** No build step, so
  `protocol.go` and the inline script must be kept in step by hand.
- **No auth.** The bind address is the only access control. Fine on loopback,
  questionable with `--screen-host 0.0.0.0`.

## What was deliberately not copied from mr-robot

- WebRTC + TURN + ICE (5,213 lines). Replaced by WebSocket + WebCodecs.
- The per-device supervisor state machine. Not needed while a session is
  single-shot; needed the moment reconnection is added.
- `ControlOnly` mode (control without video) and `DeviceScrcpyPool`. Neither has
  a caller here yet.
