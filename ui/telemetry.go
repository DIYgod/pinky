package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/telemetry"
)

// usageStatsByDefault is whether usage statistics start on, with a note
// on the first run that says so and offers to turn them off. Nothing is
// sent before the person dismisses the note. Off, the note asks instead.
const usageStatsByDefault = true

// telState is the client's side of usage statistics, crash reports and
// feedback.
type telState struct {
	crashes   []telemetry.CrashReport // reports waiting since the start
	crashOpen bool                    // the crash report dialog
	crashText bool                    // the dialog shows the reports
	crashAuto bool                    // "send them automatically from now on"
	crashBusy bool
	crashErr  string

	notice     bool // the note about usage statistics shows
	sharedOpen bool // "What Pinky shares"
	sharedText string

	fb feedbackState
}

func (a *app) tel() *telemetry.Client { return a.opt.Telemetry }

// startTelemetry reads what waits for the person at start: the note about
// usage statistics and crash reports of earlier runs. Reports go out by
// themselves (telemetry.Client.Run) when the person chose so.
func (a *app) startTelemetry() {
	tel := a.tel()
	if tel == nil {
		return
	}
	a.t.notice = !tel.NoticeShown() && !telemetry.DoNotTrack()
	go func() {
		list := tel.Crashes()
		auto := tel.AutoCrash() && tel.CanSend() && !telemetry.DoNotTrack()
		a.post(func() {
			a.t.crashes = list
			a.t.crashOpen = len(list) > 0 && !auto && tel.CanSend()
		})
	}()
}

// telemetryBanner is the note about usage statistics, over every page
// until the person answers it.
func (a *app) telemetryBanner(c *mg.Context) {
	tel := a.tel()
	if tel == nil || !a.t.notice {
		return
	}
	p := pal(c)
	on := a.settings.UsageStats
	text := "Pinky shares anonymous usage statistics, such as how many tasks ran and which tools agents used, to get better. " +
		"Never your screens, what's typed, apps, phone names or PINs."
	if !on {
		text = "Help make Pinky better by sharing anonymous usage statistics, such as how many tasks ran and which tools agents used? " +
			"Never your screens, what's typed, apps, phone names or PINs."
	}
	mg.Row(c.Key("usage-note")).Gap(12).Padding(10, 16).AlignItems(mg.Center).Background(p.accentTint).
		BorderWidth(0, 0, 1, 0).BorderColor(p.line).Role(mg.RoleStatus).Label("Usage statistics").Children(func() {
		mg.Text(c, text).FontSize(13.5).Grow(1).MinWidth(0)
		if button(c, "What's shared", btnOpt{small: true}).Clicked() {
			a.openShared()
		}
		no, yes := "Turn off", "OK"
		if !on {
			no, yes = "No thanks", "Share"
		}
		if button(c, no, btnOpt{small: true}).Clicked() {
			a.answerNotice(false)
		}
		if button(c, yes, btnOpt{small: true, kind: btnPrimary}).Clicked() {
			a.answerNotice(true)
		}
	})
}

// answerNotice takes the person's answer to the note.
func (a *app) answerNotice(share bool) {
	a.t.notice = false
	if a.settings.UsageStats != share {
		a.settings.UsageStats = share
		a.changed()
	}
	tel := a.tel()
	go func() {
		tel.SetUsage(share)
		tel.MarkNoticeShown()
	}()
}

// setUsageStats is the switch in Settings › Privacy. Turning it on there
// answers the note too.
func (a *app) setUsageStats(on bool) {
	a.t.notice = false
	tel := a.tel()
	go func() {
		tel.SetUsage(on)
		tel.MarkNoticeShown()
	}()
}

func (a *app) openShared() {
	tel := a.tel()
	a.t.sharedOpen, a.t.sharedText = true, ""
	go func() {
		r := tel.Report()
		data, _ := json.MarshalIndent(r, "", "  ")
		text := string(data)
		if len(r.Counts) == 0 {
			text += "\n\nNothing has been counted since the last report."
		}
		a.post(func() { a.t.sharedText = text })
	}()
}

// telemetryDialogs are the crash report and "What Pinky shares" dialogs,
// and the feedback dialog.
func (a *app) telemetryDialogs(c *mg.Context) {
	if a.tel() == nil {
		return
	}
	a.crashDialog(c)
	a.sharedDialog(c)
	a.feedbackDialog(c)
}

func (a *app) sharedDialog(c *mg.Context) {
	if !a.t.sharedOpen {
		return
	}
	p := pal(c)
	mg.Modal(c, &a.t.sharedOpen, func() {
		mg.Column(c).Width(560).Gap(12).Label("What Pinky shares").Children(func() {
			mg.Text(c, "What Pinky shares").FontSize(17).FontWeight(650).Role(mg.RoleHeading)
			mg.Text(c, "About once a day, Pinky sends counts like these, with a random install ID, its version and your computer's system. "+
				"They never include what is on your phones' screens, what is typed, app names, phone names or IDs, PINs or file names. "+
				"Turn this off in Settings › Privacy, or set DO_NOT_TRACK=1.").FontSize(13.5).TextColor(p.ink2)
			text := a.t.sharedText
			if text == "" {
				text = "Reading…"
			}
			mg.Scroll(c).MaxHeight(320).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).Children(func() {
				mg.Text(c, text).Font("monospace").FontSize(12).Selectable()
			})
			mg.Row(c).Justify(mg.End).Children(func() {
				if button(c, "Close", btnOpt{kind: btnPrimary}).Clicked() {
					a.t.sharedOpen = false
				}
			})
		})
	})
}

// crashDialog asks whether to send the crash reports of earlier runs.
func (a *app) crashDialog(c *mg.Context) {
	if !a.t.crashOpen || len(a.t.crashes) == 0 {
		return
	}
	p := pal(c)
	list := a.t.crashes
	r := list[0]
	what := "Pinky"
	if r.Process == "daemon" {
		what = "Pinky's background gateway"
	}
	title := what + " quit unexpectedly"
	if len(list) > 1 {
		title = fmt.Sprintf("Pinky quit unexpectedly %d times", len(list))
	}
	if mg.Modal(c, &a.t.crashOpen, func() {
		mg.Column(c).Width(560).Gap(12).Label(title).Children(func() {
			mg.Text(c, title).FontSize(17).FontWeight(650).Role(mg.RoleHeading)
			when := r.Crashed.Local().Format("January 2 at 15:04")
			if time.Since(r.Crashed) < 24*time.Hour {
				when = "today at " + r.Crashed.Local().Format("15:04")
			}
			mg.Text(c, fmt.Sprintf("The last time, %s stopped %s. Send a crash report to help fix it? "+
				"It has Pinky's version, your computer's system and where in Pinky's code it stopped, nothing from your phones.", what, when)).
				FontSize(13.5).TextColor(p.ink2)
			mg.Collapsible(c, "Show the report", &a.t.crashText, func() {
				var b strings.Builder
				for i, x := range list {
					if i > 0 {
						b.WriteString("\n\n")
					}
					fmt.Fprintf(&b, "Pinky %s (%s) · %s %s · %s\n%s\n\n%s", x.Version, x.Process, x.OS, x.OSVersion, x.Arch, x.Reason, x.Stack)
				}
				mg.Scroll(c).MaxHeight(260).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).Children(func() {
					mg.Text(c, b.String()).Font("monospace").FontSize(11.5).Selectable()
				})
			})
			mg.Checkbox(c, &a.t.crashAuto, "Send crash reports automatically from now on")
			if a.t.crashErr != "" {
				mg.Text(c, a.t.crashErr).FontSize(13).TextColor(p.red)
			}
			mg.Row(c).Gap(8).Justify(mg.End).Children(func() {
				if button(c, "Don't Send", btnOpt{}).Clicked() {
					a.answerCrashes(false)
				}
				label := "Send Report"
				if len(list) > 1 {
					label = "Send Reports"
				}
				if a.t.crashBusy {
					label = "Sending…"
				}
				b := button(c, label, btnOpt{kind: btnPrimary})
				if a.t.crashBusy {
					b.Disabled(true)
				} else if b.Clicked() {
					a.answerCrashes(true)
				}
			})
		})
	}).Dismissed() {
		a.answerCrashes(false)
	}
}

// answerCrashes sends or forgets the waiting crash reports, and keeps the
// choice to send them automatically.
func (a *app) answerCrashes(send bool) {
	tel := a.tel()
	list := a.t.crashes
	if a.t.crashAuto && !a.settings.CrashReports {
		a.settings.CrashReports = true
		a.changed()
		go tel.SetAutoCrash(true)
	}
	if !send {
		a.t.crashOpen, a.t.crashes = false, nil
		go func() {
			for _, r := range list {
				tel.DiscardCrash(r.ID)
			}
		}()
		return
	}
	a.t.crashBusy, a.t.crashErr = true, ""
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, time.Minute)
		defer cancel()
		var err error
		for _, r := range list {
			if err = tel.SendCrash(ctx, r.ID); err != nil && !telemetry.Permanent(err) {
				break
			}
			err = nil
		}
		a.post(func() {
			a.t.crashBusy = false
			if err != nil {
				a.t.crashErr = "Couldn't send: " + err.Error()
				return
			}
			a.t.crashOpen, a.t.crashes = false, nil
			a.toasts = append(a.toasts, "Thanks, the crash report was sent")
		})
	}()
}

// privacyTelemetry is Settings › Privacy's part about usage statistics and
// crash reports.
func (a *app) privacyTelemetry(c *mg.Context) {
	tel := a.tel()
	if tel == nil {
		return
	}
	p := pal(c)
	st := &a.settings
	mg.Column(c).Gap(8).Label("Usage statistics and crash reports").Children(func() {
		mg.Text(c, "Helping Pinky get better").FontSize(15).FontWeight(650).Margin(0, 0, 4, 0)
		mg.Column(c).Children(func() {
			if telemetry.DoNotTrack() {
				settingRow(c, "Share anonymous usage statistics", "Off: DO_NOT_TRACK is set on this computer.", false, nil)
			} else if a.toggleRow(c, "usage", "Share anonymous usage statistics",
				"Counts such as tasks run, tools used and phones connected, about once a day. Never your screens, what's typed, apps, phone names or PINs.",
				false, &st.UsageStats) {
				a.setUsageStats(st.UsageStats)
			}
			if a.toggleRow(c, "crash", "Send crash reports automatically",
				"Otherwise Pinky asks after it quits unexpectedly. A report has Pinky's version, your system and where in Pinky's code it stopped.",
				false, &st.CrashReports) {
				on := st.CrashReports
				go tel.SetAutoCrash(on)
			}
			settingRow(c, "What's shared", "The usage report as it would go out now", true, func() {
				if button(c, "Show…", btnOpt{}).Label("Show what's shared").Clicked() {
					a.openShared()
				}
			})
		})
		if !tel.CanSend() {
			mg.Text(c, "This build of Pinky sends nothing: it has no address to send to.").FontSize(12.5).TextColor(p.ink3)
		}
	})
}

// helpMenu is the menu bar's Help menu, with Send Feedback.
func (a *app) helpMenu() *mygo.MenuItem {
	if a.tel() == nil {
		return nil
	}
	items := []*mygo.MenuItem{
		{Label: "Send Feedback…", Click: func(*mygo.MenuItem, *mygo.Window) { a.openFeedback() }},
	}
	if runtime.GOOS == "darwin" {
		return &mygo.MenuItem{Role: mygo.RoleHelp, Submenu: items}
	}
	return &mygo.MenuItem{Label: "Help", Submenu: items}
}

// feedbackLink is Send Feedback at the foot of Settings' sections.
func (a *app) feedbackLink(c *mg.Context) {
	if a.tel() == nil {
		return
	}
	p := pal(c)
	mg.Spacer(c)
	b := mg.ButtonBase(c.Key("send-feedback")).Height(32).Padding(0, 12).Radius(8).Justify(mg.Start).AlignItems(mg.Center).
		Label("Send feedback")
	col := p.ink2
	if b.Hovered() {
		b.Background(p.bg2)
		col = p.ink
	}
	if b.Clicked() {
		a.openFeedback()
	}
	b.TextColor(col).Children(func() { mg.Text(c, "Send feedback…").FontSize(13.5) })
}
