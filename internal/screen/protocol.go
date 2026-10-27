package screen

import (
	"encoding/binary"
	"fmt"
	"unicode/utf8"
)

// Control message types, mirroring ControlMessage.java on the device.
//
// Every value here was read out of the server's parser rather than guessed: a
// wrong field width does not fail loudly, it shifts every subsequent byte and
// the device simply does nothing.
const (
	msgInjectKeycode     = 0
	msgInjectText        = 1
	msgInjectTouch       = 2
	msgInjectScroll      = 3
	msgBackOrScreenOn    = 4
	msgExpandNotifPanel  = 5
	msgExpandSettings    = 6
	msgCollapsePanels    = 7
	msgGetClipboard      = 8
	msgSetClipboard      = 9
	msgSetDisplayPower   = 10
	msgRotateDevice      = 11
	msgStartApp          = 16
	msgResetVideo        = 17
	msgSetVideoEnabled   = 18
	msgSetAudioEnabled   = 19
)

// Touch actions, matching MotionEvent.
const (
	ActionDown = 0
	ActionUp   = 1
	ActionMove = 2
)

// Buttons, matching MotionEvent.BUTTON_*.
const (
	ButtonNone    = 0
	ButtonPrimary = 1
	ButtonSecond  = 2
)

// Device message types, sent by the server on the control socket.
const (
	devClipboard       = 0
	devAckClipboard    = 1
	devUhidOutput      = 2
	devDisplayGeometry = 3
)

const maxTextLen = 300 // ControlMessageReader.INJECT_TEXT_MAX_LENGTH

// InjectKeycode encodes a key event: type, action, keycode, repeat, metaState.
func InjectKeycode(action, keycode, repeat, metaState int) []byte {
	b := make([]byte, 14)
	b[0] = msgInjectKeycode
	b[1] = byte(action)
	binary.BigEndian.PutUint32(b[2:6], uint32(keycode))
	binary.BigEndian.PutUint32(b[6:10], uint32(repeat))
	binary.BigEndian.PutUint32(b[10:14], uint32(metaState))
	return b
}

// InjectText encodes text: type, 4-byte length, UTF-8 bytes.
//
// The device rejects anything over 300 bytes, and a naive truncation can split a
// multi-byte rune, which then arrives as U+FFFD. Trim on a rune boundary.
func InjectText(text string) []byte {
	raw := []byte(text)
	if len(raw) > maxTextLen {
		cut := maxTextLen
		for cut > 0 && !utf8.Valid(raw[:cut]) {
			cut--
		}
		raw = raw[:cut]
	}
	b := make([]byte, 5+len(raw))
	b[0] = msgInjectText
	binary.BigEndian.PutUint32(b[1:5], uint32(len(raw)))
	copy(b[5:], raw)
	return b
}

// InjectTouch encodes a touch event. x and y are in video pixels; w and h are
// the video dimensions, which the device uses to map onto its real display.
func InjectTouch(action, pointerID, x, y, w, h, pressure, actionButton, buttons int) []byte {
	b := make([]byte, 32)
	b[0] = msgInjectTouch
	b[1] = byte(action)
	binary.BigEndian.PutUint64(b[2:10], uint64(pointerID))
	binary.BigEndian.PutUint32(b[10:14], uint32(x))
	binary.BigEndian.PutUint32(b[14:18], uint32(y))
	binary.BigEndian.PutUint16(b[18:20], uint16(w))
	binary.BigEndian.PutUint16(b[20:22], uint16(h))
	binary.BigEndian.PutUint16(b[22:24], uint16(pressure)) // u16 fixed point: 0xFFFF == 1.0
	binary.BigEndian.PutUint32(b[24:28], uint32(actionButton))
	binary.BigEndian.PutUint32(b[28:32], uint32(buttons))
	return b
}

// InjectScroll encodes a scroll event. Scroll values are i16 fixed point in
// units of 1/16, so the integer part is scaled by 16 before packing.
func InjectScroll(x, y, w, h int, hScroll, vScroll float64, buttons int) []byte {
	// 1 type + 4 x + 4 y + 2 w + 2 h + 2 hScroll + 2 vScroll + 4 buttons = 21
	b := make([]byte, 21)
	b[0] = msgInjectScroll
	binary.BigEndian.PutUint32(b[1:5], uint32(x))
	binary.BigEndian.PutUint32(b[5:9], uint32(y))
	binary.BigEndian.PutUint16(b[9:11], uint16(w))
	binary.BigEndian.PutUint16(b[11:13], uint16(h))
	binary.BigEndian.PutUint16(b[13:15], uint16(int16(hScroll*16)))
	binary.BigEndian.PutUint16(b[15:17], uint16(int16(vScroll*16)))
	binary.BigEndian.PutUint32(b[17:21], uint32(buttons))
	return b
}

// SetDisplayPower turns the panel on or off without touching wakefulness.
func SetDisplayPower(on bool) []byte {
	v := byte(0)
	if on {
		v = 1
	}
	return []byte{msgSetDisplayPower, v}
}

// SetClipboard pushes text to the device clipboard.
// paste=true also asks the device to paste it into the focused field.
func SetClipboard(sequence uint64, text string, paste bool) []byte {
	raw := []byte(text)
	p := byte(0)
	if paste {
		p = 1
	}
	b := make([]byte, 14+len(raw))
	b[0] = msgSetClipboard
	binary.BigEndian.PutUint64(b[1:9], sequence)
	b[9] = p
	binary.BigEndian.PutUint32(b[10:14], uint32(len(raw)))
	copy(b[14:], raw)
	return b
}

// GetClipboard asks the device to send its clipboard back (copyKey: 0 none,
// 1 copy, 2 cut).
func GetClipboard(copyKey int) []byte {
	return []byte{msgGetClipboard, byte(copyKey)}
}

// BackOrScreenOn is the power-button-shaped action: turns the screen on if it is
// off, otherwise acts as Back.
func BackOrScreenOn(action int) []byte {
	return []byte{msgBackOrScreenOn, byte(action)}
}

// StartApp launches an app by package name (1-byte length prefix).
func StartApp(name string) []byte {
	raw := []byte(name)
	b := make([]byte, 2+len(raw))
	b[0] = msgStartApp
	b[1] = byte(len(raw))
	copy(b[2:], raw)
	return b
}

// Simple builds a one-byte message for the types that carry no payload.
func Simple(msgType int) []byte {
	return []byte{byte(msgType)}
}

// Convenience wrappers for the payload-free messages.
func ExpandNotificationPanel() []byte { return Simple(msgExpandNotifPanel) }
func ExpandSettingsPanel() []byte     { return Simple(msgExpandSettings) }
func CollapsePanels() []byte          { return Simple(msgCollapsePanels) }
func RotateDevice() []byte            { return Simple(msgRotateDevice) }
func ResetVideo() []byte              { return Simple(msgResetVideo) }
func SetVideoEnabled(on bool) []byte  { return []byte{msgSetVideoEnabled, boolByte(on)} }
func SetAudioEnabled(on bool) []byte  { return []byte{msgSetAudioEnabled, boolByte(on)} }

func boolByte(v bool) byte {
	if v {
		return 1
	}
	return 0
}

// DeviceEvent is one message sent by the server on the control socket.
type DeviceEvent struct {
	Type     int
	Text     string // clipboard
	Sequence uint64 // ack
	Rotation int    // display geometry
	Width    int
	Height   int
}

// ParseDeviceEvent reads one device message from the start of buf.
// It returns the event and how many bytes it consumed; a short buffer returns
// (nil, 0, nil) meaning "need more data".
func ParseDeviceEvent(buf []byte) (*DeviceEvent, int, error) {
	if len(buf) < 1 {
		return nil, 0, nil
	}
	typ := int(buf[0])
	switch typ {
	case devClipboard:
		if len(buf) < 5 {
			return nil, 0, nil
		}
		n := int(binary.BigEndian.Uint32(buf[1:5]))
		if len(buf) < 5+n {
			return nil, 0, nil
		}
		return &DeviceEvent{Type: typ, Text: string(buf[5 : 5+n])}, 5 + n, nil

	case devAckClipboard:
		if len(buf) < 9 {
			return nil, 0, nil
		}
		return &DeviceEvent{Type: typ, Sequence: binary.BigEndian.Uint64(buf[1:9])}, 9, nil

	case devUhidOutput:
		if len(buf) < 5 {
			return nil, 0, nil
		}
		n := int(binary.BigEndian.Uint16(buf[3:5]))
		if len(buf) < 5+n {
			return nil, 0, nil
		}
		return &DeviceEvent{Type: typ}, 5 + n, nil

	case devDisplayGeometry:
		if len(buf) < 10 {
			return nil, 0, nil
		}
		return &DeviceEvent{
			Type:     typ,
			Rotation: int(buf[1]),
			Width:    int(binary.BigEndian.Uint32(buf[2:6])),
			Height:   int(binary.BigEndian.Uint32(buf[6:10])),
		}, 10, nil
	}
	return nil, 0, fmt.Errorf("unknown device message type %d", typ)
}
