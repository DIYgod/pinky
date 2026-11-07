package gateway

import (
	"errors"
	"fmt"
	"strings"

	"github.com/DIYgod/pinky/internal/approval"
	"github.com/DIYgod/pinky/internal/policy"
	"github.com/DIYgod/pinky/internal/screen"
)

// "Always allow" on an approval keeps the person's answer in the rules
// file (policy.Exemption): in the app, the rules that asked no longer ask
// about such steps, in later tasks and after a restart, until the person
// takes it back in Settings › Safety. It also allows the rest of the task,
// as Allow for this task does, which covers what an exemption may not
// lift: a task's app list. Approvals it cannot be kept for (the agent's
// own questions, copying files, opening apps and links, every action
// asking) count as Allow for this task, or as Allow once where that keeps
// nothing either (Gateway.Decide), and NeedView.AlwaysAllow tells the
// client not to offer it there.

// Exemption is one of the person's Always allow answers, as Settings ›
// Safety lists it: in the app, Rule no longer asks about the steps it asks
// about, nor do the rules in With about those steps.
type Exemption struct {
	Rule    string   // the rule the approval named, such as "Payments"
	With    []string // rules that asked with it, such as "Commits"
	AppID   string   // package name or bundle id; empty when the phone gave none
	AppName string   // the app's name; empty when the phone gave none
}

// Exemptions lists the person's Always allow answers, oldest first.
func (g *Gateway) Exemptions() []Exemption {
	xs := g.policy.Exemptions()
	out := make([]Exemption, len(xs))
	for i, x := range xs {
		out[i] = Exemption{Rule: x.Rule, With: append([]string(nil), x.With...), AppID: x.App, AppName: x.AppName}
	}
	return out
}

// RemoveExemption takes back an Always allow, as Exemptions listed it: its
// rules ask again in its app.
func (g *Gateway) RemoveExemption(x Exemption) error {
	return g.policy.Unexempt(policy.Exemption{Rule: x.Rule, App: x.AppID, AppName: x.AppName})
}

// canAlwaysAllow reports whether an approval's Always allow can be kept:
// an action the policy asked about, in an app, not a launch (whose app is
// not the one the panel names), with a rule an exemption may lift, and
// not while every action asks.
func (g *Gateway) canAlwaysAllow(info approval.Info) bool {
	snap, ok := info.Data.(*snapshot)
	return ok && info.Kind == approval.KindAction && !snap.launch && g.policy.CanExempt(snap.asks, snap.grantApp)
}

// alwaysAllow keeps an approval's Always allow, when it can be kept. The
// engine applies it even when the rules file cannot be saved, which the
// error reports.
func (g *Gateway) alwaysAllow(info approval.Info) error {
	if !g.canAlwaysAllow(info) {
		return nil
	}
	snap := info.Data.(*snapshot)
	x, err := g.policy.Exempt(snap.asks, snap.grantApp)
	switch {
	case errors.Is(err, policy.ErrNotExemptable):
		return nil
	case err != nil:
		g.log.Warn("always allow: save the rules file", "rule", x.Rule, "app", appLabel(snap.grantApp), "err", err)
		return fmt.Errorf("the step is allowed, but Always allow was not saved: %w", err)
	}
	g.log.Info("always allow", "rule", x.Rule, "with", x.With, "app", appLabel(snap.grantApp))
	return nil
}

// exemptedNote tells the agent that an action ran without asking because
// the person always allows it in the app.
func exemptedNote(res *Result, v policy.Verdict, p *prep) *Result {
	if res == nil || len(v.Exempted) == 0 || (res.Status != StatusSettled && res.Status != StatusUncertain) {
		return res
	}
	note := fmt.Sprintf("No approval needed: the person always allows %q in %s.", v.Exempted[0], exemptionApp(grantApp(p)))
	res.Message = strings.TrimSpace(note + " " + res.Message)
	return res
}

func exemptionApp(a screen.App) string {
	if l := appLabel(a); l != "" {
		return l
	}
	return "this app"
}
