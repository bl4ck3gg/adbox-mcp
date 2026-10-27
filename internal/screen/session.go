package screen

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
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
	maxSize   int
	bitRate   int

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

func NewSession(dev *adb.Device, serial string, maxSize, bitRate int) *Session {
	if maxSize <= 0 {
		maxSize = 1080
	}
	if bitRate <= 0 {
		bitRate = 4000000
	}
	scid := scidCounter.Add(1) & 0x7fffffff
	return &Session{
		dev:       dev,
		serial:    serial,
		scid:      scid,
		remoteJar: embedded.ScrcpyRemotePath,
		maxSize:   maxSize,
		bitRate:   bitRate,
		hub:       newHub(),
	}
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

	// 3. Launch scrcpy-server via app_process
	socketName := fmt.Sprintf("scrcpy_%08x", s.scid)
	maxLong := s.maxSize &^ 7
	launchCmd := fmt.Sprintf("/system/bin/nohup env CLASSPATH=%s app_process / %s %s scid=%08x log_level=debug tunnel_forward=true audio=false max_size=%d video_bit_rate=%d max_fps=30 video_codec_options=i-frame-interval=1 >/dev/null 2>&1 </dev/null &",
		s.remoteJar, embedded.ScrcpyMainClass, embedded.ScrcpyVersion, s.scid, maxLong, s.bitRate)

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

	// 5. Connect Video Socket
	videoConn, err := s.dev.DialRemote("127.0.0.1", fwdPort)
	if err != nil {
		return "", fmt.Errorf("dial video socket: %w", err)
	}

	// Dummy byte on first socket
	dummy := make([]byte, 1)
	if _, err := io.ReadFull(videoConn, dummy); err != nil {
		_ = videoConn.Close()
		return "", fmt.Errorf("read video dummy: %w", err)
	}

	// 6. Connect Control Socket
	controlConn, err := s.dev.DialRemote("127.0.0.1", fwdPort)
	if err != nil {
		_ = videoConn.Close()
		return "", fmt.Errorf("dial control socket: %w", err)
	}
	s.controlConn.Store(&controlConn)

	// Enable video encoding (scrcpy extension requirement)
	if _, err := controlConn.Write([]byte{18, 1}); err != nil {
		_ = videoConn.Close()
		_ = controlConn.Close()
		return "", fmt.Errorf("enable video: %w", err)
	}

	// 7. Start fanout hub loop (pure channel-based)
	go s.hub.Run()

	// Drain device messages for the life of the session. Not doing this backs up
	// the control socket and stalls clipboard and geometry updates.
	go s.readControlLoop(controlConn)

	// 8. Start HTTP viewer server
	bindHost := os.Getenv("ADBOX_SCREEN_HOST")
	if bindHost == "" {
		bindHost = "127.0.0.1"
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(bindHost, "0"))
	if err != nil {
		_ = videoConn.Close()
		_ = controlConn.Close()
		s.hub.Stop()
		return "", fmt.Errorf("listen viewer: %w", err)
	}
	s.httpListener = ln
	s.httpPort = ln.Addr().(*net.TCPAddr).Port
	s.running.Store(true)

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/ws", s.handleWS)
	go http.Serve(ln, mux)

	// 9. Start video demuxer loop
	go s.readVideoLoop(videoConn)

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

	s.hub.unregister <- c
	_ = conn.Close()
	<-done
}

func (s *Session) readVideoLoop(conn net.Conn) {
	defer conn.Close()

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
		s.hub.Broadcast(out)
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
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 2048)
	for s.running.Load() {
		n, err := conn.Read(chunk)
		if err != nil {
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

			switch ev.Type {
			case devClipboard:
				s.lastClipboard.Store(&ev.Text)
			case devDisplayGeometry:
				s.hub.rotation.Store(int32(ev.Rotation))
				if ev.Width > 0 && ev.Height > 0 {
					s.hub.width.Store(int32(ev.Width))
					s.hub.height.Store(int32(ev.Height))
				}
			}
		}
	}
}

// InjectTouch sends a touch event in VIDEO pixel coordinates, which the device
// maps onto its real display using the width and height carried in the packet.
func (s *Session) InjectTouch(action, x, y int) {
	w := int(s.hub.width.Load())
	h := int(s.hub.height.Load())
	if w <= 0 || h <= 0 {
		return // dimensions unknown yet; a wrong-size packet taps the wrong place
	}
	buttons := ButtonPrimary
	if action == ActionUp {
		buttons = ButtonNone
	}
	s.InjectRawControl(InjectTouch(action, 0, x, y, w, h, 0xFFFF, buttons, buttons))
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
