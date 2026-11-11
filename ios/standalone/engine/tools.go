package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/ios/standalone/agent"
)

// tools are the agent's tools on the engine: a few of Pinky's phone_*
// tools, cut down to what the prototype needs.
type tools struct {
	e    *Engine
	prev *screen.Screen
}

func obj(props map[string]any, required ...string) map[string]any {
	m := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, len(required))
		for i, r := range required {
			req[i] = r
		}
		m["required"] = req
	}
	return m
}

var (
	intProp  = map[string]any{"type": "integer"}
	boolProp = map[string]any{"type": "boolean"}
	strProp  = map[string]any{"type": "string"}
)

func (t *tools) List() []agent.Tool {
	return []agent.Tool{
		{Name: "observe", Description: "Read the whole screen again. screenshot true adds a picture.",
			Schema: obj(map[string]any{"screenshot": boolProp})},
		{Name: "tap", Description: "Tap an element by ref, or a point x,y. long true is a long press.",
			Schema: obj(map[string]any{"ref": intProp, "x": intProp, "y": intProp, "long": boolProp})},
		{Name: "swipe", Description: "Scroll the content: direction up shows what is below, down what is above. Or swipe from x1,y1 to x2,y2.",
			Schema: obj(map[string]any{"direction": map[string]any{"type": "string", "enum": []any{"up", "down", "left", "right"}},
				"x1": intProp, "y1": intProp, "x2": intProp, "y2": intProp})},
		{Name: "type", Description: "Type text into the field with ref (tapping it first) or the focused one. It replaces the field's text unless append is true; submit true presses return after.",
			Schema: obj(map[string]any{"text": strProp, "ref": intProp, "append": boolProp, "submit": boolProp}, "text")},
		{Name: "key", Description: "Press home, enter or delete.",
			Schema: obj(map[string]any{"key": map[string]any{"type": "string", "enum": []any{"home", "enter", "delete"}}}, "key")},
		{Name: "launch", Description: "Open an app by its bundle id.",
			Schema: obj(map[string]any{"bundle_id": strProp}, "bundle_id")},
		{Name: "wait", Description: "Wait 1 to 10 seconds for the screen to change, then read it.",
			Schema: obj(map[string]any{"seconds": intProp})},
		{Name: "request_approval", Description: "Ask the person to approve one action before you do it, and wait for the answer. Describe the action in one sentence in the language of the task.",
			Schema: obj(map[string]any{"action": strProp}, "action")},
	}
}

func num(args map[string]any, k string) (int, bool) {
	switch v := args[k].(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	}
	return 0, false
}

func flag(args map[string]any, k string) bool {
	b, _ := args[k].(bool)
	return b
}

func errResult(code string, err error) agent.Result {
	return agent.Result{Text: "error: " + code + "\n" + err.Error(), IsError: true}
}

func blocked(err error) agent.Result {
	switch {
	case errors.Is(err, ErrOwnUI), errors.Is(err, ErrApproval), errors.Is(err, ErrOwnLaunch):
		return errResult("blocked", err)
	case errors.Is(err, ErrStale):
		return errResult("stale", err)
	case errors.Is(err, ErrLocked):
		return errResult("device_locked", errors.New("the phone is locked; end the task with finish"))
	case errors.Is(err, ErrNoPhone):
		return errResult("disconnected", err)
	}
	return errResult("failed", err)
}

// point is the point an action aims at: a ref's center or x,y.
func (t *tools) point(args map[string]any) (screen.Point, error) {
	if ref, ok := num(args, "ref"); ok && ref != 0 {
		t.e.mu.Lock()
		last := t.e.last
		t.e.mu.Unlock()
		if last == nil {
			return screen.Point{}, errors.New("no screen read yet; call observe")
		}
		el, ok := last.Find(ref)
		if !ok {
			return screen.Point{}, fmt.Errorf("[%d] is not on the latest screen", ref)
		}
		return el.Bounds.Center(), nil
	}
	x, okx := num(args, "x")
	y, oky := num(args, "y")
	if !okx || !oky {
		return screen.Point{}, errors.New("give ref, or x and y")
	}
	return screen.Point{X: x, Y: y}, nil
}

// Call runs one tool and logs its name, time and outcome; never its
// arguments or what it read, which may be the person's text.
func (t *tools) Call(ctx context.Context, name string, args map[string]any) agent.Result {
	start := time.Now()
	r := t.call(ctx, name, args)
	outcome := "ok"
	if r.IsError {
		outcome, _, _ = strings.Cut(strings.TrimPrefix(r.Text, "error: "), "\n")
	}
	t.e.logf("tool %s %dms %s", name, time.Since(start).Milliseconds(), outcome)
	return r
}

func (t *tools) call(ctx context.Context, name string, args map[string]any) agent.Result {
	e := t.e
	var err error
	switch name {
	case "observe":
		r := t.screen(ctx, true)
		if flag(args, "screenshot") && !r.IsError {
			png, err := e.Screenshot(ctx)
			if err != nil {
				r.Text += "\n(no screenshot: " + err.Error() + ")"
			} else {
				r.Images = []agent.Image{{Data: png, MIME: "image/png"}}
			}
		}
		return r
	case "tap":
		var p screen.Point
		if p, err = t.point(args); err != nil {
			return errResult("invalid_argument", err)
		}
		err = e.tapIn(ctx, t.aim(), p, flag(args, "long"))
	case "swipe":
		var from, to screen.Point
		if from, to, err = t.swipe(args); err != nil {
			return errResult("invalid_argument", err)
		}
		err = e.swipeIn(ctx, t.aim(), from, to, 300*time.Millisecond)
	case "type":
		text, _ := args["text"].(string)
		if ref, ok := num(args, "ref"); ok && ref != 0 {
			var p screen.Point
			if p, err = t.point(args); err != nil {
				return errResult("invalid_argument", err)
			}
			if err = e.tapIn(ctx, t.aim(), p, false); err != nil {
				return blocked(err)
			}
			sleep(ctx, 400*time.Millisecond)
		}
		err = e.typeIn(ctx, t.aim(), text, driver.TypeOptions{Append: flag(args, "append"), Submit: flag(args, "submit")})
	case "key":
		k, _ := args["key"].(string)
		switch driver.Key(k) {
		case driver.KeyHome, driver.KeyEnter, driver.KeyDelete:
		default:
			return errResult("invalid_argument", fmt.Errorf("unknown key %q", k))
		}
		err = e.keyIn(ctx, t.aim(), driver.Key(k))
	case "launch":
		id, _ := args["bundle_id"].(string)
		if strings.TrimSpace(id) == "" {
			return errResult("invalid_argument", errors.New("bundle_id is required"))
		}
		err = e.Launch(ctx, strings.TrimSpace(id))
		if err == nil {
			sleep(ctx, time.Second)
		}
	case "wait":
		s, _ := num(args, "seconds")
		sleep(ctx, time.Duration(min(max(s, 1), 10))*time.Second)
		return t.screen(ctx, false)
	case "request_approval":
		text, _ := args["action"].(string)
		start := time.Now()
		yes, err := e.RequestApproval(ctx, strings.TrimSpace(text))
		r := agent.Result{Waited: time.Since(start)}
		switch {
		case ctx.Err() != nil:
			r.Stop = true
		case err != nil:
			return errResult("failed", err)
		case yes:
			r.Text = "approved: go ahead with exactly this action."
		default:
			r.Text = "denied: do not do it. Find another way within the task, or end with finish."
		}
		// The person may have answered in Pinky's own app, where nothing
		// can be done: go back to the app the agent was in.
		went, err := e.back(ctx, t.aim())
		switch {
		case err != nil:
			r.Text += "\nPinky's own app is in front and the engine could not leave it (" + err.Error() + "). Wait for the person, or end with finish."
		case went != "":
			sleep(ctx, time.Second)
			r.Text += "\nThe person answered in Pinky's app; the engine went back to " + went + ":\n" + t.screen(ctx, false).Text
		}
		return r
	default:
		return errResult("invalid_argument", fmt.Errorf("unknown tool %q", name))
	}
	if err != nil {
		return blocked(err)
	}
	sleep(ctx, e.cfg.Settle)
	return t.screen(ctx, false)
}

func (t *tools) swipe(args map[string]any) (from, to screen.Point, err error) {
	if d, _ := args["direction"].(string); d != "" {
		t.e.mu.Lock()
		last := t.e.last
		t.e.mu.Unlock()
		if last == nil || last.Width == 0 {
			return from, to, errors.New("no screen read yet; call observe")
		}
		w, h := last.Width, last.Height
		c := screen.Point{X: w / 2, Y: h / 2}
		dx, dy := w*3/10, h*3/10
		switch d {
		case "up":
			return screen.Point{X: c.X, Y: c.Y + dy}, screen.Point{X: c.X, Y: c.Y - dy}, nil
		case "down":
			return screen.Point{X: c.X, Y: c.Y - dy}, screen.Point{X: c.X, Y: c.Y + dy}, nil
		case "left":
			return screen.Point{X: c.X + dx, Y: c.Y}, screen.Point{X: c.X - dx, Y: c.Y}, nil
		case "right":
			return screen.Point{X: c.X - dx, Y: c.Y}, screen.Point{X: c.X + dx, Y: c.Y}, nil
		}
		return from, to, fmt.Errorf("unknown direction %q", d)
	}
	x1, ok1 := num(args, "x1")
	y1, ok2 := num(args, "y1")
	x2, ok3 := num(args, "x2")
	y2, ok4 := num(args, "y2")
	if !ok1 || !ok2 || !ok3 || !ok4 {
		return from, to, errors.New("give direction, or x1, y1, x2 and y2")
	}
	return screen.Point{X: x1, Y: y1}, screen.Point{X: x2, Y: y2}, nil
}

// screen reads the screen and returns it, or only what changed since the
// screen the agent saw last.
// aim is the app of the screen the agent last saw: its input is meant for
// that app and goes nowhere else.
func (t *tools) aim() string {
	if t.prev == nil {
		return ""
	}
	return appKey(t.prev.App)
}

func (t *tools) screen(ctx context.Context, full bool) agent.Result {
	s, err := t.e.Observe(ctx)
	if err != nil {
		return blocked(err)
	}
	prev := t.prev
	t.prev = s
	if !full {
		if lines, whole := screen.Diff(prev, s); !whole {
			if len(lines) == 0 {
				return agent.Result{Text: "settled; the screen did not change"}
			}
			return agent.Result{Text: "settled; changes:\n" + strings.Join(lines, "\n")}
		}
	}
	return agent.Result{Text: s.Render()}
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}
