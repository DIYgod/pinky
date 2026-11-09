package ui

import (
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// harmonyBeingAdded finds a native HarmonyOS phone without confusing it
// with an Android-based Huawei phone. A phone already found before the
// wizard opened still explains why the Huawei/ADB steps cannot work.
func (a *app) harmonyBeingAdded() *gateway.DeviceView {
	if a.droid.brand != "" && a.droid.brand != "huawei" && a.droid.brand != "other" {
		return nil
	}
	if d := a.device(a.add.focus); d != nil && d.Info.Platform == driver.HarmonyOS {
		return d
	}
	if d := a.add.fresh(a, driver.HarmonyOS); d != nil && d.Info.State != driver.StateOffline {
		return d
	}
	if a.droid.brand == "huawei" {
		for i := range a.devices {
			d := &a.devices[i]
			if d.Info.Platform == driver.HarmonyOS && d.Info.State != driver.StateOffline {
				return d
			}
		}
	}
	return nil
}

// addHarmony replaces ADB toggles, Companion installation, and the
// indefinite Android search with the HDC phone's actual setup state.
func (a *app) addHarmony(c *mg.Context, d gateway.DeviceView) {
	p := pal(c)
	statusBox(c, func() {
		if d.Info.State == driver.StateOffline {
			mg.Icon(c, icAlert).FontSize(16).TextColor(p.amberDot)
			mg.Text(c, locales.PhoneOffline(d.Info.Name)).Grow(1).MinWidth(0)
		} else {
			mg.Icon(c, icCheck).FontSize(16).TextColor(p.green)
			mg.Text(c, locales.AddPhoneFound(d.Info.Name)).Grow(1).MinWidth(0)
		}
	})
	if d.Info.State == driver.StateOffline {
		guideNote(c, d.Info.Detail)
	} else {
		guideProblem(c, a.setupStep(d).text, "")
	}
	if d.Info.State == driver.StateUnauthorized {
		guideNote(c, locales.HarmonyHandshakeNote())
	}
	mg.Row(c).Children(func() {
		if button(c, locales.AddPhoneDone(), btnOpt{kind: btnPrimary}).Clicked() {
			a.selectPhone(d.Info.ID)
			a.add.step = ""
		}
	})
}
