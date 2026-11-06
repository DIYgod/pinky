package ui

import (
	"strings"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// addState is the Add phone wizard's. It finds the new phone by watching
// Devices for one it did not know when the step began: gateway.API has
// no pairing methods yet, so plugging in (or the gateway pairing by
// itself) is what moves it on.
type addState struct {
	step     string // "choose", "android", "qr", "iphone"
	known    map[string]bool
	old      bool   // the older-iPhone path
	oldPick  string // "helper" or "mirror"
	code     string // the iPhone's pairing code
	pairing  bool
	tryAgain bool
}

func (s *addState) reset(a *app) {
	*s = addState{step: "choose"}
	s.snapshot(a)
}

func (s *addState) snapshot(a *app) {
	s.known = map[string]bool{}
	for _, d := range a.devices {
		s.known[d.Info.ID] = true
	}
}

// fresh returns the newest device of the platform that was not there
// when the step began.
func (s *addState) fresh(a *app, pf driver.Platform) *gateway.DeviceView {
	var out *gateway.DeviceView
	for i := range a.devices {
		d := &a.devices[i]
		if !s.known[d.Info.ID] && d.Info.Platform == pf {
			out = d
		}
	}
	return out
}

func (a *app) addPhonePage(c *mg.Context) {
	p := pal(c)
	s := &a.add
	if s.step == "" {
		s.reset(a)
	}
	// As in the prototype, the wizard has no header: a strip at the top
	// drags the window.
	mg.Row(c).Height(32).Shrink(0).Background(p.bg2).DragWindow()
	mg.Column(c).Grow(1).Center().Padding(0, 24, 32, 24).Background(p.bg2).Children(func() {
		card := mg.Column(c).FillWidth().MaxWidth(470).Padding(32).Radius(16).Background(p.panel).Border(1, p.line).Gap(18).Label("Add phone")
		shadow(c, card).Children(func() {
			mg.Row(c).MinHeight(22).AlignItems(mg.Center).Margin(0, 0, -6, 0).Children(func() {
				if s.step != "choose" && linkButton(c, "‹ Back").Clicked() {
					switch s.step {
					case "qr":
						s.step = "android"
					default:
						s.step, s.old, s.pairing, s.oldPick = "choose", false, false, ""
					}
					s.snapshot(a)
				}
				mg.Spacer(c)
				if len(a.devices) > 0 && quietButton(c, "Cancel").Clicked() {
					a.page = pagePhones
				}
			})
			switch s.step {
			case "android":
				a.addAndroid(c)
			case "qr":
				a.addQR(c)
			case "iphone":
				a.addIPhone(c)
			default:
				a.addChoose(c)
			}
		})
	})
}

func heading(c *mg.Context, title, desc string) {
	p := pal(c)
	mg.Column(c).Children(func() {
		mg.Text(c, title).FontSize(22).FontWeight(650).Role(mg.RoleHeading)
		mg.Text(c, desc).FontSize(14).TextColor(p.ink2).Margin(6, 0, 0, 0)
	})
}

// statusBox is the prototype's role=status line.
func statusBox(c *mg.Context, fn func()) {
	p := pal(c)
	mg.Row(c).Gap(10).Padding(12, 14).Radius(10).Background(p.bg2).AlignItems(mg.Center).Role(mg.RoleStatus).Children(fn)
}

func (a *app) addChoose(c *mg.Context) {
	p := pal(c)
	s := &a.add
	heading(c, "Which phone?", "Pinky works with Android phones and iPhones.")
	mg.Grid(c).Columns(2).Gap(12).Children(func() {
		choice := func(ic *mg.SVG, title, desc, step string) {
			b := mg.ButtonBase(c.Key(step)).Padding(20, 18).Radius(12).Gap(4).AlignItems(mg.Start).Column().Label(title)
			border := p.line2
			if b.Hovered() {
				border = p.accent
				b.Shadow(0, 0, 0, 1, p.accent)
			}
			if b.Clicked() {
				s.step = step
				s.snapshot(a)
			}
			b.Border(1, border).Background(p.panel).Children(func() {
				mg.Icon(c, ic).FontSize(26).TextColor(p.ink2).Margin(0, 0, 8, 0)
				mg.Text(c, title).FontSize(16).FontWeight(650)
				mg.Text(c, desc).FontSize(13).TextColor(p.ink2)
			})
		}
		choice(icAndroid, "Android", "Pixel, Galaxy and others", "android")
		choice(icIphone, "iPhone", "Best on iOS 27; older versions work too", "iphone")
	})
}

func (a *app) addAndroid(c *mg.Context) {
	p := pal(c)
	s := &a.add
	heading(c, "Plug in your Android phone", "Connect it to this computer with a USB cable. If the phone asks to allow USB debugging, tap Allow.")
	d := s.fresh(a, driver.Android)
	found := d != nil && d.Info.State == driver.StateOnline
	fix := d != nil && !found && !s.tryAgain
	statusBox(c, func() {
		switch {
		case found:
			dot(c, p.green, false)
			mg.Text(c, "Found "+d.Info.Name).FontWeight(600)
		case fix:
			dot(c, p.amberDot, false)
			why := "it needs setup"
			if d.Info.Detail != "" {
				why = lowerFirst(d.Info.Detail)
			}
			mg.Text(c, "Found "+d.Info.Name+", but "+why)
		default:
			spinner(c)
			mg.Text(c, "Looking for your phone…")
		}
	})
	if d != nil && !found && s.tryAgain && d.Info.State != driver.StateOnline {
		// Looking again; the gateway reports the phone once it is ready.
		c.After(2_000_000_000)
		s.tryAgain = false
	}
	if fix {
		mg.Column(c).Padding(16, 18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(10).Children(func() {
			mg.Text(c, "Turn on USB debugging").FontSize(15).FontWeight(650)
			mg.Column(c).Gap(6).Role(mg.RoleList).Children(func() {
				for i, line := range []string{
					"Open Settings › About phone and tap Build number 7 times.",
					"Go to Settings › System › Developer options.",
					"Turn on USB debugging, then tap Allow on the phone.",
				} {
					mg.Row(c.Key(i)).Gap(8).AlignItems(mg.Start).Role(mg.RoleListItem).Children(func() {
						mg.Text(c, string(rune('1'+i))+".").Width(14).TextColor(p.ink2)
						mg.Text(c, line).Grow(1).MinWidth(0)
					})
				}
			})
			mg.Row(c).Children(func() {
				if button(c, "Try again", btnOpt{}).Clicked() {
					s.tryAgain = true
				}
			})
		})
	}
	if found {
		mg.Row(c).Children(func() {
			if button(c, "Done", btnOpt{kind: btnPrimary}).Clicked() {
				a.selectPhone(d.Info.ID)
				s.step = ""
			}
		})
		return
	}
	mg.Row(c).Children(func() {
		if linkButton(c, "Or pair over Wi-Fi").Clicked() {
			s.step = "qr"
			s.snapshot(a)
		}
	})
}

func (a *app) addQR(c *mg.Context) {
	p := pal(c)
	s := &a.add
	heading(c, "Pair over Wi-Fi", "On the phone, open Developer options › Wireless debugging › Pair device with QR code, then scan this code.")
	mg.Box(c).AlignSelf(mg.Center).Size(200, 200).Padding(12).Radius(12).Background(hex("#ffffff")).Border(1, p.line).Children(func() {
		mg.Column(c).Fill().Radius(6).Border(1.5, hex("#d9d9e0")).BorderStyle(mg.BorderDashed).Center().Gap(6).Padding(12).Children(func() {
			mg.Icon(c, icGrid).FontSize(28).TextColor(hex("#6c6c75"))
			mg.Text(c, "The code shows here once the gateway can start wireless pairing.").FontSize(12).TextColor(hex("#5b5b64")).TextAlign(mg.Center)
		})
	})
	if d := s.fresh(a, driver.Android); d != nil && d.Info.State == driver.StateOnline {
		statusBox(c, func() {
			dot(c, p.green, false)
			mg.Text(c, "Found "+d.Info.Name).FontWeight(600)
		})
		mg.Row(c).Children(func() {
			if button(c, "Done", btnOpt{kind: btnPrimary}).Clicked() {
				a.selectPhone(d.Info.ID)
				s.step = ""
			}
		})
		return
	}
	statusBox(c, func() {
		spinner(c)
		mg.Text(c, "Waiting for the phone…")
	})
	mg.Row(c).Children(func() {
		if linkButton(c, "Use a USB cable instead").Clicked() {
			s.step = "android"
			s.snapshot(a)
		}
	})
}

func (a *app) addIPhone(c *mg.Context) {
	p := pal(c)
	s := &a.add
	d := s.fresh(a, driver.IOS)
	if !s.old {
		heading(c, "Pair your iPhone", "On the iPhone, turn on Developer Mode in Settings › Privacy & Security. Keep it on the same Wi-Fi, then enter the code it shows.")
		ready := d != nil && d.Info.State == driver.StateOnline
		statusBox(c, func() {
			switch {
			case ready:
				dot(c, p.green, false)
				mg.Text(c, d.Info.Name+" is ready").FontWeight(600)
			case d != nil:
				dot(c, p.green, false)
				mg.Text(c, strings.TrimSpace("Found "+d.Info.Name+" · iOS "+d.Info.OSVersion))
			default:
				spinner(c)
				mg.Text(c, "Looking for your iPhone…")
			}
		})
		if ready {
			mg.Row(c).Children(func() {
				if button(c, "Done", btnOpt{kind: btnPrimary}).Clicked() {
					a.selectPhone(d.Info.ID)
					s.step = ""
				}
			})
			return
		}
		mg.Row(c).Gap(8).Role(mg.RoleGroup).Label("Pairing code").Children(func() {
			digits := []rune(s.code)
			for i := 0; i < 6; i++ {
				ch := ""
				if i < len(digits) {
					ch = string(digits[i])
				}
				box := mg.Box(c.Key(i)).Size(44, 52).Radius(10).Border(1, p.line2).Background(p.panel).Center()
				if i == len(digits) && !s.pairing {
					box.Border(2, p.brand)
				}
				box.Children(func() { mg.Text(c, ch).FontSize(22).FontWeight(600) })
			}
		})
		in := mg.TextInputBase(c.Key("code"), &s.code).Label("Pairing code").Placeholder("Type the 6-digit code").FontSize(14).AutoFocus()
		if in.Changed() {
			s.code = keepDigits(s.code, 6)
		}
		mg.Row(c).Gap(16).AlignItems(mg.Center).Wrap().Children(func() {
			pair := button(c, "Pair", btnOpt{kind: btnPrimary}).Disabled(len(s.code) < 6 || s.pairing)
			if pair.Clicked() || (in.Submitted() && len(s.code) == 6) {
				s.pairing = true
			}
			mg.Spacer(c)
			if quietButton(c, "Try an older iPhone").Clicked() {
				s.old = true
			}
		})
		if s.pairing {
			mg.Text(c, "Waiting for the iPhone. It shows up here once the gateway has paired with it.").FontSize(13.5).TextColor(p.ink2)
		}
		return
	}
	heading(c, "Pair your iPhone", "Older iPhones work too. Pick how to connect.")
	if d != nil {
		statusBox(c, func() {
			dot(c, p.amberDot, false)
			mg.Text(c, strings.TrimSpace("Found "+d.Info.Name+" · iOS "+d.Info.OSVersion))
		})
	}
	mg.Column(c).Padding(16, 18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(10).Children(func() {
		mg.Text(c, "This iPhone needs one more step").FontSize(15).FontWeight(650)
		opt := func(key, title, desc string) {
			b := mg.ButtonBase(c.Key(key)).Column().Gap(3).AlignItems(mg.Start).Padding(12, 14).Radius(10).Background(p.panel).Label(title)
			border := p.line2
			if b.Hovered() || s.oldPick == key {
				border = p.accent
				b.Shadow(0, 0, 0, 1, p.accent)
			}
			if b.Clicked() {
				s.oldPick = key
			}
			b.Border(1, border).Children(func() {
				mg.Text(c, title).FontWeight(650)
				mg.Text(c, desc).FontSize(13).TextColor(p.ink2)
			})
		}
		opt("helper", "Install the Pinky helper", "WebDriverAgent, signed with your Apple ID. With a free Apple ID it needs re-signing every 7 days; a paid developer account lasts a year.")
		opt("mirror", "Use iPhone Mirroring (this Mac only)", "Nothing to install, but Pinky can read the screen only as an image, so it is slower.")
	})
	if s.oldPick != "" {
		mg.Text(c, "This build can't set that up yet: the gateway's iOS backends for older iPhones come in M3.").FontSize(13.5).TextColor(p.ink2)
	} else {
		mg.Text(c, "Or update to iOS 27 for the best experience.").FontSize(13.5).TextColor(p.ink2).Margin(-6, 0, 0, 0)
	}
}

func keepDigits(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' && b.Len() < n {
			b.WriteRune(r)
		}
	}
	return b.String()
}
