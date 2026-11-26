package ui

import (
	"errors"
	"strconv"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/secret"
)

// Unlock without asking (#231): for 1, 4 or 8 hours, Pinky enters a phone's
// saved PIN whenever an agent's step finds the phone locked, rather than
// asking the person to confirm each unlock (internal/gateway/unlockwindow.go).
// It is per phone and off by default. The person turns it on in Settings ›
// Phones or in ⋯ under the mirror, picks how long, and confirms once with
// Touch ID, Windows Hello or the keyring's password, which the gateway asks
// for. While it is on, a strip under the mirror says how long it has left,
// with End, and both switches say it too. The gateway keeps the window
// (DeviceView.UnlockUntil); the client keeps only what the switches show
// while the gateway opens or ends one.

// uwState is unlock without asking's part of the client.
type uwState struct {
	// choosing is the phone whose lengths show, once its switch was turned
	// on, and where is which switch: "settings" or "menu".
	choosing, where string
	busy            map[string]bool // phones whose window is being opened or ended
	want            map[string]bool // what their switch shows meanwhile
	// refused is DeviceView.UnlockRefused as last seen, by phone, to tell
	// when the gateway sets it (noteRefusedPINs).
	refused map[string]bool
}

// windower returns the gateway's unlock windows, nil where the API has
// none, as a client of a gateway elsewhere: it shows no switch.
func (a *app) windower() unlockWindower {
	uw, _ := a.api.(unlockWindower)
	return uw
}

// windowOn reports whether the phone's unlock window is open now.
func windowOn(d gateway.DeviceView) bool { return timeNow().Before(d.UnlockUntil) }

// showsWindow reports whether the phone's page shows the strip of an open
// unlock window under its mirror.
func (a *app) showsWindow(d gateway.DeviceView) bool { return a.windower() != nil && windowOn(d) }

// windowBlocked says why the phone's unlock window cannot be turned on, ""
// when it can: Pinky can unlock the phone from this computer, Settings ›
// Safety › Unlock from this computer is on, and the phone's PIN is saved.
// A phone that refused its saved PIN waits until it is unlocked or its PIN
// changes, as Pinky never enters a refused PIN again by itself: a window
// would enter it at the next locked step.
func (a *app) windowBlocked(d gateway.DeviceView) string {
	st := &a.pin
	id := d.Info.ID
	switch {
	case !d.Features.Unlock:
		return locales.UnlockWindowNoUnlock()
	case !a.settings.UnlockFromComputer:
		return locales.UnlockWindowOff()
	case st.ready && !st.supported:
		return locales.UnlockNotAvailable()
	case !st.saved[id]:
		return locales.UnlockWindowNoPIN()
	case st.wrong[id]:
		return locales.UnlockWindowWrongPIN()
	}
	return ""
}

// windowSwitch is what the phone's Unlock without asking switch at where
// shows: whether it is on, whether the person can flip it, and the line
// under it.
func (a *app) windowSwitch(c *mg.Context, d gateway.DeviceView, where string) (on, enabled bool, line string) {
	id := d.Info.ID
	switch {
	case a.uw.busy[id]:
		if a.uw.want[id] {
			return true, false, locales.UnlockWindowConfirming()
		}
		return false, false, locales.UnlockWindowDesc()
	case windowOn(d):
		left := windowLeft(c, d)
		return true, true, locales.UnlockWindowUntil(d.UnlockUntil.Local().Format("15:04"), left)
	case a.uw.choosing == id && a.uw.where == where:
		return true, true, locales.UnlockWindowDesc()
	}
	if why := a.windowBlocked(d); why != "" {
		return false, false, why
	}
	return false, true, locales.UnlockWindowDesc()
}

// windowLeft says how long the phone's open unlock window has left, in
// minutes rounded up, and has the view built again when that changes.
func windowLeft(c *mg.Context, d gateway.DeviceView) string {
	left := d.UnlockUntil.Sub(timeNow())
	mins := max(1, int((left+time.Minute-1)/time.Minute))
	c.After(left - time.Duration(mins-1)*time.Minute + 50*time.Millisecond)
	return leftWords(mins)
}

// leftWords says mins minutes in hours and minutes: "42 min", "4 h",
// "3 h 12 min".
func leftWords(mins int) string {
	h, m := strconv.Itoa(mins/60), strconv.Itoa(mins%60)
	switch {
	case mins < 60:
		return locales.UnlockWindowMinutes(m)
	case mins%60 == 0:
		return locales.UnlockWindowWholeHours(h)
	}
	return locales.UnlockWindowHours(h, m)
}

// flipWindow follows the person's flip of the phone's switch at where: on
// shows the lengths to pick from there, off ends the open window or puts
// the lengths away.
func (a *app) flipWindow(d gateway.DeviceView, on bool, where string) {
	id := d.Info.ID
	switch {
	case a.uw.busy[id]:
	case on:
		if !windowOn(d) && a.windowBlocked(d) == "" {
			a.uw.choosing, a.uw.where = id, where
		}
	case windowOn(d):
		a.endWindow(d)
	case a.uw.choosing == id:
		a.uw.choosing = ""
	}
}

// openWindow has the gateway ask the person to confirm, and open the
// phone's unlock window for length once they do.
func (a *app) openWindow(d gateway.DeviceView, length time.Duration) {
	uw := a.windower()
	id, name := d.Info.ID, d.Info.Name
	if uw == nil || a.uw.busy[id] {
		return
	}
	a.uw.choosing = ""
	a.uw.busy[id], a.uw.want[id] = true, true
	go func() {
		err := uw.OpenUnlockWindow(id, length)
		// The phone's view first (posts run in order), so that its switch
		// does not show off between the two.
		a.refresh(true, false, false)
		a.post(func() {
			delete(a.uw.busy, id)
			delete(a.uw.want, id)
			if err != nil {
				if msg := windowError(err, name); msg != "" {
					a.toasts = append(a.toasts, msg)
				}
				return
			}
			until := timeNow().Add(length)
			if dv := a.device(id); dv != nil && windowOn(*dv) {
				until = dv.UnlockUntil
			}
			a.toasts = append(a.toasts, locales.UnlockWindowOnToast(until.Local().Format("15:04"), name))
		})
	}()
}

// endWindow ends the phone's unlock window.
func (a *app) endWindow(d gateway.DeviceView) {
	uw := a.windower()
	id, name := d.Info.ID, d.Info.Name
	if uw == nil || a.uw.busy[id] {
		return
	}
	a.uw.busy[id], a.uw.want[id] = true, false
	go func() {
		err := uw.EndUnlockWindow(id)
		a.refresh(true, false, false)
		a.post(func() {
			delete(a.uw.busy, id)
			delete(a.uw.want, id)
			if err != nil {
				a.toasts = append(a.toasts, locales.ToastCouldNot(locales.UnlockWindowEndWhat(name), err.Error()))
				return
			}
			a.toasts = append(a.toasts, locales.UnlockWindowEndedToast(name))
		})
	}()
}

// windowError says in plain words why the phone's unlock window did not
// open, nothing when the person did not confirm.
func windowError(err error, name string) string {
	switch {
	case err == nil, errors.Is(err, secret.ErrCancelled):
		return ""
	case errors.Is(err, gateway.ErrUnlockWindowEnded):
		return locales.UnlockWindowChanged(name)
	case errors.Is(err, gateway.ErrNoUnlock):
		return locales.UnlockWindowFailed(name, locales.UnlockWindowNoUnlock())
	case errors.Is(err, secret.ErrNotFound), errors.Is(err, secret.ErrMicrosoftAccount),
		errors.Is(err, secret.ErrAccountRestricted), errors.Is(err, secret.ErrUnsupported),
		errors.Is(err, driver.ErrOffline):
		return unlockError(err, name)
	}
	return locales.UnlockWindowFailed(name, err.Error())
}

// windowEnder returns what ends the phone's unlock window before its PIN
// changes or goes, which runs off the main thread, and whether a window
// was open: the gateway would go on entering the PIN it took when the
// window opened. The mark of a PIN the phone refused goes with it. It
// returns nil when there is nothing to end.
func (a *app) windowEnder(id string) (end func(), open bool) {
	uw := a.windower()
	d := a.device(id)
	if uw == nil || d == nil || !(windowOn(*d) || a.uw.busy[id] || d.UnlockRefused) {
		return nil, false
	}
	return func() { _ = uw.EndUnlockWindow(id) }, windowOn(*d)
}

// noteRefusedPINs takes a phone's refusal of the PIN its unlock window
// entered (DeviceView.UnlockRefused) as a refusal of an unlock from here
// (pinState.wrong): the unlock card says so and offers Try again rather
// than Unlock, and the window does not open again, until the phone is
// unlocked or its PIN changes. Only a new refusal counts, so that a PIN
// saved since is not taken for refused again.
func (a *app) noteRefusedPINs() {
	for _, d := range a.devices {
		id := d.Info.ID
		if !d.UnlockRefused {
			delete(a.uw.refused, id)
			continue
		}
		if !a.uw.refused[id] {
			a.uw.refused[id] = true
			a.pin.wrong[id] = true
		}
	}
}

// windowSettingsRow is the phone's Unlock without asking row in Settings ›
// Phones: its switch, and the lengths to pick from once it is turned on.
// Phones that Pinky cannot unlock from here leave it out.
func (a *app) windowSettingsRow(c *mg.Context, d gateway.DeviceView) {
	if a.windower() == nil || !(d.Features.Unlock || windowOn(d)) {
		return
	}
	id := d.Info.ID
	on, enabled, line := a.windowSwitch(c, d, "settings")
	title := locales.UnlockWindowTitle()
	groupRow(c, mg.Column(c.Key("uw-"+id)).Gap(sp3), false).Children(func() {
		mg.Row(c).Gap(sp4).AlignItems(mg.Center).Children(func() {
			words := mg.Column(c)
			if enabled {
				// Clicking the words flips the switch, as in the other rows.
				words.Cursor(mg.CursorPointer)
				if words.Clicked() {
					a.flipWindow(d, !on, "settings")
				}
			}
			rowWords(c, words, title, line)
			if a.uw.busy[id] {
				spinner(c)
			}
			sw := on
			if toggle(c.Key("uw-"+id+"-switch"), &sw, title).Disabled(!enabled).Changed() {
				a.flipWindow(d, sw, "settings")
			}
		})
		if a.uw.choosing == id && a.uw.where == "settings" {
			a.windowLengths(c, d)
		}
	})
}

// windowMenuRow is the phone's Unlock without asking switch in ⋯ under the
// mirror, after Work in the background, with the lengths to pick from
// under it once it is turned on.
func (a *app) windowMenuRow(c *mg.Context, d gateway.DeviceView) {
	if a.windower() == nil {
		return
	}
	p := pal(c)
	id := d.Info.ID
	on, enabled, line := a.windowSwitch(c, d, "menu")
	title := locales.UnlockWindowTitle()
	mg.Column(c.Key("uw-menu-"+id)).Gap(sp2).Padding(sp2, 10).Children(func() {
		mg.Row(c).Gap(sp3).AlignItems(mg.Center).Children(func() {
			ink := p.ink
			if !enabled && !a.uw.busy[id] {
				ink = p.ink3
			}
			menuText(c, icUnlock, title, line, ink)
			if a.uw.busy[id] {
				spinner(c)
			}
			sw := on
			if toggle(c.Key("uw-pop-"+id), &sw, title).Disabled(!enabled).Changed() {
				a.flipWindow(d, sw, "menu")
			}
		})
		if a.uw.choosing == id && a.uw.where == "menu" {
			mg.Column(c).Margin(0, 0, 0, menuIndent).Children(func() { a.windowLengths(c, d) })
		}
	})
}

// windowLengths offers the lengths an unlock window may have, each of which
// has the person confirm, and Cancel, which leaves the switch off.
func (a *app) windowLengths(c *mg.Context, d gateway.DeviceView) {
	p := pal(c)
	mg.Column(c).Gap(sp2).Children(func() {
		mg.Row(c).Gap(sp3).AlignItems(mg.Center).Children(func() {
			mg.Text(c, locales.UnlockWindowHowLong()).FontSize(fsSmall).TextColor(p.ink2).Grow(1).MinWidth(0)
			if quietButton(c, locales.UnlockWindowCancel()).Clicked() {
				a.uw.choosing = ""
			}
		})
		mg.Row(c).Gap(sp2).AlignItems(mg.Center).Wrap().Children(func() {
			for _, l := range gateway.UnlockWindowLengths {
				n := int(l / time.Hour)
				if button(c.Key(n), locales.UnlockWindowFor(n), btnOpt{small: true}).Clicked() {
					a.openWindow(d, l)
				}
			}
		})
	})
}

// windowStripHeight is the height of the strip under the mirror while the
// phone's unlock window is open: End's, with the strip's padding and
// border.
const windowStripHeight = smallButtonHeight + 2*sp1 + 2

// windowStrip is the strip under the mirror while the phone's unlock window
// is open, no wider than width: how long it has left, and End.
func (a *app) windowStrip(c *mg.Context, d gateway.DeviceView, width float32) {
	p := pal(c)
	id, name := d.Info.ID, d.Info.Name
	text := locales.UnlockWindowBanner(windowLeft(c, d))
	strip := mg.Row(c.Key("uw-strip-"+id)).Height(windowStripHeight).MaxWidth(width).Shrink(0).
		Padding(sp1, sp1, sp1, sp3).Gap(sp2).AlignItems(mg.Center).Radius(rCard).Background(p.card).Border(1, p.line).
		Transition(bannerIn())
	strip.Children(func() {
		mg.Icon(c, icUnlock).FontSize(iconBody).TextColor(p.accentInk)
		mg.Text(c, text).FontSize(fsSmall).TextColor(p.ink2).SingleLine().Shrink(1).MinWidth(0).Tooltip(text)
		end := button(c, locales.UnlockWindowEnd(), btnOpt{small: true}).Label(locales.UnlockWindowEndLabel(name)).Disabled(a.uw.busy[id])
		if end.Clicked() {
			a.endWindow(d)
		}
	})
}
