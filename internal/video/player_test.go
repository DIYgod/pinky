package video

import (
	"context"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// gatedStream hands over packets recorded beforehand only when the test
// sends them, so it can pause a stream as a still phone screen does.
type gatedStream struct {
	codec   driver.Codec
	w, h    int
	packets chan driver.Packet
}

func (s *gatedStream) Codec() driver.Codec           { return s.codec }
func (s *gatedStream) Size() (int, int)              { return s.w, s.h }
func (s *gatedStream) Packets() <-chan driver.Packet { return s.packets }
func (s *gatedStream) Err() error                    { return nil }
func (s *gatedStream) Close() error                  { return nil }

// A mirror of a still screen stops taking pictures until OnFrame wakes it.
// When the stream comes back after a pause longer than the player's idle
// second, the pictures after the first must still be converted: the mirror
// takes them, just not before the next has come.
func TestPlayerAfterPause(t *testing.T) {
	needFFmpeg(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	src, err := StartTestStream(ctx, TestStreamOptions{Width: 320, Height: 640, FPS: 30})
	if err != nil {
		t.Fatal(err)
	}
	var recorded []driver.Packet
	for p := range src.Packets() {
		recorded = append(recorded, p)
		if len(recorded) == 8 {
			break
		}
	}
	src.Close()
	if len(recorded) < 8 {
		t.Fatalf("recorded %d packets: %v", len(recorded), src.Err())
	}

	s := &gatedStream{codec: driver.H264, w: 320, h: 640, packets: make(chan driver.Packet, 16)}
	frames := make(chan struct{}, 16)
	p, err := NewPlayer(s, PlayerOptions{YUV: true, OnFrame: func() { frames <- struct{}{} }})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	defer close(s.packets)
	next := 0
	send := func() {
		// The configuration rides with the first key frame.
		for next < len(recorded) {
			pkt := recorded[next]
			next++
			s.packets <- pkt
			if !pkt.Config {
				return
			}
		}
	}
	wait := func(what string) {
		select {
		case <-frames:
		case <-ctx.Done():
			t.Fatalf("%s: no picture: %+v", what, p.Stats())
		}
	}

	send()
	wait("first")
	if !p.Pending() || p.Take() == nil || p.Pending() {
		t.Fatal("the first picture was not pending, or still is after Take")
	}
	// The screen stays still for longer than the idle second.
	time.Sleep(1200 * time.Millisecond)
	// Two pictures come before the woken mirror takes any.
	send()
	wait("second")
	send()
	wait("third")
	f := p.Take()
	st := p.Stats()
	if f == nil || f.Seq != 3 || st.Idle != 0 || st.Dropped != 1 {
		t.Fatalf("after the pause took %+v; stats %+v; want the third picture, none idle, one dropped", f, st)
	}
}
