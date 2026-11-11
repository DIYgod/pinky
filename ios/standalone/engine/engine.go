// Package engine is the core of the standalone prototype: Pinky on the
// iPhone it drives. It holds the phone (Pinky's iOS driver over the phone's
// own CoreDevice tunnel), refuses input that could reach Pinky's own UI,
// keeps the one approval the agent may wait on, runs the agent and answers
// the Swift side's calls (ops.go). It has no build tag, so it is tested on a
// Mac against a fake phone.
package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/ios/standalone/agent"
)

// Phone is what the engine drives: internal/driver/ios's Device opened with
// OpenOverTunnel.
type Phone interface {
	Observe(ctx context.Context, opt driver.ObserveOptions) (*screen.Screen, error)
	Tap(ctx context.Context, p screen.Point, opt driver.TapOptions) error
	Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error
	Type(ctx context.Context, text string, opt driver.TypeOptions) error
	Key(ctx context.Context, k driver.Key) error
	LaunchID(ctx context.Context, bundleID string) error
	ScreenshotPNG(ctx context.Context) ([]byte, error)
	Close() error
}

// Config is an engine's setup.
type Config struct {
	// Self are the bundle ids of Pinky's own app and extensions: no input
	// while one of them is in front, and launch refuses them.
	Self []string
	// Settle is how long an action waits before it observes the result.
	Settle time.Duration
	// Log, if set, receives one line per event worth logging.
	Log func(string)
	// Agent is the agent's model and limits, less the key, which comes
	// with each task.
	Agent agent.Config
}

// Errors of guarded calls.
var (
	ErrNoPhone   = errors.New("not connected to the phone")
	ErrOwnUI     = errors.New("blocked: Pinky's own app is in front; nothing was done")
	ErrApproval  = errors.New("blocked: an approval is waiting for the person; nothing was done")
	ErrLocked    = errors.New("the phone is locked; nothing was done")
	ErrOwnLaunch = errors.New("blocked: Pinky does not open its own app")
)

// Engine is the prototype's state. Its methods are safe for concurrent use.
type Engine struct {
	cfg Config

	mu       sync.Mutex
	phone    Phone
	state    string // "connecting", "ready", or why not
	uiActive bool
	refs     *screen.Refs
	last     *screen.Screen
	approval *approval
	run      *agent.Run
	events   []Event
	seq      int
	soak     *soak
	stats    Stats

	// input serializes input, so the guard sees the state each action
	// starts from.
	input sync.Mutex
}

// New returns an engine without a phone.
func New(cfg Config) *Engine {
	if cfg.Settle == 0 {
		cfg.Settle = 600 * time.Millisecond
	}
	return &Engine{cfg: cfg, state: "connecting", refs: screen.NewRefs(), stats: Stats{Started: time.Now()}}
}

func (e *Engine) logf(format string, args ...any) {
	if e.cfg.Log != nil {
		e.cfg.Log(fmt.Sprintf(format, args...))
	}
}

// SetPhone hands the engine a connected phone, closing the one it had.
func (e *Engine) SetPhone(p Phone) {
	e.mu.Lock()
	old := e.phone
	e.phone, e.state = p, "ready"
	e.refs, e.last = screen.NewRefs(), nil
	e.stats.Connects++
	e.stats.ConnectedAt = time.Now()
	e.mu.Unlock()
	if old != nil && old != p {
		old.Close()
	}
	e.logf("phone ready")
}

// SetState records why there is no phone, dropping the one it had.
func (e *Engine) SetState(state string) {
	e.mu.Lock()
	old := e.phone
	e.phone, e.state = nil, state
	e.mu.Unlock()
	if old != nil {
		old.Close()
	}
	e.logf("phone: %s", state)
}

// SetUIActive records whether Pinky's own UI is in front. The app calls it
// as soon as its scene becomes active and when it leaves.
func (e *Engine) SetUIActive(on bool) {
	e.mu.Lock()
	e.uiActive = on
	e.mu.Unlock()
}

func (e *Engine) isSelf(id string) bool { return id != "" && slices.Contains(e.cfg.Self, id) }

// guard returns the phone when input may go to it now.
func (e *Engine) guard() (Phone, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	switch {
	case e.phone == nil:
		return nil, fmt.Errorf("%w (%s)", ErrNoPhone, e.state)
	case e.uiActive:
		return nil, ErrOwnUI
	case e.approval != nil:
		return nil, ErrApproval
	case e.last != nil && e.isSelf(e.last.App.ID):
		return nil, ErrOwnUI
	case e.last != nil && e.last.Locked:
		return nil, ErrLocked
	}
	return e.phone, nil
}

func (e *Engine) current() (Phone, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.phone == nil {
		return nil, fmt.Errorf("%w (%s)", ErrNoPhone, e.state)
	}
	return e.phone, nil
}

// Observe reads the screen and gives its elements refs.
func (e *Engine) Observe(ctx context.Context) (*screen.Screen, error) {
	p, err := e.current()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	s, err := p.Observe(ctx, driver.ObserveOptions{})
	e.record("observe", start, err)
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	e.refs.Assign(s)
	e.last = s
	e.mu.Unlock()
	return s, nil
}

// act runs one input under the guard. The guard runs again right before
// the input, after a fresh look at the screen when the last one is old, so
// a switch to Pinky's own app between two actions stops the second.
func (e *Engine) act(ctx context.Context, name string, f func(Phone) error) error {
	e.input.Lock()
	defer e.input.Unlock()
	if _, err := e.guard(); err != nil {
		return err
	}
	e.mu.Lock()
	stale := e.last == nil || time.Since(e.last.CapturedAt) > 2*time.Second
	e.mu.Unlock()
	if stale {
		if _, err := e.Observe(ctx); err != nil {
			return err
		}
	}
	p, err := e.guard()
	if err != nil {
		return err
	}
	start := time.Now()
	err = f(p)
	e.record(name, start, err)
	return err
}

// Tap taps a point.
func (e *Engine) Tap(ctx context.Context, pt screen.Point, long bool) error {
	return e.act(ctx, "tap", func(p Phone) error { return p.Tap(ctx, pt, driver.TapOptions{LongPress: long}) })
}

// Swipe swipes from one point to another.
func (e *Engine) Swipe(ctx context.Context, from, to screen.Point, dur time.Duration) error {
	return e.act(ctx, "swipe", func(p Phone) error { return p.Swipe(ctx, from, to, dur) })
}

// Type types text into the focused field.
func (e *Engine) Type(ctx context.Context, text string, opt driver.TypeOptions) error {
	return e.act(ctx, "type", func(p Phone) error { return p.Type(ctx, text, opt) })
}

// Key presses a key: home, enter, delete.
func (e *Engine) Key(ctx context.Context, k driver.Key) error {
	return e.act(ctx, "key", func(p Phone) error { return p.Key(ctx, k) })
}

// Launch opens an app by its bundle id.
func (e *Engine) Launch(ctx context.Context, bundleID string) error {
	if e.isSelf(bundleID) {
		return ErrOwnLaunch
	}
	return e.act(ctx, "launch", func(p Phone) error { return p.LaunchID(ctx, bundleID) })
}

// leave takes Pinky's own UI off the screen before a task or a soak the
// person just started from it: the engine presses home itself, outside the
// guard, and waits for the app to report that it left.
func (e *Engine) leave(ctx context.Context) error {
	e.mu.Lock()
	p, active := e.phone, e.uiActive
	e.mu.Unlock()
	if p == nil {
		return ErrNoPhone
	}
	if !active {
		return nil
	}
	if err := p.Key(ctx, driver.KeyHome); err != nil {
		return err
	}
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); {
		sleep(ctx, 100*time.Millisecond)
		e.mu.Lock()
		active = e.uiActive
		if !active {
			e.last = nil
		}
		e.mu.Unlock()
		if !active {
			return nil
		}
	}
	return errors.New("Pinky's app did not leave the screen")
}

// Screenshot is the screen as PNG.
func (e *Engine) Screenshot(ctx context.Context) ([]byte, error) {
	p, err := e.current()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	b, err := p.ScreenshotPNG(ctx)
	e.record("screenshot", start, err)
	return b, err
}

// Event is something the Swift side tells the person about: an approval
// to answer, a task that ended.
type Event struct {
	Seq     int    `json:"seq"`
	Kind    string `json:"kind"` // "approval", "approval_closed", "task_ended"
	Text    string `json:"text,omitempty"`
	ID      string `json:"id,omitempty"`
	End     string `json:"end,omitempty"`
	Summary string `json:"summary,omitempty"`
}

func (e *Engine) emit(ev Event) {
	e.mu.Lock()
	e.seq++
	ev.Seq = e.seq
	e.events = append(e.events, ev)
	if len(e.events) > 50 {
		e.events = e.events[len(e.events)-50:]
	}
	e.mu.Unlock()
	e.logf("event %s %s", ev.Kind, ev.ID)
}

// Events returns the events after seq.
func (e *Engine) Events(after int) []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Event
	for _, ev := range e.events {
		if ev.Seq > after {
			out = append(out, ev)
		}
	}
	return out
}

type approval struct {
	id     string
	text   string
	asked  time.Time
	answer chan bool
}

// Approval is the approval waiting, if any.
type Approval struct {
	ID    string    `json:"id"`
	Text  string    `json:"text"`
	Asked time.Time `json:"asked"`
}

// RequestApproval asks the person and waits for the answer. Nothing can be
// typed or tapped while it waits.
func (e *Engine) RequestApproval(ctx context.Context, text string) (bool, error) {
	e.mu.Lock()
	if e.approval != nil {
		e.mu.Unlock()
		return false, errors.New("another approval is waiting")
	}
	e.seq++
	a := &approval{id: fmt.Sprintf("a%d", e.seq), text: text, asked: time.Now(), answer: make(chan bool, 1)}
	e.approval = a
	e.mu.Unlock()
	e.emit(Event{Kind: "approval", ID: a.id, Text: text})
	defer func() {
		e.mu.Lock()
		if e.approval == a {
			e.approval = nil
		}
		e.mu.Unlock()
		e.emit(Event{Kind: "approval_closed", ID: a.id})
	}()
	select {
	case yes := <-a.answer:
		return yes, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// Answer answers the approval with the given id.
func (e *Engine) Answer(id string, yes bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.approval == nil || e.approval.id != id {
		return errors.New("no such approval waiting")
	}
	select {
	case e.approval.answer <- yes:
	default:
	}
	return nil
}

// Pending is the approval waiting, if any.
func (e *Engine) Pending() *Approval {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.approval == nil {
		return nil
	}
	return &Approval{ID: e.approval.id, Text: e.approval.text, Asked: e.approval.asked}
}

// StartTask starts the agent on a task. key is the model API key, held in
// memory for this task only; model "" is the engine's default.
func (e *Engine) StartTask(ctx context.Context, task, key, model string) error {
	e.mu.Lock()
	if e.run != nil && e.run.Status().Running() {
		e.mu.Unlock()
		return errors.New("a task is running")
	}
	e.mu.Unlock()
	if err := e.leave(ctx); err != nil {
		return err
	}
	first := "(the screen could not be read)"
	if s, err := e.Observe(ctx); err == nil {
		first = s.Render()
	} else if errors.Is(err, ErrNoPhone) {
		return err
	}
	cfg := e.cfg.Agent
	cfg.Key = key
	if model != "" {
		cfg.Model = model
	}
	r := agent.Start(cfg, task, first, &tools{e: e}, func(st agent.Status) {
		if !st.Running() {
			e.emit(Event{Kind: "task_ended", End: string(st.End), Summary: st.Summary})
		}
	})
	e.mu.Lock()
	e.run = r
	e.mu.Unlock()
	e.logf("task started")
	return nil
}

// StopTask stops the running task, if any, and declines its approval.
func (e *Engine) StopTask() {
	e.mu.Lock()
	r := e.run
	if a := e.approval; a != nil {
		select {
		case a.answer <- false:
		default:
		}
	}
	e.mu.Unlock()
	if r != nil {
		r.Stop()
	}
	e.stopSoak()
}

// Task is the current or last task, if any.
func (e *Engine) Task() *agent.Status {
	e.mu.Lock()
	r := e.run
	e.mu.Unlock()
	if r == nil {
		return nil
	}
	st := r.Status()
	return &st
}
