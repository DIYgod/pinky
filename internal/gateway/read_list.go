package gateway

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

const (
	readListOutputBytes = 16 << 10
	readListCursorLife  = 15 * time.Minute
	readListMinArea     = 4 // at least a quarter of the screen
	readListSwipeTime   = time.Second
)

// ReadList implements phone_read_list. It walks one vertical list in a
// single MCP call, collecting the text exposed by each settled viewport and
// merging adjacent overlaps. It returns one compact transcript for the agent
// to summarise, rather than one complete screen or image per swipe.
func (s *Session) ReadList(ctx context.Context, req ReadListRequest) *Result {
	req.Direction = strings.ToLower(strings.TrimSpace(req.Direction))
	directionGiven := req.Direction != ""
	if req.Direction == "" {
		req.Direction = "up"
	}
	if req.Direction != "up" && req.Direction != "down" {
		return errResult(ErrInvalidArgument, "direction must be up or down.")
	}
	if req.Ref > 0 && req.Area != nil {
		return errResult(ErrInvalidArgument, "pass ref or scroll_area, not both.")
	}
	if req.Area != nil && (req.Area.W < 100 || req.Area.H < 200) {
		return errResult(ErrInvalidArgument, "scroll_area must be at least 100 by 200 pixels.")
	}
	if req.MaxScreens == 0 {
		req.MaxScreens = 10
	}
	if req.MaxScreens < 1 || req.MaxScreens > MaxReadScreens {
		return errResult(ErrInvalidArgument, "max_screens must be between 1 and %d.", MaxReadScreens)
	}
	if req.MaxMS == 0 {
		req.MaxMS = 30000
	}
	if req.MaxMS < 1000 || req.MaxMS > int(MaxReadTime/time.Millisecond) {
		return errResult(ErrInvalidArgument, "max_ms must be between 1000 and %d.", MaxReadTime.Milliseconds())
	}
	if req.ScrollPercent == 0 {
		req.ScrollPercent = 60
	}
	if req.ScrollPercent < 30 || req.ScrollPercent > 80 {
		return errResult(ErrInvalidArgument, "scroll_percent must be between 30 and 80.")
	}
	switch req.OCR {
	case "":
		req.OCR = "auto"
	case "auto", "all", "off":
	default:
		return errResult(ErrInvalidArgument, "ocr must be auto, all or off.")
	}

	start := time.Now()
	l, res := s.acquire(req.Device)
	if res != nil {
		return res
	}
	defer s.done(l)
	g := s.g
	ctx, cancel := mergeCtx(ctx, l.ctx)
	defer cancel()
	if res := g.gate(l, "", true); res != nil {
		return g.finish(l, res)
	}
	g.readyScreen(ctx, l)

	d := l.dev
	d.act.Lock()
	defer d.act.Unlock()
	if res := g.gate(l, "", true); res != nil {
		return g.finish(l, res)
	}
	scan, result := g.readListLocked(ctx, l, req, start, directionGiven)
	result = g.finish(l, result)
	g.recordReadList(l, req, scan, result, start)
	return result
}

func (g *Gateway) readListLocked(ctx context.Context, l *lease, req ReadListRequest, start time.Time, directionGiven bool) (*ScanView, *Result) {
	view := &ScanView{}
	deadline := start.Add(time.Duration(req.MaxMS) * time.Millisecond)
	scanCtx, scanCancel := context.WithDeadline(ctx, deadline)
	defer scanCancel()
	var previous, previousRaw, previousFull, pinned []screen.ViewportLine
	var cursor readListCursor
	resume := req.Continuation != ""
	startScreen := 0
	if req.Continuation != "" {
		c, err := decodeReadListCursor(req.Continuation, l, req)
		if err != nil {
			return view, errResult(ErrInvalidArgument, "continuation is invalid or has expired.")
		}
		cursor = c
		if c.Direction != "up" && c.Direction != "down" {
			return view, errResult(ErrInvalidArgument, "continuation has an invalid direction.")
		}
		if directionGiven && req.Direction != c.Direction {
			return view, errResult(ErrInvalidArgument, "continuation direction does not match the request.")
		}
		req.Direction = c.Direction
		previous = viewportFromCursor(c.Previous)
		pinned = viewportFromCursorLines(c.Pinned)
		startScreen = c.Page
		if req.Ref == 0 {
			req.Ref = c.Ref
		}
		if req.Area == nil {
			req.Area = c.Area
		} else if !sameReadArea(req.Area, c.Area) {
			return view, errResult(ErrInvalidArgument, "continuation belongs to a different scroll_area.")
		}
		if req.Ref > 0 && req.Area != nil {
			return view, errResult(ErrInvalidArgument, "pass ref or scroll_area, not both.")
		}
	}
	view.StartScreen = startScreen

	firstPath := ""
	if resume && req.Area == nil {
		firstPath = cursor.List
	}
	first, list, res := g.readListScreen(scanCtx, l, req.OCR, req.Ref, firstPath, req.Area)
	if res != nil {
		if stopped := g.gate(l, "", true); stopped != nil {
			view.Stop = readListStop(stopped)
			return view, withScan(stopped, view)
		}
		if errors.Is(scanCtx.Err(), context.DeadlineExceeded) {
			view.Stop, view.Truncated = "deadline", true
			return view, readListResult(l, view)
		}
		view.Stop = readListStop(res)
		return view, withScan(res, view)
	}
	if res := g.gate(l, "", true); res != nil {
		view.Stop = readListStop(res)
		return view, withScan(res, view)
	}
	app := first.App
	g.markReadListSensitive(view, app)
	if list.Role == "" {
		return view, withScan(errResult(ErrNotFound,
			"No list was found on this screen. Pass ref with the list or scrollable element to read."), view)
	}
	listPath := list.Path
	if cursor.Task != "" {
		if cursor.App != app.ID || cursor.List != listPath {
			return view, withScan(errResult(ErrInvalidArgument,
				"The continuation belongs to a different app or list."), view)
		}
	}
	if req.Ref == 0 {
		req.Ref = list.Ref
	}

	currentRaw := screen.CollectListViewport(first, list)
	current := currentRaw
	anchors := append(append([]screen.ViewportLine(nil), pinned...), previousRaw...)
	if len(anchors) > 0 {
		current = screen.FilterPinned(anchors, currentRaw)
	}
	fresh, overlap := appendFirstViewport(previous, current, req.Direction)
	if !resume {
		view.Pages++
	} else if len(fresh) > 0 {
		view.Pages++
	}
	view.Overlap += overlap
	pageNumber := startScreen + view.Pages
	if pageNumber == 0 {
		pageNumber = 1
	}
	if !appendScanLines(view, pageNumber, fresh, req.Direction) {
		if view.Pages > 0 {
			view.Pages--
		}
		view.Stop = "output_limit"
		view.Truncated = true
		if view.Lines == 0 {
			view.Stop = "page_too_large"
		}
	} else {
		previous = current
		previousRaw = currentRaw
		previousFull = collectFullViewport(first)
	}
	if req.Progress != nil {
		req.Progress(float64(time.Since(start).Seconds()),
			float64(req.MaxMS)/1000, fmt.Sprintf("Read %d screen%s", view.Pages, plural(view.Pages)))
	}
	if view.Stop == "" && screen.ViewportEndsAt(current) {
		view.Stop = "end"
		view.Complete = !view.Truncated
		return view, readListResult(l, view)
	}

	noNew := 0
	for view.Pages < req.MaxScreens && view.Stop == "" {
		if time.Now().After(deadline) {
			view.Stop, view.Truncated = "deadline", true
			break
		}
		if res := g.gate(l, "", true); res != nil {
			view.Stop = readListStop(res)
			return view, withScan(res, view)
		}
		from, to, v, _, res := chooseListSwipe(scanCtx, g, l, first, list, req.Direction, req.ScrollPercent)
		if res != nil {
			view.Stop = readListStop(res)
			return view, withScan(res, view)
		}
		if v.Decision != policy.Allow {
			res := statusResult(StatusBlocked,
				"The scan stopped before scrolling because the gesture could begin on a protected control. Nothing was done for that screen.")
			res.Rule = v.Rule
			res.Message = "The scan stopped before scrolling because the gesture could trigger a protected control. Nothing was done for that screen."
			view.Stop = readListStop(res)
			return view, withScan(res, view)
		}

		if res := g.preflight(scanCtx, l); res != nil {
			if errors.Is(scanCtx.Err(), context.DeadlineExceeded) {
				view.Stop, view.Truncated = "deadline", true
				break
			}
			view.Stop = readListStop(res)
			return view, withScan(res, view)
		}
		actx, cancel := context.WithCancel(scanCtx)
		g.mu.Lock()
		dev, _ := g.screenOfLocked(l.dev)
		l.dev.inflight = cancel
		g.inputLocked(l.dev)
		g.mu.Unlock()
		if dev == nil {
			cancel()
			g.mu.Lock()
			l.dev.inflight = nil
			g.mu.Unlock()
			view.Stop = "offline"
			return view, withScan(errResult(ErrDeviceOffline, "%s is offline.", l.dev.name()), view)
		}
		// A deliberate drag keeps the next viewport overlapping this one. A
		// short fling can carry several pages, especially with browser inertia,
		// leaving no text that proves the two captures are adjacent.
		err := dev.Swipe(actx, from, to, readListSwipeTime)
		g.mu.Lock()
		l.dev.inflight = nil
		g.mu.Unlock()
		cancel()
		if err != nil {
			if stopped := g.gate(l, "", true); stopped != nil {
				view.Stop = readListStop(stopped)
				return view, withScan(stopped, view)
			}
			if scanCtx.Err() != nil {
				view.Stop, view.Truncated = "deadline_uncertain", true
				break
			}
			view.Stop = "swipe_failed"
			return view, withScan(g.actionError(l, err), view)
		}
		view.Swipes++
		settled := g.settle(scanCtx, l.dev)
		if res := g.gate(l, "", true); res != nil {
			view.Stop = readListStop(res)
			return view, withScan(res, view)
		}
		next, nextList, res := g.readListScreen(scanCtx, l, req.OCR, req.Ref, listPath, req.Area)
		if res != nil {
			if stopped := g.gate(l, "", true); stopped != nil {
				view.Stop = readListStop(stopped)
				return view, withScan(stopped, view)
			}
			if errors.Is(scanCtx.Err(), context.DeadlineExceeded) || time.Now().After(deadline) {
				view.Pages++
				view.Stop, view.Truncated = "deadline", true
				break
			}
			view.Stop = readListStop(res)
			return view, withScan(res, view)
		}
		if res := g.gate(l, "", true); res != nil {
			view.Stop = readListStop(res)
			return view, withScan(res, view)
		}
		if next.Locked {
			view.Stop = "locked"
			return view, withScan(statusResult(StatusLocked,
				"The phone locked while reading the list. The pages above were returned."), view)
		}
		if readListAppKnown(app) && readListAppKnown(next.App) && !sameApp(app, next.App) {
			view.Stop = "app_changed"
			view.Truncated = true
			break
		}
		g.markReadListSensitive(view, next.App)
		if nextList.Role == "" {
			view.Stop = "list_changed"
			view.Truncated = true
			break
		}
		if req.Area == nil && nextList.Path != "" && listPath != "" && nextList.Path != listPath {
			view.Stop = "list_changed"
			view.Truncated = true
			break
		}
		view.Pages++
		pageNumber = startScreen + view.Pages
		list = nextList
		currentRaw = screen.CollectListViewport(next, list)
		nextFull := collectFullViewport(next)
		pinned = mergePinnedLines(pinned, screen.PinnedLines(previousFull, nextFull))
		if !settled {
			// A live video or decorative animation can keep the pixels moving
			// after the text is final. Confirm that the list content itself is
			// stable before accepting this viewport.
			for attempt := 0; attempt < 2 && !settled; attempt++ {
				if !sleepCtx(scanCtx, g.opt.PollInterval, nil) {
					view.Stop, view.Truncated = "deadline", true
					break
				}
				retry, retryList, retryRes := g.readListScreen(scanCtx, l, req.OCR, req.Ref, listPath, req.Area)
				if retryRes != nil {
					if stopped := g.gate(l, "", true); stopped != nil {
						view.Stop = readListStop(stopped)
						return view, withScan(stopped, view)
					}
					if errors.Is(scanCtx.Err(), context.DeadlineExceeded) || time.Now().After(deadline) {
						view.Stop, view.Truncated = "deadline", true
						break
					}
					view.Stop = readListStop(retryRes)
					return view, withScan(retryRes, view)
				}
				if res := g.gate(l, "", true); res != nil {
					view.Stop = readListStop(res)
					return view, withScan(res, view)
				}
				if retry.Locked {
					view.Stop = "locked"
					return view, withScan(statusResult(StatusLocked,
						"The phone locked while confirming the list. The pages above were returned."), view)
				}
				if readListAppKnown(app) && readListAppKnown(retry.App) && !sameApp(app, retry.App) {
					view.Stop = "app_changed"
					view.Truncated = true
					break
				}
				g.markReadListSensitive(view, retry.App)
				if retryList.Role == "" {
					view.Stop = "list_changed"
					view.Truncated = true
					break
				}
				if req.Area == nil && retryList.Path != "" && listPath != "" && retryList.Path != listPath {
					view.Stop = "list_changed"
					view.Truncated = true
					break
				}
				retryRaw := screen.CollectListViewport(retry, retryList)
				settled = screen.SameViewport(currentRaw, retryRaw)
				currentRaw, list = retryRaw, retryList
			}
		}
		if view.Stop != "" {
			break
		}
		current = currentRaw
		anchors = append(append([]screen.ViewportLine(nil), pinned...), previousRaw...)
		if len(anchors) > 0 {
			current = screen.FilterPinned(anchors, currentRaw)
		}
		if req.Direction == "down" {
			fresh, overlap = screen.AppendViewportBackward(previous, current)
		} else {
			fresh, overlap = screen.AppendViewport(previous, current)
		}
		gapBetween := len(previous) > 0 && len(current) > 0 &&
			(overlap == 0 || (req.Direction != "down" && screen.UnseenPrefix(previous, current)))
		if gapBetween && overlap > 0 && screen.ViewportEndsAt(current) {
			fresh = screen.NewViewportContent(previous, current)
		} else if gapBetween {
			view.Pages--
			view.Stop, view.Truncated = "gap", true
			break
		}
		view.Overlap += overlap
		if !appendScanLines(view, pageNumber, fresh, req.Direction) {
			view.Pages--
			view.Stop, view.Truncated = "output_limit", true
			break
		}
		if len(fresh) == 0 {
			noNew++
		} else {
			noNew = 0
		}
		previous = current
		previousRaw = currentRaw
		previousFull = nextFull
		if !settled {
			view.Stop, view.Truncated = "unsettled", true
			break
		}
		if req.Progress != nil {
			req.Progress(float64(time.Since(start).Seconds()),
				float64(req.MaxMS)/1000, fmt.Sprintf("Read %d screen%s", view.Pages, plural(view.Pages)))
		}
		if screen.ViewportEndsAt(current) {
			view.Stop = "end"
			view.Complete = !view.Truncated
			break
		}
		if noNew >= 2 {
			view.Stop, view.Truncated = "no_new_content", true
			break
		}
		if view.Pages >= req.MaxScreens {
			view.Stop, view.Truncated = "max_screens", true
			break
		}
		if time.Now().After(deadline) {
			view.Stop, view.Truncated = "deadline", true
			break
		}
		first = next
	}
	if view.Stop == "" {
		view.Stop = "max_screens"
	}
	if readListCanContinue(view.Stop) {
		view.Truncated = true
		view.Continuation = encodeReadListCursor(l, first.App.ID, listPath, req.Direction, req.Ref, req.Area,
			startScreen+view.Pages, previous, pinned)
	}
	return view, readListResult(l, view)
}

func (g *Gateway) readListScreen(ctx context.Context, l *lease, ocr string, ref int, listPath string, area *screen.Rect) (*screen.Screen, screen.Element, *Result) {
	if res := g.gate(l, "", false); res != nil {
		return nil, screen.Element{}, res
	}
	mode := ocrOff
	if ocr == "all" {
		mode = ocrAll
	}
	raw, err := g.observeOCR(ctx, l.dev, mode)
	if err != nil {
		return nil, screen.Element{}, g.readError(l, err)
	}
	if raw.Locked {
		return nil, screen.Element{}, statusResult(StatusLocked,
			"The phone is locked. Call phone_handoff so the person can unlock it.")
	}
	view, rule := g.agentView(l, raw)
	if rule != "" {
		return nil, screen.Element{}, &Result{Status: StatusBlocked, Rule: rule,
			Message: "The foreground app is blocked by the person's rules, so the list is hidden."}
	}
	list, res := listForScreen(view, ref, listPath, area)
	if res != nil {
		return nil, screen.Element{}, res
	}
	if ocr == "auto" && (list.Role == "" || viewportNeedsOCR(view, list)) {
		raw, err = g.observeOCR(ctx, l.dev, ocrAuto)
		if err != nil {
			return nil, screen.Element{}, g.readError(l, err)
		}
		if raw.Locked {
			return nil, screen.Element{}, statusResult(StatusLocked,
				"The phone is locked. Call phone_handoff so the person can unlock it.")
		}
		view, rule = g.agentView(l, raw)
		if rule != "" {
			return nil, screen.Element{}, &Result{Status: StatusBlocked, Rule: rule,
				Message: "The foreground app is blocked by the person's rules, so the list is hidden."}
		}
		list, res = listForScreen(view, ref, listPath, area)
		if res != nil {
			return nil, screen.Element{}, res
		}
	}
	return view, list, nil
}

func listForScreen(s *screen.Screen, ref int, listPath string, area *screen.Rect) (screen.Element, *Result) {
	if area != nil {
		if area.X < 0 || area.Y < 0 || area.W < 100 || area.H < 200 ||
			area.X >= s.Width || area.Y >= s.Height ||
			area.W > s.Width-area.X || area.H > s.Height-area.Y {
			return screen.Element{}, errResult(ErrInvalidArgument,
				"scroll_area must be at least 100 by 200 pixels and lie inside the %dx%d screen.", s.Width, s.Height)
		}
		return screen.Element{Role: screen.RoleList, Bounds: *area, Path: "pinky:scroll-area"}, nil
	}
	list := firstList(s, ref)
	if listPath != "" && (list.Role == "" || list.Path != listPath) {
		for _, e := range s.Elements {
			if e.Path == listPath && readableList(e) {
				list = e
				break
			}
		}
	}
	return list, nil
}

func firstList(s *screen.Screen, ref int) screen.Element {
	if s == nil {
		return screen.Element{}
	}
	if ref > 0 {
		if e, ok := s.Find(ref); ok {
			if readableList(e) {
				return e
			}
			return screen.Element{}
		}
		return screen.Element{}
	}
	var candidates []screen.Element
	for _, e := range s.Elements {
		if readableList(e) {
			candidates = append(candidates, e)
		}
	}
	if len(candidates) == 0 {
		return screen.Element{}
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i].Bounds, candidates[j].Bounds
		return a.W*a.H > b.W*b.H
	})
	largest := candidates[0]
	if largest.Bounds.W*largest.Bounds.H*readListMinArea < s.Width*s.Height {
		return screen.Element{}
	}
	return largest
}

func readableList(e screen.Element) bool {
	if e.Bounds.Empty() {
		return false
	}
	return e.Role == screen.RoleList || e.Has(screen.StateScrollable)
}

func viewportNeedsOCR(s *screen.Screen, list screen.Element) bool {
	area := list.Bounds
	if s == nil || area.Empty() {
		return false
	}
	lines, chars, unlabelled := 0, 0, 0
	for _, e := range s.Elements {
		if list.Ref != 0 && e.Ref == list.Ref {
			continue
		}
		if e.Bounds.Empty() || !area.Contains(e.Bounds.Center()) {
			continue
		}
		if strings.TrimSpace(e.Label) != "" {
			lines++
			chars += utf8.RuneCountInString(e.Label)
		} else if e.Bounds.W*e.Bounds.H*8 >= area.W*area.H {
			unlabelled++
		}
	}
	return lines < 3 || chars < 24 || unlabelled >= 2
}

func appendFirstViewport(previous, current []screen.ViewportLine, direction string) ([]screen.ViewportLine, int) {
	if len(previous) == 0 {
		return current, 0
	}
	if direction == "down" {
		return screen.AppendViewportBackward(previous, current)
	}
	return screen.AppendViewport(previous, current)
}

func collectFullViewport(s *screen.Screen) []screen.ViewportLine {
	if s == nil {
		return nil
	}
	return screen.CollectViewport(s, screen.Rect{X: 0, Y: 0, W: s.Width, H: s.Height})
}

func mergePinnedLines(existing, found []screen.ViewportLine) []screen.ViewportLine {
	out := append([]screen.ViewportLine(nil), existing...)
	for _, line := range found {
		duplicate := false
		for _, old := range out {
			if samePinnedLine(old, line) {
				duplicate = true
				break
			}
		}
		if !duplicate {
			out = append(out, line)
		}
	}
	if len(out) > 80 {
		out = out[len(out)-80:]
	}
	return out
}

func samePinnedLine(a, b screen.ViewportLine) bool {
	if screen.ViewportKeys([]screen.ViewportLine{a})[0] != screen.ViewportKeys([]screen.ViewportLine{b})[0] {
		return false
	}
	const tolerance = 4
	abs := func(n int) int {
		if n < 0 {
			return -n
		}
		return n
	}
	return abs(a.Bounds.X-b.Bounds.X) <= tolerance && abs(a.Bounds.Y-b.Bounds.Y) <= tolerance &&
		abs(a.Bounds.W-b.Bounds.W) <= tolerance && abs(a.Bounds.H-b.Bounds.H) <= tolerance
}

func appendScanLines(view *ScanView, page int, lines []screen.ViewportLine, direction string) bool {
	if len(lines) == 0 {
		return true
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		text := strings.TrimSpace(line.Text)
		if text == "" {
			continue
		}
		var clipped bool
		text, clipped = truncateRunesMarked(text, 1000)
		if clipped {
			view.Truncated = true
		}
		if view.OutputBytes+len([]byte(text))+3 > readListOutputBytes {
			return false
		}
		out = append(out, text)
		view.OutputBytes += len([]byte(text)) + 3
	}
	if len(out) > 0 {
		section := ScanPage{Number: page, Lines: out}
		if direction == "down" {
			view.Sections = append([]ScanPage{section}, view.Sections...)
		} else {
			view.Sections = append(view.Sections, section)
		}
		view.Lines += len(out)
	}
	return true
}

func readListResult(l *lease, view *ScanView) *Result {
	res := &Result{Status: StatusSettled, Device: l.dev.id, DeviceName: l.dev.name(), Scan: view}
	state := "Read"
	if view.Truncated {
		state = "Read part of"
	}
	res.Message = fmt.Sprintf("%s the list: %d screen%s, %d new line%s, %d overlapping line%s; stopped at %s.",
		state, view.Pages, plural(view.Pages), view.Lines, plural(view.Lines),
		view.Overlap, plural(view.Overlap), strings.ReplaceAll(view.Stop, "_", " "))
	if view.Truncated && view.Continuation != "" {
		res.Message += " Use continuation to read more from the current position."
	}
	return res
}

func readListAppKnown(app screen.App) bool {
	return app.ID != "" || app.Name != ""
}

func (g *Gateway) markReadListSensitive(view *ScanView, app screen.App) {
	if _, blocked := g.policy.Blocked(app); blocked {
		view.HistoryRedacted = true
	}
}

func withScan(res *Result, view *ScanView) *Result {
	if view.Pages > 0 && (res.Error != "" || res.Status != StatusSettled) {
		view.Truncated = true
	}
	res.Scan = view
	return res
}

func readListStop(res *Result) string {
	switch {
	case res.Status == StatusStopped:
		return "stopped"
	case res.Status == StatusTookOver || res.Status == StatusPaused:
		return "took_over"
	case res.Status == StatusBlocked:
		return "blocked"
	case res.Status == StatusLocked:
		return "locked"
	case res.Status == StatusUncertain:
		return "unstable"
	case res.Error == ErrDeviceOffline:
		return "offline"
	default:
		return "failed"
	}
}

func chooseListSwipe(ctx context.Context, g *Gateway, l *lease, s *screen.Screen, list screen.Element, direction string, percent int) (screen.Point, screen.Point, policy.Verdict, *prep, *Result) {
	area := list.Bounds
	distance := area.H * percent / 100
	if distance < 80 {
		distance = 80
	}
	xs := []int{area.X + area.W*85/100, area.X + area.W*15/100, area.X + area.W*75/100, area.X + area.W*25/100, area.Center().X}
	var firstV policy.Verdict
	var firstP *prep
	var firstErr *Result
	for _, x := range xs {
		x = max(area.X+1, min(x, area.X+area.W-2))
		from := screen.Point{X: x, Y: area.Y + area.H/2 + distance/2}
		to := screen.Point{X: x, Y: area.Y + area.H/2 - distance/2}
		if direction == "down" {
			from, to = to, from
		}
		a := Action{Tool: "swipe", From: &from, To: &to, DurationMS: 300}
		p, res := g.prepare(ctx, l, a, s)
		if res != nil {
			if firstErr == nil {
				firstErr = res
			}
			continue
		}
		v := g.evaluate(p, s)
		if firstP == nil {
			firstP, firstV = p, v
		}
		if v.Decision == policy.Allow {
			if swipeNeedsOCR(s, list, from) {
				if !g.readsText(l.dev) {
					if firstErr == nil {
						firstErr = errResult(ErrUnsupported,
							"The swipe start may contain controls visible only as pixels, but this computer has no OCR. The list was not read.")
					}
					continue
				}
				ocr, err := g.observeOCR(ctx, l.dev, ocrAll)
				if err != nil {
					if firstErr == nil {
						firstErr = g.readError(l, err)
					}
					continue
				}
				if ocr.Locked {
					return screen.Point{}, screen.Point{}, v, p, statusResult(StatusLocked,
						"The phone locked while checking the swipe target.")
				}
				if s.App.ID != "" && ocr.App.ID != "" && ocr.App.ID != s.App.ID {
					return screen.Point{}, screen.Point{}, v, p, statusResult(StatusUncertain,
						"The app changed while checking the swipe target. No swipe was sent.")
				}
				collectOCRAt(p, ocr, from)
				v = g.evaluate(p, ocr)
			}
			if v.Decision == policy.Ask && g.granted(l, v, p) {
				return from, to, policy.Verdict{Decision: policy.Allow, Kinds: v.Kinds, Exempted: v.Exempted}, p, nil
			}
			return from, to, v, p, nil
		}
	}
	if firstErr != nil && firstP == nil {
		return screen.Point{}, screen.Point{}, firstV, &prep{}, firstErr
	}
	if firstP == nil {
		firstP = &prep{}
	}
	return screen.Point{}, screen.Point{}, firstV, firstP, nil
}

func swipeNeedsOCR(s *screen.Screen, list screen.Element, p screen.Point) bool {
	covered := false
	for _, e := range s.Elements {
		if e.Ref == list.Ref || e.Source == screen.SourceOCR || e.Bounds.Empty() || !e.Bounds.Contains(p) {
			continue
		}
		covered = true
		if strings.TrimSpace(e.Label) == "" || e.Bounds.W*e.Bounds.H*6 >= s.Width*s.Height {
			return true
		}
	}
	// A blank point can still contain a control drawn only in pixels. If the
	// element tree names nothing under it, ask OCR before sending the swipe.
	return !covered
}

func collectOCRAt(p *prep, s *screen.Screen, point screen.Point) {
	p.ocr = nil
	for _, e := range s.Elements {
		if e.Source == screen.SourceOCR && e.Label != "" && !ocrInStatusBar(s, e) && ocrReach(e.Bounds).Contains(point) {
			p.ocr = append(p.ocr, e)
		}
	}
}

type readListCursor struct {
	Version   int          `json:"v"`
	Device    string       `json:"d"`
	Task      string       `json:"t"`
	App       string       `json:"a"`
	List      string       `json:"l"`
	Page      int          `json:"p"`
	Ref       int          `json:"n"`
	Area      *screen.Rect `json:"b,omitempty"`
	Direction string       `json:"r"`
	Previous  []string     `json:"s"`
	Pinned    []cursorLine `json:"i,omitempty"`
	Expires   int64        `json:"e"`
}

type cursorLine struct {
	Key    string      `json:"k"`
	Bounds screen.Rect `json:"b"`
}

func encodeReadListCursor(l *lease, app, list, direction string, ref int, area *screen.Rect,
	page int, previous, pinned []screen.ViewportLine) string {
	c := readListCursor{Version: 1, Device: l.dev.id, Task: l.task.id, App: app, List: list,
		Page: page, Ref: ref, Area: area, Direction: direction,
		Previous: viewportKeyTail(previous), Pinned: cursorLineTail(pinned, 20),
		Expires: time.Now().Add(readListCursorLife).Unix()}
	b, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(b)
}

func viewportKeyTail(lines []screen.ViewportLine) []string {
	start := max(0, len(lines)-80)
	return screen.ViewportKeys(lines[start:])
}

func cursorLineTail(lines []screen.ViewportLine, limit int) []cursorLine {
	start := max(0, len(lines)-limit)
	keys := screen.ViewportKeys(lines[start:])
	out := make([]cursorLine, 0, len(keys))
	for i, key := range keys {
		out = append(out, cursorLine{Key: key, Bounds: lines[start+i].Bounds})
	}
	return out
}

func decodeReadListCursor(raw string, l *lease, req ReadListRequest) (readListCursor, error) {
	if len(raw) > 8192 {
		return readListCursor{}, errors.New("continuation is too long")
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return readListCursor{}, err
	}
	var c readListCursor
	if err := json.Unmarshal(b, &c); err != nil {
		return readListCursor{}, err
	}
	if c.Version != 1 || c.Device != l.dev.id || c.Task != l.task.id ||
		c.Page < 0 || c.Page > 1_000_000 || len(c.Previous) > 80 ||
		len(c.Pinned) > 20 || c.Expires < time.Now().Unix() {
		return readListCursor{}, errors.New("expired or for another task")
	}
	validKey := func(key string) bool {
		if len(key) != 16 {
			return false
		}
		for _, r := range key {
			if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
				return false
			}
		}
		return true
	}
	for _, key := range c.Previous {
		if !validKey(key) {
			return readListCursor{}, errors.New("invalid viewport key")
		}
	}
	for _, line := range c.Pinned {
		if !validKey(line.Key) || line.Bounds.W < 0 || line.Bounds.H < 0 ||
			line.Bounds.X < -1_000_000 || line.Bounds.Y < -1_000_000 ||
			line.Bounds.W > 1_000_000 || line.Bounds.H > 1_000_000 {
			return readListCursor{}, errors.New("invalid pinned line")
		}
	}
	return c, nil
}

func viewportFromCursor(keys []string) []screen.ViewportLine {
	out := make([]screen.ViewportLine, 0, len(keys))
	for _, key := range keys {
		if key != "" {
			out = append(out, screen.ViewportLineFromKey(key))
		}
	}
	return out
}

func viewportFromCursorLines(lines []cursorLine) []screen.ViewportLine {
	out := make([]screen.ViewportLine, 0, len(lines))
	for _, line := range lines {
		out = append(out, screen.ViewportLineFromKeyAt(line.Key, line.Bounds))
	}
	return out
}

func sameReadArea(a, b *screen.Rect) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func truncateRunesMarked(s string, max int) (string, bool) {
	if utf8.RuneCountInString(s) <= max {
		return s, false
	}
	r := []rune(s)
	return string(r[:max-1]) + "…", true
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func readListCanContinue(stop string) bool {
	switch stop {
	case "deadline", "max_screens", "output_limit", "unsettled":
		return true
	}
	return false
}

func (g *Gateway) recordReadList(l *lease, req ReadListRequest, scan *ScanView, res *Result, start time.Time) {
	args := map[string]any{"ref": req.Ref, "direction": req.Direction, "max_screens": req.MaxScreens,
		"max_ms": req.MaxMS, "ocr": req.OCR, "scroll_percent": req.ScrollPercent}
	if req.Area != nil {
		args["scroll_area"] = map[string]int{"x": req.Area.X, "y": req.Area.Y, "width": req.Area.W, "height": req.Area.H}
	}
	if req.Continuation != "" {
		args["continuation"] = true
	}
	text := res.Message
	if text == "" {
		text = fmt.Sprintf("Read %d screens from a list.", scan.Pages)
	}
	status := string(res.Status)
	if res.Error != "" {
		status = string(res.Error)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	screenText := scanText(scan)
	if scan.HistoryRedacted {
		screenText = ""
	}
	g.addStepLocked(l.task, StepView{Tool: "phone_read_list", Args: argsJSON(args), Text: text,
		Status: status, At: start, Elapsed: time.Since(start)}, req.Intent, screenText)
}

func scanText(scan *ScanView) string {
	var b strings.Builder
	for _, page := range scan.Sections {
		fmt.Fprintf(&b, "screen %d\n", page.Number)
		for _, line := range page.Lines {
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
