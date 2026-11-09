package ui

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/video"
	"github.com/DIYgod/pinky/ui/fakeapi"
)

// TestRecordingPlaysAndSeeks plays a fake recording through the real
// decoder: the picture moves on, a step's mark jumps to that step's
// picture (the fake paints each step its own color), and Pause holds it.
func TestRecordingPlaysAndSeeks(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.page = pageActivity
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "Activity", func() bool { tt.Frame(); return ta.act.loaded })
	ta.act.sel = "h-lunch"
	ta.waitFor(t, "the recording", func() bool { tt.Frame(); return hasText(tt, "Watch recording") })
	if err := tt.Click("Watch recording"); err != nil {
		t.Fatal(err)
	}
	pl := ta.rec.pl
	t.Cleanup(ta.closeRecording)
	ta.waitFor(t, "the first pictures", func() bool {
		tt.Frame()
		return pl.bitmap != nil && pl.pos > 300*time.Millisecond
	})
	if pl.state != "playing" || pl.picW != 540 || pl.picH != 1200 {
		t.Fatalf("state %s, picture %dx%d, err %v", pl.state, pl.picW, pl.picH, pl.err)
	}

	var mark gateway.RecordingMark
	idx := -1
	for i, m := range pl.rec.Marks {
		if strings.HasPrefix(m.Text, "Placed the order") {
			mark, idx = m, i
		}
	}
	if err := tt.Click("Jump to " + clock(mark.At) + ": Placed the order"); err != nil {
		t.Fatal(err)
	}
	ta.waitFor(t, "the step's picture", func() bool {
		tt.Frame()
		return pl.state == "playing" && pl.pos >= mark.At && pl.pos < mark.At+3*time.Second && pl.markAt(pl.pos) == idx
	})
	// The fake paints step idx in its color; look a little below the
	// status bar, away from the digits.
	r, ok := tt.Find("Recording of Reorder last week's lunch")
	if !ok {
		t.Fatal("no player")
	}
	want := [][3]int{{38, 70, 140}, {32, 112, 96}, {130, 60, 120}, {150, 92, 30}, {60, 60, 72}, {44, 104, 150}}[idx%6]
	ta.waitFor(t, "the step's color", func() bool {
		tt.Frame()
		img := tt.Image()
		i := img.PixOffset(int(r.X+r.W/2), int(r.Y+r.H*0.15))
		got := [3]int{int(img.Pix[i]), int(img.Pix[i+1]), int(img.Pix[i+2])}
		for k := range got {
			if d := got[k] - want[k]; d > 24 || d < -24 {
				return false
			}
		}
		return true
	})

	if err := tt.Click("Pause"); err != nil {
		t.Fatal(err)
	}
	held := pl.pos
	time.Sleep(300 * time.Millisecond)
	tt.Frame()
	if pl.state != "paused" || pl.pos != held {
		t.Errorf("after Pause: %s at %v, was %v", pl.state, pl.pos, held)
	}
	before := len(ta.api.RecordingOpens())
	if err := tt.Click("Play"); err != nil {
		t.Fatal(err)
	}
	// The player opens the recording on a goroutine of its own.
	ta.waitFor(t, "the recording to open again", func() bool { return len(ta.api.RecordingOpens()) > before })
	opens := ta.api.RecordingOpens()
	if got := opens[len(opens)-1]; got != held {
		t.Errorf("Play went on from %v, not %v", got, held)
	}
}

// TestRecordingTimelineSeekWhilePaused clicks the timeline while paused and
// expects playback to open there, not at the start, and to stay paused.
func TestRecordingTimelineSeekWhilePaused(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("needs the ffmpeg command")
	}
	if err := video.Load(); err != nil {
		t.Skip(err)
	}
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.page = pageActivity
	tt := mg.NewTester(ta.view, 1280, 820)
	ta.waitFor(t, "Activity", func() bool { tt.Frame(); return ta.act.loaded })
	ta.act.sel = "h-lunch"
	ta.waitFor(t, "the recording", func() bool { tt.Frame(); return hasText(tt, "Watch recording") })
	if err := tt.Click("Watch recording"); err != nil {
		t.Fatal(err)
	}
	pl := ta.rec.pl
	t.Cleanup(ta.closeRecording)
	ta.waitFor(t, "the first pictures", func() bool {
		tt.Frame()
		return pl.bitmap != nil && pl.pos > 300*time.Millisecond
	})
	if err := tt.Click("Pause"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if pl.state != "paused" {
		t.Fatalf("before the seek: %s", pl.state)
	}
	r, ok := tt.Find(locales.RecordingPosition())
	if !ok {
		t.Fatal("no timeline")
	}
	want := pl.rec.Length * 3 / 4
	before := len(ta.api.RecordingOpens())
	tt.ClickAt(r.X+r.W*0.75, r.Y+r.H/2)
	ta.waitFor(t, "the seek", func() bool {
		tt.Frame()
		return len(ta.api.RecordingOpens()) > before
	})
	opens := ta.api.RecordingOpens()
	got := opens[len(opens)-1]
	if got < want-time.Second || got > want+2*time.Second {
		t.Errorf("opened at %v, want about %v", got, want)
	}
	ta.waitFor(t, "the paused picture", func() bool {
		tt.Frame()
		return pl.state == "paused" && pl.pos >= want-time.Second
	})
	if pl.state != "paused" {
		t.Errorf("after the seek: %s at %v", pl.state, pl.pos)
	}
}
