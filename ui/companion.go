package ui

import (
	"context"
	"errors"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// Pinky Companion is the Android app that reads the screen and types for
// Pinky. It ships inside Pinky; a phone without it, or with an older one,
// gets an Install card on its page. Nothing is installed without the
// person's click, except that with Update automatically on Pinky updates
// an older companion by itself while the phone is free (companionTick in
// update.go), keeping its service as it was.

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
			a.toasts = append(a.toasts, locales.CompanionOnToast(name))
		})
	}()
}

func companionError(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return locales.CompanionErrTimeout()
	case strings.Contains(msg, "INSTALL_FAILED_USER_RESTRICTED"):
		return locales.CompanionErrRestricted()
	case strings.Contains(msg, "INSTALL_FAILED_VERIFICATION_FAILURE") || strings.Contains(msg, "Play Protect"):
		return locales.CompanionErrPlayProtect()
	case strings.Contains(msg, "INSTALL_FAILED_INSUFFICIENT_STORAGE"):
		return locales.CompanionErrStorage()
	case errors.Is(err, driver.ErrOffline):
		return locales.CompanionErrOffline()
	}
	return locales.CompanionErrOther(msg)
}

// companionCard offers to install, update or turn on the companion app on
// a phone's page.
func (a *app) companionCard(c *mg.Context, d gateway.DeviceView) {
	st := &a.comp
	id := d.Info.ID
	cs := a.companionFor(d)
	// auto: Pinky updates the app by itself (companionTick), without the
	// phone asking anything.
	auto := a.companionUpdating(id)
	busy := !st.installing[id].IsZero() || auto
	if st.later[id] && !busy {
		return
	}
	title, body, action, actionLabel, extra := "", "", "", "", ""
	switch cs {
	case driver.CompanionMissing:
		title, action = locales.CompanionInstallTitle(), locales.CompanionInstall()
		actionLabel = locales.CompanionInstallLabel(d.Info.Name)
		body = locales.CompanionInstallBody(d.Info.Name)
	case driver.CompanionOutdated:
		title, action = locales.CompanionUpdateTitle(), locales.CompanionUpdate()
		actionLabel = locales.CompanionUpdateLabel(d.Info.Name)
		body = locales.CompanionUpdateBody()
		extra = a.companionAutoLine(id)
	case driver.CompanionOff:
		title, action = locales.CompanionTurnOnTitle(), locales.CompanionTurnOn()
		actionLabel = locales.CompanionTurnOnLabel(d.Info.Name)
		body = locales.CompanionTurnOnBody()
	default:
		if !busy {
			return // on, unknown, or no app to install
		}
		title = locales.CompanionInstallingTitle()
	}
	if auto {
		title = "Updating Pinky Companion"
	}
	p := pal(c)
	mg.Column(c.Key("companion-"+id)).Padding(14).Radius(12).Background(p.bg2).Border(1, p.line).Gap(8).
		Label(locales.CompanionCardLabel(d.Info.Name)).Children(func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Icon(c, icAndroid).FontSize(16).TextColor(p.ink2)
			mg.Text(c, title).FontSize(15).FontWeight(650).Grow(1).MinWidth(0)
		})
		if body != "" && !busy {
			mg.Text(c, body).FontSize(13.5).TextColor(p.ink2).LineHeight(1.4)
		}
		if extra != "" && !busy {
			mg.Text(c, extra).FontSize(13).TextColor(p.ink2)
		}
		if auto {
			mg.Progress(c, -1).FillWidth().Label("Updating")
			mg.Text(c, "Agents can use the phone again in a few seconds.").FontSize(13).TextColor(p.ink2)
			c.After(time.Second)
			return
		}
		if busy {
			mg.Progress(c, -1).FillWidth().Label(locales.CompanionInstalling())
			mg.Text(c, locales.CompanionAllowHint()).FontSize(13.5).FontWeight(600)
			c.After(time.Second)
			if time.Since(st.installing[id]) > 20*time.Second {
				mg.Text(c, locales.CompanionStillWaiting()).FontSize(13).TextColor(p.ink2)
			}
			return
		}
		if msg := st.err[id]; msg != "" {
			mg.Text(c, msg).FontSize(13).TextColor(p.red).Role(mg.RoleStatus)
		}
		mg.Row(c).Gap(12).AlignItems(mg.Center).Margin(4, 0, 0, 0).Children(func() {
			if button(c, action, btnOpt{kind: btnPrimary}).Label(actionLabel).Clicked() {
				a.installCompanion(d)
			}
			if quietButton(c, locales.CompanionNotNow()).Clicked() {
				st.later[id] = true
			}
		})
		mg.Text(c, locales.CompanionAllowHint()).FontSize(12.5).TextColor(p.ink3)
	})
}

// companionLine is a phone's companion app in Settings › Phones.
func companionLine(cs driver.CompanionState) string {
	switch cs {
	case driver.CompanionOn:
		return locales.CompanionLineOn()
	case driver.CompanionOff:
		return locales.CompanionLineOff()
	case driver.CompanionMissing:
		return locales.CompanionLineMissing()
	case driver.CompanionOutdated:
		return locales.CompanionLineOutdated()
	case driver.CompanionNoAPK:
		return locales.CompanionLineNoAPK()
	}
	return ""
}
