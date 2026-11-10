package gateway

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
)

// Connections chosen for one task. An iPhone on iOS 27 left on Automatic
// (Settings › Phones) runs over CoreDevice, which streams its screen, so
// apps that refuse screen sharing do not run while an agent works there.
// When the phone has the WebDriverAgent helper too, a task can move to the
// helper for such apps and back afterwards, without the person changing
// the setting:
//
//   - the agent asks for it: phone_connect's connection "helper", or
//     "coredevice" to go back;
//   - Pinky asks for it: the task's app list names, or phone_launch opens,
//     an app known to refuse screen sharing (refusesSharing).
//
// The move is the same reconnect as a change in Settings › Phones
// (SetConnection), but nothing is saved: the phone goes back to the
// person's choice when the task ends (switchBackAfter later, so tasks in a
// row stay on the helper), when the person changes the setting, or when
// Pinky restarts. A connection the person chose by hand is kept: tasks ask
// in vain, and are told so.

// Ways a task may run on, as the provider names them.
const (
	wayCoreDevice = "coredevice"
	wayHelper     = "wda"
)

// switchWait bounds how long a call waits for the phone to come back over
// the other connection: the helper starts in about 5 s, discovery and the
// first read add a few more.
const switchWait = 60 * time.Second

// switchBackAfter is how long a phone stays on the helper after the task
// that moved it there ended, so that the next task (the next bank in a
// weekly round) does not switch back and forth.
var switchBackAfter = 30 * time.Second

// TaskConnection normalizes phone_connect's connection argument: "" for
// automatic, wayHelper or wayCoreDevice. ok is false for a word it does not
// know.
func TaskConnection(s string) (way string, ok bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "auto", "automatic":
		return "", true
	case "helper", "wda", "webdriveragent":
		return wayHelper, true
	case "coredevice", "core_device", "core-device":
		return wayCoreDevice, true
	}
	return "", false
}

// sharingRefusers are apps known to refuse to run while the screen is
// shared (UIScreen.isCaptured), by bundle id and by the names people and
// the phone use for them, lowercased. Seen in the field:
// docs/notes/field-test-iphone.md item 5.
var sharingRefusers = map[string]string{
	"com.dbs.sg.dbsmbanking": "DBS digibank",
	"digibank":               "DBS digibank",
	"dbs digibank":           "DBS digibank",
	"dbs":                    "DBS digibank",
	"cmb.pb":                 "China Merchants Bank",
	"招商银行":                   "China Merchants Bank",
	"china merchants bank":   "China Merchants Bank",
	"cmb":                    "China Merchants Bank",
}

// refusesSharing reports whether app (an id or a name, as an agent passes
// it) is known to refuse screen sharing, and its name.
func refusesSharing(app string) (string, bool) {
	name, ok := sharingRefusers[strings.ToLower(strings.TrimSpace(app))]
	return name, ok
}

// firstRefuser is the first app of list known to refuse screen sharing.
func firstRefuser(list []string) (string, bool) {
	for _, a := range list {
		if name, ok := refusesSharing(a); ok {
			return name, true
		}
	}
	return "", false
}

// switchingLine is the status line of a phone moving to another
// connection for a task. The caller holds g.mu.
func switchingLine(d *device) string {
	if d.switching == wayHelper {
		return "Switching to the helper"
	}
	return "Switching to CoreDevice"
}

// useConnection moves the task's phone to way (wayHelper, wayCoreDevice,
// or "" for the person's choice) for the task, when it can. why names the
// app that needs it, "" when the agent asked. It returns a result to
// return instead of going on (the phone waits for the person, such as for
// the passcode iOS asks before the helper starts), or a note for the
// agent, "" when there is nothing to say.
func (g *Gateway) useConnection(ctx context.Context, l *lease, way, why string) (*Result, string) {
	d := l.dev
	g.mu.Lock()
	cc, ok := d.provider.(driver.ConnectionChooser)
	platform, backend, online := d.info.Platform, d.info.Backend, d.online
	chosen, moved := g.chosen[d.id], d.taskWay
	if d.switchBack != nil {
		// A task uses the phone again: it stays where it is for now.
		d.switchBack.Stop()
		d.switchBack = nil
	}
	g.mu.Unlock()
	asked := why == ""
	if !ok || platform != driver.IOS {
		if asked && way != "" {
			return nil, fmt.Sprintf("%s connects only one way, so the connection you asked for changes nothing.", d.name())
		}
		return nil, ""
	}
	if chosen != "" {
		if asked && way != "" && way != chosen {
			return nil, fmt.Sprintf("The person chose to connect %s through %s in Settings › Phones, so Pinky keeps that connection for every task. Ask them to choose Automatic if tasks should switch.",
				d.name(), wayName(chosen))
		}
		return nil, ""
	}
	if !online {
		return nil, "" // it is away; gate says so
	}
	switch {
	case way == wayHelper && backend == wayHelper:
		return nil, "" // already there, by the person's choice or an earlier task
	case way != wayHelper && moved == "":
		if asked && way == wayCoreDevice && backend != wayCoreDevice {
			return nil, fmt.Sprintf("%s connects through %s on its iOS version, not CoreDevice.", d.name(), wayName(backend))
		}
		return nil, ""
	case way != wayHelper:
		// Back to the person's choice.
		if res := g.switchTo(ctx, d, cc, ""); res != nil {
			return res, ""
		}
		return nil, "Pinky moved the phone back to CoreDevice."
	}
	// To the helper: only when it is installed.
	if c, ok := connectionWay(cc.Connections(ctx, d.id), wayHelper); !ok || !c.Available {
		reason := "the WebDriverAgent helper is not installed on it"
		if ok && c.Detail != "" {
			reason = strings.TrimSuffix(c.Detail, ".")
		}
		note := fmt.Sprintf("Pinky could not move %s to the WebDriverAgent helper: %s.", d.name(), reason)
		if why != "" {
			note += fmt.Sprintf(" %s refuses to run while the screen is shared, which CoreDevice does: tell the person it needs the helper installed (Settings › Phones).", why)
		}
		return nil, note
	}
	g.log.Info("task connection", "device", d.id, "to", wayHelper, "for", why)
	if res := g.switchTo(ctx, d, cc, wayHelper); res != nil {
		return res, ""
	}
	g.mu.Lock()
	now, there := d.info.Backend, d.online
	g.mu.Unlock()
	if !there || now != wayHelper {
		// It did not come up on the helper: back to the person's choice.
		detail := "the helper did not start"
		g.mu.Lock()
		if d.info.Detail != "" {
			detail = strings.TrimSuffix(d.info.Detail, ".")
		}
		g.mu.Unlock()
		g.log.Warn("task connection: the helper did not start", "device", d.id, "detail", detail)
		if res := g.switchTo(ctx, d, cc, ""); res != nil {
			return res, ""
		}
		note := fmt.Sprintf("Pinky could not move %s to the WebDriverAgent helper (%s), so the task stays on CoreDevice.", d.name(), detail)
		if why != "" {
			note += fmt.Sprintf(" %s may refuse to run while the screen is shared.", why)
		}
		return nil, note
	}
	note := "Pinky moved this task to the WebDriverAgent helper, which shares no screen"
	if why != "" {
		note += fmt.Sprintf(", because %s refuses to run while the screen is shared", why)
	}
	return nil, note + ". Reading the screen is slower there (several seconds on a home screen). The phone goes back to CoreDevice after the task."
}

// connectionWay finds one way in what a ConnectionChooser says.
func connectionWay(cs driver.Connections, id string) (driver.Connection, bool) {
	for _, c := range cs.Ways {
		if c.ID == id {
			return c, true
		}
	}
	return driver.Connection{}, false
}

// wayName is how agents read a connection's name.
func wayName(way string) string {
	switch way {
	case wayHelper:
		return "the WebDriverAgent helper"
	case wayCoreDevice:
		return "CoreDevice"
	case "accessibility":
		return "accessibility only (no helper)"
	}
	return way
}

// switchTo reconnects a phone over way for a task ("" for the person's
// choice), without saving it, and waits until it is back, waits for the
// person, or switchWait passed. It returns device_needs_setup when the
// phone waits for the person.
func (g *Gateway) switchTo(ctx context.Context, d *device, cc driver.ConnectionChooser, way string) *Result {
	if err := cc.SetConnection(d.id, way); err != nil {
		g.log.Warn("task connection", "device", d.id, "way", way, "err", err)
		return nil
	}
	target := way
	if target == "" {
		target = wayCoreDevice
	}
	g.mu.Lock()
	d.taskWay = way
	if !d.online && d.info.State == driver.StateOffline {
		// Away: it comes back the new way by itself.
		g.mu.Unlock()
		return nil
	}
	d.switching = target
	d.moving = time.Now().Add(switchWait) // calls wait while it reconnects
	g.mu.Unlock()
	why := "switching to CoreDevice"
	if way == wayHelper {
		why = "switching to the helper"
	}
	g.setOffline(d, why)
	go func() {
		ctx, cancel := context.WithTimeout(g.ctx, reconnectTimeout)
		defer cancel()
		g.discover(ctx)
	}()
	g.mu.Lock()
	defer g.mu.Unlock()
	for g.waitMovingLocked(ctx, d) {
	}
	d.switching = ""
	d.moving = time.Time{}
	g.emit(Event{Kind: EventDevices, DeviceID: d.id})
	if waitingLocked(d) {
		return setupResultLocked(d, fmt.Sprintf("Pinky is moving %s to %s for this task, and the phone waits for a step first. Nothing was done.", d.name(), wayName(target)))
	}
	return nil
}

// switchBackLater puts a phone a task moved to another connection back on
// the person's choice, switchBackAfter after its task ended, unless a task
// uses it by then. The caller holds g.mu.
func (g *Gateway) switchBackLater(d *device) {
	if d.taskWay == "" || d.removed {
		return
	}
	if d.switchBack != nil {
		d.switchBack.Stop()
	}
	d.switchBack = time.AfterFunc(switchBackAfter, func() {
		g.mu.Lock()
		if d.lease != nil || d.taskWay == "" || d.removed || g.closed {
			g.mu.Unlock()
			return
		}
		cc, ok := d.provider.(driver.ConnectionChooser)
		d.switchBack = nil
		g.mu.Unlock()
		if !ok {
			return
		}
		g.log.Info("task connection: back to the person's choice", "device", d.id)
		ctx, cancel := context.WithTimeout(g.ctx, switchWait)
		defer cancel()
		g.switchTo(ctx, d, cc, "")
	})
}

// forgetTaskWay drops what tasks changed about a phone's connection, when
// the person chooses one in Settings › Phones. The caller holds g.mu.
func forgetTaskWay(d *device) {
	d.taskWay, d.switching = "", ""
	if d.switchBack != nil {
		d.switchBack.Stop()
		d.switchBack = nil
	}
}

// withNote adds a note for the agent to a result.
func withNote(res *Result, note string) *Result {
	if note == "" || res == nil {
		return res
	}
	res.Message = strings.TrimSpace(res.Message + " " + note)
	return res
}

// taskConnection moves a new task's phone the way phone_connect asked
// (way, as TaskConnection reads it), or to the helper when the task's app
// list names an app that refuses screen sharing. A result it returns is
// the call's.
func (g *Gateway) taskConnection(ctx context.Context, l *lease, way string, apps []string) (*Result, string) {
	why := ""
	if way == "" {
		name, ok := firstRefuser(apps)
		if !ok {
			return nil, ""
		}
		way, why = wayHelper, name
	}
	res, note := g.useConnection(ctx, l, way, why)
	if res != nil {
		g.mu.Lock()
		res.Task = l.task.id
		g.mu.Unlock()
		return g.finish(l, res), ""
	}
	return nil, note
}

// launchConnection moves the task to the helper before phone_launch opens
// an app that refuses screen sharing.
func (g *Gateway) launchConnection(ctx context.Context, l *lease, a Action) (*Result, string) {
	name, ok := refusesSharing(a.App)
	if !ok {
		return nil, ""
	}
	return g.useConnection(ctx, l, wayHelper, name)
}
