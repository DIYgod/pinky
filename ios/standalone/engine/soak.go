package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// A soak is the survival measurement without a model: with another app in
// front, the engine keeps reading the screen and, in Calculator, tapping
// digits, round after round, and counts what worked. It acts only while
// the soak's app is in front, so it does nothing if the person switches
// away. When the elements cannot be read it takes a screenshot instead,
// which counts as pixels, and taps nothing: without the elements it cannot
// tell which app is in front.
type soak struct {
	cancel context.CancelFunc
	app    string
	tap    bool
	rounds int
	ok     int
	away   int
	pixels int
	last   string
	done   bool
}

// SoakConfig is a soak's setup.
type SoakConfig struct {
	App     string        `json:"app"`     // bundle id; Calculator by default
	Tap     bool          `json:"tap"`     // tap digits, not only read
	Every   time.Duration `json:"every"`   // between rounds
	Minutes int           `json:"minutes"` // how long
}

// SoakStatus is a soak's progress.
type SoakStatus struct {
	App    string `json:"app"`
	Tap    bool   `json:"tap"`
	Rounds int    `json:"rounds"`
	OK     int    `json:"ok"`
	Away   int    `json:"away"`
	Pixels int    `json:"pixels"` // rounds with a screenshot but no elements
	Last   string `json:"last,omitempty"`
	Done   bool   `json:"done"`
}

const calculator = "com.apple.calculator"

var clearLabels = []string{"All Clear", "Clear", "AC", "C", "全部清除", "清除"}

// StartSoak starts a soak, ending the one before.
func (e *Engine) StartSoak(cfg SoakConfig) error {
	if cfg.App == "" {
		cfg.App = calculator
	}
	if cfg.Tap && cfg.App != calculator {
		return errors.New("a soak taps only in Calculator")
	}
	if e.isSelf(cfg.App) {
		return ErrOwnLaunch
	}
	if cfg.Every < time.Second {
		cfg.Every = 10 * time.Second
	}
	if cfg.Minutes <= 0 {
		cfg.Minutes = 10
	}
	e.stopSoak()
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Minutes)*time.Minute)
	s := &soak{cancel: cancel, app: cfg.App, tap: cfg.Tap}
	e.mu.Lock()
	e.soak = s
	e.mu.Unlock()
	go e.runSoak(ctx, s, cfg)
	e.logf("soak started: %s tap=%t every=%s for %dm", cfg.App, cfg.Tap, cfg.Every, cfg.Minutes)
	return nil
}

func (e *Engine) stopSoak() {
	e.mu.Lock()
	s := e.soak
	e.mu.Unlock()
	if s != nil {
		s.cancel()
	}
}

// Soak is the current or last soak, if any.
func (e *Engine) Soak() *SoakStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.soak
	if s == nil {
		return nil
	}
	return &SoakStatus{App: s.app, Tap: s.tap, Rounds: s.rounds, OK: s.ok, Away: s.away, Pixels: s.pixels, Last: s.last, Done: s.done}
}

func (e *Engine) runSoak(ctx context.Context, s *soak, cfg SoakConfig) {
	defer func() {
		e.mu.Lock()
		s.done = true
		e.mu.Unlock()
		e.logf("soak ended: %d/%d ok, %d away, %d pixels only", s.ok, s.rounds, s.away, s.pixels)
	}()
	if err := e.leave(ctx); err != nil {
		e.soakRound(s, outcome{note: "leave: " + err.Error()})
		return
	}
	if err := e.Launch(ctx, cfg.App); err != nil {
		e.soakRound(s, outcome{note: "launch: " + err.Error()})
	}
	sleep(ctx, 2*time.Second)
	t := time.NewTicker(cfg.Every)
	defer t.Stop()
	for i := 0; ; i++ {
		o := e.soakOnce(ctx, cfg, i)
		if ctx.Err() != nil {
			return
		}
		e.soakRound(s, o)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// outcome is one soak round's.
type outcome struct {
	ok, away, pixels bool
	note             string
}

func (e *Engine) soakRound(s *soak, o outcome) {
	e.mu.Lock()
	s.rounds++
	if o.ok {
		s.ok++
	}
	if o.away {
		s.away++
	}
	if o.pixels {
		s.pixels++
	}
	s.last = o.note
	e.mu.Unlock()
	e.logf("soak round %d: %s", s.rounds, o.note)
}

func (e *Engine) soakOnce(ctx context.Context, cfg SoakConfig, i int) outcome {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	before, err := e.Observe(ctx)
	if err != nil {
		note := "observe: " + err.Error()
		start := time.Now()
		b, err := e.Screenshot(ctx)
		if err != nil {
			return outcome{note: note + "; screenshot: " + err.Error()}
		}
		return outcome{pixels: true, note: fmt.Sprintf("screenshot %d bytes in %v; %s", len(b), time.Since(start).Round(time.Millisecond), note)}
	}
	read := time.Since(start).Round(time.Millisecond)
	if before.App.ID != cfg.App {
		return outcome{away: true, note: "away: " + before.App.ID}
	}
	if !cfg.Tap {
		return outcome{ok: true, note: fmt.Sprintf("read %d elements in %v", len(before.Elements), read)}
	}
	label := fmt.Sprint(i%9 + 1)
	if i%5 == 4 {
		if el, found := findLabel(before, clearLabels...); found {
			label = el.Label
		}
	}
	el, found := findLabel(before, label)
	if !found {
		return outcome{note: "no button " + label}
	}
	tapped := time.Now()
	if err := e.Tap(ctx, el.Bounds.Center(), false); err != nil {
		return outcome{note: "tap: " + err.Error()}
	}
	tap := time.Since(tapped).Round(time.Millisecond)
	sleep(ctx, e.cfg.Settle)
	after, err := e.Observe(ctx)
	if err != nil {
		return outcome{note: "observe after tap: " + err.Error()}
	}
	if lines, full := screen.Diff(before, after); !full && len(lines) == 0 {
		return outcome{note: "tapped " + label + "; nothing changed"}
	}
	return outcome{ok: true, note: fmt.Sprintf("read in %v, tapped %s in %v", read, label, tap)}
}

func findLabel(s *screen.Screen, labels ...string) (screen.Element, bool) {
	for _, el := range s.Elements {
		if el.Role == screen.RoleButton && slices.Contains(labels, el.Label) && !el.Bounds.Empty() {
			return el, true
		}
	}
	return screen.Element{}, false
}

// Probe is the Q1 check in one call: leave Pinky's UI, open app, read its
// screen as elements and as pixels and, in Calculator, tap 7 and read the
// change. When the elements cannot be read, it still measures the pixels
// and, given blind, taps there and compares screenshots instead.
func (e *Engine) Probe(ctx context.Context, app string, blind *screen.Point) (map[string]any, error) {
	if app == "" {
		app = calculator
	}
	if e.isSelf(app) {
		return nil, ErrOwnLaunch
	}
	out := map[string]any{"app": app}
	step := func(name string, f func() error) error {
		start := time.Now()
		err := f()
		out[name+"_ms"] = time.Since(start).Milliseconds()
		if err != nil {
			out[name+"_error"] = err.Error()
		}
		return err
	}
	if err := step("leave", func() error { return e.leave(ctx) }); err != nil {
		return out, nil
	}
	if err := step("launch", func() error { return e.Launch(ctx, app) }); err != nil {
		return out, nil
	}
	launched := time.Now()
	sleep(ctx, 1500*time.Millisecond)
	var before *screen.Screen
	if err := step("observe", func() (err error) { before, err = e.Observe(ctx); return }); err == nil {
		out["foreground"] = before.App.ID
		out["elements"] = len(before.Elements)
	}
	var shot []byte
	step("screenshot", func() (err error) {
		shot, err = e.Screenshot(ctx)
		out["screenshot_bytes"] = len(shot)
		return err
	})
	if app != calculator {
		return out, nil
	}
	if before == nil {
		if blind == nil || shot == nil {
			return out, nil
		}
		if err := step("tap", func() error { return e.tapBlind(ctx, *blind, launched) }); err != nil {
			return out, nil
		}
		sleep(ctx, e.cfg.Settle)
		var after []byte
		if step("screenshot_after", func() (err error) { after, err = e.Screenshot(ctx); return }) == nil {
			out["pixels_changed"] = !bytes.Equal(shot, after)
		}
		return out, nil
	}
	el, found := findLabel(before, "7")
	if !found {
		out["tap_error"] = "no button 7"
		return out, nil
	}
	if err := step("tap", func() error { return e.Tap(ctx, el.Bounds.Center(), false) }); err != nil {
		return out, nil
	}
	sleep(ctx, e.cfg.Settle)
	var after *screen.Screen
	if err := step("observe_after", func() (err error) { after, err = e.Observe(ctx); return }); err != nil {
		return out, nil
	}
	lines, _ := screen.Diff(before, after)
	out["changes"] = lines
	return out, nil
}

// tapBlind taps without the look at the screen act takes first, for the
// probe when the elements cannot be read. Only within a few seconds of a
// launch the phone accepted, which it refuses while locked, so the app
// launched is the one in front; the guard still stops it while Pinky's own
// UI is.
func (e *Engine) tapBlind(ctx context.Context, pt screen.Point, launched time.Time) error {
	e.input.Lock()
	defer e.input.Unlock()
	if time.Since(launched) > 5*time.Second {
		return errors.New("too long since the launch to tap without reading the screen")
	}
	p, err := e.guard()
	if err != nil {
		return err
	}
	start := time.Now()
	err = p.Tap(ctx, pt, driver.TapOptions{})
	e.record("tap", start, err)
	return err
}
