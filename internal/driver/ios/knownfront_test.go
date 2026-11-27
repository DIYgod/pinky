package ios

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// fakeInspector is an inspector session that answers as a test sets it.
// Tests change its fields between reads, not during one.
type fakeInspector struct {
	mu      sync.Mutex
	nodes   []axNode
	walkErr error
	pingErr error
	apps    []axApp   // the app states it was told of
	since   time.Time // when it began to listen
	closed  bool
	walks   int
	pings   int
}

func (f *fakeInspector) walk(context.Context, walkLimits) ([]axNode, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.walks++
	return f.nodes, f.walkErr == nil && len(f.nodes) > 0, f.walkErr
}

func (f *fakeInspector) ping(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pings++
	return f.pingErr
}

func (f *fakeInspector) events() ([]axApp, time.Time, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return nil, time.Time{}, false
	}
	return append([]axApp(nil), f.apps...), f.since, true
}

func (f *fakeInspector) counts() (walks, pings int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.walks, f.pings
}

// owned is an element of process pid, as the walk gives it.
func owned(pid uint64, caption string) axNode {
	h := make([]byte, 8)
	binary.LittleEndian.PutUint32(h, uint32(pid))
	return axNode{handle: h, caption: caption}
}

// testShot is a screenshot of a blank screen.
var testShot = sync.OnceValue(func() []byte {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewGray(image.Rect(0, 0, 375, 812))); err != nil {
		panic(err)
	}
	return b.Bytes()
})

// noteLines is what OCR reads in a picture: a note.
var noteLines = []ocrLine{
	{Text: "Shopping list", Confidence: 0.95, X: 0.08, Y: 0.12, W: 0.4, H: 0.03},
	{Text: "Milk and eggs", Confidence: 0.9, X: 0.08, Y: 0.18, W: 0.45, H: 0.03},
}

var (
	bankApp    = driver.App{ID: "com.bank.app", Name: "Bank"} // mayCapture blocks it
	notesApp   = driver.App{ID: "com.apple.mobilenotes", Name: "Notes"}
	bankFront  = screen.App{ID: bankApp.ID, Name: bankApp.Name}
	notesFront = screen.App{ID: notesApp.ID, Name: notesApp.Name}
)

// errSilentWalk is the inspector walk of a daemon that does not answer:
// its first focus request timed out (#248).
var errSilentWalk = fmt.Errorf("%w: focus: %v", errAXWalk, context.DeadlineExceeded)

// fallbackPhone is an iOS 27 iPhone whose remoteAXService just did not
// answer, so that its reads fall back to the inspector session insp. Its
// home screen lists Bank (process 950) and Notes (951); processes 952
// (WebKit's web content) and 953 (a system service) are no app. It is
// unlocked; shots counts its screenshots, ocrs the pictures OCR read, and
// asked holds the apps of the screens the gateway's MayCapture was asked
// about.
type fallbackPhone struct {
	d     *Device
	insp  *fakeInspector
	logs  *syncBuffer
	shots atomic.Int32
	ocrs  atomic.Int32
	asked []screen.App
}

func newFallbackPhone() *fallbackPhone {
	now := time.Now()
	p := &fallbackPhone{insp: &fakeInspector{since: now.Add(-time.Minute)}, logs: &syncBuffer{}}
	p.d = &Device{
		info:        driver.Info{ID: "iphone"},
		log:         slog.New(slog.NewTextHandler(p.logs, nil)),
		raxErr:      fmt.Errorf("%w within 5s", errRAXTimeout),
		raxFailedAt: now,
		apps: &appCatalog{apps: []driver.App{bankApp, notesApp,
			{ID: "com.apple.Preferences", Name: "Settings", System: true}}, at: now},
		pidApps:  map[uint64]driver.App{950: bankApp, 951: notesApp},
		pidMiss:  map[uint64]time.Time{952: now, 953: now},
		web:      webCache{m: map[int]webEntry{952: {web: true, at: now}, 953: {at: now}}},
		scr:      screenInfo{Width: 375, Height: 812, Scale: 1},
		inspect:  p.insp,
		lockedFn: func(context.Context) (bool, error) { return false, nil },
		shotFn: func(context.Context) ([]byte, error) {
			p.shots.Add(1)
			return testShot(), nil
		},
		recognize: func([]byte) ([]ocrLine, error) {
			p.ocrs.Add(1)
			return noteLines, nil
		},
	}
	return p
}

// opt is a read as the gateway asks for it: a picture only of a screen it
// can name, and never of Bank's (mayCapture).
func (p *fallbackPhone) opt() driver.ObserveOptions {
	return driver.ObserveOptions{MayCapture: func(s *screen.Screen) bool {
		p.asked = append(p.asked, s.App)
		return mayCapture(s)
	}}
}

func (p *fallbackPhone) read(t *testing.T) (*screen.Screen, ObserveStats, error) {
	return p.d.ObserveWithStats(t.Context(), p.opt())
}

// known is the app the phone recorded, and when the read that named it
// began.
func (p *fallbackPhone) known() (screen.App, time.Time) {
	p.d.mu.Lock()
	defer p.d.mu.Unlock()
	return p.d.knownApp, p.d.knownAt
}

func screenLabels(s *screen.Screen) []string {
	var l []string
	for _, e := range s.Elements {
		l = append(l, e.Label)
	}
	return l
}

// TestFallbackReadNamesNoHomeScreen (#248): a read without a tree used to
// be named from the inspector session's app states, the home screen when
// the session had heard of none, so the gateway let it take a picture of
// whatever was in front, a blocked app included. Without a read that named
// the app it now names none, takes no picture, and fails with what failed;
// the states, which iOS also gives apps it wakes in the background, name
// nothing.
func TestFallbackReadNamesNoHomeScreen(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*fakeInspector)
		want error
	}{
		{"the daemon silent", func(f *fakeInspector) { f.walkErr = errSilentWalk }, errAXWalk},
		{"no elements", func(f *fakeInspector) { f.walkErr = fmt.Errorf("%w: %w", errAXWalk, errAXNoHandle) }, errAppUnknown},
		{"elements of no app", func(f *fakeInspector) { f.nodes = []axNode{owned(953, "Allow, Button")} }, errAppUnknown},
		{"an app the session saw come", func(f *fakeInspector) {
			f.nodes = []axNode{owned(953, "Allow, Button")}
			f.apps = []axApp{{id: notesApp.ID, pid: 951, state: appForeground, at: time.Now()}}
		}, errAppUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newFallbackPhone()
			c.set(p.insp)
			s, _, err := p.read(t)
			if s != nil || !errors.Is(err, c.want) {
				t.Fatalf("read = %+v, %v; want no screen and %v", s, err, c.want)
			}
			if c.want == errAppUnknown && !errors.Is(err, errRAXTimeout) {
				t.Errorf("the error does not say why remoteAXService was not used: %v", err)
			}
			if n := p.shots.Load() + p.ocrs.Load(); n != 0 {
				t.Errorf("the read took or read %d pictures", n)
			}
			if len(p.asked) != 1 || p.asked[0] != (screen.App{}) {
				t.Errorf("MayCapture was asked about %v, want one screen with no app", p.asked)
			}
		})
	}
}

// TestFallbackReadNamesTheKnownApp: a read without a tree, or with one of
// no app, is the app a read named less than lastFrontKeep before, when the
// session was told of no other app coming since (a web view or the
// keyboard is none): partial, with the picture's text, and the record stays
// as it was.
func TestFallbackReadNamesTheKnownApp(t *testing.T) {
	for _, c := range []struct {
		name   string
		web    bool // the last read remoteAXService answered showed a web page: no walk
		set    func(*fakeInspector)
		source string
		label  string
	}{
		{"no elements", false, func(f *fakeInspector) { f.walkErr = fmt.Errorf("%w: %w", errAXWalk, errAXNoHandle) }, "inspector", "Shopping list"},
		{"elements of no app", false, func(f *fakeInspector) { f.nodes = []axNode{owned(953, "Allow, Button")} }, "inspector", "Allow"},
		{"a web page", true, func(*fakeInspector) {}, "picture", "Shopping list"},
		{"a web view and the keyboard came", true, func(f *fakeInspector) {
			f.apps = []axApp{
				{id: "com.apple.WebKit.WebContent", state: appForeground, at: time.Now()},
				{id: "com.apple.InputUI", state: appForeground, at: time.Now()},
			}
		}, "picture", "Shopping list"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newFallbackPhone()
			at := time.Now().Add(-5 * time.Second)
			p.d.knewFront(notesFront, at)
			p.d.noteReadWeb(c.web)
			c.set(p.insp)
			s, st, err := p.read(t)
			if err != nil {
				t.Fatal(err)
			}
			if s.App != notesFront || !s.Partial || s.Locked || st.Source != c.source {
				t.Errorf("a screen of %+v, partial %v, locked %v, source %q", s.App, s.Partial, s.Locked, st.Source)
			}
			if !slices.Contains(screenLabels(s), c.label) {
				t.Errorf("the screen lacks %q: %q", c.label, screenLabels(s))
			}
			if p.shots.Load() != 1 || p.ocrs.Load() != 1 {
				t.Errorf("%d screenshots, %d read; want one each", p.shots.Load(), p.ocrs.Load())
			}
			if walks, pings := p.insp.counts(); c.web != (walks == 0 && pings == 1) {
				t.Errorf("%d walks and %d pings, after a web page: %v", walks, pings, c.web)
			}
			if app, known := p.known(); app != notesFront || !known.Equal(at) {
				t.Errorf("the record is %+v at %v, want Notes at %v", app, known, at)
			}
		})
	}
}

// TestFallbackReadForgetsTheApp: the record names no app once the read that
// named it began lastFrontKeep ago (a person who stayed on a web page while
// the daemon was stuck, #248), once the session was told of another app
// coming or of that one leaving, when the session began to listen after it
// or stopped listening, when the daemon answers no ping, or after a read
// found the phone locked. Such a read takes no picture and fails.
func TestFallbackReadForgetsTheApp(t *testing.T) {
	ago := func(d time.Duration) time.Time { return time.Now().Add(-d) }
	for _, c := range []struct {
		name  string
		front screen.App
		at    time.Duration // how long ago the read that named it began
		set   func(*testing.T, *fallbackPhone)
		want  error
	}{
		{"named too long ago", notesFront, lastFrontKeep + time.Second, nil, errAppUnknown},
		{"a blocked app named too long ago", bankFront, lastFrontKeep + time.Second, nil, errAppUnknown},
		{"another app came", notesFront, 5 * time.Second, func(_ *testing.T, p *fallbackPhone) {
			p.insp.apps = []axApp{{id: bankApp.ID, state: appForeground, at: ago(time.Second)}}
		}, errAppUnknown},
		{"the home screen came", notesFront, 5 * time.Second, func(_ *testing.T, p *fallbackPhone) {
			p.insp.apps = []axApp{{id: springBoard, state: appForeground, at: ago(time.Second)}}
		}, errAppUnknown},
		{"it left", notesFront, 5 * time.Second, func(_ *testing.T, p *fallbackPhone) {
			p.insp.apps = []axApp{{id: notesApp.ID, state: 4, at: ago(time.Second)}}
		}, errAppUnknown},
		{"the session listens since after it", notesFront, 5 * time.Second, func(_ *testing.T, p *fallbackPhone) {
			p.insp.since = ago(time.Second)
		}, errAppUnknown},
		{"the session stopped", notesFront, 5 * time.Second, func(_ *testing.T, p *fallbackPhone) {
			p.insp.closed = true
		}, errAppUnknown},
		{"no answer to the ping", notesFront, 5 * time.Second, func(_ *testing.T, p *fallbackPhone) {
			p.insp.pingErr = fmt.Errorf("%w: no answer to a ping: %v", errAXDaemon, context.DeadlineExceeded)
		}, errAXDaemon},
		{"locked since", notesFront, 5 * time.Second, func(t *testing.T, p *fallbackPhone) {
			p.d.lockedFn = func(context.Context) (bool, error) { return true, nil }
			if s, _, err := p.read(t); err != nil || !s.Locked {
				t.Fatalf("the locked read = %+v, %v", s, err)
			}
			p.d.lockedFn = func(context.Context) (bool, error) { return false, nil }
		}, errAppUnknown},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newFallbackPhone()
			p.d.knewFront(c.front, ago(c.at))
			p.d.noteReadWeb(true)
			if c.set != nil {
				c.set(t, p)
			}
			s, _, err := p.read(t)
			if s != nil || !errors.Is(err, c.want) {
				t.Fatalf("read = %+v, %v; want no screen and %v", s, err, c.want)
			}
			if n := p.shots.Load() + p.ocrs.Load(); n != 0 {
				t.Errorf("the read took or read %d pictures", n)
			}
			if walks, _ := p.insp.counts(); walks != 0 {
				t.Errorf("a read after a web page walked %d times", walks)
			}
		})
	}
}

// TestFallbackReadOfABlockedApp: a read the record names for an app the
// gateway blocks takes no picture, so without a tree it has nothing to
// show and fails; with elements of no app it shows those.
func TestFallbackReadOfABlockedApp(t *testing.T) {
	p := newFallbackPhone()
	p.d.knewFront(bankFront, time.Now().Add(-5*time.Second))
	p.d.noteReadWeb(true)
	s, _, err := p.read(t)
	if s != nil || !errors.Is(err, errRAXTimeout) {
		t.Fatalf("read = %+v, %v; want remoteAXService's error", s, err)
	}
	if len(p.asked) != 1 || p.asked[0] != bankFront {
		t.Errorf("MayCapture was asked about %v, want Bank", p.asked)
	}
	p.d.noteReadWeb(false)
	p.insp.nodes = []axNode{owned(953, "Allow, Button")}
	s, _, err = p.read(t)
	if err != nil || s.App != bankFront || !s.Partial || !slices.Contains(screenLabels(s), "Allow") {
		t.Fatalf("read = %+v, %v; want Bank's, partial, with the alert", s, err)
	}
	if n := p.shots.Load() + p.ocrs.Load(); n != 0 {
		t.Errorf("the reads took or read %d pictures of Bank", n)
	}
}

// TestWalkNamesItsApp: a walk whose elements' process is an app names it,
// as before, and records it for reads that cannot name one; a walk whose
// most elements are a web page's names the page's host.
func TestWalkNamesItsApp(t *testing.T) {
	p := newFallbackPhone()
	start := time.Now()
	p.insp.nodes = []axNode{owned(951, "Shopping list, Heading"), owned(951, "Milk and eggs")}
	s, _, err := p.read(t)
	if err != nil || s.App != notesFront || s.Partial {
		t.Fatalf("read = %+v, %v; want Notes', whole", s, err)
	}
	if app, at := p.known(); app != notesFront || at.Before(start) {
		t.Errorf("the record is %+v at %v, want Notes from this read", app, at)
	}
	p.insp.nodes = []axNode{owned(952, "Rates"), owned(952, "Loans"), owned(952, "Cards"), owned(950, "Back, Button")}
	s, _, err = p.read(t)
	if err != nil || s.App != bankFront || s.Partial {
		t.Fatalf("read = %+v, %v; want Bank's, whole", s, err)
	}
	if app, _ := p.known(); app != bankFront {
		t.Errorf("the record is %+v, want Bank", app)
	}
	if p.shots.Load() != 1 {
		t.Errorf("%d screenshots, want Notes' alone", p.shots.Load())
	}
}

// TestAXDeviceNamesTheSameWay: below iOS 27 the walk is the read, and a
// walk that names no app gets the same rule.
func TestAXDeviceNamesTheSameWay(t *testing.T) {
	p := newFallbackPhone()
	a := &AXDevice{d: p.d}
	p.insp.nodes = []axNode{owned(953, "Allow, Button")}
	if s, err := a.Observe(t.Context(), p.opt()); s != nil || !errors.Is(err, errAppUnknown) {
		t.Fatalf("read = %+v, %v; want no screen and errAppUnknown", s, err)
	}
	if p.shots.Load() != 0 {
		t.Errorf("an unnamed read took %d screenshots", p.shots.Load())
	}
	p.insp.nodes = []axNode{owned(951, "Shopping list, Heading")}
	if s, err := a.Observe(t.Context(), p.opt()); err != nil || s.App != notesFront || s.Partial {
		t.Fatalf("read = %+v, %v; want Notes', whole", s, err)
	}
	p.insp.nodes = []axNode{owned(953, "Allow, Button")}
	if s, err := a.Observe(t.Context(), p.opt()); err != nil || s.App != notesFront || !s.Partial {
		t.Fatalf("read = %+v, %v; want Notes', partial", s, err)
	}
}

func TestFrontStill(t *testing.T) {
	now := time.Now()
	at := now.Add(-10 * time.Second)
	since := now.Add(-time.Minute)
	ev := func(id string, state uint64, ago time.Duration) []axApp {
		return []axApp{{id: id, state: state, at: now.Add(-ago)}}
	}
	id := notesApp.ID
	for _, c := range []struct {
		name  string
		id    string
		at    time.Time
		since time.Time
		apps  []axApp
		want  bool
	}{
		{"nothing since", id, at, since, nil, true},
		{"no app", "", at, since, nil, false},
		{"named lastFrontKeep ago", id, now.Add(-lastFrontKeep), since, nil, false},
		{"listening since after it", id, at, at.Add(time.Millisecond), nil, false},
		{"listening since it", id, at, at, nil, true},
		{"the home screen came", id, at, since, ev(springBoard, appForeground, time.Second), false},
		{"another app came", id, at, since, ev(bankApp.ID, appForeground, time.Second), false},
		{"it left", id, at, since, ev(id, 4, time.Second), false},
		{"it came again", id, at, since, ev(id, appForeground, time.Second), true},
		{"another app came before it", id, at, since, ev(bankApp.ID, appForeground, 20*time.Second), true},
		{"another app went to the background", id, at, since, ev(bankApp.ID, 4, time.Second), true},
		{"a web view came", id, at, since, ev("com.apple.WebKit.WebContent", appForeground, time.Second), true},
		{"the keyboard came", id, at, since, ev("com.apple.InputUI", appForeground, time.Second), true},
	} {
		if got := frontStill(c.id, c.at, c.apps, c.since, now); got != c.want {
			t.Errorf("%s: frontStill = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestKnewFrontKeepsTheLatest: of reads that overlap, the one that began
// last names the app; a locked phone's forgets it.
func TestKnewFrontKeepsTheLatest(t *testing.T) {
	d := &Device{}
	t0 := time.Now()
	d.knewFront(notesFront, t0)
	d.knewFront(bankFront, t0.Add(-time.Second))
	if d.knownApp != notesFront {
		t.Fatalf("a read that began earlier replaced the record: %+v", d.knownApp)
	}
	d.knewFront(bankFront, t0.Add(time.Second))
	if d.knownApp != bankFront {
		t.Fatalf("a later read did not replace the record: %+v", d.knownApp)
	}
	d.forgetFront(t0.Add(2 * time.Second))
	if d.knownApp != (screen.App{}) {
		t.Fatalf("a locked phone kept the record: %+v", d.knownApp)
	}
	if _, ok := d.knownFront(&fakeInspector{since: t0.Add(-time.Minute)}); ok {
		t.Error("a forgotten record named an app")
	}
}
