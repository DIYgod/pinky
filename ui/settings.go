package ui

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo"
	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/gateway"
)

type settingsSection int

const (
	secSafety settingsSection = iota
	secPhones
	secAgents
	secPrivacy
	secAdvanced
)

type settingsState struct {
	sec     settingsSection
	adding  bool
	newApp  string
	confirm bool
}

func (a *app) changed() {
	if a.opt.OnSettings != nil {
		s := a.settings
		s.BlockedApps = append([]BlockedApp(nil), s.BlockedApps...)
		a.opt.OnSettings(s)
	}
}

func (a *app) settingsPage(c *mg.Context) {
	p := pal(c)
	s := &a.set
	pageHeader(c, func() { mg.Text(c, "Settings").FontSize(17).FontWeight(650) })
	mg.Row(c).Grow(1).MinHeight(0).AlignItems(mg.Stretch).Children(func() {
		mg.Column(c).Width(200).Shrink(0).Padding(16, 12).Gap(2).BorderWidth(0, 1, 0, 0).BorderColor(p.line).
			Role(mg.RoleTabList).Label("Settings sections").Children(func() {
			tab := func(sec settingsSection, name string, needs bool) {
				on := s.sec == sec
				b := mg.ButtonBase(c.Key(name)).Height(36).Padding(0, 12).Radius(8).Gap(8).AlignItems(mg.Center).Justify(mg.Start).
					Role(mg.RoleTab).Selected(on).Label(name)
				col := p.ink2
				switch {
				case on:
					b.Background(p.bg3)
					selectionMark(c, b)
					col = p.ink
				case b.Hovered():
					b.Background(p.bg2)
					col = p.ink
				}
				if b.Clicked() {
					s.sec = sec
				}
				b.TextColor(col).Children(func() {
					t := mg.Text(c, name).FontSize(14).Grow(1)
					if on {
						t.FontWeight(600)
					}
					if needs {
						dot(c, p.amberDot, false).Label("Needs you")
					}
				})
			}
			tab(secPhones, "Phones", false)
			agentsWait := false
			for _, n := range a.needs {
				agentsWait = agentsWait || newAgentNeed(n)
			}
			tab(secAgents, "Agents", agentsWait)
			tab(secSafety, "Safety", false)
			tab(secPrivacy, "Privacy", false)
			tab(secAdvanced, "Advanced", false)
		})
		mg.Scroll(c).Grow(1).MinWidth(0).Padding(24, 40).Children(func() {
			mg.Column(c).MaxWidth(640).Gap(22).Children(func() {
				switch s.sec {
				case secPhones:
					a.settingsPhones(c)
				case secAgents:
					a.settingsAgents(c)
				case secPrivacy:
					a.settingsPrivacy(c)
				case secAdvanced:
					a.settingsAdvanced(c)
				default:
					a.settingsSafety(c)
				}
			})
		})
	})
}

func settingsTitle(c *mg.Context, title, desc string) {
	p := pal(c)
	mg.Column(c).Children(func() {
		mg.Text(c, title).FontSize(20).FontWeight(650).Role(mg.RoleHeading)
		mg.Text(c, desc).FontSize(13.5).TextColor(p.ink2).Margin(4, 0, 0, 0)
	})
}

// toggleRow is a setting row with a switch; clicking its words toggles
// it too, as a form's field label does. It reports a change.
func (a *app) toggleRow(c *mg.Context, key, title, desc string, last bool, on *bool) bool {
	p := pal(c)
	changed := false
	r := mg.Row(c.Key(key)).Gap(16).Padding(10, 0).AlignItems(mg.Center)
	if !last {
		r.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	}
	r.Children(func() {
		words := mg.Column(c).Grow(1).MinWidth(0).Gap(2).Cursor(mg.CursorPointer)
		if words.Clicked() {
			*on = !*on
			changed = true
		}
		words.Children(func() {
			mg.Text(c, title).FontWeight(600)
			if desc != "" {
				mg.Text(c, desc).FontSize(13.5).TextColor(p.ink2)
			}
		})
		if toggle(c.Key(key+"-switch"), on, title).Changed() {
			changed = true
		}
	})
	if changed {
		a.changed()
	}
	return changed
}

// settingRow is the prototype's .trow in Settings: a label and a
// description, with a control at the end.
func settingRow(c *mg.Context, title, desc string, last bool, control func()) {
	p := pal(c)
	r := mg.Row(c).Gap(16).Padding(10, 0).AlignItems(mg.Center)
	if !last {
		r.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
	}
	r.Children(func() {
		mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			mg.Text(c, title).FontWeight(600)
			if desc != "" {
				mg.Text(c, desc).FontSize(13.5).TextColor(p.ink2)
			}
		})
		if control != nil {
			control()
		}
	})
}

func (a *app) settingsSafety(c *mg.Context) {
	p := pal(c)
	st := &a.settings
	settingsTitle(c, "Safety", "Pinky checks every action before it reaches the phone. Agents can't change these settings.")
	mg.Column(c).Gap(8).Label("When agents ask").Children(func() {
		mg.Text(c, "When should agents ask you?").FontSize(15).FontWeight(650).Margin(0, 0, 4, 0)
		presets := []struct{ title, desc string }{
			{"Ask before payments, messages and deletions (Recommended)", "Also asks before calls, account changes and installing apps."},
			{"Ask before payments only", "Messages and deletions go through without asking."},
			{"Ask before every action", "Slower, but you see every tap first."},
		}
		mg.Column(c).Gap(8).Role(mg.RoleRadioGroup).Label("When should agents ask you?").Children(func() {
			for i, pr := range presets {
				v := i + 1
				opt := mg.RadioBase(c.Key(v), &st.Preset, v).Gap(12).Padding(10, 14).Radius(10).AlignItems(mg.Start).FillWidth()
				on := st.Preset == v
				border := p.line
				if on {
					border = p.accent
					opt.Shadow(0, 0, 0, 1, p.accent)
				} else if opt.Hovered() {
					border = p.line2
				}
				if opt.Changed() {
					a.changed()
				}
				opt.Border(1, border).Background(p.panel).Children(func() {
					rb := mg.Box(c).Size(16, 16).Radius(8).Margin(2, 0, 0, 0).Shrink(0).Background(p.panel)
					if on {
						rb.Border(5, p.accentBtn)
					} else {
						rb.Border(1.5, p.line2)
					}
					mg.Column(c).Gap(2).Grow(1).MinWidth(0).Children(func() {
						mg.Text(c, pr.title).FontWeight(600)
						mg.Text(c, pr.desc).FontSize(13.5).TextColor(p.ink2)
					})
				})
			}
		})
	})
	mg.Column(c).Gap(8).Label("Blocked apps").Children(func() {
		mg.Row(c).Gap(12).AlignItems(mg.End).Children(func() {
			mg.Column(c).Grow(1).Children(func() {
				mg.Text(c, "Blocked apps").FontSize(15).FontWeight(650)
				mg.Text(c, "Agents can't open these apps or see their screens.").FontSize(13.5).TextColor(p.ink2).Margin(2, 0, 0, 0)
			})
			if button(c, "Add app", btnOpt{}).Clicked() {
				a.set.adding = !a.set.adding
			}
		})
		if a.set.adding {
			mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
				in := mg.TextInput(c.Key("new-blocked"), &a.set.newApp).Placeholder("App name or package").Grow(1).AutoFocus().Label("App to block")
				add := button(c, "Block", btnOpt{kind: btnPrimary})
				if (in.Submitted() || add.Clicked()) && strings.TrimSpace(a.set.newApp) != "" {
					st.BlockedApps = append(st.BlockedApps, BlockedApp{Name: strings.TrimSpace(a.set.newApp), Kind: "Blocked by you"})
					a.set.newApp, a.set.adding = "", false
					a.changed()
				}
			})
		}
		mg.Column(c).Children(func() {
			for i, b := range st.BlockedApps {
				i, b := i, b
				mg.Row(c.Key("b-"+b.Name)).Gap(12).Padding(7, 0).AlignItems(mg.Center).BorderWidth(0, 0, 1, 0).BorderColor(p.line).Children(func() {
					appIcon(c, b.Name, 28, 8, 13, blockedColor(b.Name))
					mg.Text(c, b.Name).Grow(1)
					mg.Text(c, b.Kind).FontSize(13.5).TextColor(p.ink2)
					rm := mg.ButtonBase(c).Size(26, 26).Radius(6).Center().Label("Unblock " + b.Name).Tooltip("Unblock")
					if rm.Hovered() {
						rm.Background(p.bg3)
					}
					if rm.Clicked() {
						st.BlockedApps = append(st.BlockedApps[:i:i], st.BlockedApps[i+1:]...)
						a.changed()
					}
					rm.TextColor(p.ink3).Children(func() { mg.Icon(c, icX).FontSize(14) })
				})
			}
		})
	})
	mg.Column(c).Label("Protections").Children(func() {
		if a.toggleRow(c, "ro", "Read-only mode", "Agents can browse, not commit", false, &st.ReadOnly) {
			if ro, ok := a.api.(readOnlySetter); ok {
				on := st.ReadOnly
				go ro.SetReadOnly("", on) // every phone
			}
		}
		_, keep := unlockWords()
		desc := "Save your phone PIN in the Keychain; unlock with Touch ID"
		if runtime.GOOS != "darwin" {
			desc = strings.TrimSuffix(strings.TrimPrefix(keep, "Your PIN stays in "), ". Agents never see it.")
			desc = "Save your phone PIN in " + desc
		}
		a.toggleRow(c, "unlock", "Unlock from this computer", desc, false, &st.UnlockFromComputer)
		a.toggleRow(c, "codes", "Hide codes from agents", "Passwords and one-time codes are blurred before an agent sees the screen", true, &st.HideCodes)
	})
}

func blockedColor(name string) string {
	switch name {
	case "Northbank":
		return "#1e3a8a"
	case "Vaultly":
		return "#334155"
	case "Coinpouch":
		return "#b45309"
	}
	return ""
}

func (a *app) settingsPhones(c *mg.Context) {
	p := pal(c)
	settingsTitle(c, "Phones", "Phones this computer can reach.")
	mg.Column(c).Label("Phones").Children(func() {
		if len(a.devices) == 0 {
			mg.Text(c, "No phones yet.").TextColor(p.ink2).Padding(10, 0)
		}
		for i, d := range a.devices {
			d := d
			settingRow(c.Key(d.Info.ID), d.Info.Name, osLine(d), i == len(a.devices)-1, func() {
				mg.Row(c).Gap(6).AlignItems(mg.Center).Children(func() {
					word := "Connected"
					switch {
					case d.Status == gateway.DeviceOffline:
						hollowDot(c)
						word = "Offline"
					case d.Info.State != driver.StateOnline:
						dot(c, p.amberDot, false)
						word = "Needs setup"
					default:
						dot(c, p.green, false)
					}
					mg.Text(c, word).FontSize(13).TextColor(p.ink2)
				})
			})
		}
	})
	mg.Row(c).Children(func() {
		if button(c, "Add phone", btnOpt{}).Clicked() {
			a.page = pageAddPhone
			a.add.reset(a)
		}
	})
}

// agentsState is Settings › Agents': the gateway's agents and the scopes
// being chosen.
type agentsState struct {
	list     []gateway.AgentView
	loaded   bool
	err      error
	seq      int
	lastLoad time.Time
	asks     int               // open first-connection requests, when last read
	editing  string            // the agent whose scope is being changed
	choice   map[string]string // scope label being chosen, by agent or need id
	apps     map[string]string // apps typed for "Use only the apps I choose"
}

func (s *agentsState) load(a *app) {
	al, ok := a.api.(agentLister)
	if !ok {
		return
	}
	s.seq++
	seq := s.seq
	s.lastLoad = time.Now()
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		defer cancel()
		list, err := al.Agents(ctx)
		a.post(func() {
			if seq != s.seq {
				return
			}
			sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
			s.list, s.err, s.loaded = list, err, true
		})
	}()
}

func splitApps(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ",") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// scopePicker is the select of what an agent may do, with the apps field
// for "Use only the apps I choose". It returns the scope chosen.
func (a *app) scopePicker(c *mg.Context, key, label string, initial gateway.Scope) gateway.Scope {
	st := &a.agents
	if st.choice == nil {
		st.choice, st.apps = map[string]string{}, map[string]string{}
	}
	choice, ok := st.choice[key]
	if !ok {
		choice = scopeLabel(initial.Mode)
		st.apps[key] = strings.Join(initial.Apps, ", ")
	}
	labels := make([]string, len(scopeChoices))
	for i, sc := range scopeChoices {
		labels[i] = sc.label
	}
	mg.Select(c.Key("scope-"+key), &choice, labels).Label(label).MaxWidth(360)
	st.choice[key] = choice
	scope := gateway.Scope{Mode: scopeOf(choice)}
	if scope.Mode == gateway.ScopeApps {
		apps := st.apps[key]
		mg.TextInput(c.Key("apps-"+key), &apps).Placeholder("Apps, separated by commas: Dinely, Chatter").Label("Apps").MaxWidth(360)
		st.apps[key] = apps
		scope.Apps = splitApps(apps)
	}
	return scope
}

// approveAgent allows an agent's first connection with the chosen scope.
// SetAgentScope approves the agent, so a call that comes back for the
// answer later finds it allowed with that scope. A call still waiting
// when the answer arrives sets the gateway's default scope as it goes on,
// so keepScope puts the chosen one back.
func (a *app) approveAgent(n gateway.NeedView, scope gateway.Scope) {
	a.remember(n, gateway.AllowOnce, "")
	scoper, ok := a.api.(agentScoper)
	name := n.Agent
	if ok && name != "" {
		a.wantScope.Store(name, scope)
	}
	a.call(n.DeviceID, "approve the agent", func() error {
		if ok && name != "" {
			scoper.SetAgentScope(name, scope)
		}
		return a.api.Decide(n.ID, gateway.AllowOnce, "")
	})
	if ok && name != "" {
		go a.keepScope(name)
	}
}

// keepScope watches a newly approved agent for a few minutes and sets the
// scope the person chose again if the gateway replaced it.
func (a *app) keepScope(name string) {
	al, ok := a.api.(agentLister)
	scoper, _ := a.api.(agentScoper)
	if !ok || scoper == nil {
		return
	}
	start := time.Now()
	for time.Since(start) < 3*time.Minute {
		wait := 100 * time.Millisecond
		if time.Since(start) > 10*time.Second {
			wait = time.Second
		}
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(wait):
		}
		v, ok := a.wantScope.Load(name)
		if !ok {
			return
		}
		want := v.(gateway.Scope)
		ctx, cancel := context.WithTimeout(a.ctx, 5*time.Second)
		list, err := al.Agents(ctx)
		cancel()
		if err != nil {
			continue
		}
		for _, ag := range list {
			if ag.Name == name && ag.Approved && !sameScope(ag.Scope, want) {
				scoper.SetAgentScope(name, want)
				a.post(func() { a.agents.load(a) })
				return
			}
		}
	}
}

func sameScope(x, y gateway.Scope) bool {
	if x.Mode != y.Mode || len(x.Apps) != len(y.Apps) {
		return false
	}
	for i := range x.Apps {
		if x.Apps[i] != y.Apps[i] {
			return false
		}
	}
	return true
}

func (a *app) settingsAgents(c *mg.Context) {
	p := pal(c)
	st := &a.agents
	settingsTitle(c, "Agents", "Agents connect to Pinky on this computer. Each one only gets what you allow.")
	_, canList := a.api.(agentLister)
	_, canScope := a.api.(agentScoper)
	if canList {
		if !st.loaded || time.Since(st.lastLoad) > 5*time.Second || st.lastLoad.IsZero() {
			st.load(a)
		}
		c.After(5 * time.Second)
	}
	// First connections waiting for the person, as the prototype's request.
	for _, n := range a.needs {
		if !newAgentNeed(n) {
			continue
		}
		n := n
		mg.Column(c.Key("req-"+n.ID)).Padding(18).Radius(12).Background(p.amberTint).Border(1, p.amberLine).Gap(10).
			Label("Connection request").Children(func() {
			mg.Row(c).Gap(8).AlignItems(mg.Center).Children(func() {
				dot(c, p.amberDot, false)
				mg.Text(c, "Needs you").FontSize(13).FontWeight(600).TextColor(p.amber)
			})
			mg.Column(c).Children(func() {
				mg.Text(c, n.Title).FontSize(16).FontWeight(650)
				if d := agentRequestDetail(n, canScope); d != "" {
					mg.Text(c, d).FontSize(13.5).TextColor(p.ink2).Margin(2, 0, 0, 0)
				}
			})
			a.agentRequestControls(c, n)
		})
	}
	type row struct {
		name, line string
		state      string // "working", "allowed", "waiting"
		scope      gateway.Scope
		known      bool
	}
	var rows []row
	working := map[string]bool{}
	for _, t := range a.tasks {
		if running(t.State) && t.Agent != "" {
			working[t.Agent] = true
		}
	}
	switch {
	case canList && st.loaded:
		for _, ag := range st.list {
			r := row{name: ag.Name, line: scopeLine(ag.Scope), scope: ag.Scope, known: true, state: "allowed"}
			if !ag.Approved {
				r.line, r.state = "Not allowed yet", "waiting"
			}
			if working[ag.Name] {
				r.state = "working"
			}
			rows = append(rows, r)
		}
	case a.opt.Agents != nil:
		for _, ag := range a.opt.Agents {
			r := row{name: ag.Name, line: nonEmpty(ag.Scope, "Connected through MCP"), state: "allowed"}
			if ag.Connected {
				r.state = "working"
			}
			rows = append(rows, r)
		}
	default:
		seen := map[string]bool{}
		for _, t := range append(append([]gateway.TaskView(nil), a.tasks...), a.act.tasks...) {
			if t.Agent != "" && !seen[t.Agent] {
				seen[t.Agent] = true
				r := row{name: t.Agent, line: "Connected through MCP", state: "allowed"}
				if working[t.Agent] {
					r.state = "working"
				}
				rows = append(rows, r)
			}
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].name < rows[j].name })
	}
	mg.Column(c).Label("Connected agents").Children(func() {
		if len(rows) == 0 {
			mg.Text(c, "No agent has connected yet. Point an agent at `pinky mcp` to start.").TextColor(p.ink2).Padding(10, 0)
		}
		for i, r := range rows {
			r := r
			last := i == len(rows)-1
			box := mg.Column(c.Key("agent-"+r.name)).Padding(10, 0).Gap(10)
			if !last {
				box.BorderWidth(0, 0, 1, 0).BorderColor(p.line)
			}
			box.Children(func() {
				mg.Row(c).Gap(16).AlignItems(mg.Center).Children(func() {
					appIcon(c, r.name, 28, 8, 13, "#5450d6")
					mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
						mg.Text(c, r.name).FontWeight(600)
						mg.Text(c, r.line).FontSize(13.5).TextColor(p.ink2)
					})
					mg.Row(c).Gap(6).AlignItems(mg.Center).Children(func() {
						switch r.state {
						case "working":
							dot(c, p.green, false)
							mg.Text(c, "Working").FontSize(13).TextColor(p.ink2)
						case "waiting":
							dot(c, p.amberDot, false)
							mg.Text(c, "Waiting for you").FontSize(13).TextColor(p.ink2)
						default:
							dot(c, p.ink3, false)
							mg.Text(c, "Allowed").FontSize(13).TextColor(p.ink2)
						}
					})
					if canScope && r.known && r.state != "waiting" {
						label := "Change"
						if st.editing == r.name {
							label = "Close"
						}
						if button(c, label, btnOpt{}).Label("Change what " + r.name + " may do").Clicked() {
							if st.editing == r.name {
								st.editing = ""
							} else {
								st.editing = r.name
								delete(st.choice, r.name)
							}
						}
					}
				})
				if st.editing == r.name && canScope {
					mg.Column(c).Gap(8).Padding(0, 0, 0, 44).Children(func() {
						scope := a.scopePicker(c, r.name, "What "+r.name+" may do", r.scope)
						if button(c, "Save", btnOpt{kind: btnPrimary}).AlignSelf(mg.Start).Clicked() {
							if sc, ok := a.api.(agentScoper); ok {
								name := r.name
								a.wantScope.Store(name, scope)
								go func() {
									sc.SetAgentScope(name, scope)
									a.post(func() { a.agents.load(a) })
								}()
							}
							st.editing = ""
						}
					})
				}
			})
		}
	})
	if !canList {
		mg.Text(c, "This gateway does not list its agents; these are the ones seen in tasks.").FontSize(12.5).TextColor(p.ink3)
	}
}

func (a *app) settingsPrivacy(c *mg.Context) {
	p := pal(c)
	st := &a.settings
	settingsTitle(c, "Privacy", "Activity and recordings stay on this computer.")
	mg.Column(c).Label("Privacy").Children(func() {
		settingRow(c, "Keep activity for", "Older tasks are deleted automatically", false, func() {
			days := fmt.Sprintf("%d days", st.KeepDays)
			if mg.Select(c.Key("keep"), &days, []string{"7 days", "30 days", "90 days"}).Label("Keep activity for").Changed() {
				fmt.Sscanf(days, "%d", &st.KeepDays)
				a.changed()
			}
		})
		a.toggleRow(c, "rec", "Record the screen during tasks", "Used for replays in Activity", false, &st.Record)
		settingRow(c, "Delete all activity", "Removes every task, step and recording", true, func() {
			b := button(c, "Delete…", btnOpt{})
			if _, ok := a.api.(historyWiper); !ok {
				b.Disabled(true).Tooltip("This gateway cannot delete its history")
			} else if b.Clicked() {
				a.set.confirm = true
			}
		})
	})
	if a.set.confirm {
		switch mg.AlertDialog(c, &a.set.confirm, "Delete all activity?",
			"Every finished task, its steps, approvals and screenshots are removed from this computer. Running tasks go on.",
			"Cancel", "Delete") {
		case 1:
			a.wipeHistory()
		}
	}
	_ = p
}

// wipeHistory deletes the gateway's history, from Settings › Privacy.
func (a *app) wipeHistory() {
	w, ok := a.api.(historyWiper)
	if !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(a.ctx, 30*time.Second)
		defer cancel()
		err := w.WipeHistory(ctx)
		a.post(func() {
			if err != nil {
				a.toasts = append(a.toasts, "Couldn't delete the activity: "+err.Error())
				return
			}
			a.mediaCache = map[string]*mediaEntry{}
			a.act.tasks, a.act.sel = nil, ""
			a.act.load(a)
			a.toasts = append(a.toasts, "Deleted all activity")
		})
	}()
}

func (a *app) settingsAdvanced(c *mg.Context) {
	p := pal(c)
	st := &a.settings
	settingsTitle(c, "Advanced", "For people who build with Pinky.")
	mg.Column(c).Label("Advanced").Children(func() {
		a.toggleRow(c, "dev", "Developer mode", "Shows tool calls, element lists, timings and backends. A Details switch appears on the task card.", false, &st.DeveloperMode)
		if st.DeveloperMode {
			var parts []string
			for _, d := range a.devices {
				if d.Info.Backend != "" {
					parts = append(parts, fmt.Sprintf("%s: %s", d.Info.Name, d.Info.Backend))
				}
			}
			backends := strings.Join(parts, ". ")
			if backends == "" {
				backends = "No phones connected."
			} else {
				backends += "."
			}
			settingRow(c, "Backends", backends, false, nil)
			settingRow(c, "Agent connection", "MCP over stdio: pinky mcp", false, nil)
			if v := videoLine(a); v != "" {
				settingRow(c, "Video", v, false, nil)
			}
		}
		settingRow(c, "Rules file", "The rules behind Safety, as text", true, func() {
			b := button(c, "Edit rules file", btnOpt{})
			if a.opt.RulesFile == "" {
				b.Disabled(true).Tooltip("The gateway did not say where its rules file is")
			} else if b.Clicked() {
				path := a.opt.RulesFile
				go func() {
					if err := mygo.Shell.OpenPath(path); err != nil {
						a.post(func() { a.toasts = append(a.toasts, "Couldn't open the rules file: "+err.Error()) })
					}
				}()
			}
		})
	})
	if a.opt.Version != "" {
		mg.Text(c, "Pinky "+a.opt.Version).FontSize(12.5).TextColor(p.ink3)
	}
}
