package ios

import (
	"context"
	"time"

	"github.com/DIYgod/pinky/internal/screen"
)

// Naming the app of a read that has no tree. When remoteAXService fails, a
// read falls back to the inspector's walk, whose elements' process names
// the app (observeInspector). The walk can come back empty, when the daemon
// does not answer, or with elements of a process Pinky cannot name; and a
// read after one that showed web content goes without the walk. Such a
// read has only the picture's text. It used to be named from the inspector
// session's app states, which fell back to the home screen when the
// session had heard of no app, as it has not on iOS 27 of an app that was
// in front since before it connected. The gateway then let the read take
// its picture as the home screen's, of whatever was on screen, a blocked
// app included (GitHub issue #248: the daemon stopped answering for over
// two hours, and every read went that way). Those states cannot name the
// app on screen anyway: iOS reports the apps it wakes in the background in
// the foreground too.
//
// So such a read names only the app that the last read able to tell named
// (knewFront): remoteAXService's frontmost app, where screenApp settles it,
// or the home screen app that owns a walk's elements (walkApp). And only
// while
//
//   - that read began less than lastFrontKeep ago;
//   - the inspector session has listened since before it, and answered
//     during this read (its caller knows): a daemon that does not answer
//     reports no app changing either;
//   - the session was told of no other app coming to the foreground since,
//     nor of that app leaving it.
//
// Otherwise the read names no app, the gateway lets it take no picture
// (an app it cannot name is never captured), and it fails with what
// failed. A read named so is partial, and does not renew the record.

// inspector is what a read asks of the accessibility inspector's session
// (axSession); tests give their own.
type inspector interface {
	walk(ctx context.Context, lim walkLimits) ([]axNode, bool, error)
	ping(ctx context.Context) error
	events() (apps []axApp, since time.Time, ok bool)
}

// inspectorReady returns the inspector session, connecting it (axReady).
func (d *Device) inspectorReady(ctx context.Context) (inspector, error) {
	if d.inspect != nil {
		return d.inspect, nil
	}
	a, err := d.axReady(ctx)
	if err != nil {
		return nil, err
	}
	return a, nil
}

// knewFront records the app that a read which began at at named from what
// it read. Reads may overlap: one that began before the recorded one does
// not replace it.
func (d *Device) knewFront(app screen.App, at time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !at.Before(d.knownAt) {
		d.knownApp, d.knownAt = app, at
	}
}

// forgetFront records that a read which began at at found the phone
// locked: the app recorded before is not named again.
func (d *Device) forgetFront(at time.Time) { d.knewFront(screen.App{}, at) }

// knownFront returns the app that a read with no tree, or with none whose
// process Pinky can name, may name (see above), by what the inspector
// session a told; ok is false when there is none. Callers have heard from
// the daemon on a during their read.
func (d *Device) knownFront(a inspector) (screen.App, bool) {
	d.mu.Lock()
	app, at := d.knownApp, d.knownAt
	d.mu.Unlock()
	if app.ID == "" {
		return screen.App{}, false
	}
	apps, since, ok := a.events()
	if !ok || !frontStill(app.ID, at, apps, since, time.Now()) {
		return screen.App{}, false
	}
	return app, true
}

// frontStill reports whether app id, which a read that began at at named,
// may still be on screen at now, by what an inspector session listening
// since since was told of the apps' states.
func frontStill(id string, at time.Time, apps []axApp, since, now time.Time) bool {
	if id == "" || now.Sub(at) >= lastFrontKeep || since.After(at) {
		return false
	}
	for _, a := range apps {
		if !a.at.After(at) {
			continue
		}
		if a.id == id {
			if a.state != appForeground {
				return false // it left
			}
			continue
		}
		if a.state == appForeground && !besideApp(a.id) {
			return false // another app came, the home screen included
		}
	}
	return true
}
