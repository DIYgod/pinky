package ui

import (
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

// addState is the Add phone wizard's. Over USB it finds the new phone by
// watching Devices for one it did not know when the step began; over
// Wi-Fi the gateway's pairing says which phone paired.
type addState struct {
	step     string // "choose", "android", "qr", "iphone"
	known    map[string]bool
	old      bool   // the older-iPhone path
	oldPick  string // "helper" or "mirror"
	code     string // the iPhone's pairing code
	pairing  bool
	tryAgain bool
	pair     pairState // pairing an Android phone over Wi-Fi
}

func (s *addState) reset(a *app) {
	a.stopPairing()
	seq := s.pair.seq // stays counting, so a stale pairing's answer is dropped
	*s = addState{step: "choose"}
	s.pair.seq = seq
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
		card := mg.Column(c).FillWidth().MaxWidth(470).Padding(32).Radius(16).Background(p.panel).Border(1, p.line).Gap(18).Label(locales.AddPhoneLabel())
		shadow(c, card).Children(func() {
			mg.Row(c).MinHeight(22).AlignItems(mg.Center).Margin(0, 0, -6, 0).Children(func() {
				if s.step != "choose" && linkButton(c, locales.AddPhoneBack()).Clicked() {
					switch s.step {
					case "qr":
						s.step = "android"
					default:
						s.step, s.old, s.pairing, s.oldPick = "choose", false, false, ""
					}
					s.snapshot(a)
				}
				mg.Spacer(c)
				if len(a.devices) > 0 && quietButton(c, locales.AddPhoneCancel()).Clicked() {
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
	heading(c, locales.AddPhoneChooseTitle(), locales.AddPhoneChooseDetail())
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
		choice(icAndroid, "Android", locales.AddPhoneChooseAndroid(), "android")
		choice(icIphone, "iPhone", locales.AddPhoneChooseIPhone(), "iphone")
	})
}

func (a *app) addAndroid(c *mg.Context) {
	p := pal(c)
	s := &a.add
	heading(c, locales.AddPhoneAndroidTitle(), locales.AddPhoneAndroidDetail())
	d := s.fresh(a, driver.Android)
	found := d != nil && d.Info.State == driver.StateOnline
	fix := d != nil && !found && !s.tryAgain
	statusBox(c, func() {
		switch {
		case found:
			dot(c, p.green, false)
			mg.Text(c, locales.AddPhoneFound(d.Info.Name)).FontWeight(600)
		case fix:
			dot(c, p.amberDot, false)
			msg := locales.AddPhoneFoundNeedsSetup(d.Info.Name)
			if d.Info.Detail != "" {
				msg = locales.AddPhoneFoundBut(d.Info.Name, lowerFirst(d.Info.Detail))
			}
			mg.Text(c, msg)
		default:
			spinner(c)
			mg.Text(c, locales.AddPhoneLooking())
		}
	})
	if d != nil && !found && s.tryAgain && d.Info.State != driver.StateOnline {
		// Looking again; the gateway reports the phone once it is ready.
		c.After(2_000_000_000)
		s.tryAgain = false
	}
	if fix {
		mg.Column(c).Padding(16, 18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(10).Children(func() {
			mg.Text(c, locales.AddPhoneUSBTitle()).FontSize(15).FontWeight(650)
			mg.Column(c).Gap(6).Role(mg.RoleList).Children(func() {
				for i, line := range []string{
					locales.AddPhoneUSBStep1(),
					locales.AddPhoneUSBStep2(),
					locales.AddPhoneUSBStep3(),
				} {
					mg.Row(c.Key(i)).Gap(8).AlignItems(mg.Start).Role(mg.RoleListItem).Children(func() {
						mg.Text(c, string(rune('1'+i))+".").Width(14).TextColor(p.ink2)
						mg.Text(c, line).Grow(1).MinWidth(0)
					})
				}
			})
			mg.Row(c).Children(func() {
				if button(c, locales.AddPhoneTryAgain(), btnOpt{}).Clicked() {
					s.tryAgain = true
				}
			})
		})
	}
	if found {
		mg.Row(c).Children(func() {
			if button(c, locales.AddPhoneDone(), btnOpt{kind: btnPrimary}).Clicked() {
				a.selectPhone(d.Info.ID)
				s.step = ""
			}
		})
		return
	}
	mg.Row(c).Children(func() {
		if linkButton(c, locales.AddPhoneOrWiFi()).Clicked() {
			s.step = "qr"
			s.snapshot(a)
		}
	})
}

func (a *app) addQR(c *mg.Context) {
	p := pal(c)
	s := &a.add
	st := &s.pair
	_, canPair := a.api.(pairer)
	switch {
	case !canPair:
		heading(c, locales.PairingTitle(), locales.PairingUnsupported())
	case st.form:
		heading(c, locales.PairingCodeTitle(), locales.PairingCodeDetail())
		a.pairCodeForm(c)
	default:
		if st.state == "" {
			a.startPairing()
		}
		heading(c, locales.PairingTitle(), locales.PairingQRDetail())
		mg.Box(c).AlignSelf(mg.Center).Padding(6).Radius(16).Background(hex("#ffffff")).Border(1, p.line).Children(func() {
			if st.code != nil {
				qrView(c, st.code, 208)
				return
			}
			mg.Column(c).Size(208, 208).Center().Gap(8).Padding(16).Children(func() {
				if st.state == "failed" {
					mg.Icon(c, icAlert).FontSize(26).TextColor(hex("#975800"))
					mg.Text(c, locales.PairingNoCode()).FontSize(13).TextColor(hex("#5b5b64"))
				} else {
					spinner(c)
				}
			})
		})
	}
	if canPair && !st.form {
		statusBox(c, func() {
			switch st.state {
			case "failed":
				mg.Icon(c, icAlert).FontSize(16).TextColor(p.amberDot)
				mg.Text(c, st.err).Grow(1).MinWidth(0)
				if button(c, locales.AddPhoneTryAgain(), btnOpt{small: true}).Clicked() {
					a.startPairing()
				}
			case "waiting":
				spinner(c)
				mg.Column(c).Gap(4).Grow(1).MinWidth(0).Children(func() {
					mg.Text(c, locales.PairingWaiting())
					// Pinky finds the phone through adb, which can stop
					// seeing the network after it has run for days.
					c.After(time.Second)
					if !st.since.IsZero() && time.Since(st.since) > 30*time.Second {
						mg.Text(c, locales.PairingSlowHint()).
							FontSize(13).TextColor(p.ink2).LineHeight(1.4)
					}
				})
			default:
				spinner(c)
				mg.Text(c, locales.PairingGettingCode())
			}
		})
	}
	if d := s.fresh(a, driver.Android); d != nil && d.Info.State == driver.StateOnline {
		statusBox(c, func() {
			dot(c, p.green, false)
			mg.Text(c, locales.AddPhoneFound(d.Info.Name)).FontWeight(600)
		})
		mg.Row(c).Children(func() {
			if button(c, locales.AddPhoneDone(), btnOpt{kind: btnPrimary}).Clicked() {
				a.selectPhone(d.Info.ID)
				s.step = ""
			}
		})
		return
	}
	mg.Row(c).Gap(16).Wrap().Children(func() {
		if canPair {
			other := locales.PairingUseCode()
			if st.form {
				other = locales.PairingUseQR()
			}
			if linkButton(c, other).Clicked() {
				st.form, st.formErr = !st.form, ""
				if st.form {
					a.stopPairing()
				}
			}
		}
		if linkButton(c, locales.PairingUseUSB()).Clicked() {
			s.step = "android"
			s.snapshot(a)
		}
	})
}

// pairCodeForm is Pair device with pairing code: the address and port,
// and the six-digit code.
func (a *app) pairCodeForm(c *mg.Context) {
	p := pal(c)
	st := &a.add.pair
	mg.Column(c).Gap(12).Children(func() {
		field := func(label string) {
			mg.Text(c, label).FontSize(13).FontWeight(600)
		}
		var addr, code mg.Element
		mg.Column(c).Gap(6).Children(func() {
			field(locales.PairingAddrField())
			addr = mg.TextInput(c.Key("pair-addr"), &st.addr).Placeholder("192.168.1.20:37123").Label(locales.PairingAddrField()).AutoFocus()
		})
		mg.Column(c).Gap(6).Children(func() {
			field(locales.PairingCodeField())
			code = mg.TextInput(c.Key("pair-code"), &st.pin).Placeholder(locales.PairingCodePlaceholder()).Label(locales.PairingCodeField()).MaxWidth(160)
		})
		if code.Changed() {
			st.pin = keepDigits(st.pin, 6)
		}
		mg.Row(c).Gap(10).AlignItems(mg.Center).Children(func() {
			pair := button(c, locales.PairingPair(), btnOpt{kind: btnPrimary}).Disabled(st.formBusy || len(st.pin) < 6 || st.addr == "")
			if (pair.Clicked() || code.Submitted() || addr.Submitted()) && !st.formBusy && len(st.pin) == 6 {
				a.pairWithCode()
			}
			if st.formBusy {
				spinner(c)
				mg.Text(c, locales.PairingBusy()).FontSize(13.5).TextColor(p.ink2)
			}
		})
		if st.formErr != "" {
			mg.Text(c, st.formErr).FontSize(13.5).TextColor(p.red).Role(mg.RoleStatus)
		}
	})
}

func (a *app) addIPhone(c *mg.Context) {
	p := pal(c)
	s := &a.add
	d := s.fresh(a, driver.IOS)
	if !s.old {
		heading(c, locales.AddPhoneIPhoneTitle(), locales.AddPhoneIPhoneDetail())
		ready := d != nil && d.Info.State == driver.StateOnline
		statusBox(c, func() {
			switch {
			case ready:
				dot(c, p.green, false)
				mg.Text(c, locales.AddPhoneReady(d.Info.Name)).FontWeight(600)
			case d != nil:
				dot(c, p.green, false)
				mg.Text(c, strings.TrimSpace(locales.AddPhoneFoundIOS(d.Info.Name, d.Info.OSVersion)))
			default:
				spinner(c)
				mg.Text(c, locales.AddPhoneLookingIPhone())
			}
		})
		if ready {
			mg.Row(c).Children(func() {
				if button(c, locales.AddPhoneDone(), btnOpt{kind: btnPrimary}).Clicked() {
					a.selectPhone(d.Info.ID)
					s.step = ""
				}
			})
			return
		}
		// Six boxes show the code; a transparent field over them takes
		// the typing, and the box the next digit goes in is outlined.
		var in mg.Element
		mg.Box(c).AlignSelf(mg.Start).Children(func() {
			mg.Row(c).Gap(8).Role(mg.RoleGroup).Label(locales.AddPhoneIPhoneCode()).PassThrough().Children(func() {
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
			in = mg.TextInputBase(c.Key("code"), &s.code).Label(locales.AddPhoneIPhoneCode()).Absolute().Top(0).Left(0).Right(0).Bottom(0).
				Opacity(0).FontSize(22).AutoFocus()
		})
		if in.Changed() {
			s.code = keepDigits(s.code, 6)
		}
		mg.Row(c).Gap(16).AlignItems(mg.Center).Wrap().Children(func() {
			pair := button(c, locales.PairingPair(), btnOpt{kind: btnPrimary}).Disabled(len(s.code) < 6 || s.pairing)
			if pair.Clicked() || (in.Submitted() && len(s.code) == 6) {
				s.pairing = true
			}
			mg.Spacer(c)
			if quietButton(c, locales.AddPhoneOlderIPhone()).Clicked() {
				s.old = true
			}
		})
		if s.pairing {
			mg.Text(c, locales.AddPhoneIPhoneWaiting()).FontSize(13.5).TextColor(p.ink2)
		}
		return
	}
	heading(c, locales.AddPhoneIPhoneTitle(), locales.AddPhoneOlderDetail())
	if d != nil {
		statusBox(c, func() {
			dot(c, p.amberDot, false)
			mg.Text(c, strings.TrimSpace(locales.AddPhoneFoundIOS(d.Info.Name, d.Info.OSVersion)))
		})
	}
	mg.Column(c).Padding(16, 18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(10).Children(func() {
		mg.Text(c, locales.AddPhoneOlderTitle()).FontSize(15).FontWeight(650)
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
		opt("helper", locales.AddPhoneOlderHelper(), locales.AddPhoneOlderHelperDetail())
		opt("mirror", locales.AddPhoneOlderMirroring(), locales.AddPhoneOlderMirroringDetail())
	})
	if s.oldPick != "" {
		mg.Text(c, locales.AddPhoneOlderNotYet()).FontSize(13.5).TextColor(p.ink2)
	} else {
		mg.Text(c, locales.AddPhoneOlderUpdate()).FontSize(13.5).TextColor(p.ink2).Margin(-6, 0, 0, 0)
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
