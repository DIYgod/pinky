package policy

import (
	"fmt"
	"regexp"
	"strings"
	"sync"

	"github.com/DIYgod/pinky/internal/screen"
)

// Engine evaluates actions against the rules. It is safe for concurrent use.
type Engine struct {
	mu   sync.RWMutex
	file File
	apps *Apps
	// Runtime read-only switches from the client, on top of the file's.
	readOnlyAll     *bool
	readOnlyDevices map[string]bool

	rulesPath, appsPath string
}

// New returns an engine for the given rules and app table. A nil table uses
// the bundled one.
func New(f File, apps *Apps) *Engine {
	if apps == nil {
		apps = BundledApps()
	}
	return &Engine{file: f, apps: apps, readOnlyDevices: map[string]bool{}}
}

// Default returns an engine with the bundled rules and app table.
func Default() *Engine { return New(DefaultFile(), nil) }

// Rules returns a copy of the rules.
func (e *Engine) Rules() []Rule {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return append([]Rule(nil), e.file.Rules...)
}

// AddApps puts apps into a category at run time, for tests and fake devices.
func (e *Engine) AddApps(cat string, entries CategoryEntries) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.apps.Add(cat, entries)
}

// Categories returns the categories of an app.
func (e *Engine) Categories(app screen.App) []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.apps.Categories(app)
}

// IsSystem reports whether the app is a launcher or system UI, which every
// scope and allow-list includes.
func (e *Engine) IsSystem(app screen.App) bool {
	return contains(e.Categories(app), CatSystem)
}

// SetReadOnly turns read-only mode on or off for one device, or for every
// device when deviceID is empty. It overrides the rules file until restart.
func (e *Engine) SetReadOnly(deviceID string, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if deviceID == "" {
		e.readOnlyAll = &on
		return
	}
	e.readOnlyDevices[deviceID] = on
}

// ReadOnly reports whether read-only mode applies to the device.
func (e *Engine) ReadOnly(deviceID string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.readOnlyLocked(deviceID)
}

func (e *Engine) readOnlyLocked(deviceID string) bool {
	all := e.file.ReadOnly
	if e.readOnlyAll != nil {
		all = *e.readOnlyAll
	}
	if all {
		return true
	}
	if on, ok := e.readOnlyDevices[deviceID]; ok {
		return on
	}
	return contains(e.file.ReadOnlyDevices, deviceID)
}

// Blocked reports whether a deny rule blocks the whole app, and names the
// rule. The agent gets neither the screen nor the notifications of a blocked
// app, and its steps are not recorded.
func (e *Engine) Blocked(app screen.App) (rule string, blocked bool) {
	if app.ID == "" && app.Name == "" {
		return "", false
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	cats := e.apps.Categories(app)
	for _, r := range e.file.Rules {
		if r.Decision == Deny && r.Match.appLevel() && r.matchesApp(app, cats) {
			return r.Name, true
		}
	}
	return "", false
}

// Evaluate decides one action.
func (e *Engine) Evaluate(in Input) Verdict {
	e.mu.RLock()
	defer e.mu.RUnlock()

	app := in.App
	if in.Op == OpLaunch {
		app = in.Launch
	}
	cats := e.apps.Categories(app)
	kinds := detect(in, cats)
	system := contains(cats, CatSystem)

	if in.Op == OpType && IsCredentialField(in.Target) {
		return Verdict{Decision: Deny, Rule: RuleCredentials, Kinds: kinds,
			Reason: "Pinky never types into password or one-time-code fields; call phone_handoff so the person can fill it."}
	}
	if len(in.ScopeApps) > 0 && !system && !appIn(app, in.ScopeApps) && !(in.Op == OpKey && in.Key == "home") {
		return Verdict{Decision: Deny, Rule: RuleAgentScope, Kinds: kinds, Scope: true,
			Reason: fmt.Sprintf("%s is outside this agent's scope.", appName(app))}
	}
	taskAsk := false
	if len(in.TaskApps) > 0 && !system && !appIn(app, in.TaskApps) {
		switch in.Op {
		case OpLaunch:
			return Verdict{Decision: Deny, Rule: RuleTaskApps, Kinds: kinds, Scope: true,
				Reason: fmt.Sprintf("%s is not in this task's app list (%s).", appName(app), strings.Join(in.TaskApps, ", "))}
		case OpType:
			taskAsk = true
		}
	}

	v := Verdict{Decision: Allow, Kinds: kinds}
	for _, r := range e.file.Rules {
		if !r.matches(app, cats, kinds, in) {
			continue
		}
		if r.Decision > v.Decision || (v.Rule == "" && r.Decision == v.Decision && r.Decision != Allow) {
			v.Decision, v.Rule = r.Decision, r.Name
		}
	}
	if v.Rule != "" {
		v.Reason = reason(v, app)
	}
	if taskAsk && v.Decision < Ask {
		v.Decision, v.Rule = Ask, RuleTaskApps
		v.Reason = fmt.Sprintf("Typing into %s, which is not in this task's app list.", appName(app))
	}

	if in.ReadOnly || e.readOnlyLocked(in.Device) {
		switch {
		case v.Decision == Ask:
			v.Decision = Deny
			v.Reason = fmt.Sprintf("Read-only mode is on, and this would need approval (%s).", v.Rule)
			v.Rule = RuleReadOnly
		case v.Decision == Allow && in.Op == OpType && !IsSearchField(in.Target):
			v.Decision, v.Rule = Deny, RuleReadOnly
			v.Reason = "Read-only mode is on: the agent may type only into search fields."
		}
	}
	return v
}

func reason(v Verdict, app screen.App) string {
	switch {
	case v.Decision == Deny && len(v.Kinds) == 0:
		return fmt.Sprintf("%s is blocked by the rule %q.", appName(app), v.Rule)
	case len(v.Kinds) > 0:
		ks := make([]string, len(v.Kinds))
		for i, k := range v.Kinds {
			ks[i] = string(k)
		}
		return fmt.Sprintf("The rule %q matched this action (%s).", v.Rule, strings.Join(ks, ", "))
	}
	return fmt.Sprintf("The rule %q matched this action.", v.Rule)
}

func appName(app screen.App) string {
	switch {
	case app.Name != "":
		return app.Name
	case app.ID != "":
		return app.ID
	}
	return "This app"
}

func (r Rule) matchesApp(app screen.App, cats []string) bool {
	m := r.Match
	if len(m.App) > 0 && !appIn(app, m.App) {
		return false
	}
	if len(m.Category) > 0 && !anyIn(cats, m.Category) {
		return false
	}
	return true
}

func (r Rule) matches(app screen.App, cats []string, kinds []Kind, in Input) bool {
	m := r.Match
	if !r.matchesApp(app, cats) {
		return false
	}
	// Whole-app rules never stop the agent from pressing Home to leave.
	if m.appLevel() && in.Op == OpKey && in.Key == "home" {
		return false
	}
	if len(m.Action) > 0 {
		ok := false
		for _, a := range m.Action {
			if Op(a) == in.Op || hasKind(kinds, Kind(a)) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(m.Label) > 0 {
		if in.Target == nil {
			return false
		}
		l := strings.ToLower(in.Target.Label)
		ok := false
		for _, s := range m.Label {
			if s != "" && strings.Contains(l, strings.ToLower(s)) {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

// appIn reports whether the app matches any entry, by id or by name.
func appIn(app screen.App, list []string) bool {
	for _, x := range list {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if app.ID != "" && (x == app.ID || (strings.HasSuffix(x, "*") && strings.HasPrefix(app.ID, strings.TrimSuffix(x, "*")))) {
			return true
		}
		if app.Name != "" && strings.EqualFold(x, app.Name) {
			return true
		}
	}
	return false
}

func anyIn(have, want []string) bool {
	for _, w := range want {
		if contains(have, w) {
			return true
		}
	}
	return false
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// FilterEvents removes notifications from blocked apps and masks digit
// codes in the rest, as the rules file says.
func (e *Engine) FilterEvents(events []screen.Event) []screen.Event {
	if len(events) == 0 {
		return events
	}
	e.mu.RLock()
	mask := e.file.MaskCodes == nil || *e.file.MaskCodes
	e.mu.RUnlock()
	out := make([]screen.Event, 0, len(events))
	for _, ev := range events {
		if ev.Kind == screen.EventNotification && ev.App != "" {
			if _, blocked := e.Blocked(screen.App{ID: ev.App, Name: ev.App}); blocked {
				continue
			}
		}
		if mask && ev.Kind == screen.EventNotification {
			ev.Text = MaskCodes(ev.Text)
		}
		out = append(out, ev)
	}
	return out
}

var codeRe = regexp.MustCompile(`(?:^|[^\d.,$€£¥₩₹])(\d{4,8}|\d{3}[ -]\d{3}|\d{4}[ -]\d{4})(?:[^\d]|$)`)

// MaskCodes hides digit codes of four to eight digits in a notification
// that talks about a code, in any supported language: "Your code is
// 123456" becomes "Your code is ••••••".
func MaskCodes(text string) string {
	lower := strings.ToLower(text)
	if !anyWord(tokenize(lower), codeWords) && !containsAny(lower, codePhrases) && !containsAny(text, cjkCodeKeywords) {
		return text
	}
	// Mask until nothing changes, since matches can touch.
	for i := 0; i < 4; i++ {
		next := codeRe.ReplaceAllStringFunc(text, func(m string) string {
			var b strings.Builder
			for _, r := range m {
				if r >= '0' && r <= '9' {
					b.WriteString("•")
				} else {
					b.WriteRune(r)
				}
			}
			return b.String()
		})
		if next == text {
			break
		}
		text = next
	}
	return text
}
