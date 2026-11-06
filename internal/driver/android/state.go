package android

import (
	"regexp"
	"strconv"
	"strings"
)

// Parsers for dumpsys output. Android versions word these differently, so
// each parser accepts every form seen on the devices Pinky supports
// (Android 10 to 17).

var keyguardRE = regexp.MustCompile(`(?m)^\s*(showing|mIsShowing|isKeyguardShowing|mShowingLockscreen|isStatusBarKeyguard)=(true|false)\b`)

// parseKeyguard reports whether the lock screen is showing, from
// `dumpsys window policy`. It returns ok=false when the output has no
// keyguard state at all.
func parseKeyguard(out string) (showing, ok bool) {
	for _, m := range keyguardRE.FindAllStringSubmatch(out, -1) {
		ok = true
		if m[2] == "true" {
			return true, true
		}
	}
	return false, ok
}

var (
	inputShownRE = regexp.MustCompile(`\bmInputShown=(true|false)\b`)
	imeVisRE     = regexp.MustCompile(`\bmImeWindowVis=(\d+)`)
)

// parseIMEShown reports whether the soft keyboard is showing, from
// `dumpsys input_method`.
func parseIMEShown(out string) bool {
	for _, m := range inputShownRE.FindAllStringSubmatch(out, -1) {
		if m[1] == "true" {
			return true
		}
	}
	for _, m := range imeVisRE.FindAllStringSubmatch(out, -1) {
		// IME_VISIBLE is bit 1 of the window visibility flags.
		if v, err := strconv.Atoi(m[1]); err == nil && v&2 != 0 {
			return true
		}
	}
	return false
}

var imeWindowRE = regexp.MustCompile(`\b(isVisible|isOnScreen)=(true|false)\b`)

// parseIMEWindowVisible reports whether `dumpsys window InputMethod` shows
// a visible keyboard window.
func parseIMEWindowVisible(out string) bool {
	for _, m := range imeWindowRE.FindAllStringSubmatch(out, -1) {
		if m[2] == "true" {
			return true
		}
	}
	return false
}

var focusedAppRE = regexp.MustCompile(`mFocusedApp=.*?ActivityRecord\{[0-9a-f]+ u\d+ ([A-Za-z0-9_.]+)/`)

// parseFocusedApp returns the package of the focused activity, from
// `dumpsys window displays` or `dumpsys window`.
func parseFocusedApp(out string) string {
	if m := focusedAppRE.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

var wakefulnessRE = regexp.MustCompile(`mWakefulness=(\w+)`)

// parseAwake reports whether the screen is on, from `dumpsys power`.
// Dozing (always-on display) counts as off.
func parseAwake(out string) (awake, ok bool) {
	m := wakefulnessRE.FindStringSubmatch(out)
	if m == nil {
		return false, false
	}
	return m[1] == "Awake", true
}

var sizeRE = regexp.MustCompile(`(Physical|Override) size: (\d+)x(\d+)`)

// parseWMSize returns the display size from `wm size`: the override when
// one is set, else the physical size, in the natural orientation.
func parseWMSize(out string) (w, h int, ok bool) {
	for _, m := range sizeRE.FindAllStringSubmatch(out, -1) {
		mw, _ := strconv.Atoi(m[2])
		mh, _ := strconv.Atoi(m[3])
		if m[1] == "Override" || !ok {
			w, h, ok = mw, mh, true
		}
	}
	return w, h, ok
}

// rotate returns the size of a w x h display after quarter turns.
func rotate(w, h, rotation int) (int, int) {
	if rotation%2 == 1 {
		return h, w
	}
	return w, h
}

// orient returns the size of a w x h display (in any orientation) laid out
// like a frame of fw x fh: landscape frames give the longer side first.
func orient(w, h, fw, fh int) (int, int) {
	long, short := max(w, h), min(w, h)
	if fw > fh {
		return long, short
	}
	return short, long
}

// splitSections cuts the output of several commands separated by
// "--name--" marker lines into a map.
func splitSections(out string) map[string]string {
	sec := map[string]string{}
	name := ""
	var b strings.Builder
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if len(t) > 4 && strings.HasPrefix(t, "--") && strings.HasSuffix(t, "--") && !strings.Contains(t, " ") {
			sec[name] = b.String()
			b.Reset()
			name = strings.Trim(t, "-")
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	sec[name] = b.String()
	return sec
}
