package engine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// Request is one call from the Swift side, as JSON.
type Request struct {
	Op string `json:"op"`

	// observe, input
	X    int    `json:"x,omitempty"`
	Y    int    `json:"y,omitempty"`
	X2   int    `json:"x2,omitempty"`
	Y2   int    `json:"y2,omitempty"`
	Long bool   `json:"long,omitempty"`
	Text string `json:"text,omitempty"`
	Key  string `json:"key,omitempty"`
	App  string `json:"app,omitempty"`

	// task_start
	Task   string `json:"task,omitempty"`
	APIKey string `json:"api_key,omitempty"`
	Model  string `json:"model,omitempty"`

	// approve
	ID  string `json:"id,omitempty"`
	Yes bool   `json:"yes,omitempty"`

	// ui_active
	On bool `json:"on,omitempty"`

	// soak_start
	Tap     bool `json:"tap,omitempty"`
	EveryS  int  `json:"every_s,omitempty"`
	Minutes int  `json:"minutes,omitempty"`

	// status
	After int `json:"after,omitempty"`
}

// Status is what the app shows.
type Status struct {
	State     string      `json:"state"`
	UIActive  bool        `json:"ui_active"`
	Pending   *Approval   `json:"pending,omitempty"`
	Task      any         `json:"task,omitempty"`
	Soak      *SoakStatus `json:"soak,omitempty"`
	Events    []Event     `json:"events,omitempty"`
	Heartbeat string      `json:"heartbeat"`
	Stats     Stats       `json:"stats"`
}

// Status is the engine's status, with the events after seq.
func (e *Engine) Status(after int) Status {
	st := Status{Pending: e.Pending(), Soak: e.Soak(), Events: e.Events(after), Heartbeat: e.Heartbeat()}
	if t := e.Task(); t != nil {
		st.Task = t
	}
	e.mu.Lock()
	st.State, st.UIActive = e.state, e.uiActive
	st.Stats = e.stats
	e.mu.Unlock()
	return st
}

// Handle answers one JSON request with JSON: {"ok":true,...} or
// {"ok":false,"error":"..."}.
func (e *Engine) Handle(ctx context.Context, req []byte) []byte {
	var r Request
	if err := json.Unmarshal(req, &r); err != nil {
		return reply(nil, fmt.Errorf("bad request: %w", err))
	}
	v, err := e.do(ctx, r)
	return reply(v, err)
}

func reply(v any, err error) []byte {
	m := map[string]any{"ok": err == nil}
	if err != nil {
		m["error"] = err.Error()
	} else if v != nil {
		m["result"] = v
	}
	b, merr := json.Marshal(m)
	if merr != nil {
		b, _ = json.Marshal(map[string]any{"ok": false, "error": merr.Error()})
	}
	return b
}

func (e *Engine) do(ctx context.Context, r Request) (any, error) {
	timed := func(d time.Duration) (context.Context, context.CancelFunc) { return context.WithTimeout(ctx, d) }
	switch r.Op {
	case "status":
		return e.Status(r.After), nil
	case "ui_active":
		e.SetUIActive(r.On)
		return nil, nil
	case "observe":
		ctx, cancel := timed(20 * time.Second)
		defer cancel()
		start := time.Now()
		s, err := e.Observe(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"screen": s.Render(), "ms": time.Since(start).Milliseconds(), "elements": len(s.Elements)}, nil
	case "screenshot":
		ctx, cancel := timed(20 * time.Second)
		defer cancel()
		start := time.Now()
		b, err := e.Screenshot(ctx)
		if err != nil {
			return nil, err
		}
		return map[string]any{"png": base64.StdEncoding.EncodeToString(b), "bytes": len(b), "ms": time.Since(start).Milliseconds()}, nil
	case "tap", "swipe", "type", "key", "launch":
		ctx, cancel := timed(20 * time.Second)
		defer cancel()
		start := time.Now()
		var err error
		switch r.Op {
		case "tap":
			err = e.Tap(ctx, screen.Point{X: r.X, Y: r.Y}, r.Long)
		case "swipe":
			err = e.Swipe(ctx, screen.Point{X: r.X, Y: r.Y}, screen.Point{X: r.X2, Y: r.Y2}, 300*time.Millisecond)
		case "type":
			err = e.Type(ctx, r.Text, driver.TypeOptions{Append: true})
		case "key":
			err = e.Key(ctx, driver.Key(r.Key))
		case "launch":
			err = e.Launch(ctx, r.App)
		}
		if err != nil {
			return nil, err
		}
		return map[string]any{"ms": time.Since(start).Milliseconds()}, nil
	case "task_start":
		ctx, cancel := timed(20 * time.Second)
		defer cancel()
		return nil, e.StartTask(ctx, r.Task, r.APIKey, r.Model)
	case "task_stop", "stop":
		e.StopTask()
		return nil, nil
	case "approve":
		return nil, e.Answer(r.ID, r.Yes)
	case "probe":
		ctx, cancel := timed(60 * time.Second)
		defer cancel()
		out, err := e.Probe(ctx, r.App)
		if err == nil {
			b, _ := json.Marshal(out)
			e.logf("probe %s", b)
		}
		return out, err
	case "soak_start":
		return nil, e.StartSoak(SoakConfig{App: r.App, Tap: r.Tap, Every: time.Duration(r.EveryS) * time.Second, Minutes: r.Minutes})
	case "soak_stop":
		e.stopSoak()
		return nil, nil
	}
	return nil, fmt.Errorf("unknown op %q", r.Op)
}
