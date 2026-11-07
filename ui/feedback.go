package ui

import (
	"fmt"
	"strings"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/telemetry"
)

// Feedback goes to GitHub: Send Feedback writes it here, then opens a new
// issue with it filled in, which the person reads and submits with their
// GitHub account (telemetry.IssueURL). The issue is public.

// feedbackState is the Send Feedback dialog's.
type feedbackState struct {
	open    bool
	title   string
	message string
	attach  bool // attach the diagnostics
	review  bool // the diagnostics show
	diag    telemetry.Diagnostics
	reading bool
	err     string
	seq     int
}

// openURL opens an address in the browser; e2e builds may log it instead.
var openURL = func(c *mg.Context, u string, done func(error)) { c.OpenURLThen(u, done) }

// openFeedback opens Send Feedback, from the Help menu, the menu bar
// icon's menu and Settings. A draft that was not sent stays.
func (a *app) openFeedback() {
	tel := a.tel()
	if tel == nil {
		return
	}
	fb := &a.t.fb
	if !fb.open && fb.message == "" && fb.title == "" {
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
		d := tel.Diagnostics(phones, extra...)
		a.post(func() {
			if seq == fb.seq {
				fb.diag, fb.reading = d, false
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

// feedback is what the dialog holds, as telemetry.Feedback.
func (a *app) feedback() telemetry.Feedback {
	fb := &a.t.fb
	f := telemetry.Feedback{Title: fb.title, Message: fb.message, Version: a.tel().VersionLine()}
	if fb.attach && !fb.reading {
		d := fb.diag
		f.Diagnostics = &d
	}
	return f
}

// feedbackDialog is Send Feedback: a title, a message and the
// diagnostics, which the person can read, then GitHub.
func (a *app) feedbackDialog(c *mg.Context) {
	fb := &a.t.fb
	if !fb.open {
		return
	}
	p := pal(c)
	mg.Modal(c, &fb.open, func() {
		mg.Column(c).Width(560).Gap(12).Label("Send feedback").Children(func() {
			mg.Text(c, "Send feedback").FontSize(17).FontWeight(650).Role(mg.RoleHeading)
			mg.Text(c, "What happened, or what would make Pinky better? If something went wrong, say what you were doing.").
				FontSize(13.5).TextColor(p.ink2)
			mg.TextInput(c.Key("fb-title"), &fb.title).Label("Title").Placeholder("Title (optional): a few words")
			mg.TextArea(c.Key("fb-message"), &fb.message).Label("Message").Placeholder("Your message").Lines(5, 10).AutoFocus()
			cut := false
			mg.Column(c).Gap(4).Children(func() {
				mg.Checkbox(c, &fb.attach, "Attach diagnostics: versions, settings, phone models and Pinky's recent logs")
				if !fb.attach {
					return
				}
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
				if fb.reading {
					return
				}
				_, cut = telemetry.IssueURL(a.feedback())
				if fb.review {
					mg.Scroll(c).MaxHeight(240).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).Children(func() {
						mg.Text(c, fb.diag.String()).Font("monospace").FontSize(11.5).Selectable()
					})
				}
			})
			note := "Pinky opens a new issue on GitHub with this filled in; read it there and submit it with your GitHub account. Issues are public."
			if cut {
				note += " Not all of the logs fit in the link: Copy puts everything on the clipboard to paste into the issue."
			}
			mg.Text(c, note).FontSize(12.5).TextColor(p.ink3)
			if fb.err != "" {
				mg.Text(c, fb.err).FontSize(13).TextColor(p.red).Role(mg.RoleStatus)
			}
			mg.Row(c).Gap(8).Justify(mg.End).Children(func() {
				if button(c, "Copy", btnOpt{icon: icCopy}).Label("Copy feedback").Tooltip("Copy the message and diagnostics").Clicked() {
					c.WriteClipboard(a.feedback().Text())
					a.toasts = append(a.toasts, "Copied")
				}
				if button(c, "Cancel", btnOpt{}).Clicked() {
					fb.open = false
				}
				open := button(c, "Open on GitHub", btnOpt{kind: btnPrimary})
				switch {
				case strings.TrimSpace(fb.message) == "" || (fb.attach && fb.reading):
					open.Disabled(true)
				case open.Clicked():
					a.openIssue(c)
				}
			})
		})
	})
}

// openIssue opens the new issue in the browser and closes the dialog.
func (a *app) openIssue(c *mg.Context) {
	fb := &a.t.fb
	u, _ := telemetry.IssueURL(a.feedback())
	fb.err = ""
	openURL(c, u, func(err error) {
		if err != nil {
			fb.err = "Couldn't open the browser: " + err.Error() + ". Copy the feedback and send it another way."
			return
		}
		*fb = feedbackState{seq: fb.seq}
		a.toasts = append(a.toasts, "Finish your feedback on GitHub. Thanks!")
	})
}
