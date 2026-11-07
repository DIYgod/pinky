package gateway

import (
	"github.com/DIYgod/pinky/internal/policy"
)

// RuleView is a policy rule, as Settings › Safety shows and edits it. An
// action matches when every field that is set matches; within a field,
// any value does.
type RuleView struct {
	Name string
	// Apps are package names, bundle ids or app names.
	Apps []string
	// Categories are app table categories (policy.Categories).
	Categories []string
	// Actions are kinds detected in context, such as commit or payment, or
	// operations, such as tap (policy.Actions).
	Actions []string
	// Labels are words in the label of the element acted on.
	Labels []string
	// Decision is "allow", "ask" or "deny".
	Decision string
}

// RulesView is what Settings › Safety shows of the policy rules.
type RulesView struct {
	// File holds the rules file's rules, in order: the ones the person
	// edits.
	File []RuleView
	// BuiltIn holds the rules Pinky adds, read-only: the preset's, which
	// come before File, then the apps blocked in Settings.
	BuiltIn []RuleView
}

// Rules returns the policy rules for Settings › Safety.
func (g *Gateway) Rules() RulesView {
	return RulesView{File: ruleViews(g.policy.FileRules()), BuiltIn: ruleViews(g.policy.BuiltInRules())}
}

// SetRules replaces the rules file's rules, in order, and saves the file
// (policy.Engine.SetFileRules). The strictest matching rule still wins.
func (g *Gateway) SetRules(rules []RuleView) error {
	out := make([]policy.Rule, len(rules))
	for i, r := range rules {
		d, err := policy.ParseDecision(r.Decision)
		if err != nil {
			return err
		}
		out[i] = policy.Rule{Name: r.Name, Decision: d, Match: policy.Match{
			App: r.Apps, Category: r.Categories, Action: r.Actions, Label: r.Labels}}
	}
	if err := g.policy.SetFileRules(out); err != nil {
		return err
	}
	g.log.Info("rules changed", "rules", len(out))
	return nil
}

func ruleViews(rules []policy.Rule) []RuleView {
	out := make([]RuleView, len(rules))
	for i, r := range rules {
		m := r.Match
		out[i] = RuleView{Name: r.Name, Apps: m.App, Categories: m.Category, Actions: m.Action, Labels: m.Label,
			Decision: r.Decision.String()}
	}
	return out
}
