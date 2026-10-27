package screen

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"adbox-mcp/internal/adb"
	"adbox-mcp/internal/embedded"
)

// Session manages one live scrcpy streaming instance for a device.
// Frame fanout and state use channels and atomics: no mutex touches the delivery path.
type Session struct {
	dev       *adb.Device
	serial    string
	scid      uint32
	remoteJar string
	maxSize    int
	bitRate    int
	viewerPort     int
	enableVideoSrc bool
	enableAudioSrc bool
	onEnded        func(reason string)
	uhidOn      atomic.Bool
	audioActive atomic.Bool
	audioConn   atomic.Pointer[net.Conn]
	firstFrame  chan struct{}
	probeReply  chan net.Conn
	frameOnce   sync.Once
	videoOn     atomic.Bool
	audioOn     atomic.Bool

	running   atomic.Bool
	hub       *Hub

	controlConn   atomic.Pointer[net.Conn]
	lastClipboard atomic.Pointer[string]
	clipSeq       atomic.Uint64
	httpListener  net.Listener
	httpPort      int
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	scidCounter atomic.Uint32
)

func init() {
	scidCounter.Store(0x12340000)
}

// Options are launch-time choices for the on-device server.
//
// These are not the same switches as SetVideoEnabled/SetAudioEnabled. Launching
// with Video=false allocates no encoder and opens no video socket at all; a
// stream that was never started cannot be turned on later. The runtime messages
// stop and resume an encoder that already exists. Both layers are useful: the
// first saves the expensive resource outright, the second toggles without a
// reconnect.
type Options struct {
	MaxSize    int
	BitRate    int
	ViewerPort int
	// Video and Audio are pointers so "not specified" stays distinguishable from
	// "explicitly false", which is what the MCP layer needs.
	Video *bool
	Audio *bool
}

func (o Options) videoEnabled() bool { return o.Video == nil || *o.Video }
func (o Options) audioEnabled() bool { return o.Audio == nil || *o.Audio }

func NewSession(dev *adb.Device, serial string, opts Options) *Session {
	maxSize := opts.MaxSize
	if maxSize <= 0 {
		maxSize = 1080
	}
	bitRate := opts.BitRate
	if bitRate <= 0 {
		bitRate = 4000000
	}
	scid := scidCounter.Add(1) & 0x7fffffff
	se := &Session{
		dev:        dev,
		serial:     serial,
		scid:       scid,
		remoteJar:  embedded.ScrcpyRemotePath,
		maxSize:    maxSize,
		bitRate:    bitRate,
		viewerPort: opts.ViewerPort,
		enableVideoSrc: opts.videoEnabled(),
		enableAudioSrc: opts.audioEnabled(),
		hub:        newHub(),
	}
	se.videoOn.Store(opts.videoEnabled())
	se.audioOn.Store(opts.audioEnabled())
	se.firstFrame = make(chan struct{})

	// The encoder is the expensive part of a mirror. When the last viewer goes
	// away there is nobody to send frames to, so stop encoding; resume when
	// somebody attaches. This is the reference implementation's resident
	// keep-alive, expressed as viewer count instead of a consumer counter.
	//
	// Only meaningful when video was enabled at launch: a session started with
	// video=false has no encoder to stop or start.
	if opts.videoEnabled() {
		se.hub.onFirstViewer = func() {
			if !se.videoOn.Load() {
				slog.Info("screen: viewer attached, resuming encoder", "device", se.serial)
				se.SetVideoEnabled(true)
			}
		}
		se.hub.onLastViewer = func() {
			if se.videoOn.Load() {
				slog.Info("screen: last viewer left, pausing encoder", "device", se.serial)
				se.SetVideoEnabled(false)
			}
		}
	}
	return se
}

func (s *Session) Start() (webUrl string, err error) {
	// 1. Push scrcpy-server.jar if size differs
	needPush := true
	out, _ := s.dev.RunCommand("stat -c %s " + s.remoteJar + " 2>/dev/null")
	if strings.TrimSpace(out) == strconv.Itoa(len(embedded.ScrcpyServer)) {
		needPush = false
	}
	if needPush {
		w, err := s.dev.OpenWrite(s.remoteJar, 0644, time.Now())
		if err != nil {
			return "", fmt.Errorf("open jar writer: %w", err)
		}
		if _, err = w.Write(embedded.ScrcpyServer); err != nil {
			_ = w.Close()
			return "", fmt.Errorf("write jar: %w", err)
		}
		_ = w.Close()
	}

	// 2. Kill stale instances
	_, _ = s.dev.RunCommand("pkill -f " + embedded.ScrcpyMainClass)
	time.Sleep(200 * time.Millisecond)

	// 3. Launch scrcpy-server via app_process.
	//
	// Audio is requested unconditionally and the socket layout is discovered
	// afterwards rather than predicted from the API level.
	//
	// Scrcpy disables audio capture below Android 11 (API 30) -- the
	// AudioPlaybackCapture API is not usable there -- and the accept order is
	// fixed at video, audio, control. So on a device that silently dropped audio
	// the server accepts only TWO sockets, and a client that assumed three
	// would park its control channel in the audio slot and its enable-video
	// message would land nowhere: sockets all connect, nothing streams.
	//
	// Guessing from ro.build.version.sdk fixes that for the versions we know
	// about and breaks for every other reason audio can fail (a build with audio
	// stripped, a codec the device lacks, a future API bump). So request it,
	// then probe which layout actually happened and adapt.
	socketName := fmt.Sprintf("scrcpy_%08x", s.scid)
	maxLong := s.maxSize &^ 7

	// Assemble launch flags according to the requested streams.
	var args []string
	if !s.enableVideoSrc {
		args = append(args, "video=false")
	} else {
		args = append(args, fmt.Sprintf("max_size=%d", maxLong), fmt.Sprintf("video_bit_rate=%d", s.bitRate), "max_fps=30", "video_codec_options=i-frame-interval=1")
	}
	if !s.enableAudioSrc {
		args = append(args, "audio=false")
	} else {
		args = append(args, "audio=true", "audio_codec=opus", "audio_source=output")
	}
	launchCmd := fmt.Sprintf("/system/bin/nohup env CLASSPATH=%s app_process / %s %s scid=%08x log_level=debug tunnel_forward=true %s >/dev/null 2>&1 </dev/null &",
		s.remoteJar, embedded.ScrcpyMainClass, embedded.ScrcpyVersion, s.scid, strings.Join(args, " "))

	if err := s.dev.RunInteractiveShellLinesFireAndClose([]string{launchCmd, "sleep 1"}); err != nil {
		return "", fmt.Errorf("spawn scrcpy: %w", err)
	}

	time.Sleep(300 * time.Millisecond)

	// 4. Forward abstract socket to a free host port
	remSpec := adb.ForwardSpec{Protocol: adb.FProtocolAbstract, PortOrName: socketName}
	fwdPort, err := s.dev.ForwardToFreePort(remSpec)
	if err != nil {
		return "", fmt.Errorf("forward scrcpy socket: %w", err)
	}

	s.running.Store(true)
	go s.hub.Run()

	var videoConn net.Conn
	if s.enableVideoSrc {
		// Video socket. Always first when video is enabled, and the only one that
		// receives the dummy byte.
		videoConn, err = s.dev.DialRemote("127.0.0.1", fwdPort)
		if err != nil {
			return "", fmt.Errorf("dial video socket: %w", err)
		}
		dummy := make([]byte, 1)
		if _, err := io.ReadFull(videoConn, dummy); err != nil {
			_ = videoConn.Close()
			return "", fmt.Errorf("read video dummy: %w", err)
		}
		go s.readVideoLoop(videoConn)
	}

	// 7. Discover the control socket.
	//
	// In control-only mode (video=false, audio=false) there is exactly ONE socket
	// and it gets the dummy byte because sendDummyByte is once per server run.
	if !s.enableVideoSrc && !s.enableAudioSrc {
		control, err := s.dev.DialRemote("127.0.0.1", fwdPort)
		if err != nil {
			return "", fmt.Errorf("dial control-only socket: %w", err)
		}
		dummy := make([]byte, 1)
		if _, err := io.ReadFull(control, dummy); err != nil {
			_ = control.Close()
			return "", fmt.Errorf("read control-only dummy: %w", err)
		}
		s.controlConn.Store(&control)
		go s.readControlLoop(control)
		slog.Info("screen: control-only session started", "device", s.serial)
		return "", nil // no web viewer URL; caller gets pure control
	}
	//
	// The server accepts video, then optionally audio, then control, so after the
	// video socket the next connections are (audio?, control). Whether audio
	// exists depends on the build and the API level, and guessing from either is
	// how a wrong assumption turns into "sockets connect but nothing streams".
	//
	// Probe instead: send a clipboard request on each candidate and see which one
	// answers. It is idempotent, tiny, and harmless if it lands on the audio
	// socket (whose input the server never reads). Enable-video is NOT safe to
	// probe with -- writing it into the audio slot corrupts the audio stream and
	// the encoder never starts.
	first := mustDial(s.dev, fwdPort)
	if first == nil {
		_ = videoConn.Close()
		return "", fmt.Errorf("dial control candidate 1")
	}
	second := mustDial(s.dev, fwdPort)
	if second == nil {
		_ = videoConn.Close()
		_ = first.Close()
		return "", fmt.Errorf("dial control candidate 2")
	}

	reply := make(chan net.Conn, 2)
	s.probeReply = reply
	go s.readControlLoop(first)
	go s.readControlLoop(second)

	// SetClipboard with sequence != 0 produces an ACK_CLIPBOARD device message
	// unconditionally (GetClipboard returns nothing when autosync is on). The
	// audio socket accepts no control frames and will never answer.
	probePkt := SetClipboard(1, "", false)
	_, _ = first.Write(probePkt)
	_, _ = second.Write(probePkt)

	var control net.Conn
	select {
	case control = <-reply:
	case <-time.After(1500 * time.Millisecond):
	}

	if control == nil {
		// Neither answered. That happens when the server accepted fewer sockets
		// than we dialed and one candidate is parked in the backlog, or when the
		// clipboard path is unavailable on this build. Fall back to the layout
		// scrcpy uses when audio is off: the first candidate is control.
		slog.Warn("screen: no clipboard reply, assuming two-socket layout", "device", s.serial)
		control = first
		_ = second.Close()
		s.audioActive.Store(false)
	} else if control == first {
		slog.Info("screen: audio unavailable (two sockets)", "device", s.serial)
		_ = second.Close()
		s.audioActive.Store(false)
	} else {
		slog.Info("screen: audio active (three sockets)", "device", s.serial)
		s.audioActive.Store(true)
		s.audioConn.Store(&first)
		go s.readAudioLoop(first)
	}

	s.probeReply = nil
	s.controlConn.Store(&control)

	// Enable video now that the control channel is known.
	_, _ = control.Write([]byte{18, 1})
	if !s.waitForFirstFrame(2500 * time.Millisecond) {
		_, _ = control.Write([]byte{18, 1})
	}

	// 9. Start HTTP viewer server
	bindHost := os.Getenv("ADBOX_SCREEN_HOST")
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	portSpec := "0"
	if s.viewerPort > 0 {
		portSpec = strconv.Itoa(s.viewerPort)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, portSpec))
	if err != nil {
		_ = videoConn.Close()
		if cc := s.controlConn.Load(); cc != nil {
			_ = (*cc).Close()
		}
		s.hub.Stop()
		return "", fmt.Errorf("listen viewer: %w", err)
	}
	s.httpListener = ln
	s.httpPort = ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/ws", s.handleWS)
	go http.Serve(ln, mux)

	webUrl = fmt.Sprintf("http://%s:%d/", bindHost, s.httpPort)
	slog.Info("screen session started", "device", s.serial, "web", webUrl, "fwd", fwdPort)
	return webUrl, nil
}

func (s *Session) Close() {
	s.running.Store(false)
	s.hub.Stop()

	if s.httpListener != nil {
		_ = s.httpListener.Close()
	}
	if c := s.controlConn.Load(); c != nil {
		_ = (*c).Close()
	}
	_, _ = s.dev.RunCommand("pkill -f " + embedded.ScrcpyMainClass)
}

func (s *Session) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	c := newClient(conn)
	s.hub.register <- c

	// Dedicated per-client writer: pulls frames from bounded channel
	done := make(chan struct{})
	go func() {
		defer close(done)
		for frame := range c.frames {
			if err := conn.WriteMessage(websocket.BinaryMessage, frame); err != nil {
				return
			}
		}
	}()

	// Reader: pipes browser touch/key packets to control socket
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		s.InjectRawControl(msg)
	}

	select {
	case s.hub.unregister <- c:
	case <-s.hub.stop:
	}
	_ = conn.Close()
	<-done
}

func (s *Session) readVideoLoop(conn net.Conn) {
	defer conn.Close()
	// Video EOF means the on-device server stopped encoding: the app rotated
	// into a state it could not capture, the process was killed, or the device
	// went away. Either way this session is dead and must be reported, or every
	// later call fails on a socket nobody is behind.
	defer func() { s.notifyEnded("video stream ended") }()

	// 1. Consume device name (64B) or direct 12B codec header
	first4 := make([]byte, 4)
	if _, err := io.ReadFull(conn, first4); err != nil {
		return
	}
	codecID := binary.BigEndian.Uint32(first4)
	if codecID != 0x68323634 {
		remain := make([]byte, 60)
		_, _ = io.ReadFull(conn, remain)
		vhdr := make([]byte, 12)
		if _, err := io.ReadFull(conn, vhdr); err != nil {
			return
		}
		s.hub.width.Store(int32(binary.BigEndian.Uint32(vhdr[4:8])))
		s.hub.height.Store(int32(binary.BigEndian.Uint32(vhdr[8:12])))
	} else {
		rest8 := make([]byte, 8)
		_, _ = io.ReadFull(conn, rest8)
		s.hub.width.Store(int32(binary.BigEndian.Uint32(rest8[0:4])))
		s.hub.height.Store(int32(binary.BigEndian.Uint32(rest8[4:8])))
	}

	headerBuf := make([]byte, 12)

	for s.running.Load() {
		if _, err := io.ReadFull(conn, headerBuf); err != nil {
			break
		}
		ptsValue := binary.BigEndian.Uint64(headerBuf[0:8])
		frameLen := binary.BigEndian.Uint32(headerBuf[8:12])
		if frameLen == 0 || frameLen > 10*1024*1024 {
			continue
		}

		frame := make([]byte, frameLen)
		if _, err := io.ReadFull(conn, frame); err != nil {
			break
		}

		isConfig := (ptsValue & (1 << 63)) != 0
		isKey := (ptsValue & (1 << 62)) != 0

		// Identify NAL unit type directly
		hasConfig := isConfig
		for i := 0; i+4 < len(frame); i++ {
			if frame[i] == 0 && frame[i+1] == 0 && (frame[i+2] == 1 || (frame[i+2] == 0 && frame[i+3] == 1)) {
				offset := i + 3
				if frame[i+2] == 0 {
					offset = i + 4
				}
				nalType := frame[offset] & 0x1f
				if nalType == 7 || nalType == 8 {
					hasConfig = true
				}
				if nalType == 5 {
					isKey = true
				}
			}
		}

		if hasConfig {
			cp := append([]byte(nil), frame...)
			s.hub.spsPps.Store(&cp)
		}

		out := frame
		if isKey {
			// Prepend SPS/PPS to every IDR keyframe so new viewers can start instantly
			if p := s.hub.spsPps.Load(); p != nil && len(*p) > 0 {
				combined := make([]byte, 0, len(*p)+len(frame))
				combined = append(combined, *p...)
				combined = append(combined, frame...)
				out = combined
			}
			cpKey := append([]byte(nil), out...)
			s.hub.lastKey.Store(&cpKey)
		}

		// Non-blocking fanout to all active browser channels
		s.markFirstFrame()
		s.hub.Broadcast(TagVideo(out), isKey || hasConfig)
	}
}

func (s *Session) InjectRawControl(data []byte) {
	if c := s.controlConn.Load(); c != nil && len(data) > 0 {
		_, _ = (*c).Write(data)
	}
}

// geometry is the last display geometry the device reported. Rotation changes
// what the video's width/height mean, so it has to be tracked.
type geometry struct {
	rotation int32
	width    int32
	height   int32
}

// readControlLoop consumes device messages. The server writes clipboard contents
// and display-geometry changes here, and a socket nobody reads eventually backs
// up and stalls the control channel -- so this runs for the session's whole life
// even when no client asks for anything.
func (s *Session) readControlLoop(conn net.Conn) {
	// The control channel ending is equally fatal: it is the only path to the
	// device, so input would silently stop working while the page kept showing
	// a frozen frame.
	defer func() { s.notifyEnded("control stream ended") }()
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 2048)
	for s.running.Load() {
		n, err := conn.Read(chunk)
		if err != nil {
			slog.Debug("screen: control read err", "err", err)
			return
		}
		buf = append(buf, chunk[:n]...)

		for {
			ev, consumed, perr := ParseDeviceEvent(buf)
			if perr != nil {
				buf = buf[:0]
				break
			}
			if ev == nil {
				break // need more bytes
			}
			buf = buf[consumed:]

			// Any valid device message identifies this connection as the control
			// socket. Reported before the switch so every message type counts.
			if ch := s.probeReply; ch != nil {
				select {
				case ch <- conn:
				default:
				}
			}

			switch ev.Type {
			case devClipboard:
				s.lastClipboard.Store(&ev.Text)
				if info, err := json.Marshal(map[string]any{
					"type": "clipboard",
					"text": ev.Text,
				}); err == nil {
					s.hub.Broadcast(TagInfo(info), true)
				}
			case devDisplayGeometry:
				s.hub.rotation.Store(int32(ev.Rotation))
				if ev.Width > 0 && ev.Height > 0 {
					s.hub.width.Store(int32(ev.Width))
					s.hub.height.Store(int32(ev.Height))
				}
				// Tell viewers so the page can adjust its aspect box immediately
				// instead of waiting for the next SPS with a new resolution.
				if info, err := json.Marshal(map[string]any{
					"type":     "geometry",
					"rotation": ev.Rotation,
					"width":    ev.Width,
					"height":   ev.Height,
				}); err == nil {
					s.hub.Broadcast(TagInfo(info), true)
				}
			}
		}
	}
}

// InjectTouch sends a touch event in VIDEO pixel coordinates, which the device
// maps onto its real display using the width and height carried in the packet.
func (s *Session) InjectTouch(action, x, y int) {
	s.InjectTouchPointer(action, 0, x, y, 0xFFFF)
}

// InjectTouchPointer is InjectTouch with an explicit pointer id and pressure.
//
// Multi-touch is what makes pinch-to-zoom possible: two fingers are two pointer
// ids moving at once, and the device only recognises a pinch when both are down
// in the same gesture. Sending them as one pointer just pans.
func (s *Session) InjectTouchPointer(action, pointerID, x, y, pressure int) {
	w := int(s.hub.width.Load())
	h := int(s.hub.height.Load())
	if w <= 0 || h <= 0 {
		return // dimensions unknown yet; a wrong-size packet taps the wrong place
	}
	buttons := ButtonPrimary
	if action == ActionUp {
		buttons = ButtonNone
	}
	s.InjectRawControl(InjectTouch(action, pointerID, x, y, w, h, pressure, buttons, buttons))
}

// InjectSwipe drives a complete down-move-up gesture.
func (s *Session) InjectSwipe(x1, y1, x2, y2 int, durationMs int) {
	if durationMs <= 0 {
		durationMs = 200
	}
	const steps = 12
	s.InjectTouch(ActionDown, x1, y1)
	for i := 1; i <= steps; i++ {
		f := float64(i) / float64(steps)
		x := x1 + int(float64(x2-x1)*f)
		y := y1 + int(float64(y2-y1)*f)
		s.InjectTouch(ActionMove, x, y)
		time.Sleep(time.Duration(durationMs/steps) * time.Millisecond)
	}
	s.InjectTouch(ActionUp, x2, y2)
}

// InjectKeycode sends a full press (down then up).
func (s *Session) InjectKeycode(keycode int) {
	s.InjectRawControl(InjectKeycode(0, keycode, 0, 0))
	s.InjectRawControl(InjectKeycode(1, keycode, 0, 0))
}

// InjectText types text through the device, which is how non-ASCII works: the
// keystroke path only carries keycodes.
func (s *Session) InjectText(text string) {
	s.InjectRawControl(InjectText(text))
}

// InjectScroll sends a scroll event at a position, in video coordinates.
func (s *Session) InjectScroll(x, y int, hScroll, vScroll float64) {
	w := int(s.hub.width.Load())
	h := int(s.hub.height.Load())
	if w <= 0 || h <= 0 {
		return
	}
	s.InjectRawControl(InjectScroll(x, y, w, h, hScroll, vScroll, 0))
}

// SetClipboard pushes text to the device clipboard. paste also pastes it.
func (s *Session) SetClipboard(text string, paste bool) {
	seq := s.clipSeq.Add(1)
	s.InjectRawControl(SetClipboard(seq, text, paste))
}

// Clipboard returns the last clipboard text the device reported.
func (s *Session) Clipboard() string {
	if p := s.lastClipboard.Load(); p != nil {
		return *p
	}
	return ""
}

// RefreshClipboard asks the device to send its current clipboard.
func (s *Session) RefreshClipboard() {
	s.InjectRawControl(GetClipboard(0))
}

// Tap is a complete down-up at one point, in video pixel coordinates.
func (s *Session) Tap(x, y int) {
	s.InjectTouch(ActionDown, x, y)
	s.InjectTouch(ActionUp, x, y)
}

// readAudioLoop consumes the audio stream and fans it out tagged as audio.
//
// The header is identical in shape to video: 8 bytes of PTS/flags then a 4-byte
// size. The very first 4 bytes of the connection are the codec id, sent once.
func (s *Session) readAudioLoop(conn net.Conn) {
	defer conn.Close()

	codecBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, codecBuf); err != nil {
		return
	}
	codec := audioCodecFromID(binary.BigEndian.Uint32(codecBuf))
	s.hub.audioCodec.Store(int32(codec))
	slog.Info("screen audio codec", "codec", codec.String(), "device", s.serial)

	// Tell viewers which codec to configure their AudioDecoder with.
	if info, err := json.Marshal(map[string]any{
		"type":  "audio-codec",
		"codec": codec.String(),
	}); err == nil {
		s.hub.Broadcast(TagInfo(info), true)
	}

	header := make([]byte, 12)
	for s.running.Load() {
		if _, err := io.ReadFull(conn, header); err != nil {
			return
		}
		size := binary.BigEndian.Uint32(header[8:12])
		if size == 0 || size > 64*1024 {
			continue
		}
		payload := make([]byte, size)
		if _, err := io.ReadFull(conn, payload); err != nil {
			return
		}
		s.hub.Broadcast(TagAudio(payload), true)
	}
}

// ---- UHID ----------------------------------------------------------------

// UhidIDs are the virtual device ids we register. They only need to be unique
// within this connection.
// KeyEvent actions, shared with MotionEvent: 0 down, 1 up.
const (
	keyActionDown = 0
	keyActionUp   = 1
)

const (
	uhidKeyboard = 1
	uhidMouse    = 2
)

// EnableUhid registers a virtual keyboard and mouse on the device. This is the
// path that works when injected InputEvents do not: screen off, hardware-keyboard
// fields, and apps that filter synthetic input.
func (s *Session) EnableUhid() error {
	if s.uhidOn.Load() {
		return nil
	}
	if err := s.probeUhid(); err != nil {
		return err
	}
	s.InjectRawControl(UhidCreate(uhidKeyboard, 0x1234, 0x5678, "adbox keyboard", KeyboardReportDescriptor))
	time.Sleep(80 * time.Millisecond)
	s.InjectRawControl(UhidCreate(uhidMouse, 0x1234, 0x5679, "adbox mouse", MouseReportDescriptor))
	time.Sleep(80 * time.Millisecond)
	s.uhidOn.Store(true)
	slog.Info("uhid devices registered", "device", s.serial)
	return nil
}

// probeUhid checks the device actually exposes /dev/uhid to the shell user
// before pretending the capability exists.
func (s *Session) probeUhid() error {
	out, err := s.dev.RunCommand("test -r /dev/uhid && echo OK || echo NO")
	if err != nil {
		return fmt.Errorf("probe /dev/uhid: %w", err)
	}
	if !strings.Contains(out, "OK") {
		return fmt.Errorf("/dev/uhid is not readable by the shell user on this device")
	}
	return nil
}

// UhidKeyPress sends a key as a real HID scancode, down then up.
// Returns false when the key has no scancode mapping, so the caller can fall
// back to the keycode path.
func (s *Session) UhidKeyPress(androidKeycode int) bool {
	scan, ok := KeycodeToScancode[androidKeycode]
	if !ok || !s.uhidOn.Load() {
		return false
	}
	// 8-byte boot keyboard report: modifier, reserved, 6 scancodes
	down := make([]byte, 8)
	down[2] = scan
	s.InjectRawControl(UhidInput(uhidKeyboard, down))
	up := make([]byte, 8)
	s.InjectRawControl(UhidInput(uhidKeyboard, up))
	return true
}

// UhidTypeText sends text as HID scancodes, for fields that reject inject-text.
func (s *Session) UhidTypeText(text string) bool {
	if !s.uhidOn.Load() {
		return false
	}
	for _, r := range text {
		scan, ok := asciiToScancode(r)
		if !ok {
			return false
		}
		report := make([]byte, 8)
		if r >= 'A' && r <= 'Z' {
			report[0] = 0x02 // left shift
		}
		report[2] = scan
		s.InjectRawControl(UhidInput(uhidKeyboard, report))
		time.Sleep(12 * time.Millisecond)
		s.InjectRawControl(UhidInput(uhidKeyboard, make([]byte, 8)))
		time.Sleep(8 * time.Millisecond)
	}
	return true
}

// UhidMouseMove sends a relative mouse move with optional button state.
func (s *Session) UhidMouseMove(dx, dy int, buttons byte) bool {
	if !s.uhidOn.Load() {
		return false
	}
	report := []byte{buttons, byte(int8(clampInt(dx, -127, 127))), byte(int8(clampInt(dy, -127, 127))), 0}
	s.InjectRawControl(UhidInput(uhidMouse, report))
	return true
}

// UhidMouseClick clicks at the current pointer position.
func (s *Session) UhidMouseClick() bool {
	if !s.uhidOn.Load() {
		return false
	}
	s.InjectRawControl(UhidInput(uhidMouse, []byte{0x01, 0, 0, 0}))
	time.Sleep(20 * time.Millisecond)
	s.InjectRawControl(UhidInput(uhidMouse, []byte{0x00, 0, 0, 0}))
	return true
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// asciiToScancode maps printable ASCII to USB HID usage ids (0x04..0x38 range).
func asciiToScancode(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(0x04 + (r - 'a')), true
	case r >= 'A' && r <= 'Z':
		return byte(0x04 + (r - 'A')), true
	case r >= '1' && r <= '9':
		return byte(0x1e + (r - '1')), true
	case r == '0':
		return 0x27, true
	case r == '\n':
		return 0x28, true
	case r == ' ':
		return 0x2c, true
	case r == '-':
		return 0x2d, true
	case r == '=':
		return 0x2e, true
	case r == '[':
		return 0x2f, true
	case r == ']':
		return 0x30, true
	case r == '\\':
		return 0x31, true
	case r == ';':
		return 0x33, true
	case r == '\'':
		return 0x34, true
	case r == '`':
		return 0x35, true
	case r == ',':
		return 0x36, true
	case r == '.':
		return 0x37, true
	case r == '/':
		return 0x38, true
	}
	return 0, false
}

// SetVideoEnabled stops or resumes encoding without restarting the process.
// Useful when a viewer goes away and the device should stop burning power on an
// encoder nobody is watching.
func (s *Session) SetVideoEnabled(on bool) {
	s.InjectRawControl(SetVideoEnabled(on))
	s.videoOn.Store(on)
}

// VideoEnabled reports the last state we asked the encoder for.
func (s *Session) VideoEnabled() bool { return s.videoOn.Load() }

// UhidEnabled reports whether virtual HID devices are registered.
func (s *Session) UhidEnabled() bool { return s.uhidOn.Load() }

// Back presses BACK, or turns the screen on when it is off.
//
// A back press is two messages, not one: the device injects KEYCODE_BACK with
// whatever action we send, so a lone ACTION_DOWN leaves the key logically held
// and the next back does nothing. The screen-off branch ignores ACTION_UP, which
// is why sending both is safe in either state.
func (s *Session) Back() {
	s.InjectRawControl(BackOrScreenOn(keyActionDown))
	s.InjectRawControl(BackOrScreenOn(keyActionUp))
}

// RequestKeyFrame asks the encoder to emit an IDR now. A viewer that fell behind
// or a decoder that lost state recovers on the next keyframe rather than waiting
// for the one-second GOP to come around.
func (s *Session) RequestKeyFrame() {
	s.InjectRawControl(ResetVideo())
}

// waitForFirstFrame blocks until the demuxer has produced a frame, which is the
// only honest proof that the socket we sent enable-video on is the control one.
func (s *Session) waitForFirstFrame(d time.Duration) bool {
	select {
	case <-s.firstFrame:
		return true
	case <-time.After(d):
		return false
	}
}

// markFirstFrame is called once by the demuxer.
func (s *Session) markFirstFrame() {
	s.frameOnce.Do(func() {
		if s.firstFrame != nil {
			close(s.firstFrame)
		}
	})
}

// AudioActive reports whether the device actually accepted an audio socket.
// It is discovered at start time, not predicted from the API level.
func (s *Session) AudioActive() bool { return s.audioActive.Load() }

func mustDial(dev *adb.Device, port int) net.Conn {
	c, err := dev.DialRemote("127.0.0.1", port)
	if err != nil {
		return nil
	}
	return c
}

// SetAudioEnabled dynamically toggles audio encoding via control message 19.
func (s *Session) SetAudioEnabled(on bool) {
	s.InjectRawControl(SetAudioEnabled(on))
	s.audioOn.Store(on)
}

// AudioEnabled reports the last runtime audio state requested.
func (s *Session) AudioEnabled() bool { return s.audioOn.Load() }

// ControlOnly reports whether this session was launched without video/audio.
func (s *Session) ControlOnly() bool { return !s.enableVideoSrc && !s.enableAudioSrc }

// Width reports the decoded video width in pixels, or 0 if unknown yet.
func (s *Session) Width() int { return int(s.hub.width.Load()) }

// Height reports the decoded video height in pixels, or 0 if unknown yet.
func (s *Session) Height() int { return int(s.hub.height.Load()) }

// SetOnEnded registers a callback for when the session terminates from the
// device side (scrcpy crashed, USB yanked, screen encoder aborted).
func (s *Session) SetOnEnded(cb func(reason string)) { s.onEnded = cb }

func (s *Session) notifyEnded(reason string) {
	if s.running.CompareAndSwap(true, false) {
		slog.Warn("screen session ended by device", "device", s.serial, "reason", reason)
		if s.onEnded != nil {
			go s.onEnded(reason)
		}
		s.hub.Stop()
	}
}

// Running reports whether the session is still alive on the device side.
func (s *Session) Running() bool { return s.running.Load() }
