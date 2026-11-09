package ios

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The stream follows a fold: when the phone moves to its other display,
// the running stream no longer shows the display in use, and live starts
// it again on the new one.
func TestNoteDisplaysFollowsFold(t *testing.T) {
	d := &Device{log: discard}
	parse := func(innerLit, outerLit bool) displayInfo {
		t.Helper()
		di, err := parseDisplayInfo(duoReply(innerLit, outerLit), d.want)
		if err != nil {
			t.Fatal(err)
		}
		return di
	}
	streaming := func(on int) {
		// What startStream leaves: the stream for the display wanted, on
		// the display it got.
		d.mu.Lock()
		d.m = &media{display: on}
		d.streamFor, d.streamOn = d.want, on
		d.mu.Unlock()
	}

	si := d.noteDisplays(parse(true, false))
	if si.DisplayID != 1 || d.want != 1 {
		t.Fatalf("unfolded: %+v", si)
	}
	streaming(1)
	if d.displayMoved() {
		t.Fatal("moved right after the start")
	}

	// Folded: the outer display lights up.
	si = d.noteDisplays(parse(false, true))
	if si.DisplayID != 2 || si.Width != 1080 || d.display().Width != 1080 {
		t.Fatalf("folded: %+v", si)
	}
	if !d.displayMoved() {
		t.Fatal("a fold did not move the stream")
	}
	streaming(2)
	if d.displayMoved() {
		t.Fatal("moved after the restart")
	}

	// The same reply again: nothing moves.
	d.noteDisplays(parse(false, true))
	if d.displayMoved() {
		t.Fatal("moved without a fold")
	}

	// Unfolded again.
	if si = d.noteDisplays(parse(true, false)); si.DisplayID != 1 || !d.displayMoved() {
		t.Fatalf("unfold: %+v, moved %v", si, d.displayMoved())
	}
}

// A stream that fell back to another display than the one wanted shows
// that one: sizes and touches go by it, and nothing moves until the phone
// does.
func TestNoteDisplaysFallback(t *testing.T) {
	d := &Device{log: discard}
	folded, _ := parseDisplayInfo(duoReply(false, true), 0)
	unfolded, _ := parseDisplayInfo(duoReply(true, false), 0)
	d.noteDisplays(folded)
	// Display 2 gave no picture; display 1 did.
	d.mu.Lock()
	d.m = &media{display: 1}
	d.streamFor, d.streamOn = 2, 1
	d.goodFor, d.goodOn = 2, 1
	d.mu.Unlock()

	if si := d.noteDisplays(folded); si.DisplayID != 1 || si.Width != 2200 {
		t.Fatalf("the stream shows display 1, the screen says %+v", si)
	}
	if d.displayMoved() {
		t.Fatal("moved without a fold")
	}

	// Unfolded: the phone moved to the display the stream already shows.
	if si := d.noteDisplays(unfolded); si.DisplayID != 1 || d.displayMoved() {
		t.Fatalf("unfold onto the streamed display: %+v, moved %v", si, d.displayMoved())
	}
	if d.streamFor != 1 {
		t.Fatalf("streamFor = %d", d.streamFor)
	}

	// Folded again: a move, and the next start tries display 2 again.
	d.noteDisplays(folded)
	if !d.displayMoved() || d.goodFor != 0 {
		t.Fatalf("fold: moved %v, goodFor %d", d.displayMoved(), d.goodFor)
	}
}

// A phone with one display never moves, whatever its reply says.
func TestNoteDisplaysSingle(t *testing.T) {
	d := &Device{log: discard}
	one := func(id int, lit bool) displayInfo {
		di, err := parseDisplayInfo(map[string]any{"displays": []any{duoDisplay(id, 1179, 2556, true, map[string]any{"active": lit})}}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return di
	}
	d.noteDisplays(one(1, true))
	d.m = &media{display: 1}
	d.streamFor, d.streamOn = 1, 1
	for _, di := range []displayInfo{one(1, false), one(5, true)} {
		d.noteDisplays(di)
		if d.displayMoved() {
			t.Fatalf("moved: %+v", di.Displays)
		}
	}
	// Nor does a foldable without a stream.
	d.m = nil
	folded, _ := parseDisplayInfo(duoReply(false, true), 0)
	d.noteDisplays(folded)
	if d.displayMoved() {
		t.Fatal("moved without a stream")
	}
}

// fakeStreams starts streams that bring a picture or not, by display.
type fakeStreams struct {
	outcome map[int]any // "picture", "none", an error to start with, or a func(*media) that ends the stream
	started []int
	stopped []int
	gone    bool
}

func (f *fakeStreams) streamer() streamDisplay {
	return streamDisplay{
		start: func(_ context.Context, id int) (*media, error) {
			f.started = append(f.started, id)
			m := &media{display: id, ready: make(chan struct{}), done: make(chan struct{})}
			switch o := f.outcome[id].(type) {
			case error:
				return nil, o
			case string:
				if o == "picture" {
					close(m.ready)
				}
			case func(*media):
				o(m)
			}
			return m, nil
		},
		stop:   func(m *media) { f.stopped = append(f.stopped, m.display) },
		gone:   func() bool { return f.gone },
		wait:   10 * time.Millisecond,
		log:    discard,
		device: "duo",
	}
}

func TestStreamDisplayFallsBack(t *testing.T) {
	f := &fakeStreams{outcome: map[int]any{2: "none", 1: "picture"}}
	ds := parseDisplays(duoReply(false, true))
	m, nv, err := f.streamer().try(context.Background(), []int{2, 1}, ds)
	if err != nil {
		t.Fatal(err)
	}
	if m.display != 1 || !reflect.DeepEqual(f.started, []int{2, 1}) || !reflect.DeepEqual(f.stopped, []int{2}) {
		t.Fatalf("streams on %d; started %v, stopped %v", m.display, f.started, f.stopped)
	}
	if nv == nil || len(nv.tries) != 1 || nv.tries[0].Display != 2 {
		t.Fatalf("tries before: %+v", nv)
	}

	// The first display works: nothing to tell.
	f = &fakeStreams{outcome: map[int]any{2: "picture"}}
	if m, nv, err = f.streamer().try(context.Background(), []int{2, 1}, ds); err != nil || m.display != 2 || nv != nil {
		t.Fatalf("got %v %+v %v", m, nv, err)
	}
}

func TestStreamDisplayNoVideoAnywhere(t *testing.T) {
	f := &fakeStreams{outcome: map[int]any{}}
	ds := parseDisplays(duoReply(false, true))
	_, _, err := f.streamer().try(context.Background(), []int{2, 1}, ds)
	var nv *noVideoError
	if !errors.As(err, &nv) {
		t.Fatalf("err = %v", err)
	}
	if got := err.Error(); got != "no video from the device (displays 2, 1): context deadline exceeded" {
		t.Errorf("message %q", got)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Error("not a deadline")
	}
	want := "2 displays: id 1 2200x2480 primary off, id 2 1080x2400 lit. " +
		"Display 2: 0 RTP packets (0 bytes), 0 RTCP. Display 1: 0 RTP packets (0 bytes), 0 RTCP."
	if got := nv.Detail(); got != want {
		t.Errorf("detail\n got %q\nwant %q", got, want)
	}
	if !reflect.DeepEqual(f.stopped, []int{2, 1}) {
		t.Errorf("stopped %v", f.stopped)
	}
}

// A phone with one display tries display 1 alone, and says so as before.
func TestStreamDisplaySingle(t *testing.T) {
	f := &fakeStreams{outcome: map[int]any{}}
	ds := parseDisplays(map[string]any{"displays": []any{map[string]any{
		"primary": true, "bounds": []any{[]any{0.0, 0.0}, []any{1179.0, 2556.0}}, "backlightState": "activeOn",
	}}})
	ids := streamDisplays(ds, 0)
	_, _, err := f.streamer().try(context.Background(), ids, ds)
	if err == nil || err.Error() != "no video from the device: context deadline exceeded" {
		t.Fatalf("err = %v", err)
	}
	if !reflect.DeepEqual(f.started, []int{1}) {
		t.Fatalf("started %v", f.started)
	}
	var nv *noVideoError
	errors.As(err, &nv)
	if got := nv.Detail(); got != "1 display: no id 1179x2556 primary lit (activeOn). Display 1: 0 RTP packets (0 bytes), 0 RTCP." {
		t.Errorf("detail %q", got)
	}
}

// A display the phone refuses to stream is passed over; a refusal that
// no display changes (an app holds the camera) or another failure ends the
// start at once with its own error.
func TestStreamDisplayStartFails(t *testing.T) {
	ds := parseDisplays(duoReply(false, true))
	refused := &CoreDeviceError{Code: 1, Domain: "com.apple.dt.CoreDeviceError", Desc: "invalid display"}
	f := &fakeStreams{outcome: map[int]any{2: refused, 1: "picture"}}
	m, nv, err := f.streamer().try(context.Background(), []int{2, 1}, ds)
	if err != nil || m.display != 1 {
		t.Fatalf("got %v, %v", m, err)
	}
	if nv == nil || nv.tries[0].Err != refused {
		t.Fatalf("tries: %+v", nv)
	}
	if d := nv.Detail(); !strings.Contains(d, "Display 2: start failed: invalid display") {
		t.Errorf("detail %q", d)
	}

	inUse := &CoreDeviceError{Code: errMediaInUse, Domain: "com.apple.dt.CoreDeviceError", Desc: "in use"}
	f = &fakeStreams{outcome: map[int]any{2: inUse, 1: "picture"}}
	if _, _, err = f.streamer().try(context.Background(), []int{2, 1}, ds); err != inUse || len(f.started) != 1 {
		t.Fatalf("in use: %v, started %v", err, f.started)
	}

	other := errors.New("connection reset")
	f = &fakeStreams{outcome: map[int]any{2: other, 1: "picture"}}
	if _, _, err = f.streamer().try(context.Background(), []int{2, 1}, ds); err != other {
		t.Fatalf("other: %v", err)
	}

	// A start that fails after a display without a picture still tells
	// of that display.
	f = &fakeStreams{outcome: map[int]any{2: "none", 1: other}}
	_, _, err = f.streamer().try(context.Background(), []int{2, 1}, ds)
	var e *noVideoError
	if !errors.As(err, &e) || len(e.tries) != 2 || e.cause != other {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(e.Detail(), "Display 1: start failed: connection reset.") {
		t.Errorf("detail %q", e.Detail())
	}
}

// When the connection goes away, no other display is tried.
func TestStreamDisplayLinkGone(t *testing.T) {
	f := &fakeStreams{outcome: map[int]any{1: "picture"}, gone: true}
	_, _, err := f.streamer().try(context.Background(), []int{2, 1}, parseDisplays(duoReply(false, true)))
	if err == nil || !reflect.DeepEqual(f.started, []int{2}) {
		t.Fatalf("err %v, started %v", err, f.started)
	}
}

// A stream the phone ends before its first picture says why.
func TestStreamDisplayEnded(t *testing.T) {
	bye := errors.New("the phone ended the video stream (RTCP BYE)")
	f := &fakeStreams{outcome: map[int]any{1: func(m *media) {
		// What fail does, without the connection a real stream has.
		m.err = bye
		m.stopped.Store(true)
		close(m.done)
	}}}
	_, _, err := f.streamer().try(context.Background(), []int{1}, nil)
	if err == nil || err.Error() != "no video from the device: "+bye.Error() || !errors.Is(err, bye) {
		t.Fatalf("err = %v", err)
	}
}
