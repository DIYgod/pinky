package ios

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// XCUIElementType values (XCTest), as the AutomationType attribute gives them.
const (
	xcOther           = 1
	xcButton          = 9
	xcRadioButton     = 10
	xcCheckBox        = 12
	xcPopUpButton     = 14
	xcMenuButton      = 16
	xcToolbarButton   = 17
	xcKeyboard        = 19
	xcKey             = 20
	xcTabBar          = 22
	xcTable           = 26
	xcCollectionView  = 32
	xcSwitch          = 40
	xcToggle          = 41
	xcLink            = 42
	xcImage           = 43
	xcIcon            = 44
	xcSearchField     = 45
	xcScrollView      = 46
	xcStaticText      = 48
	xcTextField       = 49
	xcSecureTextField = 50
	xcTextView        = 52
	xcMenuItem        = 54
	xcIncrementArrow  = 59
	xcDecrementArrow  = 60
	xcCell            = 75
	xcTab             = 80
)

// raxRecheckFailures is how long a phone whose remoteAXService failed is
// left on the inspector path before Observe tries the service again.
const raxRecheckFailures = 30 * time.Second

// raxLimits bound one walk over remoteAXService.
var raxLimits = walkLimits{MaxElements: 400, Budget: 4 * time.Second}

// raxReady returns the remote accessibility session, connecting on first
// use. A phone that refused it is asked again only after a while, so every
// Observe on it does not pay for a failed connection.
func (d *Device) raxReady(ctx context.Context) (*raxSession, error) {
	d.svcMu.Lock()
	defer d.svcMu.Unlock()
	if d.rax != nil {
		if d.rax.alive() {
			return d.rax, nil
		}
		d.rax.Close()
		d.rax = nil
	}
	if d.raxErr != nil && time.Since(d.raxFailedAt) < raxRecheckFailures {
		return nil, d.raxErr
	}
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return nil, fmt.Errorf("device closed: %w", driver.ErrOffline)
	}
	s, err := dialRAX(ctx, d.l.entry)
	if err != nil {
		if ctx.Err() == nil {
			d.raxErr, d.raxFailedAt = err, time.Now()
		}
		return nil, err
	}
	d.rax, d.raxErr = s, nil
	return s, nil
}

// observeRAX reads the screen through remoteAXService: every element with
// its frame, from one walk. OCR runs only when asked for, to add text the
// tree does not have.
func (d *Device) observeRAX(ctx context.Context, opt driver.ObserveOptions, st *ObserveStats) (*screen.Screen, error) {
	start := time.Now()
	rax, err := d.raxReady(ctx)
	if err != nil {
		return nil, err
	}
	si := d.screen()
	var wg sync.WaitGroup
	var els []raxElement
	var complete bool
	var walkErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.Now()
		var pushed bool
		els, complete, pushed, walkErr = d.raxTree(ctx, rax)
		st.Walk = time.Since(t)
		if pushed {
			st.Source = "remoteax-push"
		}
	}()
	var lines []ocrLine
	var ocrErr error
	if opt.OCR {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t := time.Now()
			png, err := d.ScreenshotPNG(ctx)
			st.Shot = time.Since(t)
			if err != nil {
				ocrErr = err
				return
			}
			t = time.Now()
			lines, ocrErr = recognizeText(png, ocrOptions{Languages: ocrLanguages})
			st.OCR = time.Since(t)
		}()
	}
	var locked bool
	wg.Add(1)
	go func() {
		defer wg.Done()
		locked, _ = d.Locked(ctx)
	}()
	wg.Wait()
	if len(els) == 0 {
		if walkErr == nil {
			walkErr = errRAXTimeout
		}
		return nil, walkErr
	}
	st.WalkError = walkErr
	st.OCRError = ocrErr
	st.Complete = complete && walkErr == nil
	if st.Source == "" {
		st.Source = "remoteax"
	}

	app := els[0]
	s := &screen.Screen{
		Width: si.Width, Height: si.Height,
		Orientation: orientationName(si.Orientation),
		Locked:      locked,
		CapturedAt:  time.Now(),
	}
	if a, ok := d.appForPID(ctx, uint64(app.Object.PID)); ok {
		s.App = screen.App{Name: a.Name, ID: a.ID}
	} else if app.Label != "" {
		s.App = screen.App{Name: app.Label}
	}
	scale := si.Scale
	if app.HasFrame && app.Frame.W > 0 {
		scale = float64(si.Width) / app.Frame.W
	}
	s.Elements, s.Keyboard = raxScreenElements(els, scale, si.Width, si.Height, s.App.ID)
	if opt.OCR && len(lines) > 0 {
		if top, kb := keyboardFromOCR(lines); kb {
			s.Keyboard = true
			kept := lines[:0:0]
			for _, l := range lines {
				if l.Y < top {
					kept = append(kept, l)
				}
			}
			lines = kept
		}
		s.Elements = append(s.Elements, ocrExtras(s.Elements, lines, si.Width, si.Height)...)
	}
	for _, e := range s.Elements {
		if e.Source == screen.SourceTree && !e.Bounds.Empty() {
			st.Placed++
		}
	}
	st.Elements = len(els) - 1
	st.OCRLines = len(lines)
	st.Total = time.Since(start)
	return s, nil
}

// AccessibilityPlatformTranslation roles seen on iOS 27 (AXPAttributeRole).
const (
	axpRoleButton      = 2
	axpRoleSwitch      = 3
	axpRoleImage       = 7
	axpRoleSearchField = 15
)

// traitRemoteHost marks a view that shows another process's elements (the
// keyboard, for one). Observed on iOS 27; not public.
const traitRemoteHost = 1 << 34

const (
	// raxPushAfter: a push whose first tree arrives sooner than this after
	// an input may describe the screen before it. The phone pushes 1.1–1.5 s
	// after a change.
	raxPushAfter = time.Second
	// raxPushTrust is how long after Pinky's last input a pushed tree is
	// trusted. Later the screen may have changed on its own in ways the
	// phone does not push (a timer's text), so Observe walks; by then the
	// phone is not building a tree, so the walk is quick.
	raxPushTrust = 6 * time.Second
	// raxSecondPush is how long Observe waits for a first push's second
	// one when the first leaves an element unnamed.
	raxSecondPush = 1200 * time.Millisecond
	// raxEmptyWait: an app that has just launched can show its screen
	// before it lists any element; a walk that finds none waits this long
	// for the phone's push, then walks again.
	raxEmptyWait = 1500 * time.Millisecond
)

// raxTree returns the elements on screen: the tree the phone pushed after
// Pinky's last input, while that is recent, else a walk. A walk right after
// a change waits behind the tree the phone is building then (1–4 s on
// screens with many images), so after an input it walks and waits for that
// push at once, and takes whichever comes first. A push counts only when
// its first tree came at least raxPushAfter after the input and its app is
// the frontmost app the walk found first: a push can arrive late, after
// the next input, and then describes the screen before it.
func (d *Device) raxTree(ctx context.Context, rax *raxSession) ([]raxElement, bool, bool, error) {
	input := d.lastInputAt()
	trusted := time.Since(input) < raxPushTrust
	var stop atomic.Bool
	apps := make(chan *axpObject, 1)
	walked := walkDetached(ctx, rax, &stop, func(app *axpObject) { apps <- app })
	if !trusted {
		select {
		case r := <-walked:
			return r.els, r.complete, false, r.err
		case <-ctx.Done():
			stop.Store(true)
			return nil, false, false, ctx.Err()
		}
	}
	var app *axpObject
	t, next := rax.pushed()
	var first *raxTree // a matching first push still waiting for its second
	var second <-chan time.Time
	// consider returns a push to use now, or remembers a first push.
	consider := func(t *raxTree) *raxTree {
		if t == nil || app == nil || t.appPID != app.PID || !t.start.After(input.Add(raxPushAfter)) || time.Since(input) >= raxPushTrust {
			return nil
		}
		if !t.first || !t.unnamed() {
			return t
		}
		if first == nil || first.start != t.start {
			first = t
			second = time.After(raxSecondPush)
		}
		return nil
	}
	var empty <-chan time.Time
	rewalked := false
	for {
		select {
		case r := <-walked:
			if r.err != nil || len(r.els) > 1 || rewalked {
				return r.els, r.complete, false, r.err
			}
			empty = time.After(raxEmptyWait)
			walked = nil
		case <-empty:
			walked = walkDetached(ctx, rax, &stop, nil)
			empty = nil
			rewalked = true
		case app = <-apps:
			if u := consider(t); u != nil {
				stop.Store(true)
				return u.els, true, true, nil
			}
		case <-next:
			t, next = rax.pushed()
			if u := consider(t); u != nil {
				stop.Store(true)
				return u.els, true, true, nil
			}
		case <-second:
			stop.Store(true)
			return first.els, true, true, nil
		case <-ctx.Done():
			stop.Store(true)
			return nil, false, false, ctx.Err()
		}
	}
}

type walkResult struct {
	els      []raxElement
	complete bool
	err      error
}

// walkDetached walks with a context the caller's cancellation does not
// reach: a request cut short would leave its answer in flight and spoil
// the session. A caller that gives up sets stop, and the walk ends between
// two requests.
func walkDetached(ctx context.Context, rax *raxSession, stop *atomic.Bool, onApp func(*axpObject)) <-chan walkResult {
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), raxLimits.Budget+raxRequestTimeout)
	ch := make(chan walkResult, 1)
	go func() {
		defer cancel()
		els, complete, err := rax.walkUntil(wctx, raxLimits, stop, onApp)
		ch <- walkResult{els, complete, err}
	}()
	return ch
}

// raxScreenElements turns a walk into screen elements: the accessibility
// elements (containers only lead to them), with frames in screen pixels.
// Elements entirely off screen are left out.
//
// Elements of other processes come with frames relative to the view that
// hosts them. A host is marked by traitRemoteHost and comes right before
// them in the walk; elements of a process with no such host (home screen
// widgets) cannot be placed and are left out. A process whose elements are
// mostly single characters is the on-screen keyboard: it only marks the
// keyboard as shown.
func raxScreenElements(els []raxElement, scale float64, w, h int, appID string) ([]screen.Element, bool) {
	if len(els) == 0 {
		return nil, false
	}
	appPID := els[0].Object.PID
	keyboard := false
	keyboardPID := map[int]bool{}
	chars := map[int]int{}
	for _, e := range els {
		if e.Object.PID != appPID && isKeyChar(e.Label) {
			chars[e.Object.PID]++
		}
	}
	for pid, n := range chars {
		if n >= 8 {
			keyboardPID[pid] = true
			keyboard = true
		}
	}
	offset := map[int]axpRect{} // by process: its host's frame
	var host *raxElement        // the last host, until an element of the app follows it
	inKeyboard := make([]bool, len(els))
	inTabBar := make([]bool, len(els))
	paths := make([]string, len(els))
	var out []screen.Element
	for i := range els {
		e := els[i]
		switch p := e.Parent; {
		case p > 0 && p < i:
			inKeyboard[i] = inKeyboard[p]
			inTabBar[i] = inTabBar[p]
			paths[i] = paths[p] + "/" + xcName(els[p])
		default: // the app, or right below it
			paths[i] = appID
		}
		switch e.Type {
		case xcKeyboard:
			inKeyboard[i] = true
			keyboard = true
		case xcTabBar:
			inTabBar[i] = true
		}
		switch {
		case e.Traits&traitRemoteHost != 0 && e.HasFrame:
			host = &els[i]
		case e.Object.PID == appPID:
			host = nil
		}
		if pid := e.Object.PID; pid != appPID && e.HasFrame {
			if _, ok := offset[pid]; !ok && host != nil && fitsIn(e.Frame, host.Frame) {
				offset[pid] = host.Frame
			}
			hf, ok := offset[pid]
			if !ok && !keyboardPID[pid] {
				continue // no host to place it by
			}
			e.Frame.X += hf.X
			e.Frame.Y += hf.Y
		}
		if i == 0 || !e.IsElement || !e.Visible {
			continue
		}
		if inKeyboard[i] || keyboardPID[e.Object.PID] {
			keyboard = true
			continue
		}
		if !e.HasFrame || e.Traits&traitRemoteHost != 0 {
			continue
		}
		r := pixelRect(e.Frame, scale, w, h)
		if r.Empty() {
			continue
		}
		role, states := raxRole(e, inTabBar[i])
		label := raxLabel(e, role)
		if label == "" && role == screen.RoleOther {
			continue
		}
		out = append(out, screen.Element{
			Role: role, Label: label, Bounds: r, States: states,
			Source: screen.SourceTree, Path: paths[i], ID: e.Ident,
		})
	}
	return out, keyboard
}

// isKeyChar reports whether a label is one letter or digit, as keyboard keys
// are labelled.
func isKeyChar(label string) bool {
	rs := []rune(strings.TrimSpace(label))
	return len(rs) == 1 && (unicode.IsLetter(rs[0]) || unicode.IsDigit(rs[0]))
}

// fitsIn reports whether a frame relative to a host lies within the host's
// size, give or take a point.
func fitsIn(f, host axpRect) bool {
	return f.X >= -1 && f.Y >= -1 && f.X+f.W <= host.W+1 && f.Y+f.H <= host.H+1
}

// xcName names an ancestor in an element's path: its identifier when it has
// one, else its type.
func xcName(e raxElement) string {
	if e.Ident != "" {
		return e.Ident
	}
	return "t" + itoa(e.Type)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// pixelRect converts a frame in points to screen pixels, clipped to the
// screen.
func pixelRect(f axpRect, scale float64, w, h int) screen.Rect {
	x0 := int(math.Round(f.X * scale))
	y0 := int(math.Round(f.Y * scale))
	x1 := int(math.Round((f.X + f.W) * scale))
	y1 := int(math.Round((f.Y + f.H) * scale))
	x0, y0 = max(x0, 0), max(y0, 0)
	x1, y1 = min(x1, w), min(y1, h)
	if x1 <= x0 || y1 <= y0 {
		return screen.Rect{}
	}
	return screen.Rect{X: x0, Y: y0, W: x1 - x0, H: y1 - y0}
}

// raxRole maps an element's XCUIElementType, or its traits when the type
// says nothing, to a role and states.
func raxRole(e raxElement, inTabBar bool) (string, []string) {
	var role string
	var states []string
	checked := func() {
		if v := strings.ToLower(e.Value); v == "1" || v == "on" || v == "开" {
			states = append(states, screen.StateChecked)
		}
	}
	switch e.Type {
	case xcButton, xcPopUpButton, xcMenuButton, xcToolbarButton, xcMenuItem, xcKey, xcIcon, xcCell, xcIncrementArrow, xcDecrementArrow:
		role = screen.RoleButton
	case xcRadioButton, xcCheckBox:
		role = screen.RoleCheckbox
		checked()
	case xcSwitch, xcToggle:
		role = screen.RoleSwitch
		checked()
	case xcLink:
		role = screen.RoleLink
	case xcImage:
		role = screen.RoleImage
	case xcSearchField, xcTextField, xcTextView:
		role = screen.RoleInput
		states = append(states, screen.StateEditable)
	case xcSecureTextField:
		role = screen.RoleInput
		states = append(states, screen.StateEditable, screen.StatePassword)
	case xcStaticText:
		role = screen.RoleText
	case xcTab:
		role = screen.RoleTab
	case xcTable, xcCollectionView, xcScrollView:
		role = screen.RoleList
	default:
		role, states = axRole(e.Traits, nil, e.Value)
		// A first pushed tree has no AutomationType; the translator's own
		// role tells what traits do not (seen on iOS 27).
		switch e.AXRole {
		case axpRoleSwitch:
			role, states = screen.RoleSwitch, nil
			checked()
		case axpRoleSearchField:
			role = screen.RoleInput
			states = appendState(states, screen.StateEditable)
		case axpRoleImage:
			if role == screen.RoleOther {
				role = screen.RoleImage
			}
		case axpRoleButton:
			if role == screen.RoleOther {
				role = screen.RoleButton
			}
		}
	}
	if inTabBar && role == screen.RoleButton {
		role = screen.RoleTab
	}
	if role == screen.RoleInput && e.Focused {
		states = append(states, screen.StateFocused)
	}
	if e.Selected || e.Traits&traitSelected != 0 {
		states = appendState(states, screen.StateSelected)
	}
	if !e.Enabled || e.Traits&traitNotEnabled != 0 {
		states = appendState(states, screen.StateDisabled)
	}
	return role, states
}

func appendState(states []string, s string) []string {
	for _, x := range states {
		if x == s {
			return states
		}
	}
	return append(states, s)
}

// raxLabel is what an agent reads for the element: its label, then its
// value as VoiceOver reads it, except for switches and check boxes whose
// value is a state.
func raxLabel(e raxElement, role string) string {
	label := strings.TrimSpace(e.Label)
	value := strings.TrimSpace(e.Value)
	if value == "" {
		value = strings.TrimSpace(e.ValueText)
	}
	if role == screen.RoleSwitch || role == screen.RoleCheckbox {
		value = ""
	}
	if label == "" {
		label, value = value, ""
	}
	if label == "" {
		label = strings.TrimSpace(e.Placeholder)
	}
	if label == "" {
		label = e.Ident
	}
	if value != "" && value != label {
		label += ", " + value
	}
	return label
}

// ocrExtras returns the OCR lines no element already shows, as OCR
// elements: a line counts as shown when its center lies in an element whose
// label contains its text.
func ocrExtras(els []screen.Element, lines []ocrLine, w, h int) []screen.Element {
	var out []screen.Element
	for _, l := range lines {
		text := strings.TrimSpace(l.Text)
		if text == "" || l.Confidence < 0.3 {
			continue
		}
		r := ocrRect(l, w, h)
		c := r.Center()
		nt := norm(text)
		shown := false
		for _, e := range els {
			if e.Bounds.Contains(c) && strings.Contains(norm(e.Label), nt) {
				shown = true
				break
			}
		}
		if !shown {
			out = append(out, screen.Element{Role: screen.RoleText, Label: text, Source: screen.SourceOCR, Bounds: r})
		}
	}
	return out
}
