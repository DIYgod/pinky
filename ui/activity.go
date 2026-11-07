package ui

import (
	"encoding/json"
	"fmt"
	"html"
	"strings"
	"time"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/gateway"
)

// activityState is the Activity screen's: finished tasks by day.
type activityState struct {
	tasks   []gateway.TaskView
	loaded  bool
	query   string
	asked   string // the query of the last load
	sel     string
	replay  bool
	at      int // the step the replay shows
	playing bool
	nextAt  time.Time
	seq     int
	lastRun time.Time
}

func (s *activityState) init() {}

// load reads History again, with the search.
func (s *activityState) load(a *app) {
	s.seq++
	seq := s.seq
	q := s.query
	s.asked = q
	s.lastRun = time.Now()
	go func() {
		tasks, err := a.api.History(a.ctx, gateway.HistoryQuery{Search: q, Limit: 200})
		a.post(func() {
			if seq != s.seq {
				return
			}
			if err != nil {
				a.toasts = append(a.toasts, "Couldn't read Activity: "+err.Error())
				return
			}
			s.tasks, s.loaded = tasks, true
			found := false
			for _, t := range tasks {
				found = found || t.ID == s.sel
			}
			if !found {
				s.sel = ""
				if len(tasks) > 0 {
					s.sel = tasks[0].ID
				}
			}
		})
	}()
}

func (a *app) activityPage(c *mg.Context) {
	p := pal(c)
	s := &a.act
	if s.seq == 0 {
		s.load(a) // first shown
	}
	// Search as the person types, once they pause.
	if s.query != s.asked && time.Since(s.lastRun) > 250*time.Millisecond {
		s.load(a)
	} else if s.query != s.asked {
		c.After(260 * time.Millisecond)
	}
	// Tasks that finish while Activity shows join it.
	if s.loaded && a.finishedSince(s.lastRun) {
		s.load(a)
	}
	pageHeader(c, func() {
		mg.Text(c, "Activity").FontSize(17).FontWeight(650)
		mg.Spacer(c)
		if button(c, "Export all", btnOpt{icon: icExport, small: true}).Tooltip("Every finished task, as a report and JSON").Clicked() {
			a.exportHistory(nil, "activity")
		}
		mg.Row(c).Width(300).Height(34).Padding(0, 12).Radius(9).Background(p.bg2).Border(1, p.line).Gap(8).AlignItems(mg.Center).Children(func() {
			mg.Icon(c, icSearch).FontSize(16).TextColor(p.ink3)
			mg.TextInputBase(c.Key("act-search"), &s.query).Grow(1).Placeholder("Search tasks and apps").Label("Search activity").FontSize(14)
		})
	})
	mg.Row(c).Grow(1).MinHeight(0).AlignItems(mg.Stretch).Children(func() {
		mg.Scroll(c).Basis(400).Grow(1).MinWidth(320).MaxWidth(520).BorderWidth(0, 1, 0, 0).BorderColor(p.line).Padding(14, 12).Gap(2).
			Label("Tasks").Children(func() {
			if !s.loaded {
				mg.Box(c).Padding(20).Center().Children(func() { spinner(c) })
				return
			}
			if len(s.tasks) == 0 {
				msg := "No finished tasks yet."
				if s.query != "" {
					msg = "No tasks match “" + s.query + "”."
				}
				mg.Text(c, msg).FontSize(14).TextColor(p.ink2).Padding(12)
				return
			}
			now := c.Now()
			group := ""
			for _, t := range s.tasks {
				if g := dayLabel(t.Started, now); g != group {
					group = g
					pad := float32(16)
					if group == dayLabel(s.tasks[0].Started, now) {
						pad = 4
					}
					mg.Text(c, group).FontSize(13).FontWeight(600).TextColor(p.ink3).Padding(pad, 12, 6, 12)
				}
				a.activityRow(c.Key(t.ID), t)
			}
		})
		mg.Scroll(c).Basis(460).Grow(999).MinWidth(0).Padding(28, 32).Gap(18).Label("Task detail").Children(func() {
			var t *gateway.TaskView
			for i := range s.tasks {
				if s.tasks[i].ID == s.sel {
					t = &s.tasks[i]
				}
			}
			if t == nil {
				if s.loaded {
					mg.Text(c, "Choose a task to see its steps.").FontSize(14).TextColor(p.ink2)
				}
				return
			}
			a.activityDetail(c, *t)
		})
	})
}

// finishedSince reports whether a task ended after t.
func (a *app) finishedSince(t time.Time) bool {
	for _, x := range a.tasks {
		if !x.Ended.IsZero() && x.Ended.After(t) {
			return true
		}
	}
	return false
}

func outcome(c *mg.Context, t gateway.TaskView) (string, mg.Element) {
	p := pal(c)
	switch {
	case t.State == gateway.TaskStopped:
		return "Stopped", dot(c, p.ink3, false)
	case t.State == gateway.TaskFailed:
		return "Failed", dot(c, p.red, false)
	case denied(t) && !strings.HasPrefix(lastText(t), "You allowed"):
		if allowedLater(t) {
			return "Done", dot(c, p.green, false)
		}
		return "Denied", hollowDot(c)
	}
	return "Done", dot(c, p.green, false)
}

func lastText(t gateway.TaskView) string {
	if len(t.Steps) == 0 {
		return ""
	}
	return t.Steps[len(t.Steps)-1].Text
}

// allowedLater reports whether the person allowed something after a
// denial, as when the agent fixed what they objected to.
func allowedLater(t gateway.TaskView) bool {
	seenDeny := false
	for _, s := range t.Steps {
		if strings.HasPrefix(s.Text, "You denied") || s.Status == "approval_denied" {
			seenDeny = true
		}
		if seenDeny && strings.HasPrefix(s.Text, "You allowed") {
			return true
		}
	}
	return false
}

func (a *app) activityRow(c *mg.Context, t gateway.TaskView) {
	p := pal(c)
	on := a.act.sel == t.ID
	row := mg.ButtonBase(c).Padding(10, 12).Radius(10).Gap(12).AlignItems(mg.Center).Justify(mg.Start).FillWidth().Label(t.Title)
	switch {
	case on:
		row.Background(p.bg3)
		selectionMark(c, row)
	case row.Hovered():
		row.Background(p.bg2)
	}
	if row.Clicked() {
		a.act.sel, a.act.replay = t.ID, false
		a.closeRecording()
	}
	app := appOf(t)
	row.Children(func() {
		appIcon(c, nonEmpty(app, t.Title), 34, 9, 15, "")
		mg.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			mg.Text(c, t.Title).FontWeight(600).SingleLine()
			sub := strings.Join(nonBlank(t.Agent, t.DeviceName, t.Started.Format("15:04")), " · ")
			mg.Text(c, sub).FontSize(13).TextColor(p.ink2).SingleLine()
		})
		mg.Column(c).AlignItems(mg.End).Gap(2).Shrink(0).Children(func() {
			mg.Row(c).Gap(6).AlignItems(mg.Center).Children(func() {
				word, _ := outcome(c, t)
				mg.Text(c, word).FontSize(13)
			})
			if n := approvals(t); n > 0 {
				mg.Text(c, plural(n, "approval", "approvals")).FontSize(12.5).TextColor(p.ink3)
			}
		})
	})
}

func nonBlank(s ...string) []string {
	var out []string
	for _, v := range s {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// summaryOf is the summary the agent gave phone_release, if any.
func summaryOf(t gateway.TaskView) string {
	sum := ""
	for _, s := range t.Steps {
		if s.Args == "" {
			continue
		}
		var a struct {
			Summary string `json:"summary"`
		}
		if json.Unmarshal([]byte(s.Args), &a) == nil && a.Summary != "" {
			sum = a.Summary
		}
	}
	return sum
}

func (a *app) activityDetail(c *mg.Context, t gateway.TaskView) {
	p := pal(c)
	app := appOf(t)
	col := appColor(nonEmpty(app, t.Title))
	mg.Row(c).Gap(14).AlignItems(mg.Start).Wrap().Children(func() {
		appIcon(c, nonEmpty(app, t.Title), 44, 11, 19, col)
		mg.Column(c).Basis(220).Grow(1).MinWidth(0).Children(func() {
			mg.Text(c, t.Title).FontSize(20).FontWeight(650).LineHeight(1.3).Role(mg.RoleHeading)
			when := dayLabel(t.Started, c.Now()) + " " + t.Started.Format("15:04")
			meta := nonBlank(t.Agent, t.DeviceName, when, elapsed(taskDuration(t, c.Now())))
			mg.Text(c, strings.Join(meta, " · ")).FontSize(13.5).TextColor(p.ink2).Margin(3, 0, 0, 0)
		})
		mg.Row(c).Gap(8).Children(func() {
			rec := a.recordingOf(t.ID)
			switch {
			case rec != nil:
				if button(c, "Watch recording", btnOpt{icon: icPlay}).Clicked() {
					a.act.replay, a.act.playing = true, false
					a.openRecording(t.ID, *rec)
				}
			default:
				if button(c, "Watch replay", btnOpt{icon: icPlay}).Tooltip("Step by step, with the screenshots").Clicked() {
					a.act.replay, a.act.at, a.act.playing = true, 0, false
					a.closeRecording()
				}
			}
			more := button(c, "", btnOpt{icon: icMore, label: "More actions"}).Width(34).Padding(0)
			more.PopoverMenuButton(func(m *mg.Menu) {
				if m.Item("Export…").Chosen() {
					a.exportHistory([]string{t.ID}, t.Title)
				}
				if rec != nil && m.Item("Step-by-step replay").Chosen() {
					a.act.replay, a.act.at, a.act.playing = true, 0, false
					a.closeRecording()
				}
				m.Separator()
				m.Item("Delete").Disabled(true)
			})
		})
	})
	if sum := summaryOf(t); sum != "" {
		mg.Text(c, sum).FontSize(15).LineHeight(1.5).MaxWidth(560)
	}
	if a.act.replay {
		if pl := a.rec.pl; pl != nil && pl.task == t.ID {
			a.recordingPanel(c, t, pl)
		} else {
			a.replayView(c, t)
		}
		return
	}
	mg.Column(c).Gap(12).Children(func() {
		mg.Text(c, "Steps").FontSize(13).FontWeight(600).TextColor(p.ink3)
		mg.Column(c).Gap(10).Role(mg.RoleList).Children(func() {
			for i, s := range t.Steps {
				text, sub, kind := stepDisplay(t, i)
				you := kind == stepYou
				if sub == "" && you && s.Elapsed >= time.Second && strings.HasPrefix(text, "You ") &&
					(strings.Contains(text, "allowed") || strings.Contains(text, "denied")) {
					sub = "Answered in " + elapsed(s.Elapsed)
				}
				key := fmt.Sprintf("act/%s/%d", t.ID, s.Index)
				shot := a.hasShot(s)
				open := shot && a.previews[key]
				mg.Column(c.Key(i)).Gap(10).Role(mg.RoleListItem).Children(func() {
					mg.Row(c).Gap(14).AlignItems(mg.Center).Children(func() {
						switch {
						case you:
							mg.Box(c).Size(28, 54).Center().Shrink(0).Children(func() {
								mg.Box(c).Size(28, 28).Radius(14).Background(p.bg3).Center().Children(func() {
									mg.Icon(c, icPerson).FontSize(15).TextColor(p.ink2)
								})
							})
						case shot:
							b := mg.ButtonBase(c).Radius(6).Shrink(0).Label("Screenshot of “" + text + "”").Expanded(open).
								Tooltip("Show the screenshot")
							if b.Clicked() {
								a.previews[key] = !open
							}
							b.Children(func() { a.shotView(c, t, s, 54) })
						default:
							stepThumb(c, col)
						}
						mg.Column(c).Gap(1).Children(func() {
							tx := mg.Text(c, text).FontSize(14)
							if you {
								tx.FontWeight(600)
							}
							if sub != "" {
								mg.Text(c, sub).FontSize(13).TextColor(p.ink2)
							}
						})
					})
					if open {
						a.shotView(c, t, s, 420).Margin(0, 0, 6, 42)
					}
				})
			}
		})
	})
}

// stepThumb is the small phone sketch beside an agent's step; the
// screenshots behind it come with the history store's media.
func stepThumb(c *mg.Context, col string) {
	p := pal(c)
	mg.Box(c).Size(28, 54).Shrink(0).Radius(6).Border(1, p.line2).Background(hex("#ffffff")).Clip().
		Draw(func(pt *mg.Painter, r mg.Rect) {
			pt.Fill(mg.Rect{X: r.X, Y: r.Y, W: r.W, H: 9}, hex(col), 0)
			pt.Fill(mg.Rect{X: r.X + 4, Y: r.Y + 15, W: r.W - 8, H: 3}, hex("#dcdce2"), 1.5)
			pt.Fill(mg.Rect{X: r.X + 4, Y: r.Y + 22, W: r.W - 13, H: 3}, hex("#dcdce2"), 1.5)
			pt.Fill(mg.Rect{X: r.X + 4, Y: r.Y + r.H - 12, W: r.W - 8, H: 7}, hex("#e7e7ec"), 2)
		})
}

// recordingPanel is Watch recording: the task's screen recording with its
// step marks, in the replay's panel.
func (a *app) recordingPanel(c *mg.Context, t gateway.TaskView, pl *recPlayer) {
	p := pal(c)
	mg.Column(c).Gap(16).Padding(20).Radius(12).Border(1, p.line).Background(p.bg2).Role(mg.RoleDialog).Label("Recording").Children(func() {
		mg.Row(c).Gap(12).AlignItems(mg.Center).Children(func() {
			mg.Text(c, "Recording · "+t.Title).FontSize(16).FontWeight(650).Grow(1)
			if button(c, "Close", btnOpt{}).Clicked() {
				a.act.replay = false
				a.closeRecording()
			}
		})
		a.recordingView(c, t, pl)
	})
}

// replayView is Watch replay: the task's steps one at a time, with the
// screenshot of each, on a timeline. Tasks with a screen recording play it
// instead (recordingPanel); this is the replay of those without.
func (a *app) replayView(c *mg.Context, t gateway.TaskView) {
	p := pal(c)
	st := &a.act
	n := len(t.Steps)
	if st.at >= n {
		st.at = max(0, n-1)
	}
	if st.playing {
		now := c.Now()
		if !now.Before(st.nextAt) {
			if st.at < n-1 {
				st.at++
				st.nextAt = now.Add(1500 * time.Millisecond)
			} else {
				st.playing = false
			}
		}
		c.After(100 * time.Millisecond)
	}
	mg.Column(c).Gap(18).Padding(20).Radius(12).Border(1, p.line).Background(p.bg2).Role(mg.RoleDialog).Label("Replay").Children(func() {
		mg.Row(c).Gap(12).AlignItems(mg.Center).Children(func() {
			mg.Text(c, "Replay · "+t.Title).FontSize(16).FontWeight(650).Grow(1)
			if button(c, "Close", btnOpt{}).Clicked() {
				a.act.replay, a.act.playing = false, false
			}
		})
		if n == 0 {
			mg.Text(c, "This task has no steps.").FontSize(13.5).TextColor(p.ink2)
			return
		}
		s := t.Steps[st.at]
		text, sub, _ := stepDisplay(t, st.at)
		mg.Row(c).Gap(20).AlignItems(mg.Start).Wrap().Children(func() {
			a.shotView(c, t, s, 420)
			mg.Column(c).Basis(200).Grow(1).MinWidth(0).Gap(6).Children(func() {
				mg.Text(c, fmt.Sprintf("Step %d of %d", st.at+1, n)).FontSize(12.5).FontWeight(600).TextColor(p.ink3)
				mg.Text(c, text).FontSize(16).FontWeight(600).LineHeight(1.35)
				if sub != "" {
					mg.Text(c, sub).FontSize(13.5).TextColor(p.ink2)
				}
				if !s.At.IsZero() {
					mg.Text(c, s.At.Format("15:04:05")).FontSize(13).TextColor(p.ink3)
				}
				mg.Row(c).Gap(8).Margin(10, 0, 0, 0).Children(func() {
					if button(c, "", btnOpt{icon: icChevL, label: "Previous step"}).Width(34).Padding(0).Disabled(st.at == 0).Clicked() {
						st.at--
						st.playing = false
					}
					play := "Play"
					if st.playing {
						play = "Pause"
					}
					if button(c, play, btnOpt{icon: icPlay}).Clicked() {
						st.playing = !st.playing
						if st.playing {
							if st.at >= n-1 {
								st.at = 0
							}
							st.nextAt = c.Now().Add(1500 * time.Millisecond)
						}
					}
					if button(c, "", btnOpt{icon: icChevR, label: "Next step"}).Width(34).Padding(0).Disabled(st.at >= n-1).Clicked() {
						st.at++
						st.playing = false
					}
				})
			})
		})
		at := st.at
		mg.Box(c).Height(30).FillWidth().Draw(func(pt *mg.Painter, r mg.Rect) {
			pt.Fill(mg.Rect{X: r.X, Y: r.Y + 13, W: r.W, H: 4}, p.bg3, 2)
			for i := range t.Steps {
				you := kindOf(t, i) == stepYou
				size := float32(10)
				col := p.ink3
				if you {
					size, col = 14, p.ink
				}
				if i == at {
					col = p.accent
				}
				x := r.X + r.W*(float32(i)+0.5)/float32(max(n, 1))
				var dotPath mg.Path
				dotPath.Circle(x, r.Y+15, size/2+2)
				pt.FillPath(&dotPath, p.bg2)
				var d2 mg.Path
				d2.Circle(x, r.Y+15, size/2)
				pt.FillPath(&d2, col)
			}
		})
		mg.Row(c).Children(func() {
			mg.Text(c, "Each dot is a step. Larger dots are moments you were asked.").FontSize(12.5).TextColor(p.ink2).Grow(1)
			mg.Text(c, clock(taskDuration(t, c.Now()))).FontSize(13).TextColor(p.ink2)
		})
	})
}

// reportHTML is a small, self-contained report of tasks, for a gateway
// that cannot export its own.
func reportHTML(tasks []gateway.TaskView) string {
	var b strings.Builder
	esc := html.EscapeString
	title := "Pinky activity"
	if len(tasks) == 1 {
		title = tasks[0].Title
	}
	fmt.Fprintf(&b, "<!doctype html><meta charset=utf-8><title>%s</title>", esc(title))
	b.WriteString(`<style>body{font:15px/1.5 -apple-system,system-ui,sans-serif;max-width:680px;margin:40px auto;padding:0 20px;color:#1d1d22}small{color:#5b5b64}li{margin:6px 0}section{margin:0 0 40px}</style>`)
	if len(tasks) != 1 {
		fmt.Fprintf(&b, "<h1>%s</h1><p><small>%d tasks</small></p>", esc(title), len(tasks))
	}
	for _, t := range tasks {
		b.WriteString("<section>")
		fmt.Fprintf(&b, "<h2>%s</h2><p><small>%s · %s · %s · %s</small></p>", esc(t.Title), esc(t.Agent), esc(t.DeviceName),
			t.Started.Format("2006-01-02 15:04"), esc(elapsed(t.Ended.Sub(t.Started))))
		if s := summaryOf(t); s != "" {
			fmt.Fprintf(&b, "<p>%s</p>", esc(s))
		}
		b.WriteString("<ol>")
		for _, s := range t.Steps {
			text, sub := splitStep(s.Text)
			fmt.Fprintf(&b, "<li>%s", esc(text))
			if sub != "" {
				fmt.Fprintf(&b, "<br><small>%s</small>", esc(sub))
			}
			b.WriteString("</li>")
		}
		b.WriteString("</ol></section>")
	}
	b.WriteString("<p><small>Exported from Pinky.</small></p>")
	return b.String()
}
