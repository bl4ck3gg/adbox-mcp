package screen

import (
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// Backlog thresholds for the per-viewer drop policy, copied from mr-robot's
// stream_supervisor.go. A viewer that falls behind skips P-frames until an IDR
// arrives and the queue has drained below the exit threshold.
const (
	backlogEnterGE      = 10 // queued >= this -> start dropping P
	backlogExitRemainLT = 3  // queued < this on a keyframe -> stop dropping
)

type client struct {
	conn   *websocket.Conn
	frames chan []byte
	dropP  atomic.Bool
}

func newClient(conn *websocket.Conn) *client {
	return &client{
		conn:   conn,
		frames: make(chan []byte, 30),
	}
}

type outFrame struct {
	data  []byte
	isKey bool
}

type Hub struct {
	register   chan *client
	unregister chan *client
	broadcast  chan outFrame
	stop       chan struct{}

	spsPps     atomic.Pointer[[]byte]
	lastKey    atomic.Pointer[[]byte]
	width      atomic.Int32
	height     atomic.Int32
	rotation   atomic.Int32
	audioCodec atomic.Int32
	viewers    atomic.Int32

	onFirstViewer func()
	onLastViewer  func()
}

func newHub() *Hub {
	return &Hub{
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan outFrame, 60),
		stop:       make(chan struct{}),
	}
}

func (h *Hub) Run() {
	clients := make(map[*client]bool)
	for {
		select {
		case <-h.stop:
			for c := range clients {
				close(c.frames)
				_ = c.conn.Close()
			}
			h.viewers.Store(0)
			return

		case c := <-h.register:
			clients[c] = true
			if p := h.spsPps.Load(); p != nil && len(*p) > 0 {
				select {
				case c.frames <- *p:
				default:
				}
			}
			if k := h.lastKey.Load(); k != nil && len(*k) > 0 {
				select {
				case c.frames <- *k:
				default:
				}
			}
			if n := h.viewers.Add(1); n == 1 && h.onFirstViewer != nil {
				go h.onFirstViewer()
			}

		case c := <-h.unregister:
			if _, ok := clients[c]; ok {
				delete(clients, c)
				close(c.frames)
				if n := h.viewers.Add(-1); n == 0 && h.onLastViewer != nil {
					go h.onLastViewer()
				}
			}

		case f := <-h.broadcast:
			for c := range clients {
				h.deliver(c, f)
			}
		}
	}
}

func (h *Hub) deliver(c *client, f outFrame) {
	drop := c.dropP.Load()
	if drop && !f.isKey {
		return
	}
	select {
	case c.frames <- f.data:
		depth := len(c.frames)
		if depth >= backlogEnterGE {
			c.dropP.Store(true)
		} else if f.isKey && depth < backlogExitRemainLT {
			c.dropP.Store(false)
		}
	default:
		c.dropP.Store(true)
	}
}

func (h *Hub) Broadcast(data []byte, isKey bool) {
	select {
	case h.broadcast <- outFrame{data: data, isKey: isKey}:
	default:
	}
}

func (h *Hub) Viewers() int { return int(h.viewers.Load()) }

func (h *Hub) Stop() {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
}
