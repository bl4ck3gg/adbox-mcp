package screen

import (
	"testing"
	"time"
)

// The drop policy is the part of the hub that decides what a slow viewer loses.
// It runs on every frame for every viewer, so its behaviour is worth pinning
// without spinning up a real WebSocket.

// drain empties a client's queue, returning how many frames it held.
func drain(c *client) int {
	n := 0
	for {
		select {
		case <-c.frames:
			n++
		default:
			return n
		}
	}
}

func TestDeliverDropsNothingWhenQueueIsShort(t *testing.T) {
	h := newHub()
	c := newClient(nil)

	for i := 0; i < 5; i++ {
		h.deliver(c, outFrame{data: []byte{byte(i)}, isKey: false})
	}
	if got := drain(c); got != 5 {
		t.Errorf("delivered %d frames, want 5", got)
	}
	if c.dropP.Load() {
		t.Error("dropP should stay false while the queue is shallow")
	}
}

func TestDeliverEntersDropModeOnBacklog(t *testing.T) {
	// Once a viewer is this far behind, sending P-frames costs bandwidth that
	// the next keyframe will invalidate anyway.
	h := newHub()
	c := newClient(nil)

	for i := 0; i < backlogEnterGE; i++ {
		h.deliver(c, outFrame{data: []byte{1}, isKey: false})
	}
	if !c.dropP.Load() {
		t.Fatalf("dropP should be set after %d queued frames", backlogEnterGE)
	}
}

func TestDropModeSkipsPFramesButKeepsKeyframes(t *testing.T) {
	// This is the entire point of the policy: a lost P-frame costs one frame,
	// a lost keyframe costs the whole GOP. So drop mode must be selective.
	h := newHub()
	c := newClient(nil)

	// Fill to the enter threshold and set drop mode.
	for i := 0; i < backlogEnterGE; i++ {
		h.deliver(c, outFrame{data: []byte{0xFF}, isKey: false})
	}
	drain(c)

	// A P-frame must be skipped.
	h.deliver(c, outFrame{data: []byte{0x01}, isKey: false})
	if got := drain(c); got != 0 {
		t.Errorf("P-frame delivered while dropping, want 0 (got %d)", got)
	}

	// A keyframe must get through.
	h.deliver(c, outFrame{data: []byte{0x02}, isKey: true})
	if got := drain(c); got != 1 {
		t.Errorf("keyframe dropped, want 1 delivered (got %d)", got)
	}
}

func TestDropModeExitsAfterKeyframeOnLowQueue(t *testing.T) {
	// Recovery: once the backlog has drained and a keyframe has been delivered,
	// normal delivery resumes. Without this the viewer would stay in drop mode
	// forever and see roughly one frame per GOP.
	h := newHub()
	c := newClient(nil)

	for i := 0; i < backlogEnterGE; i++ {
		h.deliver(c, outFrame{data: []byte{1}, isKey: false})
	}
	if !c.dropP.Load() {
		t.Fatal("precondition: drop mode should be on")
	}
	drain(c)

	h.deliver(c, outFrame{data: []byte{2}, isKey: true})
	if c.dropP.Load() {
		t.Error("drop mode should clear after a keyframe on a shallow queue")
	}
	drain(c) // take the keyframe out before checking what the next P-frame does

	h.deliver(c, outFrame{data: []byte{3}, isKey: false})
	if got := drain(c); got != 1 {
		t.Errorf("P-frame after recovery: delivered %d, want 1", got)
	}
}

func TestDeliverNeverBlocks(t *testing.T) {
	// A viewer that stops reading must not stall the demux loop, because that
	// stalls the hardware encoder behind it.
	h := newHub()
	c := newClient(nil) // 30-frame buffer

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 500; i++ {
			h.deliver(c, outFrame{data: []byte{1}, isKey: false})
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("deliver blocked when the client queue was full")
	}

	// The queue cannot exceed its buffer, and drop mode stops most of the 500
	// from being enqueued at all -- both are the point. What matters is that
	// the capacity bound held and nothing blocked.
	if n := drain(c); n <= 0 || n > 30 {
		t.Errorf("queue holds %d frames, want 1..30", n)
	}
}

func TestBroadcastNeverBlocks(t *testing.T) {
	// Same guarantee one level up: a saturated hub drops rather than blocking
	// the caller, which is the readVideoLoop goroutine.
	h := newHub()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			h.Broadcast([]byte{1}, false)
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Broadcast blocked")
	}
}

func TestViewerLifecycleCallbacks(t *testing.T) {
	// The encoder is paused when the last viewer leaves and resumed when one
	// returns. That decision is made from these two callbacks, so they must
	// fire exactly on the zero crossings and not on every change.
	h := newHub()

	first := make(chan int, 10)
	last := make(chan int, 10)
	h.onFirstViewer = func() { first <- 1 }
	h.onLastViewer = func() { last <- 1 }

	go h.Run()
	defer h.Stop()

	c1 := newClient(nil)
	h.register <- c1
	expectSignal(t, first, "first viewer")
	expectSilence(t, first, "second registration must not re-fire first-viewer")

	c2 := newClient(nil)
	h.register <- c2
	expectSilence(t, first, "first-viewer fired twice")

	h.unregister <- c1
	expectSilence(t, last, "one viewer left, should not fire last-viewer")
	h.unregister <- c2
	expectSignal(t, last, "last viewer")
}

func expectSignal(t *testing.T, ch chan int, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("%s: callback never fired", what)
	}
}

func expectSilence(t *testing.T, ch chan int, what string) {
	t.Helper()
	select {
	case <-ch:
		t.Fatalf("%s: callback fired unexpectedly", what)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTaggedFramesSurviveRoundTrip(t *testing.T) {
	// The browser splits on the first byte, so the payload must come back
	// byte-identical.
	original := []byte{0x00, 0x00, 0x00, 0x01, 0x65, 0xFF}
	tagged := TagVideo(original)
	if tagged[0] != FrameVideo {
		t.Fatalf("tag = %d", tagged[0])
	}
	if string(tagged[1:]) != string(original) {
		t.Error("payload altered by tagging")
	}
}
