package screen

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// The control-message encoders are pure byte assembly against a wire format
// defined by the on-device server. Every test here pins a layout that a bug
// once broke, so the reason for each expectation is a real incident rather
// than a restatement of the code.

func TestInjectScrollLength(t *testing.T) {
	// Regression: this packet was built in a 20-byte buffer while its fields
	// need 21, so binary.BigEndian.PutUint32 on the buttons field panicked on
	// a three-byte slice. The panic killed the whole MCP process.
	//
	// Layout: type(1) x(4) y(4) w(2) h(2) hScroll(2) vScroll(2) buttons(4)
	const want = 1 + 4 + 4 + 2 + 2 + 2 + 2 + 4
	got := InjectScroll(100, 200, 1080, 1920, 1.0, -1.0, 0)
	if len(got) != want {
		t.Fatalf("InjectScroll length = %d, want %d", len(got), want)
	}
	if got[0] != msgInjectScroll {
		t.Errorf("type byte = %d, want %d", got[0], msgInjectScroll)
	}
	if x := binary.BigEndian.Uint32(got[1:5]); x != 100 {
		t.Errorf("x = %d, want 100", x)
	}
	if y := binary.BigEndian.Uint32(got[5:9]); y != 200 {
		t.Errorf("y = %d, want 200", y)
	}
	if w := binary.BigEndian.Uint16(got[9:11]); w != 1080 {
		t.Errorf("w = %d, want 1080", w)
	}
	if h := binary.BigEndian.Uint16(got[11:13]); h != 1920 {
		t.Errorf("h = %d, want 1920", h)
	}
	// Scroll values are 16.16 fixed point: 1.0 becomes 16, -1.0 becomes -16.
	if hs := int16(binary.BigEndian.Uint16(got[13:15])); hs != 16 {
		t.Errorf("hScroll = %d, want 16", hs)
	}
	if vs := int16(binary.BigEndian.Uint16(got[15:17])); vs != -16 {
		t.Errorf("vScroll = %d, want -16", vs)
	}
	// The last four bytes must be writable, which is what the panic was about.
	if b := binary.BigEndian.Uint32(got[17:21]); b != 0 {
		t.Errorf("buttons = %d, want 0", b)
	}
}

func TestInjectTouchLayout(t *testing.T) {
	// 32-byte fixed layout: the device parses it with a struct read, so a
	// single byte of drift shifts every later field.
	got := InjectTouch(ActionDown, 1, 500, 600, 1080, 1920, 0xFFFF, ButtonPrimary, ButtonPrimary)
	if len(got) != 32 {
		t.Fatalf("length = %d, want 32", len(got))
	}
	if got[0] != msgInjectTouch {
		t.Errorf("type = %d, want %d", got[0], msgInjectTouch)
	}
	if got[1] != ActionDown {
		t.Errorf("action = %d, want %d", got[1], ActionDown)
	}
	// Pointer id is 8 bytes big-endian at offset 2.
	if id := binary.BigEndian.Uint64(got[2:10]); id != 1 {
		t.Errorf("pointerID = %d, want 1", id)
	}
	if x := binary.BigEndian.Uint32(got[10:14]); x != 500 {
		t.Errorf("x = %d, want 500", x)
	}
	if y := binary.BigEndian.Uint32(got[14:18]); y != 600 {
		t.Errorf("y = %d, want 600", y)
	}
	if w := binary.BigEndian.Uint16(got[18:20]); w != 1080 {
		t.Errorf("w = %d, want 1080", w)
	}
	if h := binary.BigEndian.Uint16(got[20:22]); h != 1920 {
		t.Errorf("h = %d, want 1920", h)
	}
	if p := binary.BigEndian.Uint16(got[22:24]); p != 0xFFFF {
		t.Errorf("pressure = %d, want 0xFFFF", p)
	}
}

func TestInjectTextRuneBoundary(t *testing.T) {
	// maxTextLen is a byte budget, not a rune budget. Truncating mid-rune
	// produces invalid UTF-8 that the device rejects, so the cut must land on
	// a rune boundary.
	text := ""
	for i := 0; i < maxTextLen; i++ {
		text += "中" // 3 bytes each, so this is far over budget
	}
	got := InjectText(text)

	if len(got) < 5 {
		t.Fatalf("packet too short: %d", len(got))
	}
	n := int(binary.BigEndian.Uint32(got[1:5]))
	payload := got[5:]
	if n != len(payload) {
		t.Fatalf("declared length %d != payload %d", n, len(payload))
	}
	if n > maxTextLen {
		t.Errorf("payload %d exceeds budget %d", n, maxTextLen)
	}
	// The cut point must be a valid rune boundary: decoding must not error and
	// must reproduce exactly the bytes that were sent.
	for i := 0; i < len(payload); {
		r, size := decodeRune(payload[i:])
		if r == 0xFFFD && size == 1 {
			t.Fatalf("payload cut mid-rune at byte %d", i)
		}
		i += size
	}
}

// decodeRune is a minimal UTF-8 decoder so the test does not depend on the
// package's own helper (a bug there would make the test vacuous).
func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return 0, 0
	}
	if b[0] < 0x80 {
		return rune(b[0]), 1
	}
	if b[0]&0xE0 == 0xC0 && len(b) >= 2 {
		return rune(b[0]&0x1F)<<6 | rune(b[1]&0x3F), 2
	}
	if b[0]&0xF0 == 0xE0 && len(b) >= 3 {
		return rune(b[0]&0x0F)<<12 | rune(b[1]&0x3F)<<6 | rune(b[2]&0x3F), 3
	}
	if b[0]&0xF8 == 0xF0 && len(b) >= 4 {
		return rune(b[0]&0x07)<<18 | rune(b[1]&0x3F)<<12 | rune(b[2]&0x3F)<<6 | rune(b[3]&0x3F), 4
	}
	return 0xFFFD, 1
}

func TestSetClipboardLayout(t *testing.T) {
	// type(1) sequence(8) paste(1) len(4) text(n)
	got := SetClipboard(42, "hello", true)
	if len(got) != 14+5 {
		t.Fatalf("length = %d, want 19", len(got))
	}
	if got[0] != msgSetClipboard {
		t.Errorf("type = %d", got[0])
	}
	if seq := binary.BigEndian.Uint64(got[1:9]); seq != 42 {
		t.Errorf("sequence = %d, want 42", seq)
	}
	if got[9] != 1 {
		t.Errorf("paste = %d, want 1", got[9])
	}
	if n := binary.BigEndian.Uint32(got[10:14]); n != 5 {
		t.Errorf("len = %d, want 5", n)
	}
	if string(got[14:]) != "hello" {
		t.Errorf("text = %q", got[14:])
	}
}

func TestInjectKeycodeLayout(t *testing.T) {
	// type(1) action(1) keycode(4) repeat(4) metaState(4)
	got := InjectKeycode(ActionDown, 3, 0, 0)
	if len(got) != 14 {
		t.Fatalf("length = %d, want 14", len(got))
	}
	if got[1] != ActionDown {
		t.Errorf("action = %d", got[1])
	}
	if k := binary.BigEndian.Uint32(got[2:6]); k != 3 {
		t.Errorf("keycode = %d, want 3", k)
	}
}

func TestParseDeviceEventPartial(t *testing.T) {
	// A reader gets arbitrary chunk boundaries. Returning "need more data" for
	// a short buffer is what lets the control loop accumulate instead of
	// discarding the tail of every read.
	ev, consumed, err := ParseDeviceEvent([]byte{devClipboard, 0, 0})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ev != nil || consumed != 0 {
		t.Fatalf("partial buffer should need more data, got ev=%v consumed=%d", ev, consumed)
	}
}

func TestParseDeviceEventAckClipboard(t *testing.T) {
	// type(1) sequence(8) = 9 bytes. The probe in Start() depends on this
	// message being parsed correctly, because ACK is what identifies the
	// control socket among the candidates.
	buf := make([]byte, 9)
	buf[0] = devAckClipboard
	binary.BigEndian.PutUint64(buf[1:9], 7)

	ev, consumed, err := ParseDeviceEvent(buf)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if ev == nil {
		t.Fatal("expected an event")
	}
	if consumed != 9 {
		t.Errorf("consumed = %d, want 9", consumed)
	}
	if ev.Type != devAckClipboard {
		t.Errorf("type = %d, want %d", ev.Type, devAckClipboard)
	}
	if ev.Sequence != 7 {
		t.Errorf("sequence = %d, want 7", ev.Sequence)
	}
}

func TestParseDeviceEventGeometry(t *testing.T) {
	// type(1) rotation(1) width(4) height(4) = 10 bytes. Rotation arrives here
	// and drives both the canvas aspect box and the coordinate mapping, so a
	// wrong parse puts every touch in the wrong place.
	buf := make([]byte, 10)
	buf[0] = devDisplayGeometry
	buf[1] = 1
	binary.BigEndian.PutUint32(buf[2:6], 1080)
	binary.BigEndian.PutUint32(buf[6:10], 1920)

	ev, consumed, err := ParseDeviceEvent(buf)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}
	if ev == nil {
		t.Fatal("expected an event")
	}
	if consumed != 10 {
		t.Errorf("consumed = %d, want 10", consumed)
	}
	if ev.Rotation != 1 || ev.Width != 1080 || ev.Height != 1920 {
		t.Errorf("geometry = rot:%d %dx%d, want rot:1 1080x1920", ev.Rotation, ev.Width, ev.Height)
	}
}

func TestFrameTags(t *testing.T) {
	// Both streams share one WebSocket. Without the tag the browser cannot tell
	// an Opus packet from an H.264 NAL, and it fails silently.
	payload := []byte{1, 2, 3}

	if got := TagVideo(payload); got[0] != FrameVideo || !bytes.Equal(got[1:], payload) {
		t.Errorf("TagVideo = %v", got)
	}
	if got := TagAudio(payload); got[0] != FrameAudio || !bytes.Equal(got[1:], payload) {
		t.Errorf("TagAudio = %v", got)
	}
	if got := TagInfo(payload); got[0] != FrameInfo || !bytes.Equal(got[1:], payload) {
		t.Errorf("TagInfo = %v", got)
	}
}

func TestUhidCreateLayout(t *testing.T) {
	// type(1) id(2) vendor(2) product(2) nameLen(1) name(n) descLen(2) desc(m)
	desc := []byte{0xAA, 0xBB}
	got := UhidCreate(1, 0x1234, 0x5678, "kbd", desc)

	want := 8 + 3 + 2 + 2
	if len(got) != want {
		t.Fatalf("length = %d, want %d", len(got), want)
	}
	if got[0] != msgUhidCreate {
		t.Errorf("type = %d", got[0])
	}
	if id := binary.BigEndian.Uint16(got[1:3]); id != 1 {
		t.Errorf("id = %d", id)
	}
	if v := binary.BigEndian.Uint16(got[3:5]); v != 0x1234 {
		t.Errorf("vendor = %#x", v)
	}
	if p := binary.BigEndian.Uint16(got[5:7]); p != 0x5678 {
		t.Errorf("product = %#x", p)
	}
	if got[7] != 3 {
		t.Errorf("nameLen = %d, want 3", got[7])
	}
	if string(got[8:11]) != "kbd" {
		t.Errorf("name = %q", got[8:11])
	}
	if n := binary.BigEndian.Uint16(got[11:13]); n != 2 {
		t.Errorf("descLen = %d, want 2", n)
	}
	if !bytes.Equal(got[13:], desc) {
		t.Errorf("desc = %v", got[13:])
	}
}

func TestUhidInputLayout(t *testing.T) {
	// type(1) id(2) size(2) data(n)
	got := UhidInput(2, []byte{1, 2, 3, 4})
	if len(got) != 9 {
		t.Fatalf("length = %d, want 9", len(got))
	}
	if got[0] != msgUhidInput {
		t.Errorf("type = %d", got[0])
	}
	if id := binary.BigEndian.Uint16(got[1:3]); id != 2 {
		t.Errorf("id = %d", id)
	}
	if n := binary.BigEndian.Uint16(got[3:5]); n != 4 {
		t.Errorf("size = %d, want 4", n)
	}
}

func TestAudioCodecFromID(t *testing.T) {
	// The device sends a four-character code. Getting this wrong means the
	// browser configures its decoder for the wrong codec, which produces
	// silence rather than an error.
	cases := []struct {
		id   uint32
		want AudioCodec
	}{
		{0x6f707573, AudioOpus}, // "opus"
		{0x666c6163, AudioFlac}, // "flac"
	}
	for _, c := range cases {
		if got := audioCodecFromID(c.id); got != c.want {
			t.Errorf("audioCodecFromID(%#x) = %v, want %v", c.id, got, c.want)
		}
	}
}

func TestSimpleMessages(t *testing.T) {
	// Single-byte control messages carry only their type. A stray extra byte
	// shifts the device's parser for every subsequent message on the socket.
	for name, pkt := range map[string][]byte{
		"expand_notifications": ExpandNotificationPanel(),
		"expand_settings":      ExpandSettingsPanel(),
		"collapse_panels":      CollapsePanels(),
		"rotate":               RotateDevice(),
		"reset_video":          ResetVideo(),
	} {
		if len(pkt) != 1 {
			t.Errorf("%s: length = %d, want 1", name, len(pkt))
		}
	}
}

func TestSetVideoEnabled(t *testing.T) {
	if got := SetVideoEnabled(true); len(got) != 2 || got[1] != 1 {
		t.Errorf("SetVideoEnabled(true) = %v", got)
	}
	if got := SetVideoEnabled(false); len(got) != 2 || got[1] != 0 {
		t.Errorf("SetVideoEnabled(false) = %v", got)
	}
}

func TestKeycodeScancodeMap(t *testing.T) {
	// Every mapped keycode must resolve to a plausible HID usage id. A zero
	// here would send "no key pressed" as a keystroke.
	for keycode, scan := range KeycodeToScancode {
		if scan == 0 {
			t.Errorf("keycode %d maps to scancode 0", keycode)
		}
	}
}

func TestASCIItoScancode(t *testing.T) {
	// The HID keyboard usage page runs a-z from 0x04 and 1-9 from 0x1e.
	if s, ok := asciiToScancode('a'); !ok || s != 0x04 {
		t.Errorf("'a' = %#x, %v", s, ok)
	}
	if s, ok := asciiToScancode('z'); !ok || s != 0x1d {
		t.Errorf("'z' = %#x, %v", s, ok)
	}
	if s, ok := asciiToScancode('1'); !ok || s != 0x1e {
		t.Errorf("'1' = %#x, %v", s, ok)
	}
	// Uppercase maps to the same usage id; the shift modifier is applied
	// separately by the caller.
	if s, ok := asciiToScancode('A'); !ok || s != 0x04 {
		t.Errorf("'A' = %#x, %v", s, ok)
	}
	if _, ok := asciiToScancode('中'); ok {
		t.Error("non-ASCII should not map")
	}
}
