package ui

import (
	"runtime"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// guideOS is the system the steps are for; tests change it.
var guideOS = runtime.GOOS

// xcodeAppStore is Xcode's page in the Mac App Store.
const xcodeAppStore = "macappstore://apps.apple.com/app/xcode/id497799835"

// hostChecker is a gateway that says what the computer lacks for a
// platform's phones (gateway.HostChecks): usbmuxd for iPhones.
type hostChecker interface {
	HostChecks() map[driver.Platform]driver.HostCheck
}

// The steps of adding an iPhone, in order.
const (
	iphoneHost    = iota // Windows, Linux: Apple's iPhone driver or usbmuxd
	iphonePlug           // plug it in
	iphoneTrust          // tap Trust
	iphoneDevMode        // turn on Developer Mode
	iphonePrepare        // Pinky mounts the developer disk image
	iphoneReady
)

// iphoneStep is the step an iPhone being added is at: d is the phone (nil
// before one shows up), host what the computer lacks.
func iphoneStep(d *gateway.DeviceView, host driver.HostCheck) int {
	if host.Missing != "" && host.Missing != "usbmuxd_stopped" {
		return iphoneHost
	}
	if d == nil || d.Info.State == driver.StateOffline {
		return iphonePlug
	}
	switch {
	case d.Info.State == driver.StateOnline:
		return iphoneReady
	case d.Info.State == driver.StateUnauthorized, d.Info.Setup == driver.SetupTrust:
		return iphoneTrust
	case d.Info.Setup == driver.SetupDeveloperMode:
		return iphoneDevMode
	}
	return iphonePrepare
}

// iphone returns the iPhone the steps follow: one plugged in since the
// step began, else the one the Phone screen sent the person here for, else
// one that is still waiting for a step (plugged in before Add phone opened,
// or back after a restart).
func (s *addState) iphone(a *app) *gateway.DeviceView {
	if d := s.fresh(a, driver.IOS); d != nil {
		return d
	}
	for i := range a.devices {
		if d := &a.devices[i]; s.focus != "" && d.Info.ID == s.focus {
			return d
		}
	}
	for i := range a.devices {
		d := &a.devices[i]
		if d.Info.Platform == driver.IOS && (d.Info.State == driver.StateUnauthorized || d.Info.State == driver.StateNeedsSetup) {
			return d
		}
	}
	return nil
}

// iphoneHost returns what the computer lacks for iPhones, asking the
// gateway every 2 s off the UI thread while the steps show.
func (a *app) iphoneHost(c *mg.Context) driver.HostCheck {
	s := &a.add
	hc, ok := a.api.(hostChecker)
	if !ok {
		return driver.HostCheck{}
	}
	if !s.hostBusy && time.Since(s.hostAt) >= 2*time.Second {
		s.hostBusy = true
		go func() {
			check := hc.HostChecks()[driver.IOS]
			a.post(func() {
				s.host, s.hostAt, s.hostBusy = check, time.Now(), false
			})
		}()
	}
	c.After(2 * time.Second)
	return s.host
}

// addIPhoneSteps walks the person through connecting an iPhone over USB:
// what the computer needs (Windows, Linux), plugging it in, trusting the
// computer, Developer Mode and Pinky's own preparation, with what to do on
// the iPhone at each step and why a step is stuck.
func (a *app) addIPhoneSteps(c *mg.Context) {
	p := pal(c)
	s := &a.add
	if s.since.IsZero() {
		s.since = time.Now()
	}
	host := a.iphoneHost(c)
	if guideOS != "windows" && guideOS != "linux" {
		host = driver.HostCheck{} // macOS has usbmuxd built in
	}
	d := s.iphone(a)
	cur := iphoneStep(d, host)
	heading(c, locales.AddPhoneIPhoneTitle(), locales.AddPhoneIPhoneDetail())

	if d != nil && d.Info.Setup == driver.SetupUpdate {
		mg.Column(c).Padding(16, 18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(8).Role(mg.RoleStatus).Children(func() {
			mg.Text(c, locales.AddPhoneIPhoneTooOldTitle(d.Info.Name)).FontSize(15).FontWeight(650)
			mg.Text(c, locales.AddPhoneIPhoneTooOld(d.Info.OSVersion)).FontSize(13.5).TextColor(p.ink2)
		})
		return
	}

	n := 0
	row := func(step int, title string, body func()) {
		n++
		state := guideLater
		switch {
		case step < cur:
			state = guideDone
		case step == cur:
			state = guideNow
		}
		guideRow(c, n, title, state, body)
	}
	mg.Column(c).Gap(14).Role(mg.RoleList).Label(locales.AddPhoneIPhoneStepsLabel()).Children(func() {
		if guideOS == "windows" || guideOS == "linux" {
			title, detail := locales.AddPhoneIPhoneStepDriver(), locales.AddPhoneIPhoneStepDriverDetail()
			if guideOS == "linux" {
				title, detail = locales.AddPhoneIPhoneStepUsbmuxd(), locales.AddPhoneIPhoneStepUsbmuxdDetail()
			}
			row(iphoneHost, title, func() {
				guideText(c, detail)
				guideWaiting(c, locales.AddPhoneIPhoneStepDriverWaiting())
			})
		}
		row(iphonePlug, locales.AddPhoneIPhoneStepPlug(), func() {
			guideText(c, locales.AddPhoneIPhoneStepPlugDetail())
			if d != nil && d.Info.State == driver.StateOffline {
				guideProblem(c, locales.AddPhoneIPhoneNoAnswer(d.Info.Name), d.Info.Detail)
				return
			}
			guideWaiting(c, locales.AddPhoneLookingIPhone())
			// usbmuxd on Linux starts when an iPhone is plugged in; say
			// how to start it if nothing shows up for a while.
			if host.Missing == "usbmuxd_stopped" && time.Since(s.since) > 8*time.Second {
				guideProblem(c, locales.AddPhoneIPhoneUsbmuxdStopped(), "")
			}
		})
		if cur > iphonePlug && d != nil {
			mg.Text(c, strings.TrimSpace(locales.AddPhoneFoundIOS(d.Info.Name, d.Info.OSVersion))).FontSize(13).TextColor(p.ink2).Margin(-10, 0, 0, 36)
		}
		row(iphoneTrust, locales.AddPhoneIPhoneStepTrust(), func() {
			guideText(c, locales.AddPhoneIPhoneStepTrustDetail())
			guideWaiting(c, locales.AddPhoneIPhoneStepTrustWaiting())
		})
		row(iphoneDevMode, locales.AddPhoneIPhoneStepDevMode(), func() {
			guideText(c, locales.AddPhoneIPhoneStepDevModeDetail())
			guideNote(c, locales.AddPhoneIPhoneStepDevModeNote())
		})
		row(iphonePrepare, locales.AddPhoneIPhoneStepPrepare(), func() {
			guideText(c, locales.AddPhoneIPhoneStepPrepareDetail())
			switch d.Info.Setup {
			case driver.SetupPreparing:
				guideWaiting(c, locales.AddPhoneIPhoneStepPreparing())
			case driver.SetupUnlock:
				guideProblem(c, locales.AddPhoneIPhoneStepUnlock(), "")
			case driver.SetupNoDiskImage:
				// Apple's image comes with Xcode; Pinky does not ship it.
				if guideOS == "darwin" {
					guideProblem(c, locales.AddPhoneIPhoneNoImageTitle(), locales.AddPhoneIPhoneNoImageMac())
					mg.Row(c).Children(func() {
						if linkButton(c, locales.AddPhoneIPhoneGetXcode()).Clicked() {
							openURL(c, xcodeAppStore, func(error) {})
						}
					})
				} else {
					guideProblem(c, locales.AddPhoneIPhoneNoImageTitle(), locales.AddPhoneIPhoneNoImageCopy())
					guideNote(c, d.Info.Detail)
				}
				guideNote(c, locales.AddPhoneIPhoneRetrying())
			default:
				guideProblem(c, locales.AddPhoneIPhoneStuck(), d.Info.Detail)
				guideNote(c, locales.AddPhoneIPhoneRetrying())
			}
		})
	})

	if cur == iphoneReady {
		statusBox(c, func() {
			dot(c, p.green, false)
			mg.Text(c, locales.AddPhoneReady(d.Info.Name)).FontWeight(600)
		})
		mg.Row(c).Children(func() {
			if button(c, locales.AddPhoneDone(), btnOpt{kind: btnPrimary}).Clicked() {
				a.selectPhone(d.Info.ID)
				s.step = ""
			}
		})
		return
	}
	mg.Row(c).Children(func() {
		mg.Spacer(c)
		if quietButton(c, locales.AddPhoneOlderIPhone()).Clicked() {
			s.old = true
		}
	})
}
