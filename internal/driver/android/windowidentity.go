package android

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/screen"
)

// A missing accessibility root is not proof that no app is on screen.
// Recover identity only from a fresh, complete WindowManager snapshot on
// the main display. Never infer it from an activity, a title, or the last
// launched app. Unsupported layouts deliberately remain unidentified.
const windowIdentityCommand = "dumpsys window -a"

type windowIdentity struct {
	app   string
	frame screen.Rect
	bars  []screen.Rect
	token string
}

var (
	wmWindowRE  = regexp.MustCompile(`(?m)^  Window #\d+ Window\{([0-9a-f]+) u\d+ [^\n]*\}:`)
	wmDisplayRE = regexp.MustCompile(`(?m)^  Display: mDisplayId=(\d+)\b`)
	wmFocusRE   = regexp.MustCompile(`(?m)^\s*mCurrentFocus=Window\{([0-9a-f]+) u\d+ `)
	wmOwnerRE   = regexp.MustCompile(`(?m)^\s*mOwnerUid=(\d+)\s+[^\n]*\bpackage=([A-Za-z0-9_.]+)\b`)
	wmTypeRE    = regexp.MustCompile(`\bty=([A-Z_]+)\b`)
	wmFrameRE   = regexp.MustCompile(`(?m)^\s*Frames: [^\n]*\bframe=\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]`)
	wmTopRE     = regexp.MustCompile(`(?m)^\s*mTopFocusedDisplayId=(\d+)\b`)
	wmSecureRE  = regexp.MustCompile(`\b(?:FLAG_)?SECURE\b`)
)

func wmSection(out, name string) string {
	start := strings.Index(out, "\nWINDOW MANAGER "+name+" (")
	if start < 0 {
		return ""
	}
	s := out[start+1:]
	if end := strings.IndexByte(s, '\n'); end >= 0 {
		s = s[end+1:]
	} else {
		return ""
	}
	if end := strings.Index(s, "\nWINDOW MANAGER "); end >= 0 {
		s = s[:end]
	}
	return s
}

func wmRect(block string) screen.Rect {
	m := wmFrameRE.FindStringSubmatch(block)
	if m == nil {
		return screen.Rect{}
	}
	n := [4]int64{}
	for i := range n {
		var err error
		n[i], err = strconv.ParseInt(m[i+1], 10, 32)
		if err != nil {
			return screen.Rect{}
		}
	}
	w, h := n[2]-n[0], n[3]-n[1]
	if w <= 0 || h <= 0 || w > 1<<31-1 || h > 1<<31-1 {
		return screen.Rect{}
	}
	return screen.Rect{X: int(n[0]), Y: int(n[1]), W: int(w), H: int(h)}
}

// A visibility signal must occur exactly once and be a complete field.
// Contradictory or malformed dumps must not hide a visible overlay.
func wmBool(block, key string) (value, known bool) {
	count := 0
	for field := range strings.FieldsSeq(block) {
		if !strings.HasPrefix(field, key+"=") {
			continue
		}
		count++
		switch field {
		case key + "=true":
			value = true
		case key + "=false":
			value = false
		default:
			return false, false
		}
	}
	return value, count == 1
}

// parseWindowIdentity fails closed on unknown lock state, a different
// focused display, system dialogs (including ANR), IME, overlays,
// protected content, split screen and missing ownership/geometry.
func parseWindowIdentity(out string, width, height int) *windowIdentity {
	if width <= 0 || height <= 0 {
		return nil
	}
	locked, known := parseKeyguard(wmSection(out, "POLICY STATE"))
	if !known || locked {
		return nil
	}
	windows := wmSection(out, "WINDOWS")
	top := wmTopRE.FindStringSubmatch(windows)
	if top == nil || top[1] != "0" {
		return nil
	}
	displays := wmSection(out, "DISPLAY CONTENTS")
	var display string
	ds := wmDisplayRE.FindAllStringSubmatchIndex(displays, -1)
	for i, m := range ds {
		if displays[m[2]:m[3]] != "0" {
			continue
		}
		end := len(displays)
		if i+1 < len(ds) {
			end = ds[i+1][0]
		}
		display = displays[m[0]:end]
	}
	focus := wmFocusRE.FindStringSubmatch(display)
	if focus == nil {
		return nil
	}
	activeApp := parseFocusedApp(display)
	if activeApp == "" {
		return nil
	}
	type win struct {
		id, pkg, uid, typ, block string
		frame                    screen.Rect
	}
	var visible []win
	ms := wmWindowRE.FindAllStringSubmatchIndex(windows, -1)
	if len(ms) == 0 || strings.Count(windows, "  Window #") != len(ms) {
		return nil // do not silently skip an unrecognized window header
	}
	for i, m := range ms {
		end := len(windows)
		if i+1 < len(ms) {
			end = ms[i+1][0]
		}
		block := windows[m[0]:end]
		did := displayIDRE.FindStringSubmatch(block)
		if did == nil {
			return nil
		}
		if did[1] != "0" {
			continue
		}
		// All three visibility signals must agree. A missing or transient
		// state must not silently hide an overlay from this proof.
		vis, visKnown := wmBool(block, "isVisible")
		on, onKnown := wmBool(block, "isOnScreen")
		surface, surfaceKnown := wmBool(block, "mHasSurface")
		if !visKnown || !onKnown || !surfaceKnown {
			return nil
		}
		if !vis && !on {
			continue
		}
		if !vis || !on || !surface || wmSecureRE.MatchString(block) {
			return nil
		}
		owner, typ := wmOwnerRE.FindStringSubmatch(block), wmTypeRE.FindStringSubmatch(block)
		if owner == nil || typ == nil {
			return nil
		}
		frame := wmRect(block)
		if frame.Empty() {
			return nil
		}
		visible = append(visible, win{windows[m[2]:m[3]], owner[2], owner[1], typ[1], block, frame})
	}
	var front *win
	for i := range visible {
		if visible[i].id == focus[1] {
			front = &visible[i]
		}
	}
	if front == nil || front.pkg != activeApp || (front.typ != "BASE_APPLICATION" && front.typ != "APPLICATION") {
		return nil
	}
	full := screen.Rect{W: width, H: height}
	if front.frame != full {
		return nil
	}
	p := &windowIdentity{app: front.pkg, frame: front.frame}
	var fingerprint strings.Builder
	belowFocus := false
	for _, w := range visible {
		fmt.Fprintf(&fingerprint, "%s/%s/%s/%s/%v;", w.id, w.uid, w.pkg, w.typ, w.frame)
		if w.id == front.id {
			belowFocus = true
			continue
		}
		// A transparent WebView can keep its own full-screen parent
		// visible behind it. Account for that window in the proof, but
		// never accept a foreign owner or an application above the focus.
		if belowFocus && w.pkg == front.pkg && w.uid == front.uid && w.frame == full &&
			(w.typ == "BASE_APPLICATION" || w.typ == "APPLICATION") {
			continue
		}
		bar := w.typ == "STATUS_BAR" || w.typ == "NAVIGATION_BAR" ||
			(w.typ == "NAVIGATION_BAR_PANEL" && strings.Contains(w.block, "TRUSTED_OVERLAY") && strings.Contains(w.block, "NOT_TOUCHABLE"))
		if !bar || w.pkg != "com.android.systemui" || !strings.Contains(w.block, "NOT_FOCUSABLE") || !isBar(w.frame, width, height) {
			return nil
		}
		p.bars = append(p.bars, w.frame)
	}
	fmt.Fprintf(&fingerprint, "focus=%s;size=%dx%d", front.id, width, height)
	p.token = fmt.Sprintf("wm:0:%x", sha256.Sum256([]byte(fingerprint.String())))
	return p
}

func (d *Device) readWindowIdentity(ctx context.Context, w, h int) *windowIdentity {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := d.client.Shell(ctx, d.info.ID, windowIdentityCommand)
	if err != nil {
		return nil
	}
	return parseWindowIdentity(string(out), w, h)
}

func (d *Device) recoverWindowIdentity(ctx context.Context, s *screen.Screen, sc textScan) textScan {
	if s.Locked || s.App.ID != "" || s.App.Name != "" || len(s.Elements) != 0 || s.Keyboard {
		return sc
	}
	p := d.readWindowIdentity(ctx, s.Width, s.Height)
	if p == nil {
		return sc
	}
	s.App = screen.App{ID: p.app}
	s.App.Name = d.companionAppName(s.App)
	s.CaptureIdentity = p.token
	s.Covered = append(s.Covered, p.bars...)
	d.cfg.logf("android: %s: missing accessibility identity verified by WindowManager on display 0 (%s)", d.info.ID, p.app)
	return textScan{areas: []textArea{{rect: p.frame, above: p.bars, kind: "window", path: "wm:" + p.app}}, bars: p.bars, proof: p}
}

func (d *Device) proofMoved(ctx context.Context, s *screen.Screen, proof *windowIdentity) bool {
	next := d.readWindowIdentity(ctx, s.Width, s.Height)
	return next == nil || next.token != proof.token
}
