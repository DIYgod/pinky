package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/DIYgod/pinky/internal/screen"
)

// A soak is the survival measurement without a model: with another app in
// front, the engine keeps reading the screen and, in Calculator, tapping
// digits, round after round, and counts what worked. It acts only while
// the soak's app is in front, so it does nothing if the person switches
// away.
type soak struct {
	cancel context.CancelFunc
	app    string
	tap    bool
	rounds int
	ok     int
	away   int
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
	return &SoakStatus{App: s.app, Tap: s.tap, Rounds: s.rounds, OK: s.ok, Away: s.away, Last: s.last, Done: s.done}
}

func (e *Engine) runSoak(ctx context.Context, s *soak, cfg SoakConfig) {
	defer func() {
		e.mu.Lock()
		s.done = true
		e.mu.Unlock()
		e.logf("soak ended: %d/%d ok, %d away", s.ok, s.rounds, s.away)
	}()
	if err := e.leave(ctx); err != nil {
		e.soakRound(s, false, false, "leave: "+err.Error())
		return
	}
	if err := e.Launch(ctx, cfg.App); err != nil {
		e.soakRound(s, false, false, "launch: "+err.Error())
	}
	sleep(ctx, 2*time.Second)
	t := time.NewTicker(cfg.Every)
	defer t.Stop()
	for i := 0; ; i++ {
		ok, away, note := e.soakOnce(ctx, cfg, i)
		if ctx.Err() != nil {
			return
		}
		e.soakRound(s, ok, away, note)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (e *Engine) soakRound(s *soak, ok, away bool, note string) {
	e.mu.Lock()
	s.rounds++
	if ok {
		s.ok++
	}
	if away {
		s.away++
	}
	s.last = note
	e.mu.Unlock()
}

func (e *Engine) soakOnce(ctx context.Context, cfg SoakConfig, i int) (ok, away bool, note string) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	before, err := e.Observe(ctx)
	if err != nil {
		return false, false, "observe: " + err.Error()
	}
	if before.App.ID != cfg.App {
		return false, true, "away: " + before.App.ID
	}
	if !cfg.Tap {
		return true, false, fmt.Sprintf("read %d elements", len(before.Elements))
	}
	label := fmt.Sprint(i%9 + 1)
	if i%5 == 4 {
		if el, found := findLabel(before, clearLabels...); found {
			label = el.Label
		}
	}
	el, found := findLabel(before, label)
	if !found {
		return false, false, "no button " + label
	}
	if err := e.Tap(ctx, el.Bounds.Center(), false); err != nil {
		return false, false, "tap: " + err.Error()
	}
	sleep(ctx, e.cfg.Settle)
	after, err := e.Observe(ctx)
	if err != nil {
		return false, false, "observe after tap: " + err.Error()
	}
	if lines, full := screen.Diff(before, after); !full && len(lines) == 0 {
		return false, false, "tapped " + label + "; nothing changed"
	}
	return true, false, "tapped " + label
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
// screen as elements and as pixels and, in Calculator, tap a digit and
// read the result. It reports what each step took.
func (e *Engine) Probe(ctx context.Context, app string) (map[string]any, error) {
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
	sleep(ctx, 1500*time.Millisecond)
	var before *screen.Screen
	if err := step("observe", func() (err error) { before, err = e.Observe(ctx); return }); err != nil {
		return out, nil
	}
	out["foreground"] = before.App.ID
	out["elements"] = len(before.Elements)
	step("screenshot", func() error {
		b, err := e.Screenshot(ctx)
		out["screenshot_bytes"] = len(b)
		return err
	})
	if app != calculator {
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
