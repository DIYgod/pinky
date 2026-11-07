package ui

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// How the client reads a step. The gateway writes steps in plain words;
// these conventions decide the mark beside each (see docs/notes/client.md):
//
//   - status "approval_pending": waiting for the person
//   - status "user", or no tool and text starting "You ": the person's own step
//   - status "" or "running" on the last step of a running task: in progress
//   - anything else: done
//
// A step's text may hold a second line, after a newline, shown smaller.
type stepKind int

const (
	stepDone stepKind = iota
	stepWait
	stepNow
	stepYou
)

func kindOf(t gateway.TaskView, i int) stepKind {
	s := t.Steps[i]
	switch {
	case s.Status == "approval_pending":
		return stepWait
	case s.Status == "user" || (s.Tool == "" && strings.HasPrefix(s.Text, "You ")):
		return stepYou
	case (s.Status == "" || s.Status == "running") && i == len(t.Steps)-1 && running(t.State):
		return stepNow
	}
	return stepDone
}

// splitStep returns a step's first line and the rest.
func splitStep(text string) (string, string) {
	first, rest, _ := strings.Cut(text, "\n")
	return first, strings.TrimSpace(rest)
}

func running(s gateway.TaskState) bool {
	return s == gateway.TaskWorking || s == gateway.TaskWaiting || s == gateway.TaskPaused
}

// denied reports whether the person said no in the task, which then ended
// "after your answer".
func denied(t gateway.TaskView) bool {
	for _, s := range t.Steps {
		if s.Status == "approval_denied" || strings.HasPrefix(s.Text, "You denied") {
			return true
		}
	}
	return false
}

// approvals counts the times the task asked the person.
func approvals(t gateway.TaskView) int {
	n := 0
	for _, s := range t.Steps {
		if s.Status == "approval_pending" || s.Status == "approval_denied" ||
			strings.HasPrefix(s.Text, "You allowed") || strings.HasPrefix(s.Text, "You denied") {
			n++
		}
	}
	return n
}

// appOf guesses the app a task used from its first launch, for the icon
// beside it in Activity.
func appOf(t gateway.TaskView) string {
	for _, s := range t.Steps {
		if s.Tool == "phone_launch" && s.Args != "" {
			var a struct {
				App string `json:"app"`
			}
			if json.Unmarshal([]byte(s.Args), &a) == nil && a.App != "" {
				return a.App
			}
		}
		if rest, ok := strings.CutPrefix(s.Text, "Opened "); ok && rest != "" && !strings.Contains(rest, " ") {
			return rest
		}
	}
	return ""
}

// appColors are the colors of the letter icons of apps.
var appColors = []string{"#c2410c", "#2563eb", "#7c3aed", "#0f766e", "#b45309", "#be185d", "#4d7c0f", "#1e3a8a"}

func appColor(name string) string {
	h := fnv.New32a()
	h.Write([]byte(name))
	return appColors[h.Sum32()%uint32(len(appColors))]
}

// nbsp is a no-break space: a line never breaks at it, so a number stays
// with its unit ("38 s", not "38" at the end of one line and "s" on the
// next).
const nbsp = "\u00a0"

// elapsed formats a duration as the prototype does: "38 s", "1 min 02 s",
// joined with no-break spaces so it never wraps.
func elapsed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return locales.TimeSeconds(strconv.Itoa(s))
	case s < 3600:
		return locales.TimeMinutes(strconv.Itoa(s/60), fmt.Sprintf("%02d", s%60))
	}
	return locales.TimeHours(strconv.Itoa(s/3600), fmt.Sprintf("%02d", s%3600/60))
}

// clock formats a duration as a player shows it: "1:48".
func clock(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func taskDuration(t gateway.TaskView, now time.Time) time.Duration {
	if !t.Ended.IsZero() {
		return t.Ended.Sub(t.Started)
	}
	return now.Sub(t.Started)
}

// connectionLine is the header's second line: "Android · USB".
func connectionLine(d gateway.DeviceView) string {
	kind := "Android"
	if d.Info.Platform == driver.IOS {
		kind = "iPhone"
	}
	if d.Status == gateway.DeviceOffline || d.Info.State == driver.StateOffline {
		return locales.ConnectionOffline(kind)
	}
	switch d.Info.Connection {
	case "wifi":
		return kind + " · Wi-Fi"
	case "usb":
		return kind + " · USB"
	case "":
		return kind
	}
	return kind + " · " + d.Info.Connection
}

// osLine is the settings line: "Android 16 · USB".
func osLine(d gateway.DeviceView) string {
	kind := "Android"
	if d.Info.Platform == driver.IOS {
		kind = "iOS"
	}
	s := strings.TrimSpace(kind + " " + d.Info.OSVersion)
	if d.Status == gateway.DeviceOffline {
		if d.Info.Detail != "" {
			return s + " · " + lowerFirst(d.Info.Detail)
		}
		return locales.ConnectionOffline(s)
	}
	switch d.Info.Connection {
	case "wifi":
		return s + " · Wi-Fi"
	case "usb":
		return s + " · USB"
	}
	return s
}

// lowerFirst lowers a sentence's first letter for use mid-sentence,
// keeping acronyms such as "USB" as they are.
func lowerFirst(s string) string {
	if s == "" || (len(s) > 1 && s[1] >= 'A' && s[1] <= 'Z') {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// dayLabel groups Activity by day.
func dayLabel(t, now time.Time) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		return locales.DayToday()
	}
	yd := now.AddDate(0, 0, -1)
	if y, m, d := yd.Date(); y1 == y && m1 == m && d1 == d {
		return locales.DayYesterday()
	}
	if y1 == y2 {
		return locales.DayThisYear(t)
	}
	return locales.DayOtherYear(t)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}

// kindNoun names a rule for "Always allow …": "Payments" → "payments".
func kindNoun(rule string) string { return ruleAction(rule) }
