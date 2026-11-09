package gateway

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/approval"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// WatchRequest is phone_watch: phone_wait_for for things that take
// minutes, such as a delivery, a download or a reply.
type WatchRequest struct {
	Call
	Text    string
	Gone    bool
	Minutes int
}

// MaxWatchMinutes is the longest phone_watch. A watch holds the task, and
// its lease does not expire while it runs (inflight), so the person sees
// the agent waiting and other agents cannot take the phone meanwhile.
const MaxWatchMinutes = 60

// How phone_watch paces itself; variables for tests.
var (
	watchMinute  = time.Minute
	defaultWatch = 10
	// watchReadEvery is how often a watch reads the screen, besides the
	// changes the device reports (its wake channel). Slower than
	// phone_wait_for: a watch runs for many minutes, and every read costs
	// the phone a tree dump.
	watchReadEvery = 2 * time.Second
	// watchOCREvery is how often a watch also reads with OCR, for text
	// only the pixels show.
	watchOCREvery = 15 * time.Second
	// watchProgressEvery is how often a watch sends progress, which keeps
	// clients from timing out a call that says nothing (Claude Code drops
	// an MCP call that is silent for 30 minutes over stdio, 5 over HTTP).
	watchProgressEvery = 15 * time.Second
)

// Watch implements phone_watch. It waits like WaitFor, for up to an hour,
// under the task's lease:
//
//   - The phone is woken once, as every call does; after that the phone's
//     own screen timeout and lock apply (wake.go). A locked phone is read
//     as it is: its lock screen may show the notification waited for, and
//     a locked screen never counts as the text being gone.
//   - While the person has the phone (Take over, or their own input on
//     it), the watch does not read the screen and waits for them to hand
//     back. Handing back with a note ends the watch with the note.
//   - A phone that goes offline is waited for: its lease ends after
//     ReconnectGrace, which ends the watch.
//   - Stop, phone_release, the session closing or the client cancelling
//     the call end it.
//
// The person sees a step "Watching for “text”" while it runs, which
// becomes "Waited for “text”" when it ends.
func (s *Session) Watch(ctx context.Context, req WatchRequest) *Result {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return errResult(ErrInvalidArgument, "text is required.")
	}
	minutes := req.Minutes
	switch {
	case minutes < 0 || minutes > MaxWatchMinutes:
		return errResult(ErrInvalidArgument, "minutes must be between 0 and %d.", MaxWatchMinutes)
	case minutes == 0:
		minutes = defaultWatch
	}
	limit := time.Duration(minutes) * watchMinute
	l, res := s.acquire(req.Device)
	if res != nil {
		return res
	}
	defer s.done(l)
	g := s.g
	ctx, cancel := mergeCtx(ctx, l.ctx)
	defer cancel()
	if res := g.gate(l, req.ApprovalID, false); res != nil {
		return g.finish(l, res)
	}
	g.readyScreen(ctx, l)
	start := time.Now()
	args := argsJSON(map[string]any{"text": text, "gone": req.Gone, "minutes": req.Minutes})
	step := g.addStep(l, StepView{Tool: "phone_watch", Args: args, Text: fmt.Sprintf("Watching for “%s”", text),
		Status: "running", At: start})
	w := watcher{g: g, l: l, text: text, gone: req.Gone}
	res = w.run(ctx, start.Add(limit), func(msg string) {
		if req.Progress != nil {
			req.Progress(time.Since(start).Seconds(), limit.Seconds(), msg)
		}
	})
	if res == nil {
		res = w.result(start, limit)
	}
	status := string(StatusSettled)
	g.mu.Lock()
	if l.ended && l.endedBy == OutcomeStopped {
		status = string(StatusStopped)
	}
	g.updateStepLocked(l.task, StepView{Index: step, Tool: "phone_watch", Args: args, Text: fmt.Sprintf("Waited for “%s”", text),
		Status: status, At: start, Elapsed: time.Since(start)}, "", "")
	g.mu.Unlock()
	return g.finish(l, res)
}

// watcher is one phone_watch call.
type watcher struct {
	g    *Gateway
	l    *lease
	text string
	gone bool

	cur      *screen.Screen // the last screen read
	matched  bool
	tookOver bool   // the person had the phone during the watch
	person   bool   // the person has the phone now
	locked   bool   // the phone was locked at the last read
	offline  bool   // the last read found the phone offline
	note     string // the person's note when they handed back
	lastOCR  time.Time
	ocrFound bool
}

// run waits until the text appears (or is gone), the deadline passes, the
// person hands back with a note, or ctx ends. It returns a result only
// when the watch fails; otherwise the watcher holds what it saw.
func (w *watcher) run(ctx context.Context, deadline time.Time, progress func(string)) *Result {
	g, l := w.g, w.l
	lastProgress := time.Now()
	for {
		g.mu.Lock()
		w.person = l.dev.person
		wake := l.dev.wake
		if !w.person && w.tookOver && l.handedBack && l.handBackNote != "" {
			// The person handed back with something to say: the agent
			// hears it now rather than when the watch ends.
			w.note = l.handBackNote
			l.handedBack, l.handBackNote = false, ""
		}
		g.mu.Unlock()
		if w.note != "" {
			w.read(ctx)
			return nil
		}
		if w.person {
			// The person uses the phone: Pinky does not read it meanwhile.
			w.tookOver = true
		} else if res := w.read(ctx); res != nil {
			return res
		} else if w.matched {
			return nil
		}
		if !time.Now().Before(deadline) {
			return nil
		}
		if time.Since(lastProgress) >= watchProgressEvery {
			lastProgress = time.Now()
			switch {
			case w.person:
				progress("Paused while the person has the phone")
			case w.offline:
				progress("The phone is offline; waiting for it to come back")
			case w.locked:
				progress(fmt.Sprintf("The phone is locked; watching its lock screen for “%s”", w.text))
			default:
				progress(fmt.Sprintf("Watching for “%s”", w.text))
			}
		}
		every := watchReadEvery
		if w.person {
			every = watchProgressEvery // until they hand back, which wakes the loop
		}
		if !sleepCtx(ctx, every, wake) && ctx.Err() != nil {
			return w.cancelled()
		}
	}
}

// read reads the screen once and checks it for the text. It returns a
// result when the read failed in a way that ends the watch.
func (w *watcher) read(ctx context.Context) *Result {
	g, l := w.g, w.l
	cur, err := g.observe(ctx, l.dev, false)
	w.offline = errors.Is(err, driver.ErrOffline)
	switch {
	case errors.Is(err, driver.ErrLocked):
		// A phone left alone locks: wait for the person to unlock it.
		w.locked = true
		return nil
	case w.offline:
		// observe marked it offline; its lease survives a while.
		return nil
	case err != nil && ctx.Err() != nil:
		return w.cancelled()
	case err != nil:
		return g.readError(l, err)
	}
	w.cur, w.locked = cur, cur.Locked
	view, _ := g.agentView(l, cur)
	g.mu.Lock()
	evs := append([]screen.Event(nil), l.dev.events...)
	g.mu.Unlock()
	found := screenHas(view, evs, w.text)
	// Text that only the pixels show (a web view, a canvas): read with OCR
	// now and then too. Never on a locked phone, whose lock screen can show
	// any app's notifications (mayCapture).
	if !found && !cur.Locked && time.Since(w.lastOCR) >= watchOCREvery {
		if o, err := g.observeOCR(ctx, l.dev, ocrAll); err == nil {
			w.lastOCR = time.Now()
			ov, _ := g.agentView(l, o)
			w.ocrFound = screenHas(ov, evs, w.text)
			if w.ocrFound || w.gone {
				w.cur = o
			}
		}
	}
	found = found || (w.ocrFound && !cur.Locked)
	// A locked screen hides the app, so its text is not gone.
	if found != w.gone && !(w.gone && cur.Locked) {
		w.matched = true
	}
	return nil
}

func (w *watcher) cancelled() *Result {
	return errResult(ErrInternal, "The watch ended before “%s” %s: the call was cancelled or the task ended.", w.text, w.verb())
}

func (w *watcher) verb() string {
	if w.gone {
		return "disappeared"
	}
	return "appeared"
}

// result is the agent's answer when the watch ran to its end.
func (w *watcher) result(start time.Time, limit time.Duration) *Result {
	g, l := w.g, w.l
	var res *Result
	switch {
	case w.person:
		res = statusResult(StatusPaused, "The person has the phone; call phone_handoff to wait for them to hand it back.")
	case w.cur == nil && w.offline:
		res = errResult(ErrDeviceOffline, "%s is offline.", l.dev.name())
	case w.cur == nil:
		res = statusResult(StatusLocked, "The phone is locked. Call phone_handoff so the person can unlock it.")
	default:
		res = g.screenResult(l, w.cur, false)
		g.startShot(l.dev, w.cur)
		res.Status = StatusSettled
	}
	matched := w.matched
	res.Matched = &matched
	msg := ""
	switch {
	case matched:
		msg = fmt.Sprintf("“%s” %s after %s.", w.text, w.verb(), approval.Human(time.Since(start)))
	case w.note != "":
		msg = fmt.Sprintf("The person handed the phone back with a note after %s; “%s” has not %s.", approval.Human(time.Since(start)), w.text, w.verb())
		res.Note = w.note
	default:
		msg = fmt.Sprintf("“%s” has not %s after %s.", w.text, w.verb(), approval.Human(limit))
	}
	if w.tookOver && !w.person {
		msg += " The person took over during the watch; Pinky did not read the screen while they had it."
	}
	if w.offline && w.cur != nil {
		msg += " The phone is offline now; this is the last screen Pinky read."
	}
	if w.locked && w.cur != nil && res.Status == StatusSettled {
		msg += " The phone is locked: call phone_handoff so the person can unlock it before you act."
	}
	res.Message = joinNote(msg, res.Message)
	return res
}
