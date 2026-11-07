package ui

import (
	"math"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
)

const (
	panelW = 360
	panelH = 330
)

// answer is an approval the person just answered, which the floating
// panel confirms for a moment.
type answer struct {
	need  gateway.NeedView
	d     gateway.Decision
	note  string
	until time.Time
}

func (a *app) remember(n gateway.NeedView, d gateway.Decision, note string) {
	if d == gateway.TakeOver {
		return
	}
	a.answered = append(a.answered, answer{need: n, d: d, note: note, until: time.Now().Add(2500 * time.Millisecond)})
	delete(a.noting, n.ID)
}

func (a *app) liveAnswers() []answer {
	now := time.Now()
	out := a.answered[:0]
	for _, x := range a.answered {
		if now.Before(x.until) {
			out = append(out, x)
		}
	}
	a.answered = out
	return out
}

// newPanel makes the floating approval panel: a panel window that floats
// over every app and shows without taking the keyboard.
func (a *app) newPanel() {
	a.panel = mygo.NewWindow(mygo.WindowOptions{
		Title: locales.PanelWindowTitle(), Width: panelW, Height: panelH,
		Hidden: true, Panel: true, Frameless: true, Transparent: true, CornerRadius: 14,
		DisableResize: true, DisableMinimize: true, DisableMaximize: true, DisableFullScreen: true,
		SkipTaskbar: true, AlwaysOnTop: true,
		Content: mg.View(a.panelView),
	})
	a.panel.OnClose(func(e *mygo.CloseEvent) {
		if !a.quitting {
			e.PreventDefault()
			a.panel.Hide()
		}
	})
}

// syncPanel shows the panel while approvals wait and the person cannot see
// them in the main window, and hides it otherwise. It never takes the
// keyboard: the person clicks it first, and then Return allows and Esc
// denies.
func (a *app) syncPanel() {
	if a.panel == nil {
		return
	}
	a.panelPage = a.page
	want := (len(a.approvals()) > 0 || len(a.liveAnswers()) > 0) && !a.needsInFront()
	switch {
	case want && !a.panel.IsVisible():
		a.placePanel()
		a.panel.ShowInactive()
	case !want && a.panel.IsVisible():
		a.panel.Hide()
	}
	a.panel.Invalidate()
}

// needsInFront reports whether the person sees the Needs you column: the
// main window is in front and shows the Phones page with a phone. Other
// pages, and the Phones page before any phone is added, have no such
// column, so their approvals go to the panel.
func (a *app) needsInFront() bool {
	return a.main != nil && a.main.IsVisible() && !a.main.IsMinimized() && a.focused &&
		a.page == pagePhones && len(a.devices) > 0
}

// showPanel brings the panel up, from a notification or the flyout.
func (a *app) showPanel(focus bool) {
	if len(a.approvals()) == 0 {
		a.showMain()
		return
	}
	if !a.panel.IsVisible() {
		a.placePanel()
	}
	if focus {
		a.panel.Show()
	} else {
		a.panel.ShowInactive()
	}
}

// placePanel puts the panel at the top right of the display the pointer
// is on, where notifications show.
func (a *app) placePanel() {
	d := mygo.Screen.DisplayNearestPoint(mygo.Screen.CursorScreenPoint())
	wa := d.WorkArea
	a.panel.SetPosition(wa.X+wa.Width-panelW-16, wa.Y+16)
}

// panelView builds the floating panel.
func (a *app) panelView(c *mg.Context) {
	p := applyTheme(c)
	c.Root().Background(mg.Transparent)
	asks := a.approvals()
	answers := a.liveAnswers()
	if len(answers) > 0 {
		c.After(200 * time.Millisecond)
	}
	if len(asks) == 0 && len(answers) == 0 {
		mg.Column(c).Fill().Radius(14).Background(p.panel).Border(1, p.line2)
		if a.main != nil {
			a.main.Update(a.syncPanel) // nothing left to show: hide
		}
		return
	}
	if a.panelIdx >= len(asks) {
		a.panelIdx = max(0, len(asks)-1)
	}
	card := mg.Column(c).Fill().Radius(14).Background(p.amberTint).Border(1, p.amberLine).Clip()
	// The last answer shows until it times out, unless more wait.
	if len(answers) > 0 && (len(asks) == 0 || answers[len(answers)-1].until.Sub(time.Now()) > 1500*time.Millisecond) {
		x := answers[len(answers)-1]
		card.Children(func() { a.panelAnswered(c, x) })
		return
	}
	n := asks[a.panelIdx]
	noting := a.noting[n.ID]
	plain := n.Kind == gateway.NeedApproval && !newAgentNeed(n)
	if !plain {
		// Handoffs and agent requests have no Return and Esc: their
		// buttons are not Allow and Deny.
	} else if !noting {
		if c.Shortcut(0, mg.KeyEnter) {
			a.decide(n, gateway.AllowOnce, "")
		}
		if c.Shortcut(0, mg.KeyEscape) {
			a.decide(n, gateway.Deny, "")
		}
	} else if c.Shortcut(0, mg.KeyEscape) {
		delete(a.noting, n.ID)
	}
	card.Children(func() {
		// The time left, as a bar along the top.
		left := time.Until(n.Deadline)
		total := n.Deadline.Sub(n.Created)
		frac := float32(1)
		if total > 0 && !n.Deadline.IsZero() {
			frac = float32(math.Max(0, math.Min(1, left.Seconds()/total.Seconds())))
		}
		mg.Box(c).Height(3).FillWidth().Background(p.amberLine).Draw(func(pt *mg.Painter, r mg.Rect) {
			pt.Fill(mg.Rect{X: r.X, Y: r.Y, W: r.W * frac, H: r.H}, p.amberDot, 0)
		})
		if !n.Deadline.IsZero() {
			c.After(250 * time.Millisecond)
		}
		mg.Column(c).Grow(1).MinHeight(0).Padding(12, 16, 16, 16).Gap(2).Children(func() {
			mg.Row(c).MinHeight(24).Gap(8).AlignItems(mg.Center).Children(func() {
				if len(asks) > 1 {
					mg.Row(c).Gap(2).AlignItems(mg.Center).Role(mg.RoleGroup).Label(locales.PanelWaitingApprovals()).Children(func() {
						if arrowButton(c, icChevL, locales.PanelPrevious(), a.panelIdx == 0).Clicked() {
							a.panelIdx--
						}
						mg.Text(c, locales.PanelPosition(a.panelIdx+1, len(asks))).FontSize(12.5).FontWeight(600).TextColor(p.ink2)
						if arrowButton(c, icChevR, locales.PanelNext(), a.panelIdx >= len(asks)-1).Clicked() {
							a.panelIdx++
						}
					})
				}
				mg.Spacer(c)
				if !n.Deadline.IsZero() {
					secs := time.Duration(math.Ceil(max(0, left.Seconds()))) * time.Second
					word := locales.PanelDeniesIn(elapsed(secs))
					if n.Kind == gateway.NeedHandoff {
						word = locales.PanelEndsIn(elapsed(secs))
					}
					mg.Text(c, word).FontSize(12.5).TextColor(p.ink2)
				}
			})
			needEyebrow(c, n)
			mg.Text(c, needTitle(n)).FontSize(16).FontWeight(650).LineHeight(1.3).Margin(6, 0, 0, 0).Role(mg.RoleHeading)
			detail := needDetail(n)
			if newAgentNeed(n) {
				_, picker := a.api.(agentScoper)
				detail = agentRequestDetail(n, picker)
			}
			if detail != "" {
				mg.Text(c, detail).FontSize(13.5).TextColor(p.ink2).MaxLines(2)
			}
			switch {
			case n.Kind == gateway.NeedHandoff:
				mg.Spacer(c)
				a.handoffControls(c, n)
				return
			case newAgentNeed(n):
				mg.Spacer(c)
				a.agentRequestControls(c, n)
				return
			case n.Unblock:
				// Return and Esc still allow (for the task) and deny.
				mg.Spacer(c)
				a.unblockControls(c, n)
				return
			}
			if !noting {
				a.screenshotCrop(c, n)
			}
			mg.Spacer(c)
			if noting {
				mg.Text(c, locales.NeedsNoteFor(agentOf(n))).FontSize(13).FontWeight(600).Margin(10, 0, 0, 0)
				note := a.notes[n.ID]
				mg.TextArea(c.Key("pnote-"+n.ID), &note).Label(locales.NeedsNoteFor(agentOf(n))).Lines(2, 3).Margin(6, 0, 0, 0).AutoFocus()
				a.notes[n.ID] = note
				mg.Grid(c).Columns(2).Gap(8).Margin(8, 0, 0, 0).Children(func() {
					if button(c, locales.PanelCancel(), btnOpt{}).Clicked() {
						delete(a.noting, n.ID)
					}
					if button(c, locales.PanelDenyWithNote(), btnOpt{kind: btnPrimary}).Clicked() {
						a.decide(n, gateway.Deny, strings.TrimSpace(note))
					}
				})
				return
			}
			mg.Grid(c).Columns(2).Gap(8).Margin(12, 0, 0, 0).Children(func() {
				if button(c, locales.NeedsDeny(), btnOpt{}).Clicked() {
					a.decide(n, gateway.Deny, "")
				}
				if button(c, locales.NeedsAllow(), btnOpt{kind: btnPrimary}).Clicked() {
					a.decide(n, gateway.AllowOnce, "")
				}
			})
			linkButton(c, locales.NeedsMoreOptions()).Margin(10, 0, 0, 0).AlignSelf(mg.Start).PopoverMenuButton(func(m *mg.Menu) {
				// As on the card (approvalControls): each only where the
				// gateway keeps it.
				if n.AllowTask && m.Item(locales.NeedsAllowTask()).Chosen() {
					a.decide(n, gateway.AllowTask, "")
				}
				if n.AlwaysAllow && m.Item(alwaysAllowLabel(n)).Chosen() {
					a.decide(n, gateway.AllowAlways, "")
				}
				if m.Item(locales.PhoneTakeOver()).Chosen() {
					a.decide(n, gateway.TakeOver, "")
				}
				if m.Item(locales.NeedsDenyWithNote()).Chosen() {
					a.noting[n.ID] = true
				}
			})
		})
	})
}

// panelAnswered confirms an answer, as the prototype's allowed and denied
// states do.
func (a *app) panelAnswered(c *mg.Context, x answer) {
	p := pal(c)
	mg.Column(c).Grow(1).Padding(16).Gap(2).Children(func() {
		needEyebrow(c, x.need)
		mg.Text(c, needTitle(x.need)).FontSize(16).FontWeight(650).Margin(6, 0, 0, 0)
		mg.Text(c, needDetail(x.need)).FontSize(13.5).TextColor(p.ink2).MaxLines(2)
		mg.Column(c).Margin(14, 0, 0, 0).Gap(4).Children(func() {
			title, detail := "", ""
			ic, col := icCheck, p.green
			kind, app := x.need.Rule, x.need.App
			agent := agentOf(x.need)
			withNote := func(s string) string {
				if x.note != "" {
					return locales.PanelWithNote(s, x.note)
				}
				return s
			}
			switch {
			case x.need.Kind == gateway.NeedHandoff && x.d == gateway.AllowOnce:
				title, detail = locales.PanelHandedBack(), withNote(locales.PanelHandedBackDetail(agent))
			case x.need.Kind == gateway.NeedHandoff:
				ic, col = icX, p.ink2
				title, detail = locales.PanelDeclined(), withNote(locales.PanelToldNo(agent))
			case newAgentNeed(x.need) && x.d == gateway.Deny:
				ic, col = icX, p.ink2
				title, detail = locales.PanelDenied(), locales.PanelAgentDenied(agent)
			case newAgentNeed(x.need):
				title, detail = locales.PanelAllowed(), locales.PanelAgentAllowed(agent)
			case x.need.Unblock && x.d != gateway.Deny:
				title, detail = locales.PanelAllowedTask(), locales.PanelUnblockedDetail(agent, nonEmpty(app, locales.PanelThisApp()))
			}
			switch {
			case title != "":
			default:
				switch x.d {
				case gateway.AllowOnce:
					title, detail = locales.PanelAllowedOnce(), locales.PanelAllowedOnceDetail()
				case gateway.AllowTask:
					title = locales.PanelAllowedTask()
					detail = locales.PanelAllowedTaskDetail(nonEmpty(ruleName(kind), locales.PanelActionsLikeThis()), nonEmpty(app, locales.PanelThisApp()))
				case gateway.AllowAlways:
					title = locales.PanelAlwaysAllowed()
					detail = locales.PanelAlwaysAllowedDetail(nonEmpty(ruleName(kind), locales.PanelActionsLikeThis()), nonEmpty(app, locales.PanelThisApp()))
				default:
					ic, col = icX, p.ink2
					title = locales.PanelDenied()
					detail = locales.PanelDeniedDetail(agentOf(x.need))
					if x.note != "" {
						detail = locales.PanelDeniedDetailNote(agentOf(x.need), x.note)
					}
				}
			}
			mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
				mg.Icon(c, ic).FontSize(18).TextColor(col)
				mg.Text(c, title).FontWeight(650)
			})
			mg.Text(c, detail).FontSize(13.5).TextColor(p.ink2)
		})
	})
}

func nonEmpty(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

func arrowButton(c *mg.Context, ic *mg.SVG, label string, disabled bool) mg.Element {
	p := pal(c)
	b := mg.ButtonBase(c).Size(24, 24).Radius(6).Center().Label(label).Disabled(disabled)
	col := p.ink2
	if disabled {
		b.Opacity(0.35)
	} else if b.Hovered() {
		b.Background(p.line)
		col = p.ink
	}
	b.TextColor(col).Children(func() { mg.Icon(c, ic).FontSize(15) })
	return b
}

// screenshotCrop shows the part of the approval's screenshot around its
// target, with the target outlined: the mirror is out of view here.
func (a *app) screenshotCrop(c *mg.Context, n gateway.NeedView) {
	p := pal(c)
	if n.Screenshot == nil {
		return
	}
	bm := a.shots[n.ID]
	if bm == nil {
		bm = mg.NewBitmap(n.Screenshot)
		a.shots[n.ID] = bm
	}
	iw, ih := bm.Size()
	tg := n.Target
	fig := mg.Box(c).Height(100).FillWidth().Margin(12, 0, 0, 0).Radius(10).Border(1, p.line2).Clip().Background(hex("#000000")).
		Label(locales.PanelShotLabel(n.DeviceName))
	fig.Draw(func(pt *mg.Painter, r mg.Rect) {
		if iw == 0 || ih == 0 {
			return
		}
		// Fit the target's width into the figure, with room around it,
		// and center it vertically.
		cw := float32(iw)
		if !tg.Empty() {
			cw = min(float32(iw), float32(tg.W)*1.12)
		}
		scale := r.W / cw
		chH := r.H / scale
		cx := float32(iw) / 2
		cy := float32(ih) / 2
		if !tg.Empty() {
			cx, cy = float32(tg.X)+float32(tg.W)/2, float32(tg.Y)+float32(tg.H)/2
		}
		src := mg.Rect{X: cx - cw/2, Y: cy - chH/2, W: cw, H: chH}
		src.X = max(0, min(src.X, float32(iw)-src.W))
		src.Y = max(0, min(src.Y, float32(ih)-src.H))
		pt.ImageRegion(bm, src, r, mg.ImageOptions{})
		if !tg.Empty() {
			box := mg.Rect{
				X: r.X + (float32(tg.X)-src.X)*scale - 5, Y: r.Y + (float32(tg.Y)-src.Y)*scale - 5,
				W: float32(tg.W)*scale + 10, H: float32(tg.H)*scale + 10,
			}
			pt.Stroke(box, p.ovl, min(15, box.H/2), 2)
		}
	})
}
