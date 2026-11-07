package ui

import (
	"encoding/json"
	"runtime"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/telemetry"
)

// usageStatsByDefault is whether usage statistics and crash reports start
// on. They are on, without a note on the first run; Settings › Privacy
// turns them off, and so does DO_NOT_TRACK (docs/TELEMETRY.md).
const usageStatsByDefault = true

// telState is the client's side of usage statistics, crash reports and
// feedback.
type telState struct {
	sharedOpen bool // "What Pinky shares"
	sharedText string

	fb feedbackState
}

func (a *app) tel() *telemetry.Client { return a.opt.Telemetry }

// setUsageStats is the switch in Settings › Privacy.
func (a *app) setUsageStats(on bool) {
	tel := a.tel()
	go tel.SetUsage(on)
}

// openShared shows the usage report as it would go out now.
func (a *app) openShared() {
	r := a.tel().Report()
	data, _ := json.MarshalIndent(r, "", "  ")
	text := string(data)
	if len(r.Counts) == 0 {
		text += "\n\nNothing has been counted since the last report."
	}
	a.t.sharedOpen, a.t.sharedText = true, text
}

// telemetryDialogs are "What Pinky shares" and Send Feedback.
func (a *app) telemetryDialogs(c *mg.Context) {
	if a.tel() == nil {
		return
	}
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
			mg.Text(c, "About once a day, Pinky sends counts like these, with a random install ID, its version and your computer's system, "+
				"and after a crash, where in Pinky's code it stopped. They never include what is on your phones' screens, what is typed, "+
				"app names, phone names or IDs, PINs or file names. Turn this off in Settings › Privacy, or set DO_NOT_TRACK=1.").
				FontSize(13.5).TextColor(p.ink2)
			mg.Scroll(c).MaxHeight(320).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).Children(func() {
				mg.Text(c, a.t.sharedText).Font("monospace").FontSize(12).Selectable()
			})
			mg.Row(c).Justify(mg.End).Children(func() {
				if button(c, "Close", btnOpt{kind: btnPrimary}).Clicked() {
					a.t.sharedOpen = false
				}
			})
		})
	})
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
			const title = "Share anonymous usage statistics and crash reports"
			if telemetry.DoNotTrack() {
				settingRow(c, title, "Off: DO_NOT_TRACK is set on this computer.", false, nil)
			} else if a.toggleRow(c, "usage", title,
				"Counts such as tasks run, tools used and phones connected, about once a day, and where Pinky's code stopped after a crash. "+
					"Never your screens, what's typed, apps, phone names or PINs.",
				false, &st.UsageStats) {
				a.setUsageStats(st.UsageStats)
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
