package ui

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// phonesPage shows the selected phone, or all of them, with the Needs you
// and Tasks panel on the right.
func (a *app) phonesPage(c *mg.Context) {
	p := pal(c)
	if len(a.devices) == 0 {
		pageHeader(c, func() { mg.Text(c, "Phones").FontSize(17).FontWeight(650) })
		mg.Column(c).Grow(1).Center().Gap(10).Children(func() {
			if !a.loaded {
				spinner(c)
				return
			}
			mg.Text(c, "No phones yet").FontSize(16).FontWeight(600)
			if b := button(c, "Add phone", btnOpt{kind: btnPrimary}); b.Clicked() {
				a.page = pageAddPhone
				a.add.reset(a)
			}
		})
		return
	}
	name, line := "All phones", ""
	if a.sel == allPhones {
		online, offline := 0, 0
		for _, d := range a.devices {
			if d.Status == gateway.DeviceOffline {
				offline++
			} else {
				online++
			}
		}
		line = fmt.Sprintf("%d connected", online)
		if offline > 0 {
			line += fmt.Sprintf(" · %d offline", offline)
		}
	} else if d := a.device(a.sel); d != nil {
		name, line = d.Info.Name, connectionLine(*d)
	}
	pageHeader(c, func() {
		mg.Row(c).AlignItems(mg.Baseline).Gap(10).Children(func() {
			mg.Text(c, name).FontSize(17).FontWeight(650)
			mg.Text(c, line).FontSize(13).TextColor(p.ink2)
		})
	})
	mg.Row(c).Grow(1).MinHeight(0).AlignItems(mg.Stretch).Children(func() {
		center := mg.Column(c).Grow(1).MinWidth(0).Padding(16).Gap(14).AlignItems(mg.Center).Justify(mg.Center)
		center.Children(func() {
			if a.sel == allPhones {
				a.allPhonesGrid(c)
			} else if d := a.device(a.sel); d != nil {
				a.phoneCenter(c, *d)
			}
		})
		a.rightPanel(c)
	})
}

// phoneCenter is the middle of the Phone screen: the banner, the mirror
// and the keys below it.
func (a *app) phoneCenter(c *mg.Context, d gateway.DeviceView) {
	p := pal(c)
	if d.Status == gateway.DeviceOffline || d.Info.State == driver.StateOffline {
		mg.Column(c).AlignItems(mg.Center).Gap(12).Children(func() {
			mg.Box(c).Size(200, 420).Radius(32).Border(2, p.line2).Background(p.bg2).Center().Children(func() {
				mg.Icon(c, icWifiOff).FontSize(30).TextColor(p.ink3)
			})
			mg.Text(c, d.Info.Name+" is offline").FontSize(16).FontWeight(600)
			mg.Row(c).Gap(4).AlignItems(mg.Baseline).Children(func() {
				detail := d.Info.Detail
				if detail == "" {
					detail = "It is not connected."
				} else {
					detail += "."
				}
				mg.Text(c, detail).FontSize(13.5).TextColor(p.ink2)
				if linkButton(c, "Reconnect").Clicked() {
					a.page = pageAddPhone
					a.add.reset(a)
				}
			})
		})
		return
	}
	if d.Info.State != driver.StateOnline {
		mg.Column(c).AlignItems(mg.Center).Gap(10).MaxWidth(420).Children(func() {
			mg.Icon(c, icAlert).FontSize(28).TextColor(p.amberDot)
			mg.Text(c, d.Info.Name+" needs setup").FontSize(16).FontWeight(600)
			if d.Info.Detail != "" {
				mg.Text(c, d.Info.Detail).FontSize(13.5).TextColor(p.ink2).TextAlign(mg.Center)
			}
		})
		return
	}

	t := a.taskOf(d.Info.ID)
	agent := ""
	if t != nil && running(t.State) {
		agent = nonEmpty(t.Agent, "The agent")
	}
	inControl := d.Status == gateway.DeviceInControl
	ho, handoff := a.handoffFor(d.Info.ID)
	switch {
	case handoff && !a.isLocked(d) && t != nil && t.State == gateway.TaskPaused:
		// The person took over and the agent waits with phone_handoff.
		inControl = true
		a.controlBanner(c, d.Info.ID, "You're in control. ", agentOf(ho)+" is paused.")
	case handoff && !a.isLocked(d):
		// The agent asked for the person: the phone is theirs until they
		// hand it back, here or with Done on the card.
		inControl = true
		a.controlBanner(c, d.Info.ID, "Your turn. ", agentOf(ho)+" waits for you: "+lowerFirst(strings.TrimSuffix(ho.Title, ".")))
	case inControl && agent != "":
		a.controlBanner(c, d.Info.ID, "You're in control. ", agent+" is paused.")
	case inControl:
		a.controlBanner(c, d.Info.ID, "You're in control. ", "Hand back to let agents use this phone again.")
	}

	// Size the phone to the room the window gives it.
	w, h := c.Size()
	availH := h - 58 - 32 - 14 - 36
	availW := w - 220 - 320 - 32
	m := a.mirrorFor(d.Info.ID)
	aspect := m.aspect(d)
	const bezel = 9
	ph := min(availH, (availW-2*bezel)/aspect+2*bezel)
	ph = max(ph, 240)
	sw := (ph - 2*bezel) * aspect
	radius, inner := float32(46), float32(38)
	if d.Info.Platform == driver.IOS {
		radius, inner = 50, 42
	}
	scale := (ph - 2*bezel) / 614 // the prototype's screen is 614 high
	radius, inner = radius*max(scale, 0.6), inner*max(scale, 0.6)

	fig := mg.Box(c).Width(sw + 2*bezel).Height(ph).Shrink(0).Radius(radius).Background(p.phone).Padding(bezel)
	fig = shadow(c, fig)
	if inControl {
		fig.Shadow(0, 0, 0, 2, p.ink2)
	}
	fig.Label(d.Info.Name + " screen").Children(func() {
		a.mirrorView(c, m, d, t, inner, false)
	})

	// The keys: Back, Home and Recents on Android, Home on iPhone.
	mg.Row(c).Gap(4).AlignItems(mg.Center).Children(func() {
		key := func(ic *mg.SVG, label string, k driver.Key) {
			b := mg.ButtonBase(c).Size(40, 36).Radius(9).Center().Label(label).Tooltip(label)
			col := p.ink2
			if b.Hovered() {
				b.Background(p.bg3)
				col = p.ink
			}
			if b.Clicked() {
				id := d.Info.ID
				a.call(id, "press "+label, func() error { return a.api.Key(id, k) })
			}
			b.TextColor(col).Children(func() { mg.Icon(c, ic).FontSize(18) })
		}
		if d.Info.Platform != driver.IOS {
			key(icBack, "Back", driver.KeyBack)
		}
		key(icHome, "Home", driver.KeyHome)
		if d.Info.Platform != driver.IOS {
			key(icRecents, "Recents", driver.KeyRecents)
		}
		mg.Box(c).Size(1, 20).Background(p.line2).Margin(0, 6)
		a.moreControls(c, d, m)
	})
}

// controlBanner is the "You're in control" banner over the mirror, with
// Hand back. The person keeps control after the agent left, until they
// hand the phone back.
func (a *app) controlBanner(c *mg.Context, id, bold, rest string) {
	p := pal(c)
	w, _ := c.Size()
	b := mg.Row(c).Attach(mg.AnchorTop, mg.AnchorTop).Top(16).Width(min(460, w-220-320-32)).
		Padding(10, 10, 10, 16).Radius(12).Background(p.panel).Border(1, p.line2).Gap(10).AlignItems(mg.Center).
		Role(mg.RoleStatus)
	shadow(c, b).Children(func() {
		mg.Icon(c, icHand).FontSize(18).TextColor(p.ink2)
		mg.RichText(c,
			mg.Span{Text: bold, Weight: 650, Color: p.ink},
			mg.Span{Text: rest, Color: p.ink2},
		).Grow(1).MinWidth(0)
		if button(c, "Hand back", btnOpt{kind: btnPrimary}).Clicked() {
			a.handBack(id)
		}
	})
}

// moreControls is the ⋯ button under the mirror and its popover.
func (a *app) moreControls(c *mg.Context, d gateway.DeviceView, m *mirror) {
	p := pal(c)
	id := d.Info.ID
	open := a.more[id]
	b := mg.ButtonBase(c.Key("more-"+id)).Size(40, 36).Radius(9).Center().Label("More phone controls").Tooltip("More phone controls")
	col := p.ink2
	if b.Hovered() || open {
		b.Background(p.bg3)
		col = p.ink
	}
	if b.Clicked() {
		open = !open
	}
	b.TextColor(col).Children(func() { mg.Icon(c, icMore).FontSize(18) })
	mg.PopoverBase(c, b, &open, func(panel mg.Element) {
		menuPanel(c, panel).Width(290).Margin(6, 0, 0, 0)
		if menuItem(c, "Take screenshot", mg.Color{}).Clicked() {
			open = false
			a.saveScreenshot(m, d.Info.Name)
		}
		menuItem(c, "Record screen", p.ink3).Disabled(true).Tooltip("Recording arrives with the history store")
		menuItem(c, "Rotate", p.ink3).Disabled(true).Tooltip("Not in the gateway API yet")
		mg.Box(c).Height(1).Background(p.line).Margin(6, 4)
		mg.Row(c).Gap(10).Padding(6, 10).AlignItems(mg.Center).Children(func() {
			mg.Column(c).Grow(1).Children(func() {
				mg.Text(c, "Background display").FontSize(14).TextColor(p.ink3)
				mg.Text(c, "The agent works on a hidden screen, so the phone stays yours").FontSize(12.5).TextColor(p.ink2)
			})
			off := false
			toggle(c, &off, "Background display").Disabled(true)
		})
	})
	a.more[id] = open
}

// saveScreenshot writes the mirror's last picture to the Desktop (or the
// home folder) as a PNG.
func (a *app) saveScreenshot(m *mirror, name string) {
	img := m.lastImage()
	if img == nil {
		a.toasts = append(a.toasts, "No picture yet")
		return
	}
	dir, err := mygo.App.Path(mygo.PathDesktop)
	if err != nil || dir == "" {
		dir, _ = os.UserHomeDir()
	}
	path := filepath.Join(dir, fmt.Sprintf("%s %s.png", name, time.Now().Format("2006-01-02 at 15.04.05")))
	go func() {
		f, err := os.Create(path)
		if err == nil {
			err = png.Encode(f, img)
			if cerr := f.Close(); err == nil {
				err = cerr
			}
		}
		a.post(func() {
			if err != nil {
				a.toasts = append(a.toasts, "Couldn't save the screenshot: "+err.Error())
			} else {
				a.toasts = append(a.toasts, "Saved "+filepath.Base(path))
			}
		})
	}()
}

// allPhonesGrid shows a live tile per phone; a click selects it.
func (a *app) allPhonesGrid(c *mg.Context) {
	p := pal(c)
	mg.Row(c).FillWidth().Grow(1).Wrap().Gap(16).Justify(mg.Center).AlignContent(mg.Start).Padding(8, 0, 0, 0).Children(func() {
		for _, d := range a.devices {
			d := d
			t := a.taskOf(d.Info.ID)
			asks := d.Status == gateway.DeviceNeedsYou || a.isLocked(d)
			offline := d.Status == gateway.DeviceOffline || d.Info.State == driver.StateOffline
			tile := mg.Box(c.Key("tile-"+d.Info.ID)).Basis(200).Grow(1).MaxWidth(232).MinWidth(196).Radius(14).Border(1, p.line).Background(p.panel)
			if asks {
				tile.Border(1, p.amberEdge).Shadow(0, 0, 0, 1, p.amberEdge)
			}
			if offline {
				tile.Background(p.bg2)
			}
			hovered := tile.Hovered()
			tile.Children(func() {
				sel := mg.ButtonBase(c).Column().AlignItems(mg.Stretch).Justify(mg.Start).FillWidth().Padding(14).Gap(10).Radius(14).Label("Show " + d.Info.Name)
				if sel.Clicked() {
					a.selectPhone(d.Info.ID)
				}
				sel.Children(func() {
					mini := mg.Box(c).Size(168, 352).Margin(0, mg.Auto).Shrink(0)
					switch {
					case offline:
						mini.Radius(24).Border(2, p.line2).Background(p.bg3).Center().Children(func() {
							mg.Icon(c, icWifiOff).FontSize(26).TextColor(p.ink3)
						})
					default:
						radius := float32(26)
						if d.Info.Platform == driver.IOS {
							radius = 30
						}
						mini.Radius(radius).Background(p.phone).Padding(5).Children(func() {
							m := a.mirrorFor(d.Info.ID)
							a.mirrorView(c, m, d, t, radius-5, true)
						})
					}
					mg.Column(c).Gap(2).Children(func() {
						mg.Row(c).Gap(7).AlignItems(mg.Center).Children(func() {
							statusDot(c, d)
							name := mg.Text(c, d.Info.Name).FontWeight(650)
							if offline {
								name.TextColor(p.ink2)
							}
						})
						mg.Text(c, d.StatusLine).FontSize(13).TextColor(p.ink2).SingleLine()
					})
				})
				run := t != nil && running(t.State)
				_, handoff := a.handoffFor(d.Info.ID)
				inControl := d.Status == gateway.DeviceInControl || (handoff && !a.isLocked(d))
				if (run || inControl) && !offline {
					act := mg.Row(c).Absolute().Left(0).Right(0).Top(150).Justify(mg.Center).Gap(6)
					act.IgnoreInput(!hovered).Opacity(act.Animate("show", b2f(hovered), 150*time.Millisecond))
					act.Children(func() {
						switch {
						case inControl:
							if button(c, "Hand back", btnOpt{small: true, dark: true}).Clicked() {
								a.handBack(d.Info.ID)
							}
						case !a.isLocked(d):
							if button(c, "Take over", btnOpt{small: true, dark: true}).Clicked() {
								a.takeOver(d.Info.ID)
							}
						}
						if run && button(c, "Stop", btnOpt{small: true, kind: btnRed, dark: true}).Clicked() {
							a.stop(d.Info.ID)
						}
					})
				}
			})
		}
	})
}

func b2f(b bool) float32 {
	if b {
		return 1
	}
	return 0
}
