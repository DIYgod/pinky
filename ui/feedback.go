package ui

import (
	"context"
	"fmt"
	"net/mail"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/telemetry"
)

// feedbackState is the Send Feedback dialog's.
type feedbackState struct {
	open    bool
	message string
	email   string
	attach  bool   // attach the diagnostics
	review  bool   // the diagnostics show
	diag    string // read when the dialog opens
	reading bool
	sending bool
	err     string
	seq     int
}

// openFeedback opens Send Feedback, from the Help menu, the menu bar
// icon's menu and Settings. A draft that was not sent stays.
func (a *app) openFeedback() {
	tel := a.tel()
	if tel == nil {
		return
	}
	fb := &a.t.fb
	if !fb.open && fb.message == "" {
		fb.attach = true
	}
	fb.open, fb.err = true, ""
	a.showMain()
	// The diagnostics are read again each time: the phones and the logs
	// change.
	fb.seq++
	seq := fb.seq
	fb.reading = true
	var phones []driver.Info
	for _, d := range a.devices {
		phones = append(phones, d.Info)
	}
	extra := a.diagnosticLines()
	go func() {
		text := tel.Diagnostics(phones, extra...)
		a.post(func() {
			if seq == fb.seq {
				fb.diag, fb.reading = text, false
			}
		})
	}()
	if a.main != nil {
		a.main.Invalidate()
	}
}

// diagnosticLines are what the client adds to the diagnostics: settings
// that change how Pinky behaves, and the video decoder.
func (a *app) diagnosticLines() []string {
	s := a.settings
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	lines := []string{fmt.Sprintf("Settings: preset %d · read-only %s · unlock from computer %s · hide codes %s · record %s · keep %s · remote agents %s · developer mode %s",
		s.Preset, onOff(s.ReadOnly), onOff(s.UnlockFromComputer), onOff(s.HideCodes), onOff(s.Record), keepLabel(s.KeepDays),
		onOff(s.RemoteAgents), onOff(s.DeveloperMode))}
	if v := videoLine(a); v != "" {
		lines = append(lines, "Video: "+v)
	}
	return lines
}

// feedbackDialog is Send Feedback: a message, an e-mail address for an
// answer, and the diagnostics, which the person can read before sending.
func (a *app) feedbackDialog(c *mg.Context) {
	fb := &a.t.fb
	if !fb.open {
		return
	}
	p := pal(c)
	tel := a.tel()
	mg.Modal(c, &fb.open, func() {
		mg.Column(c).Width(560).Gap(12).Label("Send feedback").Children(func() {
			mg.Text(c, "Send feedback").FontSize(17).FontWeight(650).Role(mg.RoleHeading)
			mg.Text(c, "What happened, or what would make Pinky better? If something went wrong, say what you were doing.").
				FontSize(13.5).TextColor(p.ink2)
			mg.TextArea(c.Key("fb-message"), &fb.message).Label("Message").Placeholder("Your message").Lines(5, 10).AutoFocus()
			mg.TextInput(c.Key("fb-email"), &fb.email).Label("E-mail (optional)").Placeholder("E-mail, if you'd like an answer (optional)")
			mg.Column(c).Gap(4).Children(func() {
				mg.Checkbox(c, &fb.attach, "Attach diagnostics: versions, phone models and Pinky's recent logs")
				if fb.attach {
					mg.Row(c).Gap(8).Padding(0, 0, 0, 26).AlignItems(mg.Center).Children(func() {
						mg.Text(c, "Phone names and IDs, your user name, e-mail and IP addresses, app and task names are taken out.").
							FontSize(12.5).TextColor(p.ink3).Grow(1).MinWidth(0)
						label := "Review"
						if fb.review {
							label = "Hide"
						}
						if button(c, label, btnOpt{small: true}).Label(label + " diagnostics").Clicked() {
							fb.review = !fb.review
						}
					})
					if fb.review {
						text := fb.diag
						if fb.reading {
							text = "Reading…"
						}
						mg.Scroll(c).MaxHeight(240).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).Children(func() {
							mg.Text(c, text).Font("monospace").FontSize(11.5).Selectable()
						})
					}
				}
			})
			if fb.err != "" {
				mg.Text(c, fb.err).FontSize(13).TextColor(p.red).Role(mg.RoleStatus)
			}
			if !tel.CanSend() {
				mg.Text(c, "This build of Pinky has no address to send feedback to. Copy it and send it another way.").
					FontSize(12.5).TextColor(p.ink3)
			}
			mg.Row(c).Gap(8).Justify(mg.End).Children(func() {
				if button(c, "Copy", btnOpt{icon: icCopy}).Label("Copy feedback").Tooltip("Copy the message and diagnostics").Clicked() {
					c.WriteClipboard(a.feedbackText())
					a.toasts = append(a.toasts, "Copied")
				}
				if button(c, "Cancel", btnOpt{}).Clicked() {
					fb.open = false
				}
				label := "Send"
				if fb.sending {
					label = "Sending…"
				}
				send := button(c, label, btnOpt{kind: btnPrimary})
				switch {
				case !tel.CanSend():
					send.Disabled(true).Tooltip("This build has nowhere to send it")
				case fb.sending || strings.TrimSpace(fb.message) == "":
					send.Disabled(true)
				case send.Clicked():
					a.sendFeedback()
				}
			})
		})
	})
}

// feedbackText is the feedback as text, for Copy.
func (a *app) feedbackText() string {
	fb := &a.t.fb
	var b strings.Builder
	b.WriteString(strings.TrimSpace(fb.message))
	if e := strings.TrimSpace(fb.email); e != "" {
		b.WriteString("\n\nFrom: " + e)
	}
	if fb.attach && fb.diag != "" {
		b.WriteString("\n\n---\n" + fb.diag)
	}
	return b.String()
}

func (a *app) sendFeedback() {
	fb := &a.t.fb
	email := strings.TrimSpace(fb.email)
	if email != "" {
		if _, err := mail.ParseAddress(email); err != nil || !strings.Contains(email, "@") {
			fb.err = "That e-mail address doesn't look right. Leave it empty to send without one."
			return
		}
	}
	f := telemetry.Feedback{Message: fb.message, Email: email}
	if fb.attach {
		if fb.reading {
			fb.err = "The diagnostics are still being read; try again in a moment."
			return
		}
		f.Diagnostics = fb.diag
	}
	fb.sending, fb.err = true, ""
	tel := a.tel()
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		err := tel.SendFeedback(ctx, f)
		a.post(func() {
			fb.sending = false
			if err != nil {
				fb.err = "Couldn't send it: " + err.Error() + ". Your message is still here."
				return
			}
			*fb = feedbackState{seq: fb.seq}
			a.toasts = append(a.toasts, "Thanks! Your feedback was sent.")
		})
	}()
}
