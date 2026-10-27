package screen

import (
	"sync/atomic"

	"github.com/gorilla/websocket"
)

// client is one attached browser. It owns a bounded channel so a slow or
// backgrounded tab drops frames rather than blocking the demux loop.
type client struct {
	conn   *websocket.Conn
	frames chan []byte
}

func newClient(conn *websocket.Conn) *client {
	return &client{
		conn:   conn,
		frames: make(chan []byte, 30), // ~1 second of video at 30 fps
	}
}

// Hub manages active viewers using channels and atomics, with no mutex in the
// frame delivery path.
type Hub struct {
	register   chan *client
	unregister chan *client
	broadcast  chan []byte
	stop       chan struct{}

	// Video parameters are read concurrently by handlers, updated atomically
	spsPps  atomic.Pointer[[]byte]
	lastKey atomic.Pointer[[]byte]
	width   atomic.Int32
	height  atomic.Int32
}

func newHub() *Hub {
	return &Hub{
		register:   make(chan *client),
		unregister: make(chan *client),
		broadcast:  make(chan []byte, 60),
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
			return

		case c := <-h.register:
			clients[c] = true
			// Replay cached configuration and latest keyframe to the new viewer
			// so its decoder starts immediately without waiting for a GOP cycle.
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

		case c := <-h.unregister:
			if _, ok := clients[c]; ok {
				delete(clients, c)
				close(c.frames)
			}

		case frame := <-h.broadcast:
			// Non-blocking fanout: if a viewer's buffer is full, drop the frame
			// for THAT viewer only. Other viewers and the source stream stay smooth.
			for c := range clients {
				select {
				case c.frames <- frame:
				default:
					// Dropped for this slow client; backpressure isolated
				}
			}
		}
	}
}

func (h *Hub) Broadcast(frame []byte) {
	select {
	case h.broadcast <- frame:
	default:
		// Hub itself is saturated; drop to avoid blocking the hardware decoder
	}
}

func (h *Hub) Stop() {
	select {
	case <-h.stop:
	default:
		close(h.stop)
	}
}
