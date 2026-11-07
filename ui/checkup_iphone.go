package ui

import (
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// The checkup's words for iPhones: what the computer lacks (usbmuxd), the
// step a phone that is not online waits for (Info.Setup, which the
// gateway gives the "authorized" item as its fix), and the items of the
// iOS driver's checkup. Fixes that are steps of Add phone › iPhone get a
// link to those steps.

// helperDocs is how to install the WebDriverAgent helper.
const helperDocs = "https://github.com/DIYgod/pinky/blob/main/docs/notes/ios.md#installing-the-webdriveragent-helper"

// iphoneCheckTitle names the items only iPhones have; "" for others.
func iphoneCheckTitle(id string) string {
	switch id {
	case driver.CheckDeveloperMode:
		return locales.CheckDeveloperModeTitle()
	case driver.CheckDiskImage:
		return locales.CheckDiskImageTitle()
	case driver.CheckHelper:
		return locales.CheckHelperTitle()
	}
	return ""
}

// iphoneCheckWords puts an iPhone's item into words; ok is false for
// items the shared words say well enough.
func (a *app) iphoneCheckWords(d gateway.DeviceView, ck driver.Check) (w checkWords, ok bool) {
	pass := ck.Status == driver.CheckPass
	w = checkWords{title: checkTitle(ck.ID), detail: true}
	switch ck.ID {
	case driver.CheckHost:
		switch ck.Fix {
		case "apple_devices":
			w.text = locales.AddPhoneIPhoneStepDriverDetail()
		case "usbmuxd":
			w.text = locales.AddPhoneIPhoneStepUsbmuxdDetail()
		case "usbmuxd_stopped":
			w.text = locales.AddPhoneIPhoneUsbmuxdStopped()
		default:
			return w, false
		}
		return w, true
	case driver.CheckAuthorized:
		if pass {
			// An iPhone trusts the computer; it has no USB debugging.
			w.title, w.text, w.detail = locales.AddPhoneIPhoneStepTrust(), locales.CheckTrustedPass(), false
			return w, true
		}
		if ck.Status == driver.CheckSkip {
			return w, false
		}
		w.title, w.text, w.note = setupWords(d, ck.Fix)
		if w.text == "" {
			return w, false
		}
		// Steps in the person's words say it all, except why a mount
		// failed.
		w.detail = ck.Fix == driver.SetupDiskImage
		return w, true
	case driver.CheckDeveloperMode:
		if pass {
			w.text, w.detail = locales.CheckDeveloperModePass(), false
		} else {
			w.text, w.note = locales.AddPhoneIPhoneStepDevModeDetail(), locales.AddPhoneIPhoneStepDevModeNote()
		}
		return w, true
	case driver.CheckDiskImage:
		switch {
		case pass:
			w.text = locales.CheckDiskImagePass()
			w.detail = false
		case ck.Fix == driver.SetupNoDiskImage:
			w.text = locales.CheckDiskImageNoCopy()
			w.detail = false
		default:
			w.text = locales.CheckDiskImageMissing()
		}
		return w, true
	case driver.CheckHelper:
		if pass {
			w.text, w.detail = locales.CheckHelperPass(), false
		} else {
			w.text, w.detail = locales.CheckHelperMissing(), false
		}
		return w, true
	case driver.CheckInput:
		switch {
		case pass:
			w.text, w.detail = locales.CheckInputIPhonePass(), false
		case ck.Fix == driver.FixInstallHelper:
			w.text, w.detail = locales.CheckInputIPhoneNoHelper(), false
		default:
			return w, false
		}
		return w, true
	}
	return w, false
}

// setupWords says an iPhone's setup step (Info.Setup): a title, what to do
// and a note.
func setupWords(d gateway.DeviceView, step string) (title, text, note string) {
	switch step {
	case driver.SetupTrust:
		return locales.AddPhoneIPhoneStepTrust(), locales.AddPhoneIPhoneStepTrustDetail(), ""
	case driver.SetupDeveloperMode:
		return locales.CheckDeveloperModeTitle(), locales.AddPhoneIPhoneStepDevModeDetail(), locales.AddPhoneIPhoneStepDevModeNote()
	case driver.SetupUnlock:
		return locales.CheckDiskImageTitle(), locales.AddPhoneIPhoneStepUnlock(), ""
	case driver.SetupPreparing:
		return locales.CheckDiskImageTitle(), locales.AddPhoneIPhoneStepPreparing(), ""
	case driver.SetupNoDiskImage:
		text := locales.AddPhoneIPhoneNoImageMac()
		if guideOS != "darwin" {
			text = locales.AddPhoneIPhoneNoImageCopy()
		}
		return locales.AddPhoneIPhoneNoImageTitle(), text, locales.CheckDiskImageDownloadNote()
	case driver.SetupDiskImage:
		return locales.CheckDiskImageTitle(), locales.AddPhoneIPhoneStuck(), locales.AddPhoneIPhoneRetrying()
	case driver.SetupUpdate:
		return locales.AddPhoneIPhoneTooOldTitle(d.Info.Name), locales.AddPhoneIPhoneTooOld(d.Info.OSVersion), ""
	}
	return "", "", ""
}

// iphoneCheckAction draws what an iPhone's item offers to do, and reports
// whether it did: the steps in Add phone for a setup step, how to install
// the helper.
func (a *app) iphoneCheckAction(c *mg.Context, d gateway.DeviceView, ck driver.Check) bool {
	switch ck.Fix {
	case driver.SetupTrust, driver.SetupDeveloperMode, driver.SetupUnlock, driver.SetupPreparing,
		driver.SetupNoDiskImage, driver.SetupDiskImage:
		mg.Row(c).Margin(4, 0, 0, 0).Children(func() {
			if button(c, locales.PhoneSetUpIPhone(), btnOpt{small: true}).Clicked() {
				a.page = pageAddPhone
				a.add.reset(a)
				a.add.step, a.add.focus = "iphone", d.Info.ID
			}
		})
		return true
	case driver.FixInstallHelper:
		mg.Row(c).Margin(4, 0, 0, 0).Children(func() {
			if button(c, locales.CheckHelperHow(), btnOpt{small: true}).Clicked() {
				openURL(c, helperDocs, func(error) {})
			}
		})
		return true
	}
	return false
}
