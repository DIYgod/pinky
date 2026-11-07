package gateway

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/approval"
	"github.com/DIYgod/pinky/internal/driver"
)

// Phones are never kept awake (round three, decision 3): their own screen
// timeout and lock apply between steps and tasks. Before an agent's call
// reads or acts on the phone's screen, the gateway wakes it (driver.Waker);
// a locked phone then shows its lock screen, and the agent gets
// device_locked as before. Unlocking from the computer (decision 5) enters
// the saved PIN through driver.Unlocker, only when the person asks in the
// client, and leaves a step in the task's history, never the PIN.

// wakeFresh is how long a wake counts: the shortest screen timeouts are 15
// s (Android) and 30 s (iOS), so a phone woken this recently is still on
// unless the person turned it off.
const wakeFresh = 5 * time.Second

// wake turns the phone's screen on when it is off, at most once every
// wakeFresh. Errors are left to the read that follows.
func (g *Gateway) wake(ctx context.Context, d *device) {
	g.mu.Lock()
	dev := d.dev
	fresh := time.Since(d.wokeAt) < wakeFresh
	g.mu.Unlock()
	w, ok := dev.(driver.Waker)
	if !ok || fresh {
		return
	}
	wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := w.Wake(wctx); err != nil {
		if errors.Is(err, driver.ErrOffline) {
			g.setOffline(d, "")
		}
		g.log.Debug("wake", "device", d.id, "err", err)
		return
	}
	g.mu.Lock()
	d.wokeAt = time.Now()
	g.mu.Unlock()
}

// unlockPINTimeout bounds getting the PIN, which waits for the person to
// confirm with Touch ID; unlockTimeout bounds entering it.
const (
	unlockPINTimeout = 2 * time.Minute
	unlockTimeout    = 30 * time.Second
)

// ErrWrongPIN means the phone was still locked after Pinky entered the
// saved PIN. Pinky does not try again: phones lock out or wipe after
// repeated failures.
var ErrWrongPIN = errors.New("the phone is still locked after entering the saved PIN; check the PIN in Settings and unlock on the phone")

// Unlock implements API: it enters the phone's saved PIN, which
// Options.PIN hands out after the person confirms on the computer. It
// never retries.
func (g *Gateway) Unlock(deviceID string) error {
	d, err := g.device(deviceID)
	if err != nil {
		return err
	}
	if g.opt.PIN == nil {
		return ErrNoUnlock
	}
	dev := g.driverOf(d)
	if dev == nil {
		return driver.ErrOffline
	}
	u, ok := dev.(driver.Unlocker)
	if !ok {
		return fmt.Errorf("%w (%s cannot enter a PIN from the computer)", ErrNoUnlock, d.name())
	}
	pctx, pcancel := context.WithTimeout(g.ctx, unlockPINTimeout)
	pin, err := g.opt.PIN(pctx, deviceID)
	pcancel()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(g.ctx, unlockTimeout)
	defer cancel()
	// The phone's own screen shows the PIN pad: nothing of it is recorded,
	// and no agent action runs meanwhile.
	d.act.Lock()
	g.mu.Lock()
	g.inputLocked(d)
	if l := d.lease; l == nil || l.bg == nil {
		g.pauseVideoLocked(d)
	}
	l := d.lease
	g.mu.Unlock()
	start := time.Now()
	err = u.Unlock(ctx, pin)
	pin = ""
	d.act.Unlock()
	g.log.Info("unlock from the computer", "device", d.id, "ok", err == nil)
	g.recordUnlock(l, start, err)
	if err != nil {
		if errors.Is(err, driver.ErrLocked) {
			g.setLocked(d, true)
			return ErrWrongPIN
		}
		return err
	}
	g.checkLock(ctx, d)
	if _, ok := dev.(driver.LockReporter); !ok {
		g.readLock(ctx, d, dev)
	}
	// The person unlocked from the computer: handoffs the agent opened for
	// the lock are answered.
	g.mu.Lock()
	var ids []string
	if !d.locked && d.lease != nil {
		ids = d.lease.unlockHandoffs
		d.lease.unlockHandoffs = nil
	}
	g.mu.Unlock()
	for _, id := range ids {
		_ = g.approvals.Decide(id, approval.AllowOnce, "The phone was unlocked.")
	}
	return nil
}

// recordUnlock adds the unlock to the task running on the phone, if any.
func (g *Gateway) recordUnlock(l *lease, start time.Time, err error) {
	if l == nil {
		return
	}
	st := StepView{Text: "Unlocked from the computer", Status: string(StatusSettled), At: start, Elapsed: time.Since(start)}
	switch {
	case errors.Is(err, driver.ErrLocked):
		st.Text, st.Status = "Unlock from the computer failed: the phone did not accept the saved PIN", "wrong_pin"
	case err != nil:
		st.Text, st.Status = "Unlock from the computer failed", string(ErrInternal)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if l.ended {
		return
	}
	g.addStepLocked(l.task, st, "", "")
}

// readLock reads the phone's own screen to learn its lock state, for
// drivers that report it only through reads.
func (g *Gateway) readLock(ctx context.Context, d *device, dev driver.Device) {
	rctx, cancel := context.WithTimeout(ctx, observeTimeout)
	defer cancel()
	s, err := dev.Observe(rctx, driver.ObserveOptions{})
	if err != nil || s == nil {
		return
	}
	g.setLocked(d, s.Locked)
}
