package standalone

import (
	"time"

	"github.com/DIYgod/pinky/internal/gateway"
	"github.com/DIYgod/pinky/internal/pilot"
)

// State is what Pinky's screens show: the phone, the agent's task with its
// steps, and what waits for the person.
type State struct {
	Device   DeviceState    `json:"device"`
	Preset   int            `json:"preset"` // when agents ask: policy.Preset
	Computer *ComputerState `json:"computer,omitempty"`
	Agent    *AgentState    `json:"agent,omitempty"`
	Task     *TaskState     `json:"task,omitempty"`
	Needs    []Need         `json:"needs"`
}

// ComputerState is Pinky on a computer while it holds the phone: the
// screens start no task meanwhile, and the pill offers Stop when it is
// Stoppable (an older desktop Pinky is not).
type ComputerState struct {
	Task      string `json:"task,omitempty"`
	Stoppable bool   `json:"stoppable"`
}

// DeviceState is the phone as the gateway sees it.
type DeviceState struct {
	Ready  bool   `json:"ready"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
	Line   string `json:"line,omitempty"`
	Detail string `json:"detail,omitempty"`
	// LineShown is Line in the screens' language, when it differs
	// (shown.go).
	LineShown string `json:"line_shown,omitempty"`
}

// AgentState is Ask Pinky's task: running, or the last one that ended.
// End is a pilot.End; the screens say it in the person's language, and
// Summary only says more for done, failed and error (it is the model's or
// the API's own words, and English for the other ends).
type AgentState struct {
	Task    string  `json:"task"`
	Model   string  `json:"model"`
	Running bool    `json:"running"`
	End     string  `json:"end,omitempty"`
	Summary string  `json:"summary,omitempty"`
	Steps   int     `json:"steps"`
	Cost    float64 `json:"cost"`
	Tokens  int64   `json:"tokens"`
	Seconds int64   `json:"seconds"`
	// Priced says Cost is in US dollars; without it the model's prices are
	// unknown, and the cost limit is TokenLimit tokens.
	Priced bool `json:"priced"`
	// The task's limits, so that the screens can say which one ended it.
	MaxSteps   int     `json:"max_steps"`
	MaxMinutes int     `json:"max_minutes"`
	MaxCost    float64 `json:"max_cost"`
	TokenLimit int64   `json:"token_limit"`
}

// TaskState is the gateway's task: its steps in plain words. ID is the
// task's in the history (PastTask.ID).
type TaskState struct {
	ID    string `json:"id,omitempty"`
	Title string `json:"title"`
	State string `json:"state"`
	Steps []Step `json:"steps"`
}

// Step is one step of the task. Text is the gateway's English, which the
// screens read for what kind of step it is; Shown and Asked say it in the
// screens' language when that is another (Shown, stepShown).
type Step struct {
	Text   string `json:"text"`
	Status string `json:"status"`
	MS     int64  `json:"ms"`
	// Shown is Text in the screens' language: its first line, and its
	// second when it has one.
	Shown string `json:"shown,omitempty"`
	// Asked is, for a step that waited for the person ("Waiting for you:
	// Tapped “Pay”"), what was asked in the screens' language ("点按“Pay”").
	Asked string `json:"asked,omitempty"`
}

// Need is an approval or a handoff the agent waits for.
type Need struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	Detail     string `json:"detail,omitempty"`
	App        string `json:"app,omitempty"`
	Rule       string `json:"rule,omitempty"`
	DeadlineMS int64  `json:"deadline_ms,omitempty"` // Unix milliseconds; no answer by then means deny
	AllowTask  bool   `json:"allow_task,omitempty"`
	// Title, Detail and Rule in the screens' language, when they differ
	// (shown.go).
	TitleShown  string `json:"title_shown,omitempty"`
	DetailShown string `json:"detail_shown,omitempty"`
	RuleShown   string `json:"rule_shown,omitempty"`
}

// State reads the current state.
func (c *Core) State() State {
	st := State{Preset: int(c.G.Policy().Preset())}
	d, ok := c.device()
	if ok {
		st.Device = DeviceState{Ready: d.Info.State == "online", Name: d.Info.Name, Status: string(d.Status),
			Line: d.StatusLine, Detail: d.Info.Detail, LineShown: lineShown(d)}
	}
	if cl := c.opt.Claims; cl != nil {
		if task, stoppable, held := cl.Computer(); held {
			st.Computer = &ComputerState{Task: task, Stoppable: stoppable}
		}
	}
	// The agent's task, never an earlier one or a dismissed one.
	var task *gateway.TaskView
	if ok {
		if s, has := c.pilot.Status(d.Info.ID); has {
			st.Agent = agentState(s)
			task = c.taskOf(d.Info.ID, s)
		}
	}
	c.mu.Lock()
	if task != nil {
		c.last = task
	} else if st.Agent != nil {
		task = c.last // ended more than ten minutes ago: keep its steps until dismissed
	}
	c.mu.Unlock()
	if task != nil {
		st.Task = &TaskState{ID: task.ID, Title: task.Title, State: string(task.State), Steps: stepsOf(*task)}
	}
	st.Needs = []Need{}
	for _, n := range c.G.Needs() {
		if ok && n.DeviceID != d.Info.ID {
			continue
		}
		need := Need{ID: n.ID, Kind: string(n.Kind), Title: n.Title, Detail: n.Detail, App: n.App, Rule: n.Rule,
			AllowTask: n.AllowTask}
		need.TitleShown, need.DetailShown, need.RuleShown = needShown(n)
		if !n.Deadline.IsZero() {
			need.DeadlineMS = n.Deadline.UnixMilli()
		}
		st.Needs = append(st.Needs, need)
	}
	return st
}

func agentState(s pilot.Status) *AgentState {
	end := s.Ended
	if end.IsZero() {
		end = time.Now()
	}
	a := &AgentState{Task: s.Task, Model: s.Config.Model, Running: s.Running(), End: string(s.End),
		Summary: s.Summary, Steps: s.Steps, Cost: s.Cost, Tokens: s.Tokens,
		Seconds: int64(end.Sub(s.Started).Seconds()), Priced: s.Priced,
		MaxSteps: s.Config.MaxSteps, MaxMinutes: int(s.Config.MaxTime / time.Minute), MaxCost: s.Config.MaxCost,
		TokenLimit: s.Config.TokenLimit()}
	if !s.Running() && a.Summary == "" {
		a.Summary = pilot.Describe(s)
	}
	return a
}
