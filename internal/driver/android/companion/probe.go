package companion

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DIYgod/pinky/internal/driver"
)

// When the service does not answer after Pinky turned it on, the client
// reads what the phone shows about it, so that the error, the log and the
// person's diagnostics say why: docs/notes/companion.md, "When the service
// does not come on".

// probeMark starts each section of the probe's output.
const probeMark = "--pinky-probe:"

// probeCmd reads, in one shell: the setting as it is now, whether the
// phone was unlocked since it started, the app's process and socket, what
// the accessibility manager has bound, the app's last log lines and a crash
// of the app. A part may fail on some phones; the shell ends with true so
// that the others still come back.
func probeCmd(app App) string {
	part := func(name, cmd string) string { return "echo " + probeMark + name + "; " + cmd }
	return strings.Join([]string{
		part("setting", "settings get secure "+settingKey),
		part("user", "am get-started-user-state 0"),
		part("pid", "pidof "+app.Package),
		part("socket", "grep -c -a "+shellQuote("@"+app.Socket+"$")+" /proc/net/unix"),
		// Only the lines about services: the rest names other apps' windows.
		part("a11y", "dumpsys accessibility | grep -E "+shellQuote("Ui Automation|services:")),
		part("log", "logcat -d -v tag -s PinkyService:V PinkyServer:V | tail -n 1"),
		part("now", "date +%s"),
		part("crash", "logcat -d -b crash -v epoch -t 200 | grep -A1 "+shellQuote("Process: "+app.Package+",")),
		"true",
	}, "; ")
}

// startError says why the service did not answer within wait, from what
// the phone shows now. It runs before Enable restores the setting, which
// would change what the phone shows.
func (c *Client) startError(ctx context.Context, wait time.Duration) error {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := c.run.Shell(pctx, probeCmd(c.app))
	if err != nil {
		return &driver.CompanionStartError{Detail: fmt.Sprintf("no answer within %s (reading the phone's state failed: %v)", wait, err)}
	}
	return parseProbe(out, c.app).startError(wait)
}

// probe is what probeCmd read. A section that did not come back leaves its
// fields at their zero values, and its "known" flag false.
type probe struct {
	settingKnown, listed bool // the setting lists the service
	locked               bool // the phone was not unlocked since it started
	pidKnown, running    bool // the app's process runs
	socketKnown, socket  bool // its socket listens
	a11yKnown            bool // the accessibility manager answered
	enabled              bool // it lists the service as enabled
	bound                bool // it has bound the service
	binding              bool // it is binding the service
	crashed              bool // it lists the service as crashed (Android 11 and later)
	uiAutomation         bool // a UiAutomation is connected
	log                  string
	crash                string // the exception of a crash of the app in the last minute
}

func parseProbe(out string, app App) probe {
	var p probe
	sections := map[string][]string{}
	name := ""
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if rest, ok := strings.CutPrefix(line, probeMark); ok {
			name = rest
			sections[name] = []string{}
			continue
		}
		if name != "" && strings.TrimSpace(line) != "" {
			sections[name] = append(sections[name], line)
		}
	}
	ours := func(component string) bool {
		return normalizeComponent(component) == app.Package+"/"+ServiceClass
	}
	hasOurs := func(line string) bool {
		for _, f := range strings.FieldsFunc(line, func(r rune) bool { return strings.ContainsRune("{}, ", r) }) {
			if ours(f) {
				return true
			}
		}
		return false
	}
	if s := sections["setting"]; len(s) > 0 {
		p.settingKnown = true
		p.listed = slices.ContainsFunc(parseServices(s[0]), ours) // "null" when unset
	}
	p.locked = slices.ContainsFunc(sections["user"], func(l string) bool { return strings.Contains(l, "RUNNING_LOCKED") })
	if s, ok := sections["pid"]; ok {
		p.pidKnown, p.running = true, len(s) > 0
	}
	if s := sections["socket"]; len(s) > 0 {
		if n, err := strconv.Atoi(strings.TrimSpace(s[0])); err == nil {
			p.socketKnown, p.socket = true, n > 0
		}
	}
	for _, line := range sections["a11y"] {
		p.a11yKnown = true
		if strings.Contains(line, "Ui Automation[") {
			p.uiAutomation = true
		}
		l := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(l, "Bound services:"):
			if strings.Contains(l, "label="+app.Label+",") || strings.Contains(l, "label="+app.Label+"]") {
				p.bound = true
			}
		case strings.HasPrefix(l, "Enabled services:"):
			p.enabled = p.enabled || hasOurs(l)
		case strings.HasPrefix(l, "Binding services:"):
			p.binding = p.binding || hasOurs(l)
		case strings.HasPrefix(l, "Crashed services:"):
			p.crashed = p.crashed || hasOurs(l)
		}
	}
	if s := sections["log"]; len(s) > 0 {
		p.log = clip(strings.TrimSpace(s[len(s)-1]), 160)
	}
	var now float64
	if s := sections["now"]; len(s) > 0 {
		now, _ = strconv.ParseFloat(strings.TrimSpace(s[0]), 64)
	}
	crash := sections["crash"]
	for i, line := range crash {
		if !strings.Contains(line, "Process: "+app.Package+",") || i+1 >= len(crash) {
			continue
		}
		f := strings.Fields(line)
		at, err := strconv.ParseFloat(f[0], 64)
		if err != nil || now == 0 || now-at > 60 {
			continue
		}
		ex := crash[i+1]
		if _, msg, ok := strings.Cut(ex, "AndroidRuntime: "); ok {
			ex = msg
		}
		p.crash = clip(strings.TrimSpace(ex), 160)
	}
	return p
}

// startError puts what the probe read into an error.
func (p probe) startError(wait time.Duration) *driver.CompanionStartError {
	e := &driver.CompanionStartError{}
	facts := []string{fmt.Sprintf("no answer within %s", wait)}
	add := func(cond bool, fact string) {
		if cond {
			facts = append(facts, fact)
		}
	}
	add(p.settingKnown && !p.listed, "something took it off enabled_accessibility_services again")
	if p.a11yKnown {
		switch {
		case p.bound:
			facts = append(facts, "Android has bound it")
		case p.binding:
			facts = append(facts, "Android is still binding it")
		case p.enabled:
			facts = append(facts, "Android lists it as enabled but has not bound it")
		default:
			facts = append(facts, "Android does not list it as enabled")
		}
		add(p.crashed, "Android lists it as crashed")
		add(p.uiAutomation, "another tool holds Android's UI automation")
	} else {
		facts = append(facts, "dumpsys accessibility showed nothing")
	}
	add(p.locked, "the phone was not unlocked since it started")
	add(p.pidKnown && p.running, "its app is running")
	add(p.pidKnown && !p.running, "its app is not running")
	add(p.socketKnown && p.socket, "its socket is listening")
	add(p.socketKnown && !p.socket, "its socket is not listening")
	add(p.crash != "", "it crashed: "+p.crash)
	add(p.log != "", fmt.Sprintf("its last log line: %q", p.log))
	e.Detail = strings.Join(facts, "; ")
	// Unlocking comes first: the service cannot start before, whatever
	// else holds it.
	switch {
	case p.bound:
	case p.locked:
		e.Reason = driver.CompanionStartNotUnlocked
	case p.uiAutomation:
		e.Reason = driver.CompanionStartUIAutomation
	}
	return e
}

// clip shortens s to at most n bytes, at a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "…"
}
