package ios

import (
	"context"
	"fmt"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// Resting through the WebDriverAgent helper (see rest.go). The runner is
// an XCTest UI test: while it runs, the phone keeps automation and its
// accessibility machinery up. Once the phone has been unused for restIdle,
// the device ends the WebDriver session, stops the runner the way Close
// does (wdaRun.stop, which makes sure it ended) and closes the tunnel. The
// next request through the client (wdaClient.enter) starts the runner
// again through the provider (reopen), as opening the phone does, and the
// client reaches the new runner from then on (useRoute).
//
// Only a runner Pinky started, or one an earlier Pinky left (leftover),
// is stopped: one Xcode runs is not Pinky's to end, and a Simulator or a
// runner at a URL has no reopen.
//
// Starting the runner again may make iOS ask for the passcode to allow
// automation, as any start may; the wake then fails, the gateway opens the
// phone again, and the person is asked for it (driver.SetupPasscode).

// AgentWorking implements driver.WorkWatcher: while an agent works on the
// phone it never rests.
func (d *WDADevice) AgentWorking(on bool) {
	d.working.Store(on)
	if on {
		d.rmu.Lock()
		d.usedAt = time.Now()
		d.rmu.Unlock()
	}
}

// Resting reports whether the device stopped the runner for want of use.
func (d *WDADevice) Resting() bool {
	d.rmu.Lock()
	defer d.rmu.Unlock()
	return d.resting
}

// enter runs before each request to WebDriverAgent: it wakes a resting
// phone, records the use and counts the request until the returned func.
func (d *WDADevice) enter(ctx context.Context) (func(), error) {
	d.rmu.Lock()
	d.usedAt = time.Now()
	if !d.resting {
		d.inflight++
		d.rmu.Unlock()
		return d.leave, nil
	}
	d.rmu.Unlock()
	if err := d.wakeUp(ctx); err != nil {
		return nil, err
	}
	d.rmu.Lock()
	d.inflight++
	d.usedAt = time.Now()
	d.rmu.Unlock()
	return d.leave, nil
}

func (d *WDADevice) leave() {
	d.rmu.Lock()
	d.inflight--
	d.usedAt = time.Now()
	d.rmu.Unlock()
}

// awake wakes a resting phone, for calls that reach it other than through
// WebDriverAgent (its tunnel: app logs, the checkup).
func (d *WDADevice) awake(ctx context.Context) error {
	leave, err := d.enter(ctx)
	if err != nil {
		return err
	}
	leave()
	return nil
}

// wakeUp starts the runner again on a resting phone.
func (d *WDADevice) wakeUp(ctx context.Context) error {
	d.wakeMu.Lock()
	defer d.wakeMu.Unlock()
	d.rmu.Lock()
	resting, lost, closed := d.resting, d.wakeLost, d.closed
	d.rmu.Unlock()
	switch {
	case !resting:
		return nil
	case lost != nil:
		return lost
	case closed:
		return fmt.Errorf("device closed: %w", driver.ErrOffline)
	}
	start := time.Now()
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), wdaStartTimeout)
	defer cancel()
	nd, err := d.reopen(wctx)
	if err != nil {
		// The gateway drops the phone and opens it again, which shows a
		// step it waits for (the passcode) the usual way.
		lost := fmt.Errorf("start the helper again: %v: %w", err, driver.ErrOffline)
		d.rmu.Lock()
		d.wakeLost = lost
		d.rmu.Unlock()
		logTo(d.log).Warn("iphone: could not wake the helper", "device", d.info.ID, "after", time.Since(start).Round(time.Millisecond), "err", err)
		return lost
	}
	d.c.useRoute(nd.c)
	d.rmu.Lock()
	d.run, d.l, d.leftover, d.leftoverEnded = nd.run, nd.l, nd.leftover, nd.leftoverEnded
	d.resting, d.usedAt = false, time.Now()
	d.rmu.Unlock()
	logTo(d.log).Info("iphone: helper awake", "device", d.info.ID, "in", time.Since(start).Round(time.Millisecond))
	return nil
}

// restIfIdle stops the runner and closes the tunnel when the phone has
// been unused for restIdle. It reports whether the device now rests.
func (d *WDADevice) restIfIdle(now time.Time) bool {
	if d.working.Load() || d.reopen == nil || !d.c.routed() || !d.wakeMu.TryLock() {
		return false
	}
	defer d.wakeMu.Unlock()
	if !d.inputMu.TryLock() {
		return false
	}
	defer d.inputMu.Unlock()
	d.rmu.Lock()
	ours := d.run != nil || d.leftover != ""
	if d.resting || d.closed || !ours || d.inflight > 0 || d.watchers > 0 ||
		!idleSince(now, restIdle, d.usedAt, d.watchedAt, d.openedAt) {
		d.rmu.Unlock()
		return false
	}
	run, l, leftover, leftoverEnded := d.run, d.l, d.leftover, d.leftoverEnded
	d.run, d.l, d.leftover, d.leftoverEnded = nil, nil, "", nil
	d.resting, d.wakeLost = true, nil
	d.rmu.Unlock()

	d.StopAppLogs()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	d.c.deleteSession(ctx)
	cancel()
	d.c.forget()
	stopRunner(d.entry, run, leftover, leftoverEnded, d.log)
	if l != nil {
		l.Close()
	}
	logTo(d.log).Info("iphone: helper resting", "device", d.info.ID, "idle", restIdle)
	return true
}

// restLoop rests the device once it is idle, until it closes.
func (d *WDADevice) restLoop() {
	t := time.NewTicker(idleCheck)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			d.restIfIdle(time.Now())
		case <-d.done:
			return
		}
	}
}

// watchStream counts a view of the picture until done is closed.
func (d *WDADevice) watchStream(done <-chan struct{}) {
	d.rmu.Lock()
	d.watchers++
	d.watchedAt = time.Now()
	d.rmu.Unlock()
	go func() {
		<-done
		d.rmu.Lock()
		d.watchers--
		d.watchedAt = time.Now()
		d.rmu.Unlock()
	}()
}

var _ driver.WorkWatcher = (*WDADevice)(nil)

// tunnelLink returns the tunnel the runner goes through, or nil.
func (d *WDADevice) tunnelLink() *link {
	d.rmu.Lock()
	defer d.rmu.Unlock()
	return d.l
}
