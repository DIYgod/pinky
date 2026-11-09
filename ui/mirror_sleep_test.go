package ui

import (
	"context"
	"os/exec"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/video"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// gatedStream hands over packets only when the test sends them, as a
// phone's stream pauses while its screen stays still.
type gatedStream struct {
	packets chan driver.Packet
}

func (s *gatedStream) Codec() driver.Codec           { return driver.H264 }
func (s *gatedStream) Size() (int, int)              { return 320, 640 }
func (s *gatedStream) Packets() <-chan driver.Packet { return s.packets }
func (s *gatedStream) Err() error                    { return nil }
func (s *gatedStream) Close() error                  { return nil }

type gatedAPI struct {
	*fakeapi.API
	s *gatedStream
}

func (g *gatedAPI) Stream(context.Context, string, driver.StreamOptions) (driver.Stream, error) {
	return g.s, nil
}

// TestMirrorSleepsOnStillScreen: a mirror paints while pictures come,
// stops painting a moment after the last, and the next picture wakes it.
func TestMirrorSleepsOnStillScreen(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	src, err := video.StartTestStream(ctx, video.TestStreamOptions{Width: 320, Height: 640, FPS: 30})
	if err != nil {
		t.Fatal(err)
	}
	var recorded []driver.Packet
	for p := range src.Packets() {
		recorded = append(recorded, p)
		if len(recorded) == 6 {
			break
		}
	}
	src.Close()
	if len(recorded) < 6 {
		t.Fatalf("recorded %d packets: %v", len(recorded), src.Err())
	}
	gs := &gatedStream{packets: make(chan driver.Packet, 16)}
	next := 0
	send := func() {
		// The configuration rides with the first key frame.
		for next < len(recorded) {
			p := recorded[next]
			next++
			gs.packets <- p
			if !p.Config {
				return
			}
		}
	}

	ta := newTestApp(t, fakeapi.OnePhone)
	ta.app.api = &gatedAPI{API: ta.api, s: gs}
	ta.app.videoErr = nil
	t.Cleanup(ta.closeMirrors)
	ta.sync()
	if err := ta.api.StopAll(); err != nil { // no touch drawn on the mirror
		t.Fatal(err)
	}
	ta.waitFor(t, "the task to stop", func() bool { return !ta.anyRunning() })
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.frameUntil(t, tt, "the mirror to open", func() bool {
		m := ta.mirrors["pixel-9"]
		return m != nil && m.player != nil
	})
	m := ta.mirrors["pixel-9"]
	shown := func(seq uint64) func() bool {
		return func() bool { return m.frame != nil && m.frame.Seq == seq }
	}

	send()
	ta.frameUntil(t, tt, "the first picture", shown(1))
	tt.Advance(100 * time.Millisecond)
	if !tt.FramePending() || m.asleep.Load() {
		t.Fatal("the mirror stopped painting right after a picture")
	}
	// The screen stays still.
	tt.Advance(time.Second)
	if tt.FramePending() || !m.asleep.Load() {
		t.Fatalf("the mirror still paints a still screen (asks for a frame: %v)", tt.FramePending())
	}

	// A new picture wakes it.
	send()
	ta.waitFor(t, "the wake", func() bool { return !m.asleep.Load() })
	ta.frameUntil(t, tt, "the second picture", shown(2))
	if !tt.FramePending() {
		t.Fatal("the woken mirror does not paint")
	}
}
