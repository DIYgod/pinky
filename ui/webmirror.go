package ui

import (
	"context"
	"net"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/webmirror"
)

// WebMirror shows the phones' screens in a web browser: on this computer
// at webmirror.URL, and on the person's other devices through `tailscale
// serve`, which the person runs. Each link carries one of the mirror's
// tokens: Token's can only watch, ControlToken's can also use the phone
// while Settings.WebMirrorControl is on.
type WebMirror interface {
	Token() (string, error)
	ControlToken() (string, error)
	// NewToken replaces both tokens, cutting off every browser.
	NewToken() (string, error)
	// Viewers is how many browsers watch now, Controllers how many can
	// use the phone.
	Viewers() int
	Controllers() int
	// Err says why the mirror could not turn on.
	Err() error
	// Tailnet says where `tailscale serve` gives the mirror to the
	// tailnet; URL is the page without its token.
	Tailnet(ctx context.Context) RemoteTailnet
}

// webMirrorState is Settings › Phones › Web mirror's.
type webMirrorState struct {
	token, control string

	tn     RemoteTailnet
	tnAt   time.Time
	tnBusy bool
}

// webMirrorServe is the command that has Tailscale give the web mirror to
// the tailnet over HTTPS at https, a port nothing else is served on.
func webMirrorServe(https string) string {
	_, port, _ := net.SplitHostPort(webmirror.Addr)
	return "tailscale serve --bg --https=" + https + " " + port
}

// webMirrorTailnet returns what Tailscale last said about the web mirror,
// and reads it again off the main thread when it is old.
func (a *app) webMirrorTailnet(c *mg.Context, wm WebMirror) RemoteTailnet {
	r := &a.webMirror
	if !r.tnBusy && time.Since(r.tnAt) >= tailnetEveryRemote {
		r.tnBusy = true
		go func() {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			tn := wm.Tailnet(ctx)
			a.post(func() { r.tn, r.tnAt, r.tnBusy = tn, time.Now(), false })
		}()
	}
	c.After(tailnetEveryRemote) // also shows how many watch
	return r.tn
}

// webMirrorSettings is the Web mirror part of Settings › Phones.
func (a *app) webMirrorSettings(c *mg.Context) {
	wm := a.opt.WebMirror
	if wm == nil {
		return
	}
	p := pal(c)
	st := &a.settings
	r := &a.webMirror
	mg.Column(c).Gap(10).Label(locales.WebMirrorTitle()).Children(func() {
		settingsHeading(c, locales.WebMirrorTitle(), locales.WebMirrorIntro())
		group(c).Children(func() {
			a.toggleRow(c, "web-mirror", locales.WebMirrorToggle(), "", !st.WebMirror, &st.WebMirror)
			if !st.WebMirror {
				return
			}
			if err := wm.Err(); err != nil {
				mg.Text(c, locales.WebMirrorFailed(err.Error())).FontSize(fsSmall).TextColor(p.red).Role(mg.RoleStatus).Margin(0, sp4, sp2, sp4)
			}
			mg.Row(c).Gap(sp2).AlignItems(mg.Start).Padding(10, sp3).Radius(rControl).Background(p.amberTint).Border(1, p.amberLine).Margin(0, sp4, sp2, sp4).
				Children(func() {
					mg.Icon(c, icAlert).FontSize(16).TextColor(p.amber)
					mg.Text(c, locales.WebMirrorWarning()).FontSize(fsSmall).LineHeight(1.4).Grow(1).MinWidth(0)
				})
			if r.token == "" {
				r.token, _ = wm.Token()
			}
			tn := a.webMirrorTailnet(c, wm)
			a.webMirrorLinks(c, tn, r.token, locales.WebMirrorLink(), locales.WebMirrorTailnetLink(), locales.WebMirrorLinkDesc(), locales.WebMirrorTailnetLinkDesc())
			if tn.URL == "" && tn.Name != "" && tn.ServePort != "" {
				url := "https://" + tn.Name + ":" + tn.ServePort + "/"
				a.commandBlock(c, locales.WebMirrorTailnetTitle(), locales.WebMirrorTailnetHint(url), webMirrorServe(tn.ServePort),
					locales.WebMirrorTailnetCommandLabel(), locales.RemoteCopyCommand(), false)
			}
			a.toggleRow(c, "web-mirror-control", locales.WebMirrorControlToggle(), locales.WebMirrorControlDesc(), false, &st.WebMirrorControl)
			if st.WebMirrorControl {
				mg.Row(c).Gap(sp2).AlignItems(mg.Start).Padding(10, sp3).Radius(rControl).Background(p.amberTint).Border(1, p.amberLine).Margin(0, sp4, sp2, sp4).
					Children(func() {
						mg.Icon(c, icAlert).FontSize(16).TextColor(p.amber)
						mg.Text(c, locales.WebMirrorControlWarning()).FontSize(fsSmall).LineHeight(1.4).Grow(1).MinWidth(0)
					})
				if r.control == "" {
					r.control, _ = wm.ControlToken()
				}
				a.webMirrorLinks(c, tn, r.control, locales.WebMirrorControlLink(), locales.WebMirrorControlLink(), locales.WebMirrorLink(), locales.WebMirrorTailnetLink())
			}
			settingRow(c, locales.WebMirrorNewLink(), locales.WebMirrorNewLinkDesc(), true, func() {
				mg.Row(c).Gap(sp3).AlignItems(mg.Center).Children(func() {
					watching := locales.WebMirrorNoViewers()
					if n := wm.Viewers() + wm.Controllers(); n > 0 {
						watching = locales.WebMirrorViewers(n)
					}
					if n := wm.Controllers(); n > 0 {
						watching += " · " + locales.WebMirrorControllers(n)
					}
					mg.Text(c, watching).FontSize(fsSmall).TextColor(p.ink2)
					if button(c, locales.WebMirrorNewLinkButton(), btnOpt{small: true}).Clicked() {
						if tok, err := wm.NewToken(); err == nil {
							r.token = tok
							r.control, _ = wm.ControlToken()
							c.Toast(locales.WebMirrorNewLinkDone())
						}
					}
				})
			})
		})
	})
}

// webMirrorLinks are the rows of a link to the mirror with token: on this
// computer, with Open and Copy, and through the tailnet while `tailscale
// serve` gives the mirror to it.
func (a *app) webMirrorLinks(c *mg.Context, tn RemoteTailnet, token, localTitle, tailnetTitle, localDesc, tailnetDesc string) {
	if token == "" {
		return
	}
	local := webmirror.URL()
	settingRow(c, localTitle, localDesc, false, func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Text(c, local+"#…").Font("monospace").FontSize(12.5)
			if button(c.Key(token+"-open"), locales.WebMirrorOpen(), btnOpt{small: true}).Clicked() {
				openURL(c, local+"#"+token, func(error) {})
			}
			if button(c.Key(token+"-copy"), "", btnOpt{small: true, icon: icCopy, label: locales.WebMirrorCopyLink()}).Clicked() {
				a.copy(c, local+"#"+token, locales.WebMirrorCopiedLink())
			}
		})
	})
	if tn.URL == "" {
		return
	}
	settingRow(c, tailnetTitle, tailnetDesc, false, func() {
		mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Text(c, tn.URL+"#…").Font("monospace").FontSize(12.5)
			if button(c.Key(token+"-copy-tn"), "", btnOpt{small: true, icon: icCopy, label: locales.WebMirrorCopyTailnetLink()}).Clicked() {
				a.copy(c, tn.URL+"#"+token, locales.WebMirrorCopiedLink())
			}
		})
	})
}
