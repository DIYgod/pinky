package android

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DIYgod/pinky/internal/driver/android/companion"
	"github.com/DIYgod/pinky/internal/screen"
)

// The companion app (android/companion) gives a 30 ms element tree, events,
// ACTION_SET_TEXT input and an accessibility settle signal. The driver uses
// it whenever the app is installed: it turns the app's accessibility
// service on at the first Observe or Type, keeps an event subscription
// open while it is on, and turns it off again (restoring the setting
// exactly) when the device closes or after a minute without driver calls.
// Without the app, or when it is too old or does not answer, the driver
// falls back to uiautomator and the keyboard. The two never run together:
// a uiautomator dump unbinds every accessibility service.

const (
	// companionIdle is how long the service stays on without driver calls.
	companionIdle = 60 * time.Second
	// companionMissingRetry is how long to wait before looking for an app
	// that was not installed.
	companionMissingRetry = 5 * time.Minute
	// companionFailRetry is how long to wait before trying an app that
	// failed or did not answer.
	companionFailRetry = 2 * time.Minute
	// maxPendingEvents caps the toasts and notifications kept between two
	// observations.
	maxPendingEvents = 20
)

// companionLink is a device's connection to its companion app.
type companionLink struct {
	mu      sync.Mutex // serializes turning the service on and off
	client  *companion.Client
	on      bool      // Enable succeeded and the subscription runs
	checked bool      // installed was looked up
	have    bool      // the app is installed
	version string    // its version name
	retryAt time.Time // do not try again before this
	reason  string    // why the companion is not used, logged once
	idle    *time.Timer
	lastUse atomic.Int64 // Unix nanoseconds of the last driver call
	sub     *companion.Subscription
	stop    context.CancelFunc // ends the subscription's context

	evMu   sync.Mutex
	last   time.Time      // arrival of the last window or content event
	events []screen.Event // toasts and notifications since the last Observe
	live   bool           // the subscription is delivering
}

// companionClient returns the companion client with its service on, or nil
// when the driver should do without it. It turns the service on at the
// first call after the device opened or went idle.
func (d *Device) companionClient(ctx context.Context) *companion.Client {
	k := &d.comp
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.on {
		k.evMu.Lock()
		live := k.live
		k.evMu.Unlock()
		if live {
			return k.client
		}
		// The subscription ended: the service stopped or was turned off.
		d.companionOffLocked("its event stream ended")
	}
	if time.Now().Before(k.retryAt) {
		return nil
	}
	if k.client == nil {
		k.client = companion.New(adbRunner{c: d.client, serial: d.info.ID})
	}
	if !k.checked {
		version, ok, err := k.client.Installed(ctx)
		if err != nil {
			d.companionUnusableLocked(companionFailRetry, fmt.Sprintf("cannot tell whether it is installed: %v", err))
			return nil
		}
		k.checked, k.have, k.version = true, ok, version
	}
	if !k.have {
		k.checked = false // look again after the wait
		d.companionUnusableLocked(companionMissingRetry, "app.pinky.companion is not installed")
		return nil
	}
	start := time.Now()
	if err := k.client.Enable(ctx); err != nil {
		d.companionUnusableLocked(companionFailRetry, fmt.Sprintf("its service did not start: %v", err))
		return nil
	}
	info, err := k.client.Ping(ctx)
	if err == nil && info.Protocol != companion.ProtocolVersion {
		err = fmt.Errorf("it speaks protocol %d, the driver %d", info.Protocol, companion.ProtocolVersion)
	}
	if err == nil {
		err = d.subscribeLocked()
	}
	if err != nil {
		d.disableLocked()
		d.companionUnusableLocked(companionFailRetry, fmt.Sprintf("version %s: %v", k.version, err))
		return nil
	}
	k.on = true
	k.reason = ""
	d.used()
	if k.idle == nil {
		k.idle = time.AfterFunc(companionIdle, d.companionIdleCheck)
	} else {
		k.idle.Reset(companionIdle)
	}
	d.cfg.logf("android: %s: companion %s on in %v", d.info.ID, info.Version, time.Since(start).Round(time.Millisecond))
	return k.client
}

// used notes a driver call; a minute without one turns the companion off.
func (d *Device) used() { d.comp.lastUse.Store(time.Now().UnixNano()) }

// companionIdleCheck runs when the idle timer fires: it turns the companion
// off after a minute without driver calls, or waits for the rest of it.
func (d *Device) companionIdleCheck() {
	k := &d.comp
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.on {
		return
	}
	if since := time.Since(time.Unix(0, k.lastUse.Load())); since < companionIdle {
		k.idle.Reset(companionIdle - since)
		return
	}
	d.companionOffLocked("a minute without driver calls")
}

// closeCompanion turns the companion off for good, when the device closes.
func (d *Device) closeCompanion() {
	k := &d.comp
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.on {
		d.companionOffLocked("the device closed")
	} else if k.client != nil {
		k.client.Close()
	}
	k.retryAt = time.Now().Add(100 * 365 * 24 * time.Hour)
}

// subscribeLocked starts the event subscription, which outlives the call
// that started it.
func (d *Device) subscribeLocked() error {
	k := &d.comp
	ctx, stop := context.WithCancel(context.Background())
	sub, err := k.client.Events(ctx,
		companion.EventWindow, companion.EventWindows, companion.EventContent,
		companion.EventToast, companion.EventNotification)
	if err != nil {
		stop()
		return fmt.Errorf("subscribe to events: %w", err)
	}
	k.sub, k.stop = sub, stop
	k.evMu.Lock()
	k.live = true
	k.events = nil
	k.last = time.Time{}
	k.evMu.Unlock()
	go d.readCompanionEvents(sub)
	return nil
}

func (d *Device) readCompanionEvents(sub *companion.Subscription) {
	k := &d.comp
	for ev := range sub.C() {
		now := time.Now()
		k.evMu.Lock()
		switch ev.Kind {
		case companion.EventWindow, companion.EventWindows, companion.EventContent, companion.EventDropped:
			k.last = now
		}
		if se, ok := ev.ScreenEvent(); ok {
			k.events = append(k.events, se)
			if len(k.events) > maxPendingEvents {
				k.events = k.events[len(k.events)-maxPendingEvents:]
			}
		}
		k.evMu.Unlock()
	}
	k.evMu.Lock()
	if k.sub == sub {
		k.live = false
	}
	k.evMu.Unlock()
}

// takeEvents returns the toasts and notifications since the last call.
func (k *companionLink) takeEvents() []screen.Event {
	k.evMu.Lock()
	defer k.evMu.Unlock()
	ev := k.events
	k.events = nil
	return ev
}

// lastChange is the arrival of the last window or content event; ok is
// false when no subscription is delivering.
func (k *companionLink) lastChange() (time.Time, bool) {
	k.evMu.Lock()
	defer k.evMu.Unlock()
	return k.last, k.live
}

// companionUnusableLocked records why the companion is not used and when
// to try again, logging each new reason once.
func (d *Device) companionUnusableLocked(wait time.Duration, reason string) {
	k := &d.comp
	k.retryAt = time.Now().Add(wait)
	if reason != k.reason {
		k.reason = reason
		d.cfg.logf("android: %s: not using the companion app: %s; using uiautomator", d.info.ID, reason)
	}
}

// companionFailed turns the companion off after a call failed, so that
// uiautomator can run, and waits before trying it again.
func (d *Device) companionFailed(err error) {
	k := &d.comp
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.on {
		d.companionOffLocked(fmt.Sprintf("a request failed: %v", err))
	}
	d.companionUnusableLocked(companionFailRetry, fmt.Sprintf("a request failed: %v", err))
}

// companionOff turns the companion off, if it is on.
func (d *Device) companionOff(why string) {
	k := &d.comp
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.on {
		d.companionOffLocked(why)
	}
}

func (d *Device) companionOffLocked(why string) {
	k := &d.comp
	d.disableLocked()
	k.on = false
	d.cfg.logf("android: %s: companion off: %s", d.info.ID, why)
}

// disableLocked ends the subscription and restores the accessibility
// setting exactly as Enable found it.
func (d *Device) disableLocked() {
	k := &d.comp
	if k.idle != nil {
		k.idle.Stop()
	}
	if k.sub != nil {
		k.sub.Close()
		k.sub = nil
	}
	if k.stop != nil {
		k.stop()
		k.stop = nil
	}
	k.evMu.Lock()
	k.live = false
	k.evMu.Unlock()
	if k.client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := k.client.Disable(ctx); err != nil {
		d.cfg.logf("android: %s: turning the companion off: %v", d.info.ID, err)
	}
	k.client.Close()
}

// observeCompanion reads the screen from the companion, with the lock and
// keyboard state from the window manager as the uiautomator path has them.
// The two dumpsys queries run as separate shells, in parallel with the
// tree: in one shell they took about 85 ms, the tree about 30.
func (d *Device) observeCompanion(ctx context.Context, c *companion.Client) (*screen.Screen, error) {
	run := func(cmd string) <-chan string {
		ch := make(chan string, 1)
		go func() {
			out, err := d.client.Shell(ctx, d.info.ID, cmd)
			if err != nil {
				d.cfg.logf("android: %s: read state: %v", d.info.ID, err)
			}
			ch <- string(out)
		}()
		return ch
	}
	keyguardc, imec := run(keyguardCmd), run(imeWindowCmd)
	t, err := c.Dump(ctx, companion.DumpOptions{})
	keyguard, ime := <-keyguardc, <-imec
	if err != nil {
		return nil, err
	}
	s := t.Screen(companion.ScreenOptions{})
	s.CapturedAt = c.HostTime(t.Time)
	if locked, ok := parseKeyguard(keyguard); ok {
		s.Locked = locked
	}
	if imeWindowRE.MatchString(ime) {
		s.Keyboard = parseIMEWindowVisible(ime)
	}
	s.App.Name = d.companionAppName(s.App)
	s.Events = d.comp.takeEvents()
	return s, nil
}

// companionAppName names the foreground app from the driver's app list,
// as the uiautomator path does, so headers stay the same across the two.
// The companion's own name may be a window title on the first tree after
// an app opens. The list loads in the background the first time.
func (d *Device) companionAppName(app screen.App) string {
	if name := d.appName(app.ID); name != "" {
		return name
	}
	d.appsMu.Lock()
	loaded := d.apps != nil
	d.appsMu.Unlock()
	if !loaded && d.appsLoading.CompareAndSwap(false, true) {
		go func() {
			defer d.appsLoading.Store(false)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = d.cachedApps(ctx, 0)
		}()
	}
	return app.Name
}

// refusedText reports whether the companion refused a set_text request
// before changing anything, so typing it another way cannot duplicate it.
func refusedText(err error) bool {
	var ce *companion.Error
	if !errors.As(err, &ce) {
		return false
	}
	switch ce.Code {
	case "not_found", "not_editable", "disabled", "unsupported", "action_failed":
		return true
	}
	return false
}

// typeCompanion writes text with the companion: into the focused field, or
// else into the field under the last tap. It returns done=false when the
// companion refused and the caller should type another way.
func (d *Device) typeCompanion(ctx context.Context, c *companion.Client, l *live, text string, append, submit bool) (bool, error) {
	o := companion.SetTextOptions{Append: append, Submit: submit}
	res, err := c.SetText(ctx, companion.Target{}, text, o)
	if refusedText(err) {
		if p, ok := d.recentTap(); ok {
			res, err = c.SetText(ctx, companion.Target{Point: &p}, text, o)
		}
	}
	switch {
	case err == nil:
		if submit && !res.Submitted {
			return true, l.key(keycodeEnter, 0)
		}
		return true, nil
	case refusedText(err):
		d.cfg.logf("android: %s: the companion could not set the text (%v); typing it instead", d.info.ID, err)
		return false, nil
	default:
		// The request may have reached the phone; typing the text again
		// could enter it twice, so report it instead.
		d.companionFailed(err)
		return true, fmt.Errorf("android: set text through the companion: %w", err)
	}
}

// recentTap returns the last tap's point if it was in the last minute.
func (d *Device) recentTap() (screen.Point, bool) {
	d.stateMu.Lock()
	defer d.stateMu.Unlock()
	if d.tapAt.IsZero() || time.Since(d.tapAt) > time.Minute {
		return screen.Point{}, false
	}
	return d.tapPoint, true
}
