package screen

import (
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"adbox-mcp/internal/adb"
	"adbox-mcp/internal/embedded"
)

// Session manages one live scrcpy streaming instance for a device.
// It owns the device-side app_process, the video socket reader, the control
// socket writer, and a fanout broadcaster serving any number of WebSocket viewers.
type Session struct {
	dev         *adb.Device
	serial      string
	scid        uint32
	remoteJar   string
	maxSize     int
	bitRate     int

	mu          sync.Mutex
	running     bool
	videoWidth  int
	videoHeight int
	spsPps      []byte // Cached configuration frame (SPS+PPS) for new viewers

	controlConn net.Conn
	frameCount  uint64
	controlMu   sync.Mutex

	clientsMu   sync.RWMutex
	clients     map[*websocket.Conn]bool

	httpListener net.Listener
	httpPort     int
}

var (
	upgrader = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	scidCounter uint32 = 0x12340000
)

func NewSession(dev *adb.Device, serial string, maxSize, bitRate int) *Session {
	if maxSize <= 0 {
		maxSize = 1080
	}
	if bitRate <= 0 {
		bitRate = 4000000 // 4 Mbps default
	}
	scidCounter++
	return &Session{
		dev:       dev,
		serial:    serial,
		scid:      scidCounter & 0x7fffffff,
		remoteJar: embedded.ScrcpyRemotePath,
		maxSize:   maxSize,
		bitRate:   bitRate,
		clients:   make(map[*websocket.Conn]bool),
	}
}

// Start pushes the jar if needed, launches scrcpy-server, connects the
// video and control sockets, and starts the HTTP+WebSocket viewer server.
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
			w.Close()
			return "", fmt.Errorf("write jar: %w", err)
		}
		_ = w.Close()
	}

	// 2. Kill any stale scrcpy session on this device
	_, _ = s.dev.RunCommand("pkill -f " + embedded.ScrcpyMainClass)
	time.Sleep(200 * time.Millisecond)

	// 3. Launch scrcpy-server via app_process
	socketName := fmt.Sprintf("scrcpy_%08x", s.scid)
	maxLong := s.maxSize &^ 7
	launchCmd := fmt.Sprintf("/system/bin/nohup env CLASSPATH=%s app_process / %s %s scid=%08x log_level=debug tunnel_forward=true audio=false max_size=%d video_bit_rate=%d >/dev/null 2>&1 </dev/null &",
		s.remoteJar, embedded.ScrcpyMainClass, embedded.ScrcpyVersion, s.scid, maxLong, s.bitRate)

	if err := s.dev.RunInteractiveShellLinesFireAndClose([]string{launchCmd, "sleep 1"}); err != nil {
		return "", fmt.Errorf("spawn scrcpy: %w", err)
	}

	// Wait for the localabstract socket to be listening
	time.Sleep(300 * time.Millisecond)

	// 4. Forward the abstract socket to a free host port
	remSpec := adb.ForwardSpec{Protocol: adb.FProtocolAbstract, PortOrName: socketName}
	fwdPort, err := s.dev.ForwardToFreePort(remSpec)
	if err != nil {
		return "", fmt.Errorf("forward scrcpy socket: %w", err)
	}

	// 5. Connect Video Socket (first connection)
	videoConn, err := s.dev.DialRemote("127.0.0.1", fwdPort)
	if err != nil {
		return "", fmt.Errorf("dial video socket: %w", err)
	}

	// Read dummy byte (0x00)
	dummy := make([]byte, 1)
	if _, err := io.ReadFull(videoConn, dummy); err != nil {
		videoConn.Close()
		return "", fmt.Errorf("read video dummy: %w", err)
	}

	// 6. Connect Control Socket (second connection)
	controlConn, err := s.dev.DialRemote("127.0.0.1", fwdPort)
	if err != nil {
		videoConn.Close()
		return "", fmt.Errorf("dial control socket: %w", err)
	}
	// scrcpy writes the dummy byte only on the FIRST accepted socket (video);
	// after that sendDummyByte is false, so the control socket gets nothing.
	// Reading here would block forever, so the control channel is used as-is.
	s.controlConn = controlConn
	slog.Debug("screen: control socket connected")

	// Video encoding starts DISABLED in this build of the server (a local
	// extension: TYPE_SET_VIDEO_ENABLED=18 / TYPE_SET_AUDIO_ENABLED=19 are not
	// upstream scrcpy). The encoder thread waits on that flag before it writes
	// the 12-byte video header, so without this two-byte message the stream
	// never begins and the video socket stays silent forever -- which looks
	// exactly like a broken tunnel.
	if _, err := controlConn.Write([]byte{18, 1}); err != nil {
		videoConn.Close()
		controlConn.Close()
		return "", fmt.Errorf("enable video stream: %w", err)
	}
	slog.Debug("screen: sent enable-video")

	// 7. Start local HTTP / WebSocket fanout server
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		videoConn.Close()
		controlConn.Close()
		return "", fmt.Errorf("listen http: %w", err)
	}
	s.httpListener = ln
	s.httpPort = ln.Addr().(*net.TCPAddr).Port
	s.running = true

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/ws", s.handleWS)
	go http.Serve(ln, mux)

	// 8. Start video demuxer & broadcaster loop
	go s.readVideoLoop(videoConn)

	webUrl = fmt.Sprintf("http://127.0.0.1:%d/", s.httpPort)
	slog.Info("screen session started", "device", s.serial, "web", webUrl, "fwd", fwdPort)
	return webUrl, nil
}

func (s *Session) Close() {
	s.mu.Lock()
	s.running = false
	if s.httpListener != nil {
		_ = s.httpListener.Close()
	}
	s.mu.Unlock()

	s.controlMu.Lock()
	if s.controlConn != nil {
		_ = s.controlConn.Close()
	}
	s.controlMu.Unlock()

	s.clientsMu.Lock()
	for c := range s.clients {
		_ = c.Close()
	}
	s.clients = make(map[*websocket.Conn]bool)
	s.clientsMu.Unlock()

	_, _ = s.dev.RunCommand("pkill -f " + embedded.ScrcpyMainClass)
}

func (s *Session) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}

	s.clientsMu.Lock()
	s.clients[conn] = true
	// If we already have the SPS/PPS config frame, send it immediately to this new viewer
	// so their WebCodecs decoder initializes without waiting for the next keyframe.
	s.mu.Lock()
	if len(s.spsPps) > 0 {
		_ = conn.WriteMessage(websocket.BinaryMessage, s.spsPps)
	}
	s.mu.Unlock()
	s.clientsMu.Unlock()

	defer func() {
		s.clientsMu.Lock()
		delete(s.clients, conn)
		s.clientsMu.Unlock()
		conn.Close()
	}()

	// Read control messages from browser and inject to scrcpy
	for {
		_, msg, err := conn.ReadMessage()
		if err != nil {
			break
		}
		// Protocol: binary control packet forwarded directly to control socket
		s.InjectRawControl(msg)
	}
}

func (s *Session) broadcast(data []byte) {
	s.clientsMu.RLock()
	defer s.clientsMu.RUnlock()
	for client := range s.clients {
		_ = client.WriteMessage(websocket.BinaryMessage, data)
	}
}

func (s *Session) readVideoLoop(conn net.Conn) {
	defer conn.Close()
	slog.Debug("screen: video loop started")

	// 1. Consume 64-byte device name header (or directly codec header)
	first4 := make([]byte, 4)
	if n, err := io.ReadFull(conn, first4); err != nil {
		slog.Debug("screen: header read failed", "n", n, "err", err)
		return
	}
	slog.Debug("screen: first4", "hex", fmt.Sprintf("%x", first4), "ascii", string(first4))
	codecID := binary.BigEndian.Uint32(first4)
	if codecID != 0x68323634 { // not "h264" directly -> deviceMeta 64 bytes
		remain := make([]byte, 60)
		if n, e := io.ReadFull(conn, remain); e != nil {
			slog.Debug("screen: devicemeta read failed", "n", n, "err", e)
			return
		}
		slog.Debug("screen: device meta consumed")
		// Read actual 12-byte video header
		vhdr := make([]byte, 12)
		if n, err := io.ReadFull(conn, vhdr); err != nil {
			slog.Debug("screen: vhdr read failed", "n", n, "err", err)
			return
		}
		slog.Debug("screen: vhdr", "codec", string(vhdr[0:4]), "w", binary.BigEndian.Uint32(vhdr[4:8]), "h", binary.BigEndian.Uint32(vhdr[8:12]))
		s.mu.Lock()
		s.videoWidth = int(binary.BigEndian.Uint32(vhdr[4:8]))
		s.videoHeight = int(binary.BigEndian.Uint32(vhdr[8:12]))
		s.mu.Unlock()
	} else {
		rest8 := make([]byte, 8)
		_, _ = io.ReadFull(conn, rest8)
		s.mu.Lock()
		s.videoWidth = int(binary.BigEndian.Uint32(rest8[0:4]))
		s.videoHeight = int(binary.BigEndian.Uint32(rest8[4:8]))
		s.mu.Unlock()
	}

	headerBuf := make([]byte, 12) // 8 bytes PTS/flags + 4 bytes size

	for s.running {
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
			slog.Debug("screen: read frame failed", "err", err)
			break
		}
		if s.frameCount == 0 {
			slog.Debug("screen: first frame", "len", frameLen, "config", (ptsValue&(1<<63)) != 0)
		}
		s.frameCount++

		isConfig := (ptsValue & (1 << 63)) != 0
		if isConfig {
			s.mu.Lock()
			s.spsPps = frame
			s.mu.Unlock()
		}

		// Broadcast to all active browsers
		s.broadcast(frame)
	}
}

// InjectRawControl sends a raw scrcpy binary control packet.
func (s *Session) InjectRawControl(data []byte) {
	s.controlMu.Lock()
	defer s.controlMu.Unlock()
	if s.controlConn != nil && len(data) > 0 {
		_, _ = s.controlConn.Write(data)
	}
}

// InjectTouch injects a touch event using scrcpy binary protocol.
// action: 0=DOWN, 1=UP, 2=MOVE
func (s *Session) InjectTouch(action int, x, y int) {
	s.mu.Lock()
	w, h := s.videoWidth, s.videoHeight
	s.mu.Unlock()
	if w <= 0 { w = 1080 }
	if h <= 0 { h = 2220 }

	// Format:
	// type: 1B (0x02 = TYPE_INJECT_TOUCH_EVENT)
	// action: 1B
	// pointerId: 8B
	// x: 4B
	// y: 4B
	// screenWidth: 2B
	// screenHeight: 2B
	// pressure: 2B (0xFFFF = 1.0f)
	// actionButton: 4B
	// buttons: 4B
	buf := make([]byte, 32)
	buf[0] = 2 // TYPE_INJECT_TOUCH_EVENT
	buf[1] = byte(action)
	binary.BigEndian.PutUint64(buf[2:10], 0) // pointerId = 0
	binary.BigEndian.PutUint32(buf[10:14], uint32(x))
	binary.BigEndian.PutUint32(buf[14:18], uint32(y))
	binary.BigEndian.PutUint16(buf[18:20], uint16(w))
	binary.BigEndian.PutUint16(buf[20:22], uint16(h))
	binary.BigEndian.PutUint16(buf[22:24], 0xffff) // pressure = 1.0
	binary.BigEndian.PutUint32(buf[24:28], 1)      // primary button
	binary.BigEndian.PutUint32(buf[28:32], 1)      // buttons

	s.InjectRawControl(buf)
}

// InjectKeycode sends an Android key event.
// action: 0=DOWN, 1=UP
func (s *Session) InjectKeycode(action, keycode int) {
	// type: 1B (0x00 = TYPE_INJECT_KEYCODE)
	// action: 1B
	// keycode: 4B
	// repeat: 4B
	// metaState: 4B
	buf := make([]byte, 14)
	buf[0] = 0 // TYPE_INJECT_KEYCODE
	buf[1] = byte(action)
	binary.BigEndian.PutUint32(buf[2:6], uint32(keycode))
	binary.BigEndian.PutUint32(buf[6:10], 0)
	binary.BigEndian.PutUint32(buf[10:14], 0)
	s.InjectRawControl(buf)
}
