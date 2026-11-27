package ios

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// count is how many reads in a row found the daemon silent.
func (s *axStuck) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reads
}

// stuckRun makes the phone's last two reads ones that found the daemon
// silent, the first three minutes ago and the other a minute ago.
func (p *fallbackPhone) stuckRun() {
	now := time.Now()
	s := &p.d.stuck
	s.mu.Lock()
	s.reads, s.first, s.last = 2, now.Add(-3*time.Minute), now.Add(-time.Minute)
	s.mu.Unlock()
}

// stuckFound makes the phone's last four reads, over three minutes, ones
// that found the daemon stuck.
func (p *fallbackPhone) stuckFound() {
	now := time.Now()
	s := &p.d.stuck
	s.mu.Lock()
	s.reads, s.first, s.last, s.cause = 4, now.Add(-4*time.Minute), now.Add(-time.Minute), errSilentWalk
	s.since = now.Add(-2 * time.Minute)
	s.mu.Unlock()
}

func TestAXStuckRun(t *testing.T) {
	t0 := time.Now()
	errSilent := errors.New("silent")
	var s axStuck
	step := func(at time.Duration, want bool) {
		t.Helper()
		if due, _ := s.silent(t0.Add(at), errSilent); due != want {
			t.Fatalf("a silent read at %v: due = %v, want %v", at, due, want)
		}
	}
	step(0, false)
	step(time.Minute, false)
	step(stuckFor-time.Second, false) // three reads, over less than stuckFor
	step(stuckFor, true)
	if newly, reads, ok := s.found(t0.Add(stuckFor)); !newly || reads != 4 || !ok {
		t.Fatalf("found = %v, %d, %v", newly, reads, ok)
	}
	if newly, _, _ := s.found(t0.Add(stuckFor)); newly {
		t.Error("found anew")
	}
	// Once found, no gap ends the run: only an answer does.
	step(stuckFor+2*stuckGap, true)
	if v := s.view(); !v.stuck || v.reads != 5 || v.lasted != stuckFor+2*stuckGap || v.cause != errSilent {
		t.Errorf("view = %+v", v)
	}
	if was, lasted := s.answered(); !was || lasted != stuckFor+2*stuckGap {
		t.Errorf("answered = %v, %v", was, lasted)
	}
	if s.view().stuck || s.count() != 0 {
		t.Error("an answer did not end the run")
	}
	if _, _, ok := s.found(t0); ok {
		t.Error("found a run an answer ended")
	}
	if was, _ := s.answered(); was {
		t.Error("an answer without a run says one was found")
	}
	// Two reads are too few, however far apart; a gap of more than
	// stuckGap before the run is found starts it again.
	step(0, false)
	step(stuckFor, false)
	step(stuckFor+stuckGap+time.Second, false)
	step(2*stuckFor+stuckGap+time.Second, false)
	step(2*stuckFor+stuckGap+2*time.Second, true)

	var m axStuck
	if !m.mayShoot(t0) || m.mayShoot(t0.Add(stuckShotEvery-time.Second)) || !m.mayShoot(t0.Add(stuckShotEvery)) {
		t.Error("the screenshots taken to see are not spaced by stuckShotEvery")
	}
}

// TestStuckDaemonTellsTheAgent (#248): reads that find the daemon silent
// over stuckFor, while the phone gives pictures, fail with words that tell
// the agent to hand the phone over for a restart. The log says so once,
// with the read's own error, and the checkup fails until a read is
// answered.
func TestStuckDaemonTellsTheAgent(t *testing.T) {
	p := newFallbackPhone()
	p.insp.walkErr = errSilentWalk
	p.d.lastShot.Store(time.Now().UnixNano()) // the gateway took a screenshot lately
	p.stuckRun()
	_, _, err := p.read(t)
	var se *axStuckError
	if !errors.As(err, &se) {
		t.Fatalf("read error = %v, want the daemon stuck", err)
	}
	if se.Cause != errSilentWalk || se.For < 3*time.Minute {
		t.Errorf("stuck for %v with %v", se.For, se.Cause)
	}
	msg := err.Error()
	for _, want := range []string{
		"accessibility service (axauditd) has stopped answering",
		"screenshots and taps still work",
		"Restarting Pinky or reconnecting the iPhone does not bring it back",
		"only restarting the iPhone does",
		"Do not try to restart the iPhone yourself",
		"call phone_handoff and ask the person to restart it",
		errSilentWalk.Error(),
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the agent's words lack %q: %s", want, msg)
		}
	}
	if p.shots.Load() != 0 {
		t.Errorf("%d screenshots, though the gateway's was fresh", p.shots.Load())
	}
	// A new session's handshake that timed out wraps the deadline.
	p.insp.walkErr = fmt.Errorf("%w: %w", errAXDaemon, context.DeadlineExceeded)
	_, _, next := p.read(t)
	if !errors.As(next, &se) || !errors.Is(se.Cause, context.DeadlineExceeded) {
		t.Fatalf("the next read error = %v, want the daemon stuck after a deadline", next)
	}
	// The gateway puts its own words in place of an error that is one of
	// these: a deadline as a slow app, offline as a dropped phone.
	for _, err := range []error{err, next} {
		for _, e := range []error{context.DeadlineExceeded, context.Canceled, driver.ErrOffline, driver.ErrLocked, driver.ErrUnsupported} {
			if errors.Is(err, e) {
				t.Errorf("the error is %v: %v", e, err)
			}
		}
	}
	logs := p.logs.String()
	if n := strings.Count(logs, "the accessibility service stopped answering"); n != 1 {
		t.Errorf("the log says %d times that the daemon stopped: %s", n, logs)
	}
	if !strings.Contains(logs, "stuck_for=") || strings.Contains(logs, "phone_handoff") {
		t.Errorf("the reads' log lines: %s", logs)
	}
	ck := p.d.checkReading(t.Context())
	if ck.ID != driver.CheckReading || ck.Status != driver.CheckFail || ck.Fix != driver.FixRestartStuck ||
		!strings.Contains(ck.Detail, "answered none of 4 screen reads") {
		t.Errorf("checkup = %+v", ck)
	}

	p.insp.walkErr, p.insp.nodes = nil, []axNode{owned(951, "Shopping list, Heading")}
	if s, _, err := p.read(t); err != nil || s.App != notesFront {
		t.Fatalf("read = %+v, %v", s, err)
	}
	if !strings.Contains(p.logs.String(), "the accessibility service answers again") {
		t.Errorf("the log does not say the daemon answers: %s", p.logs.String())
	}
	if ck := p.d.checkReading(t.Context()); ck.Status != driver.CheckPass {
		t.Errorf("checkup after an answer = %+v", ck)
	}
}

// TestStuckNeedsAPicture: without a picture of the phone lately, a read
// takes a screenshot to see, at most every stuckShotEvery, and does not
// read it; a phone that gives none is not found stuck. A frame of the live
// video counts as a picture.
func TestStuckNeedsAPicture(t *testing.T) {
	p := newFallbackPhone()
	p.insp.walkErr = errSilentWalk
	var refuse atomic.Bool
	refuse.Store(true)
	p.d.shotFn = func(context.Context) ([]byte, error) {
		p.shots.Add(1)
		if refuse.Load() {
			return nil, errors.New("screenshot: no answer")
		}
		return testShot(), nil
	}
	p.stuckRun()
	var se *axStuckError
	for range 2 {
		if _, _, err := p.read(t); errors.As(err, &se) || !errors.Is(err, errAXWalk) {
			t.Fatalf("read error = %v, want the walk's", err)
		}
	}
	if p.shots.Load() != 1 {
		t.Errorf("%d screenshots to see, want one within stuckShotEvery", p.shots.Load())
	}
	refuse.Store(false)
	p.d.stuck.mu.Lock()
	p.d.stuck.shotAt = time.Time{}
	p.d.stuck.mu.Unlock()
	if _, _, err := p.read(t); !errors.As(err, &se) {
		t.Fatalf("read error = %v, want the daemon stuck", err)
	}
	if p.shots.Load() != 2 || p.ocrs.Load() != 0 {
		t.Errorf("%d screenshots to see, %d read; want two, none read", p.shots.Load(), p.ocrs.Load())
	}

	p = newFallbackPhone()
	p.insp.walkErr = errSilentWalk
	p.d.m = &media{}
	p.d.m.lastFrame.Store(time.Now().UnixNano())
	p.stuckRun()
	if _, _, err := p.read(t); !errors.As(err, &se) {
		t.Fatalf("read error = %v, want the daemon stuck", err)
	}
	if p.shots.Load() != 0 {
		t.Errorf("%d screenshots, though a video frame came lately", p.shots.Load())
	}
}

// TestStuckCountsOnlyASilentDaemon: a read that failed otherwise neither
// counts nor ends a run: lockdown did not answer (the phone may have
// dropped), the phone is locked (its daemon may rest), or remoteAXService
// or the inspector failed for another reason; nor does a read its caller
// gave up on.
func TestStuckCountsOnlyASilentDaemon(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*fallbackPhone)
	}{
		{"lockdown failed", func(p *fallbackPhone) {
			p.d.lockedFn = func(context.Context) (bool, error) { return false, errors.New("lockdown: connection refused") }
		}},
		{"locked", func(p *fallbackPhone) {
			p.d.lockedFn = func(context.Context) (bool, error) { return true, nil }
		}},
		{"remoteAXService failed otherwise", func(p *fallbackPhone) {
			p.d.raxErr = errors.New("remote accessibility: the session ended")
		}},
		{"the inspector failed otherwise", func(p *fallbackPhone) {
			p.insp.walkErr = errors.New("StartService: InvalidService")
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newFallbackPhone()
			p.insp.walkErr = errSilentWalk
			p.d.lastShot.Store(time.Now().UnixNano())
			p.stuckRun()
			c.set(p)
			_, _, err := p.read(t)
			var se *axStuckError
			if errors.As(err, &se) || p.d.stuck.count() != 2 {
				t.Errorf("read error = %v, %d silent reads; want it neither counted nor cleared", err, p.d.stuck.count())
			}
		})
	}
	p := newFallbackPhone()
	p.d.lastShot.Store(time.Now().UnixNano())
	p.stuckRun()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	st := ObserveStats{RAXError: p.d.raxErr, WalkError: errSilentWalk}
	if err := p.d.followStuck(ctx, st, errSilentWalk, true); err != errSilentWalk || p.d.stuck.count() != 2 {
		t.Errorf("a read given up on: %v, %d silent reads", err, p.d.stuck.count())
	}
}

// TestStuckClearsOnAnAnswer: a read the daemon answers ends the run, even
// when it fails: a walk with elements, of an app or not, one whose first
// element had no handle, or the ping of a read without the walk.
func TestStuckClearsOnAnAnswer(t *testing.T) {
	for _, c := range []struct {
		name string
		set  func(*fallbackPhone)
	}{
		{"elements", func(p *fallbackPhone) { p.insp.nodes = []axNode{owned(951, "Shopping list, Heading")} }},
		{"elements of no app", func(p *fallbackPhone) { p.insp.nodes = []axNode{owned(953, "Allow, Button")} }},
		{"no handle", func(p *fallbackPhone) { p.insp.walkErr = fmt.Errorf("%w: %w", errAXWalk, errAXNoHandle) }},
		{"a ping", func(p *fallbackPhone) { p.d.noteReadWeb(true) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			p := newFallbackPhone()
			p.stuckFound()
			c.set(p)
			_, _, err := p.read(t)
			var se *axStuckError
			if errors.As(err, &se) {
				t.Fatalf("an answered read says the daemon is stuck: %v", err)
			}
			if p.d.stuck.view().stuck || p.d.stuck.count() != 0 {
				t.Error("an answered read did not end the run")
			}
			if !strings.Contains(p.logs.String(), "the accessibility service answers again") {
				t.Errorf("the log: %s", p.logs.String())
			}
		})
	}
}

// TestCheckReading: the checkup item says what reads found, and asks the
// phone nothing.
func TestCheckReading(t *testing.T) {
	p := newFallbackPhone()
	if ck := p.d.checkReading(t.Context()); ck.ID != driver.CheckReading || ck.Status != driver.CheckPass || ck.Fix != "" {
		t.Errorf("checkup of a phone read well = %+v", ck)
	}
	p.stuckRun()
	if ck := p.d.checkReading(t.Context()); ck.Status != driver.CheckPass {
		t.Errorf("checkup of a run not found stuck = %+v", ck)
	}
	p.stuckFound()
	ck := p.d.checkReading(t.Context())
	if ck.ID != driver.CheckReading || ck.Status != driver.CheckFail || ck.Fix != driver.FixRestartStuck {
		t.Errorf("checkup of a stuck daemon = %+v", ck)
	}
	for _, want := range []string{"axauditd", "answered none of 4 screen reads over 3m0s", "only restarting the iPhone", errSilentWalk.Error()} {
		if !strings.Contains(ck.Detail, want) {
			t.Errorf("the detail lacks %q: %s", want, ck.Detail)
		}
	}
	if walks, pings := p.insp.counts(); walks+pings+int(p.shots.Load()) != 0 {
		t.Errorf("the checkup asked the phone: %d walks, %d pings, %d screenshots", walks, pings, p.shots.Load())
	}
}
