package ui

import (
	"fmt"
	"log"
	"runtime"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/assets"
	"github.com/DIYgod/pinky/internal/gateway"
)

// newTray puts Pinky's icon in the menu bar (the notification area on
// Windows and Linux). A click opens the flyout; on Linux, whose tray icons
// report no clicks, its menu does.
func (a *app) newTray() {
	img, template := assets.TrayColor, false
	if runtime.GOOS == "darwin" {
		img, template = assets.TrayTemplate, true
	}
	t, err := mygo.NewTray(mygo.TrayOptions{ID: "pinky", Icon: img, IconIsTemplate: template, ToolTip: "Pinky"})
	if err != nil {
		log.Printf("pinky: tray: %v", err)
		return
	}
	a.tray = t
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "Open Pinky", Click: func(*mygo.MenuItem, *mygo.Window) { a.showMain() }},
		{Label: "Show Phones…", Click: func(*mygo.MenuItem, *mygo.Window) { a.toggleFlyout() }},
		mygo.Separator(),
		{Label: "Stop All", Click: func(*mygo.MenuItem, *mygo.Window) { a.stopAll() }},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
	if runtime.GOOS == "linux" {
		t.SetMenu(menu)
	} else {
		t.OnClick(a.toggleFlyout)
		t.OnRightClick(func() {
			if a.flyout != nil {
				a.flyout.Close()
			}
			t.SetMenu(menu)
			defer t.SetMenu(nil)
			t.PopUpMenu()
		})
	}
	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { t.Destroy() })
}

// updateTray shows how many things wait for the person next to the icon.
func (a *app) updateTray() {
	if a.tray == nil {
		return
	}
	n := len(a.needs)
	tip := "Pinky"
	title := ""
	if n > 0 {
		tip = fmt.Sprintf("Pinky · %s", plural(n, "thing needs you", "things need you"))
		title = fmt.Sprint(n)
	}
	a.tray.SetToolTip(tip)
	if runtime.GOOS == "darwin" {
		a.tray.SetTitle(title)
	}
}

// flyoutHeight sizes the flyout to its rows, as the prototype's 320 × 304
// fits one need and three phones.
func (a *app) flyoutHeight() int {
	h := 6 + 8 // padding
	if len(a.needs) > 0 {
		h += 26 + 36*min(len(a.needs), 4)
	}
	h += 26 + 4 + 36*max(1, len(a.devices))
	h += 13 + 36*2
	return h
}

func (a *app) toggleFlyout() {
	if a.flyout != nil {
		a.flyout.Close()
		return
	}
	a.flyout = mygo.NewFlyout(mygo.FlyoutOptions{
		Tray: a.tray, Placement: mygo.PlacementBottom, Gap: 4,
		Width: 320, Height: a.flyoutHeight(), Focusable: true, Shadow: true,
		Content: mg.View(a.flyoutView),
	})
	a.flyout.OnClosed(func() { a.flyout = nil })
}

// flyoutView is the menu bar flyout: what needs the person, the phones,
// Open Pinky and Stop all.
func (a *app) flyoutView(c *mg.Context) {
	p := applyTheme(c)
	if a.flyout != nil {
		if w, h := a.flyout.Size(); w > 0 && h != a.flyoutHeight() {
			a.flyout.SetSize(320, a.flyoutHeight())
		}
	}
	if c.Shortcut(0, mg.KeyEscape) && a.flyout != nil {
		a.flyout.Close()
	}
	sec := func(text string) {
		mg.Text(c, text).FontSize(12).FontWeight(600).TextColor(p.ink3).Padding(6, 10, 4, 10)
	}
	mg.Column(c).Fill().Background(p.panel).Padding(6, 8, 8, 8).Children(func() {
		if len(a.needs) > 0 {
			sec("Needs you")
			for i, n := range a.needs {
				if i == 4 {
					break
				}
				n := n
				row := flyRow(c.Key("n-" + n.ID)).Background(p.amberTint)
				if row.Clicked() {
					if a.flyout != nil {
						a.flyout.Close()
					}
					if n.Kind == gateway.NeedApproval {
						a.showPanel(true)
					} else {
						a.openNeed(n.ID)
					}
				}
				row.Label(n.DeviceName + ", " + n.Title).Children(func() {
					dot(c, p.amberDot, false)
					mg.RichText(c,
						mg.Span{Text: n.DeviceName, Weight: 600},
						mg.Span{Text: " · " + needShort(n)},
					).Grow(1).MinWidth(0).SingleLine()
					label := "Review"
					if n.Kind == gateway.NeedUnlock {
						label = "Unlock"
					}
					mg.Text(c, label).FontWeight(500).TextColor(p.accentInk)
				})
			}
		}
		sec("Phones")
		if len(a.devices) == 0 {
			mg.Text(c, "No phones connected").FontSize(13).TextColor(p.ink2).Padding(8, 10)
		}
		for _, d := range a.devices {
			d := d
			row := flyRow(c.Key("d-" + d.Info.ID))
			if d.Status != gateway.DeviceOffline && row.Clicked() {
				if a.flyout != nil {
					a.flyout.Close()
				}
				a.selectPhone(d.Info.ID)
				a.showMain()
			}
			row.Label(d.Info.Name + ", " + d.StatusLine).Children(func() {
				statusDot(c, d)
				name := mg.Text(c, d.Info.Name).FontWeight(600).Shrink(0)
				if d.Status == gateway.DeviceOffline {
					name.TextColor(p.ink2)
				}
				mg.Text(c, d.StatusLine).FontSize(13).TextColor(p.ink2).Grow(1).MinWidth(0).SingleLine()
			})
		}
		mg.Spacer(c)
		mg.Box(c).Height(1).Background(p.line).Margin(6, 6)
		open := flyRow(c).Label("Open Pinky")
		if open.Clicked() {
			if a.flyout != nil {
				a.flyout.Close()
			}
			a.showMain()
		}
		open.Children(func() { mg.Text(c, "Open Pinky") })
		stop := flyRow(c).Label("Stop all")
		if stop.Clicked() {
			a.stopAll()
		}
		stop.Children(func() {
			mg.Text(c, "Stop all").TextColor(p.red).Grow(1)
			mg.Text(c, shortcutLabel()).FontSize(13).TextColor(p.ink3)
		})
	})
}

func flyRow(c *mg.Context) mg.Element {
	p := pal(c)
	row := mg.ButtonBase(c).MinHeight(36).Padding(6, 10).Radius(8).Gap(10).AlignItems(mg.Center).Justify(mg.Start).FillWidth()
	if row.Hovered() {
		row.Background(p.bg3)
	}
	return row
}

// needShort is a need in a few words: "Pay $12.80".
func needShort(n gateway.NeedView) string {
	t := n.Title
	if len(t) > 0 && t[len(t)-1] == '?' {
		t = t[:len(t)-1]
	}
	if n.Kind == gateway.NeedUnlock {
		return "Locked"
	}
	return t
}
