package harmonyos

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
	"github.com/DIYgod/pinky/internal/swipetrace"
)

var (
	settings = screen.App{ID: "com.huawei.hmos.settings", Name: "Settings"}
	home     = screen.App{ID: "com.ohos.sceneboard", Name: "Home screen"}
)

var (
	powerPattern   = regexp.MustCompile(`(?m)^Current State:\s*([A-Z_]+)\b`)
	focusPattern   = regexp.MustCompile(`(?m)^Focus window:\s*(\d+)\s*$`)
	windowPattern  = regexp.MustCompile(`(?m)^\s*(\S+)[ \t]+(\d+)[ \t]+(\d+)[ \t]+(\d+)[ \t]+\d+[ \t]+\d+[ \t]+\S+[ \t]+(-?\d+)[ \t]+\d+[ \t]+\[[ \t]*(\d+)[ \t]+(\d+)[ \t]+(\d+)[ \t]+(\d+)[ \t]*\]`)
	settingsWindow = regexp.MustCompile(`^settings[0-9]*$`)
	desktopWindow  = regexp.MustCompile("^SCBDesktop[0-9]*$")
	boundsPattern  = regexp.MustCompile(`^\[\s*(-?\d+)\s*,\s*(-?\d+)\s*\]\[\s*(-?\d+)\s*,\s*(-?\d+)\s*\]$`)
)

type device struct {
	provider  *Provider
	serial    string
	info      driver.Info
	mu        sync.Mutex
	last      surface
	panes     []screen.Rect
	needFocus bool

	previewMu sync.Mutex
	preview   *mirrorStream
	gesture   *mirrorGesture
}

type surface struct {
	id, pid, chromePID string
	width, height      int
	top, bottom        int
	desktop            bool
}

func (d *device) Info() driver.Info { return d.info }
func (d *device) Close() error      { return nil }
func (d *device) ReadsText() bool   { return false }
func (d *device) Tap(context.Context, screen.Point, driver.TapOptions) error {
	return driver.ErrUnsupported
}
func (d *device) Type(context.Context, string, driver.TypeOptions) error {
	return driver.ErrUnsupported
}
func (d *device) Key(ctx context.Context, key driver.Key) error {
	return d.key(ctx, key, false)
}

func (d *device) MirrorKey(ctx context.Context, key driver.Key) error {
	return d.key(ctx, key, true)
}

func (d *device) key(ctx context.Context, key driver.Key, owner bool) error {
	var name string
	switch key {
	case driver.KeyBack:
		name = "Back"
	case driver.KeyHome:
		name = "Home"
	case driver.KeyRotate:
		// Rotation is sent over the running mirror's UITest control channel.
	default:
		return driver.ErrUnsupported
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	check := d.focusedSurface
	if owner {
		check = d.focusedOwnerSurface
	}
	current, err := check(ctx)
	if err != nil {
		return err
	}
	d.last, d.panes, d.gesture = surface{}, nil, nil
	d.needFocus = true
	if key == driver.KeyRotate {
		return d.rotate(ctx, current)
	}
	_, err = d.shell(ctx, "uitest uiInput keyEvent "+name)
	return err
}
func (d *device) rotate(ctx context.Context, current surface) error {
	d.previewMu.Lock()
	preview := d.preview
	d.previewMu.Unlock()
	if preview == nil || preview.control == nil || current.width == 0 || current.height == 0 {
		return fmt.Errorf("start the live HarmonyOS mirror before rotating: %w", driver.ErrUnsupported)
	}
	landscape := current.width < current.height
	display, err := d.shell(ctx, "hidumper -s DisplayManagerService -a -a")
	if err != nil {
		return err
	}
	command, err := nextDisplayRotation(string(display))
	if err != nil {
		return err
	}
	if _, err := preview.control.Write([]byte{command}); err != nil {
		return fmt.Errorf("send HarmonyOS rotation: %w", err)
	}

	// The SDK's method doesn't report whether the foreground app rotated.
	// Verify the device's real display rather than the preview bitmap.
	check, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for {
		next, err := d.focusedOwnerSurface(check)
		if err != nil {
			if check.Err() != nil && ctx.Err() == nil {
				break
			}
			return err
		}
		if next.width != 0 && next.height != 0 && (next.width > next.height) == landscape {
			return nil
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("the HarmonyOS display did not change orientation")
}

// The SDK methods request absolute 0° and 90°. A tablet can have a
// landscape natural orientation, so choose by the physical screen's angle.
func nextDisplayRotation(report string) (byte, error) {
	primary := false
	for line := range strings.SplitSeq(report, "\n") {
		name, value, _ := strings.Cut(line, ":")
		switch strings.TrimSpace(name) {
		case "DisplayId":
			primary = strings.TrimSpace(value) == "0"
		case "ScreenRotation":
			if !primary {
				continue
			}
			switch strings.TrimSpace(value) {
			case "0", "180":
				return 'H', nil
			case "90", "270":
				return 'V', nil
			}
		}
	}
	return 0, fmt.Errorf("HarmonyOS did not report the main display's rotation")
}

func (d *device) Launch(context.Context, string) error       { return driver.ErrUnsupported }
func (d *device) Apps(context.Context) ([]driver.App, error) { return nil, driver.ErrUnsupported }
func (d *device) shell(ctx context.Context, command string) ([]byte, error) {
	return d.provider.shell(ctx, d.serial, command)
}

// Both navigation and capture verify the focused window and process on the main display.
const focusCommand = `hidumper -s PowerManagerService -a "-s"; hidumper -s WindowManagerService -a "-a"; printf '\nPINKY_PROCESS_LIST\n'; ps -A -o PID,NAME`

func (d *device) focusedSettings(ctx context.Context) (surface, error) {
	s, err := d.focusedSurface(ctx)
	if err == nil && s.desktop {
		return surface{}, fmt.Errorf("open the system Settings app on the tablet: %w", driver.ErrUnsupported)
	}
	return s, err
}

func (d *device) focusedSurface(ctx context.Context) (surface, error) {
	out, err := d.shell(ctx, focusCommand)
	if err != nil {
		return surface{}, err
	}
	return verifyFocusReport(out)
}

func verifyFocusReport(out []byte) (surface, error) {
	windows, processes, _ := strings.Cut(strings.ReplaceAll(string(out), "\r\n", "\n"), "\nPINKY_PROCESS_LIST\n")
	s, err := parseFocusSurface(windows)
	if err != nil {
		return surface{}, err
	}
	names := make(map[string]string)
	for line := range strings.SplitSeq(processes, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && (fields[0] == s.pid || fields[0] == s.chromePID) {
			names[fields[0]] = fields[1]
		}
	}
	expected := settings.ID
	if s.desktop {
		expected = home.ID
	}
	if names[s.pid] != expected || (s.chromePID != "" && names[s.chromePID] != home.ID) {
		return surface{}, fmt.Errorf("could not verify the HarmonyOS system window: %w", driver.ErrUnsupported)
	}
	return s, nil
}

// primaryWindows checks the power and lock state before returning any
// main-display window. Focus can be absent during a system animation.
func primaryWindows(report string) (rows [][]string, selected []string, err error) {
	power := powerPattern.FindStringSubmatch(report)
	if len(power) != 2 {
		return nil, nil, fmt.Errorf("HarmonyOS did not report the screen power state")
	}
	if power[1] != "AWAKE" {
		return nil, nil, driver.ErrLocked
	}
	var focusID string
	if focus := focusPattern.FindStringSubmatch(report); len(focus) == 2 {
		focusID = focus[1]
	}
	for _, row := range windowPattern.FindAllStringSubmatch(report, -1) {
		if row[2] != "0" {
			continue
		}
		if strings.TrimRight(row[1], "0123456789") == "ScreenLockWindow" &&
			(row[4] == focusID || visibleWindow(row)) {
			return nil, nil, driver.ErrLocked
		}
		rows = append(rows, row)
		if row[4] == focusID {
			selected = row
		}
	}
	return rows, selected, nil
}

func parseFocusSurface(report string) (surface, error) {
	rows, selected, err := primaryWindows(report)
	if err != nil {
		return surface{}, err
	}
	if selected == nil || !visibleWindow(selected) {
		return surface{}, fmt.Errorf("no visible HarmonyOS system window: %w", driver.ErrUnsupported)
	}
	desktop := desktopWindow.MatchString(selected[1])
	if !desktop && !settingsWindow.MatchString(selected[1]) {
		return surface{}, fmt.Errorf("open system Settings or Home on the tablet: %w", driver.ErrUnsupported)
	}
	w, h := number(selected[8]), number(selected[9])
	if selected[6] != "0" || selected[7] != "0" || w > 8192 || h > 8192 || w*h > 32_000_000 {
		return surface{}, fmt.Errorf("the HarmonyOS preview needs a full-screen system window: %w", driver.ErrUnsupported)
	}
	s := surface{id: selected[4], pid: selected[3], width: w, height: h, desktop: desktop}
	for _, row := range rows {
		if !visibleWindow(row) || number(row[5]) <= number(selected[5]) {
			continue
		}
		// The full-size desktop panel must belong to the same process as Home.
		if desktop && row[3] == s.pid && strings.TrimRight(row[1], "0123456789") == "SCBScenePanel" &&
			row[6] == "0" && row[7] == "0" && number(row[8]) == w && number(row[9]) == h {
			continue
		}
		if !sceneChrome(row, w, h) || (s.chromePID != "" && s.chromePID != row[3]) {
			return surface{}, fmt.Errorf("dismiss the window over HarmonyOS Settings or Home: %w", driver.ErrUnsupported)
		}
		s.chromePID = row[3]
		addChromeInset(&s, row)
	}
	return s, nil
}

// The owner mirror shows the physical display. Its pointer only needs a
// stable full-screen target and a checked transform, irrespective of the app.
func (d *device) focusedOwnerSurface(ctx context.Context) (surface, error) {
	out, err := d.shell(ctx, focusCommand)
	if err != nil {
		return surface{}, err
	}
	return parseOwnerSurface(string(out))
}

func parseOwnerSurface(report string) (surface, error) {
	rows, selected, err := primaryWindows(report)
	if err != nil {
		return surface{}, err
	}
	var s surface
	for _, row := range rows {
		w, h := number(row[8]), number(row[9])
		if !visibleWindow(row) || row[6] != "0" || row[7] != "0" ||
			w > 8192 || h > 8192 || w*h > 32_000_000 {
			continue
		}
		if strings.TrimRight(row[1], "0123456789") == "SCBGestureBack" {
			s.width, s.height = w, h
			break
		}
		if w*h > s.width*s.height {
			s.width, s.height = w, h
		}
	}
	if s.width == 0 {
		return s, nil // awake, but the display is between layouts
	}
	if selected != nil && visibleWindow(selected) && selected[6] == "0" && selected[7] == "0" &&
		number(selected[8]) == s.width && number(selected[9]) == s.height {
		s.id, s.pid = selected[4], selected[3]
	}
	for _, row := range rows {
		if visibleWindow(row) && sceneChrome(row, s.width, s.height) {
			addChromeInset(&s, row)
		}
	}
	return s, nil
}

func addChromeInset(s *surface, row []string) {
	switch strings.TrimRight(row[1], "0123456789") {
	case "SCBStatusBar", "SCBSceneTitleBar":
		s.top = max(s.top, number(row[9]))
	case "SCBGestureNavBar":
		s.bottom = max(s.bottom, number(row[9]))
	}
}

// These SceneBoard windows are the normal bars and transparent back-gesture
// layer on the MatePad. The agent read rejects other overlays.
func sceneChrome(row []string, w, h int) bool {
	name := strings.TrimRight(row[1], "0123456789")
	y, height := number(row[7]), number(row[9])
	switch name {
	case "SCBStatusBar", "SCBSceneTitleBar":
		return y == 0 && height <= h/8
	case "SCBGestureNavBar":
		return y+height == h && height <= h/8
	case "SCBGestureBack":
		return row[6] == "0" && y == 0 && number(row[8]) == w && height == h
	default:
		return false
	}
}

func visibleWindow(row []string) bool {
	return number(row[5]) >= 0 && number(row[8]) > 0 && number(row[9]) > 0
}

func number(value string) int {
	n, _ := strconv.Atoi(value)
	return n
}

func (d *device) Observe(ctx context.Context, _ driver.ObserveOptions) (*screen.Screen, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	swipetrace.Mark(ctx, "layout.lock")
	d.last, d.panes = surface{}, nil

	// The mirror is already checking the focused Settings window. Use a
	// recent check to select that exact window, then verify the live focus
	// again before accepting its layout. The swipe itself checks it again.
	s, ok := d.recentPreviewSurface()
	afterKey := d.needFocus
	d.needFocus = false
	if !ok || afterKey {
		deadline := time.Now().Add(2500 * time.Millisecond)
		var err error
		for {
			s, err = d.focusedSurface(ctx)
			// SceneBoard can put a transition layer above the new
			// foreground window. Recheck it without sending another key.
			if !afterKey || !errors.Is(err, driver.ErrUnsupported) || !time.Now().Before(deadline) {
				break
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}
		ok = false
		if err == driver.ErrLocked {
			return &screen.Screen{Locked: true, CapturedAt: time.Now()}, nil
		}
		if err != nil {
			return nil, err
		}
	}
	result := &screen.Screen{Width: s.width, Height: s.height, App: settings, Orientation: "portrait", CapturedAt: time.Now()}
	if s.width > s.height {
		result.Orientation = "landscape"
	}
	result.Mask = screen.UprightDisplayMask(d.info.Model, 0, s.width, s.height)
	// Report the verified Home surface without exposing widgets or app icons.
	if s.desktop {
		if ok {
			current, err := d.focusedSurface(ctx)
			if err == driver.ErrLocked {
				return &screen.Screen{Locked: true, CapturedAt: time.Now()}, nil
			}
			if err != nil {
				return nil, err
			}
			if current != s {
				return nil, fmt.Errorf("the HarmonyOS Home window changed during the read")
			}
		}
		result.App = home
		return result, nil
	}
	swipetrace.Mark(ctx, "layout.window")
	out, err := d.capture(ctx, "uitest dumpLayout -w "+s.id+" -p ", ".json", "cat", focusCommand)
	swipetrace.Mark(ctx, "layout.hdc")
	if err != nil {
		return nil, err
	}
	data, after, err := checkedCapture(out)
	if err == driver.ErrLocked {
		return &screen.Screen{Locked: true, CapturedAt: time.Now()}, nil
	}
	if err != nil {
		return nil, err
	}
	if after != s {
		return nil, fmt.Errorf("the HarmonyOS window changed during the read")
	}
	if len(data) > 8<<20 {
		return nil, fmt.Errorf("HarmonyOS layout is too large")
	}
	var layout node
	if err := json.Unmarshal(data, &layout); err != nil {
		return nil, fmt.Errorf("cannot decode HarmonyOS layout: %w", err)
	}
	result.CapturedAt = time.Now()
	addElements(layout, "", screen.Rect{W: s.width, H: s.height}, &result.Elements, &d.panes)
	d.last = s
	swipetrace.Mark(ctx, "layout.parsed")
	return result, nil
}

func checkedCapture(out []byte) ([]byte, surface, error) {
	payload, report, found := bytes.Cut(out, []byte("\r\nPINKY_WINDOW_STATE\r\n"))
	if !found {
		payload, report, found = bytes.Cut(out, []byte("\nPINKY_WINDOW_STATE\n"))
	}
	if !found {
		return nil, surface{}, fmt.Errorf("HarmonyOS did not return the capture window state")
	}
	s, err := verifyFocusReport(report)
	return payload, s, err
}

func (d *device) recentPreviewSurface() (surface, bool) {
	d.previewMu.Lock()
	preview := d.preview
	d.previewMu.Unlock()
	if preview == nil {
		return surface{}, false
	}
	preview.mu.Lock()
	defer preview.mu.Unlock()
	f := preview.frame
	return preview.checked, preview.err == nil && preview.checked.id != "" && time.Since(f.at) <= 850*time.Millisecond
}

type node struct {
	Attributes map[string]string `json:"attributes"`
	Children   []node            `json:"children"`
}

func addElements(n node, path string, display screen.Rect, elements *[]screen.Element, panes *[]screen.Rect) {
	a := n.Attributes
	kind := a["type"]
	if kind == "" {
		kind = a["class"]
	}
	label := strings.TrimSpace(a["text"])
	if label == "" {
		label = strings.TrimSpace(a["description"])
	}
	bounds := parseBounds(a["bounds"], display)
	var states []string
	for _, key := range []string{"focused", "selected", "checked", "scrollable", "editable", "password"} {
		if a[key] == "true" {
			states = append(states, key)
		}
	}
	if a["enabled"] == "false" {
		states = append(states, screen.StateDisabled)
	}
	role := screen.RoleOther
	switch {
	case a["scrollable"] == "true":
		role = screen.RoleList
		if !bounds.Empty() && a["enabled"] != "false" {
			*panes = append(*panes, bounds)
		}
	case strings.Contains(kind, "Toggle") || strings.Contains(kind, "Switch"):
		role = screen.RoleSwitch
	case strings.Contains(kind, "Checkbox"):
		role = screen.RoleCheckbox
	case strings.Contains(kind, "TextInput") || strings.Contains(kind, "TextArea"):
		role = screen.RoleInput
	case strings.Contains(kind, "Button") || a["clickable"] == "true":
		role = screen.RoleButton
	case strings.Contains(kind, "Image"):
		role = screen.RoleImage
	case label != "":
		role = screen.RoleText
	}
	if !bounds.Empty() && (label != "" || role == screen.RoleList || role == screen.RoleInput) {
		*elements = append(*elements, screen.Element{ID: a["key"], Path: path, Source: screen.SourceTree, Role: role,
			Bounds: bounds, Label: label, States: states})
	}
	for _, child := range n.Children {
		addElements(child, path+"/"+kind, display, elements, panes)
	}
}

func parseBounds(value string, display screen.Rect) screen.Rect {
	m := boundsPattern.FindStringSubmatch(value)
	if len(m) != 5 {
		return screen.Rect{}
	}
	x, y := max(0, number(m[1])), max(0, number(m[2]))
	right, bottom := min(display.W, number(m[3])), min(display.H, number(m[4]))
	return screen.Rect{X: x, Y: y, W: right - x, H: bottom - y}
}

// UiTest and snapshot_display write to the device's scratch directory.
// Read the result on the same connection and remove it on exit. The second
// cleanup only runs when HDC is interrupted and the shell may not have exited.
func (d *device) capture(ctx context.Context, prefix, ext, reader, after string) ([]byte, error) {
	remote := "/data/local/tmp/pinky-" + rand.Text() + ext
	cmd := "trap 'rm -f " + remote + "' EXIT; " + prefix + remote + " >/dev/null && " + reader + " " + remote
	if after != "" {
		cmd += "; printf '\nPINKY_WINDOW_STATE\n'; " + after
	}
	data, err := d.shell(ctx, cmd)
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		d.shell(cleanup, "rm -f "+remote)
	}
	return data, err
}

func (d *device) Swipe(ctx context.Context, from, to screen.Point, duration time.Duration) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	swipetrace.Mark(ctx, "input.focus_begin")
	s, err := d.focusedSettings(ctx)
	swipetrace.Mark(ctx, "input.focus_end")
	if err != nil {
		return err
	}
	if s != d.last || !inScrollPane(d.panes, from, to) {
		return fmt.Errorf("swipe vertically within a scrollable Settings pane: %w", driver.ErrUnsupported)
	}
	speed := 600
	if duration > 0 {
		speed = min(40000, max(200, int(float64(abs(from.Y-to.Y))/duration.Seconds())))
	}
	d.last, d.panes = surface{}, nil
	swipetrace.Mark(ctx, "input.send")
	_, err = d.shell(ctx, fmt.Sprintf("uitest uiInput swipe %d %d %d %d %d", from.X, from.Y, to.X, to.Y, speed))
	swipetrace.Mark(ctx, "input.return")
	return err
}

func inScrollPane(panes []screen.Rect, from, to screen.Point) bool {
	dy := abs(from.Y - to.Y)
	if dy < 80 || abs(from.X-to.X) > dy/4 {
		return false
	}
	for _, pane := range panes {
		pane.X += 18
		pane.Y += 18
		pane.W -= 36
		pane.H -= 36
		if pane.Contains(from) && pane.Contains(to) {
			return true
		}
	}
	return false
}

func abs(n int) int { return max(n, -n) }

var _ driver.TextReader = (*device)(nil)
