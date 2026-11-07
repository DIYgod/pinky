package ui

import (
	"strings"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
	"github.com/DIYgod/pinky/internal/gateway"
)

// The client's words are in the catalogs of cmd/pinky/locales, one
// function per message (see docs/notes/client.md, "i18n"). These helpers
// put into the language the app shows what the gateway and policy send as
// finished English text. Agents' own words (task titles, reasons, notes)
// and the phone's (labels, app names) stay as they are.

// needTitle is a Needs you card's title.
func needTitle(n gateway.NeedView) string { return n.Title }

// needDetail is a Needs you card's second line.
func needDetail(n gateway.NeedView) string { return n.Detail }

// statusLine is a phone's status line in the sidebar, the tiles and the
// menu bar flyout.
func statusLine(d gateway.DeviceView) string { return d.StatusLine }

// ruleNames are the rules of the policy presets and the built-in ones, as
// approvals and steps name them.
var ruleNames = map[string]func() string{
	"Payments":           locales.RulePayments,
	"Messages":           locales.RuleMessages,
	"Commits":            locales.RuleCommits,
	"Installs and links": locales.RuleInstallsLinks,
	"Calls":              locales.RuleCalls,
	"Settings changes":   locales.RuleSettingsChanges,
	"Every action":       locales.RuleEveryAction,
	"Blocked by you":     locales.RuleBlockedByYou,
	"credentials":        locales.RuleCredentials,
	"Read-only":          locales.RuleReadOnly,
	"Task apps":          locales.RuleTaskApps,
	"Agent scope":        locales.RuleAgentScope,
	"Finance apps":       locales.RuleFinanceApps,
	"Password managers":  locales.RulePasswordManagers,
	"Files":              locales.RuleFiles,
}

// ruleName names a rule as the person reads it: "Payments". A rule of the
// person's own rules file keeps its name.
func ruleName(rule string) string {
	if f := ruleNames[rule]; f != nil {
		return f()
	}
	return rule
}

// ruleActions are what the presets' rules cover, for "Always allow …".
var ruleActions = map[string]func() string{
	"Payments":           locales.RuleActionPayments,
	"Messages":           locales.RuleActionMessages,
	"Commits":            locales.RuleActionCommits,
	"Installs and links": locales.RuleActionInstallsLinks,
	"Calls":              locales.RuleActionCalls,
	"Settings changes":   locales.RuleActionSettingsChanges,
	"Every action":       locales.RuleActionEveryAction,
}

// ruleAction says what a rule covers, inside "Always allow payments in
// Dinely": "payments", or "this kind" without a rule.
func ruleAction(rule string) string {
	if rule == "" {
		return locales.RuleActionThisKind()
	}
	if f := ruleActions[rule]; f != nil {
		return f()
	}
	return locales.RuleActionOther(lowerFirst(rule))
}

// joinAnd joins names for a sentence: "Dinely, Chatter and Calendar".
func joinAnd(s []string) string {
	switch len(s) {
	case 0:
		return ""
	case 1:
		return s[0]
	case 2:
		return locales.ListPair(s[0], s[1])
	}
	return locales.ListMany(strings.Join(s[:len(s)-1], locales.ListSeparator()), s[len(s)-1])
}
