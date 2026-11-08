package ui

import (
	"context"
	"errors"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// iPhones and iPads over Wi-Fi: Settings › Phones' switch, and the phones
// Pinky found on Wi-Fi but has not used (driver.SetupConfirmWiFi), which
// wait for the person to choose Use or Ignore, on their page, in Settings ›
// Phones and in Add phone › iPhone. Until then Pinky does nothing on them
// and agents do not see them.

type (
	// wifiSetter turns using iPhones and iPads over Wi-Fi on or off
	// (gateway.SetWiFi).
	wifiSetter interface {
		SetWiFi(on bool)
	}
	// foundChooser answers a phone found on Wi-Fi (gateway.UseFoundPhone,
	// IgnoreFoundPhone).
	foundChooser interface {
		UseFoundPhone(deviceID string) error
		IgnoreFoundPhone(ctx context.Context, deviceID string) error
	}
)

// foundState is the person's choices on their way to the gateway, by phone.
type foundState struct {
	busy map[string]bool
}

// foundOnWiFi reports whether a phone waits for the person to say whether
// it is theirs.
func foundOnWiFi(d gateway.DeviceView) bool {
	return d.Status != gateway.DeviceOffline && d.Info.State == driver.StateNeedsSetup && d.Info.Setup == driver.SetupConfirmWiFi
}

// lockedOnWiFi reports whether a phone waits to be unlocked on Wi-Fi: Pinky
// connects to it only once it is unlocked, so as not to wake its screen
// (gateway: lockedOnWiFi). Without an agent waiting for it, the gateway
// shows it at rest, offline, "Locked on Wi-Fi".
func lockedOnWiFi(d gateway.DeviceView) bool {
	return d.Info.State == driver.StateNeedsSetup && d.Info.Setup == driver.SetupUnlock && d.Info.Connection == "wifi"
}

// applyWiFi passes Settings › Phones' switch to the gateway.
func (a *app) applyWiFi(on bool) {
	if ws, ok := a.api.(wifiSetter); ok {
		a.setq.push("wifi", func() { ws.SetWiFi(on) })
	}
}

func (a *app) foundBusy(id string) bool { return a.found.busy[id] }

func (a *app) setFoundBusy(id string, on bool) {
	if a.found.busy == nil {
		a.found.busy = map[string]bool{}
	}
	if on {
		a.found.busy[id] = true
	} else {
		delete(a.found.busy, id)
	}
}

// useFound tells the gateway the phone is the person's: Pinky sets it up
// and connects to it.
func (a *app) useFound(d gateway.DeviceView) {
	fc, ok := a.api.(foundChooser)
	id, name := d.Info.ID, d.Info.Name
	if !ok || a.foundBusy(id) {
		return
	}
	a.setFoundBusy(id, true)
	go func() {
		err := fc.UseFoundPhone(id)
		a.post(func() {
			a.setFoundBusy(id, false)
			if err != nil {
				a.toasts = append(a.toasts, locales.FoundUseFailed(name, err.Error()))
			}
		})
	}()
}

// ignoreFound tells the gateway to leave the phone alone: it goes off the
// list until it is plugged in.
func (a *app) ignoreFound(d gateway.DeviceView) {
	fc, ok := a.api.(foundChooser)
	id, name := d.Info.ID, d.Info.Name
	if !ok || a.foundBusy(id) {
		return
	}
	a.setFoundBusy(id, true)
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		err := fc.IgnoreFoundPhone(ctx, id)
		a.post(func() {
			a.setFoundBusy(id, false)
			if err != nil && !errors.Is(err, gateway.ErrUnknownDevice) {
				a.toasts = append(a.toasts, locales.FoundIgnoreFailed(name, err.Error()))
				return
			}
			a.forgetPhone(id, false)
			a.toasts = append(a.toasts, locales.FoundIgnored(name))
		})
	}()
}

// foundButtons draws Use and Ignore for a phone found on Wi-Fi.
func (a *app) foundButtons(c *mg.Context, d gateway.DeviceView, small bool) {
	busy := a.foundBusy(d.Info.ID)
	_, can := a.api.(foundChooser)
	mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
		use := button(c.Key("found-use-"+d.Info.ID), locales.FoundUse(), btnOpt{kind: btnPrimary, small: small}).
			Label(locales.FoundUseLabel(d.Info.Name)).Disabled(busy || !can)
		if use.Clicked() {
			a.useFound(d)
		}
		ignore := button(c.Key("found-ignore-"+d.Info.ID), locales.FoundIgnore(), btnOpt{small: small}).
			Label(locales.FoundIgnoreLabel(d.Info.Name)).Disabled(busy || !can)
		if ignore.Clicked() {
			a.ignoreFound(d)
		}
		if busy {
			spinner(c)
		}
	})
}

// foundPage is the Phone screen of a phone found on Wi-Fi: what it is, why
// Pinky waits, and Use and Ignore.
func (a *app) foundPage(c *mg.Context, d gateway.DeviceView) {
	p := pal(c)
	mg.Column(c).AlignItems(mg.Center).Gap(10).MaxWidth(440).Role(mg.RoleStatus).Children(func() {
		mg.Icon(c, icWifi).FontSize(28).TextColor(p.accentInk)
		mg.Text(c, locales.FoundTitle(d.Info.Name)).FontSize(16).FontWeight(600).TextAlign(mg.Center)
		if line := foundLine(d); line != "" {
			mg.Text(c, line).FontSize(13).TextColor(p.ink3)
		}
		mg.Text(c, locales.FoundDetail()).FontSize(13.5).TextColor(p.ink2).TextAlign(mg.Center)
		mg.Box(c).Height(4)
		a.foundButtons(c, d, false)
		mg.Text(c, locales.FoundUseNote()).FontSize(12.5).TextColor(p.ink3).TextAlign(mg.Center)
		mg.Text(c, locales.FoundIgnoreNote()).FontSize(12.5).TextColor(p.ink3).TextAlign(mg.Center)
	})
}

// foundLine is a found phone's model and system: "iPad Pro 11-inch (3rd
// generation) · iOS 27.0.1".
func foundLine(d gateway.DeviceView) string {
	s := d.Info.Model
	if d.Info.OSVersion != "" {
		if s != "" {
			s += " · "
		}
		s += "iOS " + d.Info.OSVersion
	}
	if s == d.Info.Name {
		return ""
	}
	return s
}

// foundPhones are the phones found on Wi-Fi, in the gateway's order.
func (a *app) foundPhones() []gateway.DeviceView {
	var out []gateway.DeviceView
	for _, d := range a.devices {
		if foundOnWiFi(d) {
			out = append(out, d)
		}
	}
	return out
}

// foundBox lists the phones found on Wi-Fi above Add phone › iPhone's
// steps, each with Use and Ignore.
func (a *app) foundBox(c *mg.Context) {
	found := a.foundPhones()
	if len(found) == 0 {
		return
	}
	p := pal(c)
	mg.Column(c).Padding(14, 16).Radius(12).Background(p.bg2).Gap(10).Label(locales.AddPhoneIPhoneFoundTitle()).Children(func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Icon(c, icWifi).FontSize(16).TextColor(p.accentInk)
			mg.Text(c, locales.AddPhoneIPhoneFoundTitle()).FontSize(15).FontWeight(650)
		})
		guideText(c, locales.AddPhoneIPhoneFoundDetail())
		for _, d := range found {
			mg.Row(c.Key("found-"+d.Info.ID)).Gap(12).AlignItems(mg.Center).Padding(4, 0, 0, 0).Children(func() {
				mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					mg.Text(c, d.Info.Name).FontWeight(600)
					if line := foundLine(d); line != "" {
						mg.Text(c, line).FontSize(13).TextColor(p.ink2)
					}
				})
				a.foundButtons(c, d, true)
			})
		}
		guideNote(c, locales.FoundIgnoreNote())
	})
}
