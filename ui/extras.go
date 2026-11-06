package ui

import (
	"bytes"
	"context"
	"image"
	_ "image/jpeg" // step screenshots are JPEG
	_ "image/png"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/gateway"
)

// Methods the gateway has beyond gateway.API. The client checks for them
// on the API value and leaves out what they back when they are missing,
// so gateway.API stays as it is. The signatures are internal/gateway's.
type (
	mediaPather interface {
		MediaPath(ctx context.Context, id string) (string, error)
	}
	agentLister interface {
		Agents(ctx context.Context) ([]gateway.AgentView, error)
	}
	agentScoper interface {
		SetAgentScope(name string, scope gateway.Scope)
	}
	readOnlySetter interface {
		SetReadOnly(deviceID string, on bool)
	}
	historyWiper interface {
		WipeHistory(ctx context.Context) error
	}
)

// mediaEntry is a step screenshot, loaded once.
type mediaEntry struct {
	bitmap  *mg.Bitmap
	w, h    int
	err     error
	loading bool
}

// media returns a step screenshot by its media id, loading it in the
// background the first time: nil while it loads, or when the gateway has
// no screenshots.
func (a *app) media(id string) *mediaEntry {
	if id == "" {
		return nil
	}
	mp, ok := a.api.(mediaPather)
	if !ok {
		return nil
	}
	if e, ok := a.mediaCache[id]; ok {
		if e.bitmap == nil {
			return nil
		}
		return e
	}
	if len(a.mediaCache) > 400 {
		a.mediaCache = map[string]*mediaEntry{} // keep memory bounded; they load again
	}
	e := &mediaEntry{loading: true}
	a.mediaCache[id] = e
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
		defer cancel()
		var img image.Image
		path, err := mp.MediaPath(ctx, id)
		if err == nil {
			var data []byte
			if data, err = os.ReadFile(path); err == nil {
				img, _, err = image.Decode(bytes.NewReader(data))
			}
		}
		a.post(func() {
			e.loading = false
			if err != nil {
				e.err = err
				return
			}
			e.bitmap = mg.NewBitmap(img)
			b := img.Bounds()
			e.w, e.h = b.Dx(), b.Dy()
		})
	}()
	return nil
}

// stepShot returns the screenshot that shows a step best, the one after
// it, else the one before, and whether one is still loading.
func (a *app) stepShot(s gateway.StepView) (*mediaEntry, bool) {
	for _, id := range []string{s.After, s.Before} {
		if id == "" {
			continue
		}
		if e := a.media(id); e != nil {
			return e, false
		}
		if e := a.mediaCache[id]; e != nil && e.loading {
			return nil, true
		}
	}
	return nil, false
}

// hasShot reports whether a step has a screenshot this gateway can give.
func (a *app) hasShot(s gateway.StepView) bool {
	if _, ok := a.api.(mediaPather); !ok {
		return false
	}
	return s.After != "" || s.Before != ""
}

// shotScale is screenshot pixels per screen pixel, to place a step's
// target on its screenshot. The gateway scales screenshots down by a whole
// factor, which the live mirror's picture size gives; 0 when unknown.
func (a *app) shotScale(deviceID string, e *mediaEntry) float32 {
	m := a.mirrors[deviceID]
	if m == nil || m.picH == 0 || e.h == 0 {
		return 0
	}
	k := max(1, int(float32(m.picH)/float32(e.h)+0.5))
	return 1 / float32(k)
}

// shotView draws a step's screenshot, h points high, with the step's
// target outlined when it can be placed.
func (a *app) shotView(c *mg.Context, t gateway.TaskView, s gateway.StepView, h float32) mg.Element {
	p := pal(c)
	e, loading := a.stepShot(s)
	if e == nil {
		w := h * 9 / 19.5
		box := mg.Box(c).Size(w, h).Shrink(0).Radius(10).Border(1, p.line2).Background(p.bg3).Center()
		box.Children(func() {
			msg := "No screenshot"
			if loading {
				msg = "Loading…"
				c.After(150 * time.Millisecond)
			}
			mg.Text(c, msg).FontSize(12).TextColor(p.ink3)
		})
		return box
	}
	w := h * float32(e.w) / float32(e.h)
	k := a.shotScale(t.DeviceID, e)
	box := mg.Box(c).Size(w, h).Shrink(0).Radius(10).Border(1, p.line2).Clip().Background(hex("#000000")).
		Label("Screenshot of " + t.DeviceName)
	box.Draw(func(pt *mg.Painter, r mg.Rect) {
		pt.Clip(r, 10, func() {
			pt.Image(e.bitmap, r, mg.Contain)
			// Thumbnails are too small for the outline to say anything.
			if k > 0 && !s.Target.Empty() && h >= 120 {
				drawOverlay(pt, r, p, s, stepDone, float32(e.w)/k, float32(e.h)/k, true)
			}
		})
	})
	return box
}

// stepDisplay is how a step reads in the client: its text, a smaller
// second line, and its mark. Besides the conventions in format.go it reads
// the gateway's own: an approval's step says "Waiting for you: Tapped …"
// while it waits and keeps that text when it is denied or withdrawn, with
// only its status changing.
func stepDisplay(t gateway.TaskView, i int) (text, sub string, kind stepKind) {
	s := t.Steps[i]
	text, sub = splitStep(s.Text)
	kind = kindOf(t, i)
	action, waiting := strings.CutPrefix(text, "Waiting for you: ")
	if waiting {
		action = imperative(action)
	}
	handoff, isHandoff := strings.CutPrefix(text, "Asked you to take over: ")
	asked, isAsk := strings.CutPrefix(text, "Asked you: ")
	asked2 := func(why string) string {
		if sub != "" {
			return "Asked: “" + why + "”\n" + sub
		}
		return "Asked: “" + why + "”"
	}
	switch s.Status {
	case "approval_pending":
		switch {
		case waiting && t.State == gateway.TaskWorking:
			// Answered: the gateway closes the step once the action ran.
			text, kind = "You answered: "+action, stepYou
		case waiting:
			text = "Waiting for you: " + action
		}
	case "approval_denied":
		switch {
		case waiting:
			text = "You denied: " + action
		case isHandoff:
			text, sub = "You declined", asked2(handoff)
		case isAsk:
			text = "You said no: " + asked
		}
		if waiting || isHandoff || isAsk {
			kind = stepYou
		}
	case "settled":
		switch {
		case isHandoff:
			text, sub, kind = "You handed it back", asked2(handoff), stepYou
		case isAsk:
			text, kind = "You agreed: "+asked, stepYou
		}
	case "user_took_over":
		if waiting {
			text = upperFirst(action)
		}
		sub, kind = "Not done: you took over", stepYou
	case "paused":
		sub = "Not done: you were in control"
	case "stopped":
		if waiting {
			text = upperFirst(action)
		}
		sub = "Not done: stopped"
	case "stale":
		sub = "Not done: the screen changed while it waited"
	case "uncertain":
		sub = "May have happened"
	case "expired":
		if waiting {
			text = upperFirst(action)
		}
		sub = "Allowed, but the agent did not come back for it"
	case "device_locked":
		sub = "The phone was locked"
	case "device_offline":
		sub = "The phone went offline"
	case "lease_held", "scope_denied", "not_found", "unsupported", "invalid_argument", "internal":
		sub = "Failed: " + strings.ReplaceAll(s.Status, "_", " ")
	}
	if waiting && kind != stepWait && strings.HasPrefix(text, "Waiting for you: ") {
		text = upperFirst(action)
	}
	return text, sub, kind
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// imperative turns a step's past tense into what was asked: "Tapped “Pay”"
// becomes "tap “Pay”".
func imperative(s string) string {
	for _, v := range [][2]string{
		{"Long-pressed ", "long-press "}, {"Tapped ", "tap "}, {"Typed ", "type "}, {"Swiped ", "swipe "},
		{"Pressed ", "press "}, {"Opened ", "open "},
	} {
		if rest, ok := strings.CutPrefix(s, v[0]); ok {
			return v[1] + rest
		}
	}
	return lowerFirst(s)
}

// Scopes as the person reads them.
var scopeChoices = []struct {
	mode  gateway.ScopeMode
	label string
}{
	{gateway.ScopeEverything, "All apps except blocked ones"},
	{gateway.ScopeApps, "Only the apps I choose"},
	{gateway.ScopeReadOnly, "Browse and read, never commit"},
	{gateway.ScopeWatchOnly, "Watch only"},
}

func scopeLabel(m gateway.ScopeMode) string {
	for _, c := range scopeChoices {
		if c.mode == m {
			return c.label
		}
	}
	return string(m)
}

func scopeOf(label string) gateway.ScopeMode {
	for _, c := range scopeChoices {
		if c.label == label {
			return c.mode
		}
	}
	return gateway.ScopeEverything
}

// scopeLine describes a scope in Settings › Agents.
func scopeLine(s gateway.Scope) string {
	switch s.Mode {
	case gateway.ScopeApps:
		if len(s.Apps) == 0 {
			return "Can use no app yet"
		}
		return "Can use " + joinAnd(s.Apps)
	case gateway.ScopeReadOnly:
		return "Can browse and read, never commit"
	case gateway.ScopeWatchOnly:
		return "Can only watch"
	}
	return "Can use all apps except blocked ones"
}

func joinAnd(s []string) string {
	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// newAgentNeed reports an agent's first-connection approval.
func newAgentNeed(n gateway.NeedView) bool {
	return n.Kind == gateway.NeedApproval && n.Rule == "New agent"
}

// isLocked reports whether a phone shows its lock screen: the gateway says
// so, or an unlock card is open for it (a locked phone with a waiting task
// counts as needing the person).
func (a *app) isLocked(d gateway.DeviceView) bool {
	return d.Status == gateway.DeviceLocked || a.unlocks[d.Info.ID]
}

// handoffFor returns the open handoff on a phone. The gateway ranks it
// above the person's control, so a phone the person took over shows as
// needing them once the agent calls phone_handoff to wait.
func (a *app) handoffFor(deviceID string) (gateway.NeedView, bool) {
	for _, n := range a.needs {
		if n.Kind == gateway.NeedHandoff && n.DeviceID == deviceID {
			return n, true
		}
	}
	return gateway.NeedView{}, false
}

// agentRequestDetail is a New agent request's second line. With a scope
// picker under it, the gateway's "Allowing gives it … access." would
// contradict the choice, so it is left out.
func agentRequestDetail(n gateway.NeedView, picker bool) string {
	d := n.Detail
	if picker {
		if i := strings.Index(d, " Allowing gives it"); i >= 0 {
			d = d[:i]
		}
	}
	if !n.Created.IsZero() {
		d = strings.TrimSpace(d + " Asked at " + n.Created.Format("15:04") + ".")
	}
	return d
}
