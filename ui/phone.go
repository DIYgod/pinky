package ui

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// phonesPage shows the selected phone, or all of them, with the Needs you
// and Tasks panel on the right.
func (a *app) phonesPage(c *mg.Context) {
	p := pal(c)
	if len(a.devices) == 0 {
		pageHeader(c, func() { mg.Text(c, locales.PhonePageTitle()).FontSize(17).FontWeight(650) })
		mg.Column(c).Grow(1).Center().Gap(10).Children(func() {
			if !a.loaded {
				spinner(c)
				return
			}
			mg.Text(c, locales.PhoneNoPhones()).FontSize(16).FontWeight(600)
			if b := button(c, locales.PhoneAddPhone(), btnOpt{kind: btnPrimary}); b.Clicked() {
				a.page = pageAddPhone
				a.add.reset(a)
			}
		})
		return
	}
	name, line := locales.PhoneAllPhones(), ""
	if a.sel == allPhones {
		online, offline := 0, 0
		for _, d := range a.devices {
			if d.Status == gateway.DeviceOffline {
				offline++
			} else {
				online++
			}
		}
		line = locales.PhoneConnected(online)
		if offline > 0 {
			line += " · " + locales.PhoneOfflineCount(offline)
		}
	} else if d := a.device(a.sel); d != nil {
		name, line = d.Info.Name, connectionLine(*d)
		if t := a.taskOf(d.Info.ID); a.backgroundTask(*d, t) {
			line += " · " + locales.PhoneWorksInBackground(agentOrTheAgent(t))
		}
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
			mg.Text(c, locales.PhoneOffline(d.Info.Name)).FontSize(16).FontWeight(600)
			mg.Row(c).Gap(4).AlignItems(mg.Baseline).Children(func() {
				detail := d.Info.Detail
				if detail == "" {
					detail = locales.PhoneNotConnected()
				} else {
					detail += "."
				}
				mg.Text(c, detail).FontSize(13.5).TextColor(p.ink2)
				if linkButton(c, locales.PhoneReconnect()).Clicked() {
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
			mg.Text(c, locales.PhoneNeedsSetup(d.Info.Name)).FontSize(16).FontWeight(600)
			if d.Info.Detail != "" {
				mg.Text(c, d.Info.Detail).FontSize(13.5).TextColor(p.ink2).TextAlign(mg.Center)
			}
			// An iPhone's steps are in Add phone, which follows a phone
			// that waits for one (addiphone.go).
			if d.Info.Platform == driver.IOS && linkButton(c, locales.PhoneSetUpIPhone()).Clicked() {
				a.page = pageAddPhone
				a.add.reset(a)
				a.add.step, a.add.focus = "iphone", d.Info.ID
			}
		})
		return
	}

	t := a.taskOf(d.Info.ID)
	agent := ""
	if t != nil && running(t.State) {
		agent = nonEmpty(t.Agent, locales.AgentGenericStart())
	}
	inControl := d.Status == gateway.DeviceInControl
	ho, handoff := a.handoffFor(d.Info.ID)
	switch {
	case handoff && !a.isLocked(d) && t != nil && t.State == gateway.TaskPaused:
		// The person took over and the agent waits with phone_handoff.
		inControl = true
		a.controlBanner(c, d.Info.ID, locales.PhoneBannerInControl(), locales.PhoneBannerPaused(agentOf(ho)))
	case handoff && !a.isLocked(d):
		// The agent asked for the person: the phone is theirs until they
		// hand it back, here or with Done on the card.
		inControl = true
		reason := strings.TrimSuffix(strings.TrimSuffix(needTitle(ho), "."), "\u3002") // the agent's words, without the final period
		a.controlBanner(c, d.Info.ID, locales.PhoneBannerYourTurn(), locales.PhoneBannerWaits(agentOf(ho), lowerFirst(reason)))
	case inControl && agent != "":
		a.controlBanner(c, d.Info.ID, locales.PhoneBannerInControl(), locales.PhoneBannerPaused(agent))
	case inControl:
		a.controlBanner(c, d.Info.ID, locales.PhoneBannerInControl(), locales.PhoneBannerHandBackHint())
	}

	// Size the phone to the room the window gives it.
	w, h := c.Size()
	availH := h - 58 - 32 - 14 - 36
	availW := w - 220 - 320 - 32
	// While an agent works on its own display in the background, the
	// mirror shows that display, unless the person chose the phone's own
	// screen; the agent's outline belongs on its display only.
	m, bgTask := a.screenFor(d, t, false)
	watching := m.agent
	shown := t
	if bgTask && !watching {
		shown = nil
	}
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
	fig.Label(locales.PhoneScreenLabel(d.Info.Name)).Children(func() {
		a.mirrorView(c, m, d, shown, inner, false)
	})

	// The keys: Back, Home and Recents on Android, Home on iPhone, after
	// the switch between the agent's screen and the phone's own.
	mg.Row(c).Gap(4).AlignItems(mg.Center).Children(func() {
		if bgTask && (watching || a.phoneChosen(d.Info.ID, t)) {
			which := 0
			if !watching {
				which = 1
			}
			seg := mg.Segmented(c.Key("screen-"+d.Info.ID), &which, locales.PhoneAgentScreen(agentOrTheAgent(t)), locales.PhonePhoneScreen()).Label(locales.PhoneShowOnMirror())
			if seg.Changed() {
				a.bg.phone[d.Info.ID] = ""
				if which == 1 {
					a.bg.phone[d.Info.ID] = t.ID
				}
			}
			mg.Box(c).Size(1, 20).Background(p.line2).Margin(0, 6)
		}
		key := func(ic *mg.SVG, label string, k driver.Key) {
			b := mg.ButtonBase(c).Size(40, 36).Radius(9).Center().Label(label).Tooltip(label)
			col := p.ink2
			if watching {
				// Keys act on the phone's own screen, not the agent's.
				b.Disabled(true).Tooltip(locales.PhoneKeysAgentScreen(agentOrTheAgent(t)))
				col = p.ink3
			} else if b.Hovered() {
				b.Background(p.bg3)
				col = p.ink
			}
			if b.Clicked() && !watching {
				id := d.Info.ID
				a.call(id, locales.PhonePressKey(label), func() error { return a.api.Key(id, k) })
			}
			b.TextColor(col).Children(func() { mg.Icon(c, ic).FontSize(18) })
		}
		if d.Info.Platform != driver.IOS {
			key(icBack, locales.PhoneKeyBack(), driver.KeyBack)
		}
		key(icHome, locales.PhoneKeyHome(), driver.KeyHome)
		if d.Info.Platform != driver.IOS {
			key(icRecents, locales.PhoneKeyRecents(), driver.KeyRecents)
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
		if button(c, locales.PhoneHandBack(), btnOpt{kind: btnPrimary}).Clicked() {
			a.handBack(id)
		}
	})
}

// moreControls is the ⋯ button under the mirror and its popover.
func (a *app) moreControls(c *mg.Context, d gateway.DeviceView, m *mirror) {
	p := pal(c)
	id := d.Info.ID
	open := a.more[id]
	b := mg.ButtonBase(c.Key("more-"+id)).Size(40, 36).Radius(9).Center().Label(locales.PhoneMoreControls()).Tooltip(locales.PhoneMoreControls())
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
		if menuItem(c, locales.PhoneTakeScreenshot(), mg.Color{}).Clicked() {
			open = false
			a.saveScreenshot(m, d.Info.Name)
		}
		menuItem(c, locales.PhoneRecordScreen(), p.ink3).Disabled(true).Tooltip(locales.PhoneRecordScreenTooltip())
		menuItem(c, locales.PhoneRotate(), p.ink3).Disabled(true).Tooltip(locales.PhoneRotateTooltip())
		mg.Box(c).Height(1).Background(p.line).Margin(6, 4)
		mg.Row(c).Gap(10).Padding(6, 10).AlignItems(mg.Center).Children(func() {
			ink := p.ink
			desc := locales.PhoneBackgroundDesc()
			if !d.Features.Background {
				ink = p.ink3
				desc = locales.PhoneBackgroundUnsupported()
			}
			mg.Column(c).Grow(1).Children(func() {
				mg.Text(c, locales.PhoneWorkInBackground()).FontSize(14).TextColor(ink)
				mg.Text(c, desc).FontSize(12.5).TextColor(p.ink2)
			})
			on := a.bg.on[id] && d.Features.Background
			sw := toggle(c.Key("bg-pop-"+id), &on, locales.PhoneWorkInBackground()).Disabled(!d.Features.Background)
			if sw.Changed() && d.Features.Background {
				a.setBackground(d, on)
			}
		})
	})
	a.more[id] = open
}

// saveScreenshot writes the mirror's last picture to the Desktop (or the
// home folder) as a PNG.
func (a *app) saveScreenshot(m *mirror, name string) {
	img := m.lastImage()
	if img == nil {
		a.toasts = append(a.toasts, locales.PhoneNoPicture())
		return
	}
	dir, err := mygo.App.Path(mygo.PathDesktop)
	if err != nil || dir == "" {
		dir, _ = os.UserHomeDir()
	}
	now := time.Now()
	path := filepath.Join(dir, locales.PhoneScreenshotName(name, now.Format("2006-01-02"), now.Format("15.04.05"))+".png")
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
				a.toasts = append(a.toasts, locales.PhoneScreenshotFailed(err.Error()))
			} else {
				a.toasts = append(a.toasts, locales.PhoneScreenshotSaved(filepath.Base(path)))
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
				sel := mg.ButtonBase(c).Column().AlignItems(mg.Stretch).Justify(mg.Start).FillWidth().Padding(14).Gap(10).Radius(14).Label(locales.PhoneShow(d.Info.Name))
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
							m, bgTask := a.screenFor(d, t, true)
							shown := t
							if bgTask && !m.agent {
								shown = nil
							}
							a.mirrorView(c, m, d, shown, radius-5, true)
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
						mg.Text(c, statusLine(d)).FontSize(13).TextColor(p.ink2).SingleLine()
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
							if button(c, locales.PhoneHandBack(), btnOpt{small: true, dark: true}).Clicked() {
								a.handBack(d.Info.ID)
							}
						case !a.isLocked(d):
							if button(c, locales.PhoneTakeOver(), btnOpt{small: true, dark: true}).Clicked() {
								a.takeOver(d.Info.ID)
							}
						}
						if run && button(c, locales.TasksStop(), btnOpt{small: true, kind: btnRed, dark: true}).Clicked() {
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
