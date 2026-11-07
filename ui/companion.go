package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// Pinky Companion is the Android app that reads the screen and types for
// Pinky. It ships inside Pinky; a phone without it, or with an older one,
// gets an Install card on its page, and nothing is installed without the
// person's click.

// companionState is what the client knows of each phone's companion app.
type companionState struct {
	state      map[string]driver.CompanionState
	checked    map[string]time.Time // when CompanionState last answered, by phone
	loading    map[string]bool
	installing map[string]time.Time // installs under way, by when they began
	err        map[string]string    // why the last install failed
	later      map[string]bool      // phones whose card the person put off
}

// companionEvery is how often the client asks again while a phone's page
// shows, as the person may install or turn on the app on the phone.
const companionEvery = 30 * time.Second

// companionFor returns a phone's companion state, asking the gateway in the
// background when it is unknown or old; "" while unknown.
func (a *app) companionFor(d gateway.DeviceView) driver.CompanionState {
	ci, ok := a.api.(companionInstaller)
	if !ok || !d.Features.Companion {
		return ""
	}
	st := &a.comp
	id := d.Info.ID
	if !st.loading[id] && st.installing[id].IsZero() && time.Since(st.checked[id]) > companionEvery {
		st.loading[id] = true
		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			cs, err := ci.CompanionState(ctx, id)
			a.post(func() {
				st.loading[id] = false
				st.checked[id] = time.Now()
				if err == nil {
					st.state[id] = cs
				}
			})
		}()
	}
	return st.state[id]
}

// installCompanion installs or updates the phone's companion app and turns
// its service on. The phone may ask the person to allow the install, so it
// can take a while.
func (a *app) installCompanion(d gateway.DeviceView) {
	ci, ok := a.api.(companionInstaller)
	st := &a.comp
	id, name := d.Info.ID, d.Info.Name
	if !ok || !st.installing[id].IsZero() {
		return
	}
	st.installing[id] = time.Now()
	delete(st.err, id)
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Minute)
		defer cancel()
		err := ci.InstallCompanion(ctx, id)
		cs, serr := ci.CompanionState(ctx, id)
		a.post(func() {
			delete(st.installing, id)
			if serr == nil {
				st.state[id], st.checked[id] = cs, time.Now()
			}
			if err != nil {
				st.err[id] = companionError(err)
				return
			}
			a.toasts = append(a.toasts, "Pinky Companion is on for "+name)
		})
	}()
}

func companionError(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "The install didn't finish. If the phone asked to allow it, tap Allow there, then try again."
	case strings.Contains(msg, "INSTALL_FAILED_USER_RESTRICTED"):
		return "The phone blocked the install. Turn on Install via USB in Developer options, then try again."
	case strings.Contains(msg, "INSTALL_FAILED_VERIFICATION_FAILURE") || strings.Contains(msg, "Play Protect"):
		return "Google Play Protect stopped the install. On the phone, choose Install anyway, then try again."
	case strings.Contains(msg, "INSTALL_FAILED_INSUFFICIENT_STORAGE"):
		return "The phone is out of space. Free some up, then try again."
	case errors.Is(err, driver.ErrOffline):
		return "The phone went offline. Connect it again, then try again."
	}
	return "Couldn't install Pinky Companion: " + msg
}

// companionCard offers to install, update or turn on the companion app on
// a phone's page.
func (a *app) companionCard(c *mg.Context, d gateway.DeviceView) {
	st := &a.comp
	id := d.Info.ID
	cs := a.companionFor(d)
	busy := !st.installing[id].IsZero()
	if st.later[id] && !busy {
		return
	}
	title, body, action := "", "", ""
	switch cs {
	case driver.CompanionMissing:
		title, action = "Install Pinky Companion", "Install"
		body = "A small Pinky app for " + d.Info.Name + ". Agents control the phone faster and more steadily with it, and type without using its clipboard."
	case driver.CompanionOutdated:
		title, action = "Update Pinky Companion", "Update"
		body = "This Pinky comes with a newer version of its app for the phone. Updating keeps control fast and steady."
	case driver.CompanionOff:
		title, action = "Turn on Pinky Companion", "Turn on"
		body = "The app is installed, but its service is off, so agents control the phone the slower way. Pinky can turn it on."
	default:
		if !busy {
			return // on, unknown, or no app to install
		}
		title = "Installing Pinky Companion"
	}
	p := pal(c)
	mg.Column(c.Key("companion-"+id)).Padding(14).Radius(12).Background(p.bg2).Border(1, p.line).Gap(8).
		Label("Pinky Companion on " + d.Info.Name).Children(func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Icon(c, icAndroid).FontSize(16).TextColor(p.ink2)
			mg.Text(c, title).FontSize(15).FontWeight(650).Grow(1).MinWidth(0)
		})
		if body != "" && !busy {
			mg.Text(c, body).FontSize(13.5).TextColor(p.ink2).LineHeight(1.4)
		}
		if busy {
			mg.Progress(c, -1).FillWidth().Label("Installing")
			mg.Text(c, "If the phone asks, allow the install.").FontSize(13.5).FontWeight(600)
			c.After(time.Second)
			if time.Since(st.installing[id]) > 20*time.Second {
				mg.Text(c, "Still waiting for the phone. Look for a prompt on its screen.").FontSize(13).TextColor(p.ink2)
			}
			return
		}
		if msg := st.err[id]; msg != "" {
			mg.Text(c, msg).FontSize(13).TextColor(p.red).Role(mg.RoleStatus)
		}
		mg.Row(c).Gap(12).AlignItems(mg.Center).Margin(4, 0, 0, 0).Children(func() {
			if button(c, action, btnOpt{kind: btnPrimary}).Label(action + " Pinky Companion on " + d.Info.Name).Clicked() {
				a.installCompanion(d)
			}
			if quietButton(c, "Not now").Clicked() {
				st.later[id] = true
			}
		})
		mg.Text(c, "If the phone asks, allow the install.").FontSize(12.5).TextColor(p.ink3)
	})
}

// companionLine is a phone's companion app in Settings › Phones.
func companionLine(cs driver.CompanionState) string {
	switch cs {
	case driver.CompanionOn:
		return "Pinky Companion is on"
	case driver.CompanionOff:
		return "Pinky Companion is installed but off"
	case driver.CompanionMissing:
		return "Pinky Companion is not installed"
	case driver.CompanionOutdated:
		return "Pinky Companion has an update"
	case driver.CompanionNoAPK:
		return "This Pinky has no companion app to install"
	}
	return ""
}
