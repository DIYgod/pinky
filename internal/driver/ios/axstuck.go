package ios

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// A stuck accessibility daemon (GitHub issue #248, 2026-11-27). An iPhone
// 18 Pro on iOS 27.2, used over Wi-Fi, answered none of over 280 screen
// reads in more than two hours: remoteAXService's handshake got no answer
// within 5 s, and the inspector walk that stands in for it timed out on
// its first focus request. Screenshots, OCR, the live picture, taps and app
// launches went on working; restarting Pinky and a new tunnel did not help.
// Both services are axauditd's. It was seen once before, after iPhone
// Mirroring: eight minutes of the same, until restarting the iPhone
// brought it back (docs/notes/ios.md).
//
// Pinky cannot restart the daemon, and never presses the power key. So it
// tells: the agent, in the error of each read while it lasts, to hand the
// phone to the person to restart it (axStuckError); the person, in the
// checkup's reading item (checkReading). A read finds the daemon silent
// when
//
//   - remoteAXService did not answer (errRAXTimeout), and the inspector did
//     not either: its session's handshake, its walk's first requests, or
//     the ping of a read without the walk (inspectorSilent);
//   - lockdown answered, and the phone is not locked: a locked phone's
//     daemon may rest (an iPhone 14 Pro on 2026-11-17), and a phone that
//     dropped answers nothing;
//
// and stuck once stuckReads such reads in a row span stuckFor, no two more
// than stuckGap apart, while the phone gives pictures (pictureFresh): a
// screenshot or a video frame within stuckPictureFresh, else a screenshot
// taken to see, at most every stuckShotEvery. Each read that says so needs
// that picture again. Any read the daemon answers, remoteAXService's or
// the inspector's, clears it.
//
// The thresholds: remoteAXService alone has paused for over a minute while
// the inspector answered (2026-11-19: no such read counts), and a phone on
// Wi-Fi drops for seconds (lockdown then fails too); the daemon stuck
// stayed so for eight minutes and for over two hours. Three reads over two
// minutes keep one or two slow reads from counting, and tell the agent
// within a few reads more; remoteAXService is dialed again every
// raxRecheckFailures (30 s), so such a run has tried it again several
// times.
const (
	stuckReads = 3
	stuckFor   = 2 * time.Minute
	stuckGap   = 5 * time.Minute

	// stuckPictureFresh is how recent a picture of the phone's says that
	// it still gives them; stuckShotEvery spaces the screenshots taken to
	// see, when nothing else took one, and stuckShotWait bounds each.
	stuckPictureFresh = time.Minute
	stuckShotEvery    = 30 * time.Second
	stuckShotWait     = 5 * time.Second
)

// axStuck follows the reads that find the daemon silent.
type axStuck struct {
	mu     sync.Mutex
	reads  int       // such reads in a row
	first  time.Time // when the first of them ended
	last   time.Time // when the last did
	cause  error     // what the last failed with
	since  time.Time // when the run was found to be a stuck daemon; zero: not
	shotAt time.Time // when a read last took a screenshot to see
}

// answered ends a run: the daemon answered a read. It reports whether the
// run was found stuck, and for how long reads found it silent.
func (s *axStuck) answered() (was bool, lasted time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads == 0 {
		return false, 0
	}
	was, lasted = !s.since.IsZero(), s.last.Sub(s.first)
	s.reads, s.first, s.last, s.cause, s.since = 0, time.Time{}, time.Time{}, nil, time.Time{}
	return was, lasted
}

// silent counts a read that found the daemon silent, ending at now with
// err. due says the run is long enough to call the daemon stuck, or was
// found so; lasted is how long it has run.
func (s *axStuck) silent(now time.Time, err error) (due bool, lasted time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads == 0 || (s.since.IsZero() && now.Sub(s.last) > stuckGap) {
		s.reads, s.first, s.since = 0, now, time.Time{}
	}
	s.reads++
	s.last, s.cause = now, err
	lasted = now.Sub(s.first)
	return !s.since.IsZero() || (s.reads >= stuckReads && lasted >= stuckFor), lasted
}

// found marks the run as a stuck daemon; newly says it was not before, and
// reads is how many reads found it silent. ok is false when a read the
// daemon answered ended the run meanwhile: then nothing is marked.
func (s *axStuck) found(now time.Time) (newly bool, reads int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reads == 0 {
		return false, 0, false
	}
	if s.since.IsZero() {
		s.since, newly = now, true
	}
	return newly, s.reads, true
}

// mayShoot claims the next screenshot taken to see, at most one every
// stuckShotEvery.
func (s *axStuck) mayShoot(now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now.Sub(s.shotAt) < stuckShotEvery {
		return false
	}
	s.shotAt = now
	return true
}

// stuckView is what reads found, for the checkup.
type stuckView struct {
	stuck  bool
	reads  int
	lasted time.Duration
	cause  error
}

func (s *axStuck) view() stuckView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.since.IsZero() {
		return stuckView{}
	}
	return stuckView{stuck: true, reads: s.reads, lasted: s.last.Sub(s.first), cause: s.cause}
}

// daemonAnswered clears what reads found: one was answered.
func (d *Device) daemonAnswered() {
	if was, lasted := d.stuck.answered(); was {
		logTo(d.log).Info("iphone: the accessibility service answers again", "device", d.info.ID,
			"after", lasted.Round(time.Second))
	}
}

// followStuck follows a read that remoteAXService failed and the inspector
// stood in for, which ended with err; lockdownOK says lockdown answered,
// unlocked. It returns err, or while the daemon is stuck the error that
// tells the agent.
func (d *Device) followStuck(ctx context.Context, st ObserveStats, err error, lockdownOK bool) error {
	if ctx.Err() != nil {
		return err
	}
	if err == nil || st.heard {
		d.daemonAnswered()
		return err
	}
	if !lockdownOK || !errors.Is(st.RAXError, errRAXTimeout) || !inspectorSilent(st.WalkError) {
		return err // something else failed: it neither counts nor clears
	}
	due, lasted := d.stuck.silent(time.Now(), err)
	if !due || !d.pictureFresh(ctx) {
		return err
	}
	newly, reads, ok := d.stuck.found(time.Now())
	if !ok {
		return err
	}
	if newly {
		logTo(d.log).Warn("iphone: the accessibility service stopped answering; only restarting the iPhone brings it back",
			"device", d.info.ID, "for", lasted.Round(time.Second), "reads", reads, "err", err)
	}
	return &axStuckError{For: lasted, Cause: err}
}

// inspectorSilent reports whether the inspector's error says the daemon
// did not answer: its session's handshake or a ping, or a walk's first
// requests. A walk whose first element had no handle got an answer.
func inspectorSilent(err error) bool {
	return errors.Is(err, errAXDaemon) || (errors.Is(err, errAXWalk) && !errors.Is(err, errAXNoHandle))
}

// pictureFresh reports whether the phone gave a picture lately: a
// screenshot or a frame of the live video, else one it gives now. That
// picture is not looked at, kept or passed on: it only shows that the
// phone is there and its accessibility is not.
func (d *Device) pictureFresh(ctx context.Context) bool {
	now := time.Now()
	last := unixTime(d.lastShot.Load())
	d.mu.Lock()
	if d.m != nil {
		if f := unixTime(d.m.lastFrame.Load()); f.After(last) {
			last = f
		}
	}
	d.mu.Unlock()
	if now.Sub(last) < stuckPictureFresh {
		return true
	}
	if !d.stuck.mayShoot(now) {
		return false
	}
	c, cancel := context.WithTimeout(ctx, stuckShotWait)
	defer cancel()
	_, err := d.ScreenshotPNG(c)
	return err == nil
}

// axStuckError is a read's error while the daemon is stuck: what the agent
// reads (the gateway passes it on), with the read's own error last. It
// wraps nothing, so that the gateway puts no words of its own for a
// deadline or a dropped phone in place of these.
type axStuckError struct {
	For   time.Duration // how long reads have found the daemon silent
	Cause error         // what the read failed with
}

func (e *axStuckError) Error() string {
	return fmt.Sprintf("the iPhone's accessibility service (axauditd) has stopped answering: screen reads have failed for %s, "+
		"while screenshots and taps still work. Restarting Pinky or reconnecting the iPhone does not bring it back; "+
		"only restarting the iPhone does. Do not try to restart the iPhone yourself: call phone_handoff and ask the person "+
		"to restart it, then read the screen again (last error: %v)", e.For.Round(time.Second), e.Cause)
}

// checkReading says whether screen reads found the accessibility daemon
// stuck. It asks the phone nothing: the reads found it, and the checkup
// can do nothing about it but say to restart the iPhone.
func (d *Device) checkReading(context.Context) driver.Check {
	c := driver.Check{ID: driver.CheckReading}
	v := d.stuck.view()
	if !v.stuck {
		c.Status, c.Detail = driver.CheckPass, "no screen read found the accessibility service (axauditd) stuck"
		return c
	}
	c.Status, c.Fix = driver.CheckFail, driver.FixRestartStuck
	c.Detail = fmt.Sprintf("the accessibility service (axauditd) answered none of %d screen reads over %s, while the iPhone gave pictures; "+
		"only restarting the iPhone brings it back (last error: %v)", v.reads, v.lasted.Round(time.Second), v.cause)
	return c
}
