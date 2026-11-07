package android

import (
	"sync"
	"time"
)

// When List starts an adb server (Config.StartServer).
const (
	// serverAway is how long a server that ran may stay away before List
	// starts one: an adb client that finds a server of another version
	// kills it and starts its own at once, and a server Android Studio
	// restarts comes back within a second or two.
	serverAway = 3 * time.Second
	// serverRetry is the wait after a start failed, doubling up to
	// serverRetryMax.
	serverRetry    = 5 * time.Second
	serverRetryMax = 2 * time.Minute
)

// serverWatch decides when List starts an adb server.
type serverWatch struct {
	mu        sync.Mutex
	seen      bool      // a server answered once
	downSince time.Time // when List found none, zero while one answers
	starting  bool      // a start is under way
	next      time.Time // no start before then
	wait      time.Duration
}

// claim reports whether List should start a server now; the caller then
// reports the outcome with done.
func (w *serverWatch) claim(now time.Time, logf func(string, ...any)) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.downSince.IsZero() {
		w.downSince = now
		if w.seen {
			logf("android: the adb server went away; Pinky starts one if none is back within %v", serverAway)
		}
	}
	if w.starting || now.Before(w.next) || (w.seen && now.Sub(w.downSince) < serverAway) {
		return false
	}
	w.starting = true
	return true
}

// done records how a start that claim allowed went.
func (w *serverWatch) done(now time.Time, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.starting = false
	if err == nil {
		w.wait = 0
		w.next = time.Time{}
		return
	}
	w.wait = min(max(2*w.wait, serverRetry), serverRetryMax)
	w.next = now.Add(w.wait)
}

// answered records a server that answers.
func (w *serverWatch) answered() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = true
	w.downSince = time.Time{}
}
