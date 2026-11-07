package android

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
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

	// A server that went away restartsMin times within restartWindow is
	// being replaced by another program (CheckHost's adb_restarts).
	restartWindow = 10 * time.Minute
	restartsMin   = 3
)

// What CheckHost finds missing (driver.HostCheck.Missing).
const (
	// hostNoADB: no adb to start a server with, in a build of Pinky
	// without its own on a computer without one.
	hostNoADB = "adb"
	// hostADBServer: no adb server runs, and starting one failed; Detail
	// says why.
	hostADBServer = "adb_server"
	// hostADBOld: the server that runs is older than protocol 41
	// (platform-tools before 29, 2019), which lacks pairing over Wi-Fi and
	// more.
	hostADBOld = "adb_old"
	// hostADBRestarts: the server keeps going away, as when another
	// program restarts it with an adb of another version.
	hostADBRestarts = "adb_restarts"
)

// serverWatch decides when List starts an adb server, and keeps what
// CheckHost reports from the Lists so far.
type serverWatch struct {
	mu        sync.Mutex
	seen      bool      // a server answered once
	downSince time.Time // when List found none, zero while one answers
	starting  bool      // a start is under way
	next      time.Time // no start before then
	wait      time.Duration

	lastErr error       // why the last start failed; nil once a server answers
	version int         // the protocol version of the server that answers, 0 when unknown
	asked   bool        // its version was asked for since it came
	aways   []time.Time // when a server that ran went away, within restartWindow
}

// claim records that List found no server, and reports whether List
// should start one now; the caller then reports the outcome with done.
func (w *serverWatch) claim(now time.Time, logf func(string, ...any)) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.downSince.IsZero() {
		w.downSince = now
		w.version, w.asked = 0, false
		if w.seen {
			w.aways = append(recent(w.aways, now), now)
			logf("android: the adb server went away; Pinky starts one if none is back within %v", serverAway)
		}
	}
	if w.starting || now.Before(w.next) || (w.seen && now.Sub(w.downSince) < serverAway) {
		return false
	}
	w.starting = true
	return true
}

// recent keeps the times within restartWindow of now.
func recent(ts []time.Time, now time.Time) []time.Time {
	out := ts[:0]
	for _, t := range ts {
		if now.Sub(t) < restartWindow {
			out = append(out, t)
		}
	}
	return out
}

// done records how a start that claim allowed went.
func (w *serverWatch) done(now time.Time, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.starting = false
	w.lastErr = err
	if err == nil {
		w.wait = 0
		w.next = time.Time{}
		return
	}
	w.wait = min(max(2*w.wait, serverRetry), serverRetryMax)
	w.next = now.Add(w.wait)
}

// noADB reports whether the last start found no adb at all.
func (w *serverWatch) noADB() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return !w.downSince.IsZero() && errors.Is(w.lastErr, adb.ErrNoADB)
}

// answered records a server that answers, and reports whether its version
// is still to be asked for (once after it came).
func (w *serverWatch) answered() (askVersion bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seen = true
	w.downSince = time.Time{}
	w.lastErr = nil
	askVersion = !w.asked
	w.asked = true
	return askVersion
}

// setVersion records the protocol version of the server that answers.
func (w *serverWatch) setVersion(v int) {
	w.mu.Lock()
	w.version = v
	w.mu.Unlock()
}

// check is CheckHost's answer, from what the Lists found.
func (w *serverWatch) check(now time.Time) driver.HostCheck {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch {
	case !w.downSince.IsZero() && errors.Is(w.lastErr, adb.ErrNoADB):
		return driver.HostCheck{Missing: hostNoADB,
			Detail: "Pinky found no adb to reach Android phones with: this build of Pinky does not carry its own. Install Android's platform tools, or set " + adb.EnvADB + " to an adb."}
	case !w.downSince.IsZero() && w.lastErr != nil:
		return driver.HostCheck{Missing: hostADBServer,
			Detail: "Android's adb server is not running, and Pinky could not start one: " + w.lastErr.Error()}
	case w.downSince.IsZero() && w.version > 0 && w.version < 41:
		return driver.HostCheck{Missing: hostADBOld,
			Detail: fmt.Sprintf("The adb server that runs is an old one (protocol %d, from Android platform tools older than 29), which cannot pair phones over Wi-Fi. Quit the program that started it, or stop it with `adb kill-server`: Pinky then starts its own.", w.version)}
	}
	w.aways = recent(w.aways, now)
	if n := len(w.aways); n >= restartsMin {
		return driver.HostCheck{Missing: hostADBRestarts,
			Detail: fmt.Sprintf("Android's adb server stopped %d times in the last %v, and its phones disconnected each time: another program restarts it, most often with an adb of another version (an old one on PATH, or one that comes with another tool). Keep one adb, or quit that program.", n, restartWindow)}
	}
	return driver.HostCheck{}
}
