package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/DIYgod/pinky/internal/approval"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/screen"
)

// Unlock without asking (issue #231). Unlocking from the computer (wake.go)
// asks the person to confirm each unlock. For long tasks they may instead
// confirm once to let Pinky unlock one phone for agents' tasks for 1, 4 or
// 8 hours: an unlock window, per phone and off by default.
//
// Opening one (OpenUnlockWindow) takes the phone's saved PIN from
// Options.UnlockWindowPIN, which asks the person to confirm with Touch ID,
// Windows Hello or the keyring's password, naming the phone and the
// length. The gateway keeps the PIN as bytes in memory only: never on disk,
// in the log or in a step. The window ends, and the bytes are zeroed, when
// it runs out; when the person ends it (EndUnlockWindow); when they turn
// Settings › Unlock from this computer off (EndUnlockWindows) or change or
// delete the phone's PIN (the client ends the window then); when the phone
// is removed; when Pinky stops (Close); and when the phone does not accept
// the PIN.
//
// It is used in one case only: an agent's step needs the phone's own
// screen and finds it locked, where the agent got device_locked before.
// Pinky enters the PIN there and goes on with the step; a phone_handoff
// that asks for nothing but the unlock (unlockOnly) is answered the same
// way, at once. Tasks of Ask Pinky count as any agent's, and so does every
// agent the person lets use the phone: an agent's name is what its client
// says, so the window is the phone's, not an agent's. It is not used while
// no task runs on the phone, while the person has taken it over or paused
// it, while an approval waits for them on it, for an agent that may only
// watch, for a task on a background display (background.go), or for a run
// nobody started (a schedule's, AgentInfo.Unattended): Pinky never unlocks
// a phone for a schedule. The PIN goes in as from the computer: only on
// the lock screen, with no agent action meanwhile and nothing of the PIN
// pad recorded. Every unlock, done or failed, is a step of the task. A
// phone that does not accept the PIN is never tried again, since phones
// lock out or wipe after repeated failures: the window ends at once and the
// unlock card shows (Needs). The phone is not kept awake: it locks as it
// would, and the next step unlocks it again. The first unlock after the
// phone restarts stays the person's, on the phone, as with every unlock
// from the computer.

// UnlockWindowLengths are the lengths an unlock window may have.
var UnlockWindowLengths = []time.Duration{time.Hour, 4 * time.Hour, 8 * time.Hour}

// ErrUnlockWindowEnded is returned by OpenUnlockWindow when the window was
// ended, or the phone removed, while the person confirmed it.
var ErrUnlockWindowEnded = errors.New("unlock without asking was turned off, or the phone removed, while you confirmed")

// windowActWait bounds how long an unlock waits for an action that still
// runs on the phone, for a gate that does not hold the action lock.
const windowActWait = 10 * time.Second

// windowFailedNote tells the agent why it got device_locked although an
// unlock window was open.
const windowFailedNote = "Pinky could not unlock the phone with its saved PIN, so unlocking without asking has ended."

// windowStaleMessage answers an action that was about to run when the
// phone locked, and that an unlock window unlocked (windowStale).
const windowStaleMessage = "The phone locked just before the action, and Pinky unlocked it with its saved PIN, as the person allows for now. Nothing was done. Look at the screen and decide again."

// unlockWindow is a phone's open unlock window. Guarded by g.mu.
type unlockWindow struct {
	dev *device
	pin []byte
	// until has no monotonic reading, so it compares by the wall clock: a
	// computer that slept does not stretch the window.
	until time.Time
	timer *time.Timer
}

// windowResult is what an unlock window did for a locked phone.
type windowResult int

const (
	windowUnused   windowResult = iota // no window applies: the phone stays locked
	windowUnlocked                     // Pinky entered the PIN and the phone is unlocked
	windowNoNeed                       // the phone turned out to be unlocked already: nothing was entered
	windowFailed                       // the phone stayed locked, and the window ended
)

// unlocked reports whether the phone is unlocked after r.
func (r windowResult) unlocked() bool { return r == windowUnlocked || r == windowNoNeed }

// windowUse says how a gate may use an unlock window.
type windowUse int

const (
	windowTake  windowUse = iota // the caller does not hold d.act: the unlock takes it
	windowHeld                   // the caller holds d.act
	windowStale                  // the caller holds d.act for an action aimed at a screen read before the lock: it does not run
	windowNone                   // no unlock window
)

// OpenUnlockWindow opens an unlock window of length for the phone, one of
// UnlockWindowLengths, once the person confirms (Options.UnlockWindowPIN),
// in place of one that is open. It returns ErrNoUnlock for a phone Pinky
// cannot unlock from the computer, and the PIN source's error, such as
// secret.ErrCancelled, when the person did not confirm.
func (g *Gateway) OpenUnlockWindow(deviceID string, length time.Duration) error {
	if !slices.Contains(UnlockWindowLengths, length) {
		return fmt.Errorf("unlock without asking lasts 1, 4 or 8 hours, not %s", length)
	}
	d, err := g.device(deviceID)
	if err != nil {
		return err
	}
	if g.opt.UnlockWindowPIN == nil {
		return ErrNoUnlock
	}
	dev := g.driverOf(d)
	if dev == nil {
		return driver.ErrOffline
	}
	if _, ok := dev.(driver.Unlocker); !ok {
		return fmt.Errorf("%w (%s cannot enter a PIN from the computer)", ErrNoUnlock, d.name())
	}
	g.mu.Lock()
	epoch := g.windowEpoch
	g.mu.Unlock()
	pctx, cancel := context.WithTimeout(g.ctx, unlockPINTimeout)
	pin, err := g.opt.UnlockWindowPIN(pctx, deviceID, length)
	cancel()
	if err != nil {
		return err
	}
	w := &unlockWindow{dev: d, pin: []byte(pin), until: time.Now().Add(length).Round(0)}
	pin = ""
	g.mu.Lock()
	if g.closed || d.removed || g.devices[deviceID] != d || g.windowEpoch != epoch {
		g.mu.Unlock()
		clear(w.pin)
		return ErrUnlockWindowEnded
	}
	g.endWindowLocked(deviceID, "replaced")
	w.timer = time.AfterFunc(length, func() { g.expireWindow(deviceID, w) })
	g.windows[deviceID] = w
	delete(g.pinRefused, deviceID)
	g.mu.Unlock()
	g.log.Info("unlock without asking: on", "device", deviceID, "until", w.until.Format(time.RFC3339))
	g.emit(Event{Kind: EventDevices, DeviceID: deviceID})
	g.emit(Event{Kind: EventNeeds, DeviceID: deviceID})
	return nil
}

// EndUnlockWindow ends the phone's unlock window, also one the person is
// confirming right now. A phone without one is no error.
func (g *Gateway) EndUnlockWindow(deviceID string) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.windowEpoch++
	g.endWindowLocked(deviceID, "ended by the person")
	delete(g.pinRefused, deviceID)
	return nil
}

// EndUnlockWindows ends every phone's unlock window, also the ones being
// confirmed: Settings › Unlock from this computer was turned off.
func (g *Gateway) EndUnlockWindows() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.endWindowsLocked("unlocking from this computer was turned off")
}

// endWindowsLocked ends every unlock window and the ones being confirmed.
func (g *Gateway) endWindowsLocked(why string) {
	g.windowEpoch++
	for id := range g.windows {
		g.endWindowLocked(id, why)
	}
}

// expireWindow ends w when it runs out, unless another window replaced it.
func (g *Gateway) expireWindow(id string, w *unlockWindow) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.windows[id] == w {
		g.endWindowLocked(id, "it ran out")
	}
}

// expireWindowsLocked ends the windows that ran out by the wall clock,
// which their timers miss while the computer sleeps (pollLocks).
func (g *Gateway) expireWindowsLocked(now time.Time) {
	for id, w := range g.windows {
		if !now.Before(w.until) {
			g.endWindowLocked(id, "it ran out")
		}
	}
}

// endWindowLocked ends the phone's unlock window, if it has one, and zeroes
// its PIN. why goes to the log.
func (g *Gateway) endWindowLocked(id, why string) {
	w := g.windows[id]
	if w == nil {
		return
	}
	delete(g.windows, id)
	clear(w.pin)
	w.pin = nil
	if w.timer != nil {
		w.timer.Stop()
	}
	g.log.Info("unlock without asking: off", "device", id, "why", why)
	g.emit(Event{Kind: EventDevices, DeviceID: id})
	g.emit(Event{Kind: EventNeeds, DeviceID: id})
}

// dropWindowLocked forgets the window of a phone that leaves the list.
func (g *Gateway) dropWindowLocked(id string) {
	g.endWindowLocked(id, "the phone was removed")
	delete(g.pinRefused, id)
}

// windowForLocked returns the unlock window that may unlock the lease's
// phone now, or nil. approvalID is the approval the call holds, if any: an
// approval that waits for the person on the phone keeps it locked, the
// call's own once the person answered it does not.
func (g *Gateway) windowForLocked(l *lease, approvalID string) *unlockWindow {
	w := g.windowOpenForLocked(l)
	if w == nil {
		return nil
	}
	if busy, ok := g.approvals.Busy(l.dev.id); ok && (busy.ID != approvalID || busy.State == approval.Open) {
		return nil
	}
	return w
}

// windowOpenForLocked is windowForLocked without the approvals.
func (g *Gateway) windowOpenForLocked(l *lease) *unlockWindow {
	if l == nil {
		return nil
	}
	d := l.dev
	w := g.windows[d.id]
	if w == nil || w.dev != d {
		return nil
	}
	if !time.Now().Before(w.until) {
		g.endWindowLocked(d.id, "it ran out")
		return nil
	}
	switch {
	case l.ended, d.lease != l, d.person, g.paused[d.id], !d.online, d.dev == nil:
		return nil
	case l.background():
		// The task works on a background display, which the lock does not
		// hold up.
		return nil
	case l.session == nil || l.session.agent.Unattended:
		return nil
	}
	if _, ok := d.dev.(driver.Unlocker); !ok {
		return nil
	}
	if g.scopeOfLocked(l.session).Mode == ScopeWatchOnly {
		return nil
	}
	return w
}

// windowUnlock enters the PIN of the phone's unlock window, when one may
// unlock the locked phone for the lease now (windowForLocked). held says
// whether the caller holds d.act; without it the unlock waits for the
// action that runs, as long as windowActWait. The PIN goes in only on the
// lock screen, once: a phone that does not take it ends the window.
func (g *Gateway) windowUnlock(l *lease, approvalID string, held bool) windowResult {
	d := l.dev
	g.mu.Lock()
	w := g.windowForLocked(l, approvalID)
	g.mu.Unlock()
	if w == nil {
		return windowUnused
	}
	release := func() {}
	if !held {
		if !lockWithin(l.ctx, &d.act, windowActWait) {
			return windowUnused
		}
		release = d.act.Unlock
	}
	// As for Unlock: the phone's own screen shows the PIN pad, so nothing
	// of it is recorded, and no agent action runs meanwhile.
	g.mu.Lock()
	if w = g.windowForLocked(l, approvalID); w == nil {
		g.mu.Unlock()
		release()
		return windowUnused
	}
	dev := d.dev
	u := dev.(driver.Unlocker) // windowForLocked checked
	pin, until := string(w.pin), w.until
	g.inputLocked(d)
	if l.bg == nil {
		g.pauseVideoLocked(d)
	}
	// No phone_batch step starts from a read made before the unlock.
	l.after, l.afterAt = nil, time.Time{}
	g.mu.Unlock()
	// Not the lease's context: a task that ends meanwhile does not cut the
	// PIN off halfway.
	ctx, cancel := context.WithTimeout(g.ctx, unlockTimeout)
	defer cancel()
	// Only a lock screen gets the PIN: a phone unlocked meanwhile would
	// take it as typing in whatever app is in front.
	if locked, known := g.lockedNow(ctx, dev); known && !locked {
		release()
		g.log.Info("unlock without asking: the phone is already unlocked", "device", d.id)
		g.setLocked(d, false)
		g.answerUnlockHandoffs(d)
		return windowNoNeed
	}
	start := time.Now()
	err := u.Unlock(ctx, pin)
	pin = ""
	if err == nil {
		g.mu.Lock()
		// The next action waits for the screen to settle after the lock
		// screen went (settleOwed).
		g.oweSettleLocked(d, time.Now())
		g.mu.Unlock()
	}
	release()
	if err != nil {
		// Drivers never put the PIN in their errors.
		g.log.Info("unlock without asking", "device", d.id, "ok", false, "err", err.Error())
	} else {
		g.log.Info("unlock without asking", "device", d.id, "ok", true)
	}
	g.recordWindowUnlock(l, start, until, err)
	if err != nil {
		g.mu.Lock()
		if errors.Is(err, driver.ErrLocked) {
			// The unlock card says the phone did not take the PIN.
			g.pinRefused[d.id] = true
		}
		// Never again with this PIN: the window ends, whichever it is now.
		g.endWindowLocked(d.id, "the unlock failed")
		g.mu.Unlock()
		if errors.Is(err, driver.ErrLocked) {
			g.setLocked(d, true)
		}
		return windowFailed
	}
	// First, so a gate that runs again sees the phone unlocked.
	g.setLocked(d, false)
	g.checkLock(ctx, d)
	g.answerUnlockHandoffs(d)
	return windowUnlocked
}

// recordWindowUnlock adds an unlock without asking to the lease's task.
func (g *Gateway) recordWindowUnlock(l *lease, start, until time.Time, err error) {
	st := StepView{Text: fmt.Sprintf("Unlocked without asking (until %s)", until.Local().Format("15:04")),
		Status: string(StatusSettled), At: start, Elapsed: time.Since(start)}
	switch {
	case errors.Is(err, driver.ErrLocked):
		st.Text, st.Status = "Unlocking without asking failed: the phone did not accept the saved PIN. Unlock without asking has ended", "wrong_pin"
	case errors.Is(err, driver.ErrPINNotEntered):
		st.Text, st.Status = "Unlocking without asking failed: Pinky could not get to the passcode screen and entered nothing. Unlock without asking has ended", "pin_not_entered"
	case err != nil:
		st.Text, st.Status = "Unlocking without asking failed. Unlock without asking has ended", string(ErrInternal)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if l.ended {
		return
	}
	g.addStepLocked(l.task, st, "", "")
}

// windowRead unlocks the phone when cur, a read of its own screen, shows it
// locked and an unlock window may unlock it (windowUnlock), and then reads
// it again with read. It returns the screen to go on with: cur when the
// phone stayed locked.
func (g *Gateway) windowRead(l *lease, approvalID string, held bool, cur *screen.Screen,
	read func() (*screen.Screen, error)) (*screen.Screen, windowResult, error) {
	if cur == nil || !cur.Locked {
		return cur, windowUnused, nil
	}
	r := g.windowUnlock(l, approvalID, held)
	if !r.unlocked() {
		return cur, r, nil
	}
	again, err := read()
	return again, r, err
}

// lockedNote is msg, the agent's word that the phone is locked, with why
// the unlock window did not help when it failed.
func lockedNote(r windowResult, msg string) string {
	if r == windowFailed {
		return windowFailedNote + " " + msg
	}
	return msg
}
