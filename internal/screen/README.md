# screen

Live device screen streaming over one WebSocket, decoded by the browser's own
WebCodecs hardware decoder.

## Why not WebRTC

The reference implementation this was harvested from used WebRTC with a TURN
server. That is 5,200 lines of Go and a dependency tree (pion, DTLS, SCTP, ICE)
compensating for problems a forwarded TCP port does not have: it traverses NAT,
handles many viewers on a lossy public link, and negotiates paths it cannot take.
Here the transport is `adb forward`, one viewer-per-tab on loopback, and the
farthest the bytes travel is across a SOCKS5 tunnel we already own.

The stream is plain Annex-B H.264, which means a real streaming middleware
(MediaMTX, SRS) can be dropped in later to fan out to WebRTC for anyone who needs
it, without this package changing.

## The protocol

```
[dummy 1B]          only ever on the FIRST accepted socket
[64B device name]   "SM-N960U1..."  (sendDeviceMeta)
[12B video header]  "h264" | width:u32 | height:u32
repeat:
  [8B]  bit63 = config (SPS/PPS), bit62 = keyframe, low 62 bits = PTS in us
  [4B]  payload length
  [N B] one NAL unit
```

## The trap that costs hours

Video encoding starts **disabled** in this build of the server. Two control
messages, `0x12 0x01` (enable video) and `0x13 0x01` (enable audio), are a local
extension -- upstream scrcpy has no such message types. Without sending
`0x12 0x01` right after the control socket connects, the encoder thread waits on
a flag that nothing sets, the 12-byte video header is never written, and the
video socket delivers the device name and then silence forever.

It looks exactly like a broken tunnel, and no amount of debugging the transport
will explain it.

## Sockets

`tunnel_forward=true` makes the device listen on `localabstract:scrcpy_<scid>`.
Connections are accepted in order: video, then audio, then control -- so the
sockets must be opened in that order, and the dummy byte is written only to the
video one.
