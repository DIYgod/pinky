package policy

import (
	"fmt"
	"strings"

	"github.com/DIYgod/pinky/internal/screen"
)

// Two approvals do not come from the rules but from the gateway itself:
// changing a phone's settings (RulePhoneSettings) and reading an app's
// logs (RuleAppLogs). Their Always allow is an Exemption too, kept with
// the others under always_allow and listed in Settings › Safety, but it
// names the agent and the phone it is for (Exemption.Agent and Device), and
// for App logs the app: the person trusts one agent with one phone, not
// every agent with every phone. The gateway asks with Standing before it
// asks the person; the engine never lifts a rule's asks with one.

// RulePhoneSettings is the rule changing the phone's settings (rotation,
// light or dark, location, language) asks under.
const RulePhoneSettings = "Phone settings"

// standingRule reports whether a rule's Always allow is kept per agent
// and phone.
func standingRule(rule string) bool { return rule == RulePhoneSettings || rule == RuleAppLogs }

// standing reports whether an exemption is a per-agent and per-phone
// answer rather than one that lifts a rule's asks.
func (x Exemption) standing() bool { return x.Device != "" }

// checkStanding checks a rules file's always_allow entry that names a
// phone.
func checkStanding(x Exemption) error {
	switch {
	case !standingRule(x.Rule):
		return fmt.Errorf("always_allow %q names a device: only %q and %q answers name an agent and a device", x.Rule, RulePhoneSettings, RuleAppLogs)
	case x.Agent == "":
		return fmt.Errorf("always_allow %q names a device but no agent", x.Rule)
	case len(x.With) > 0:
		return fmt.Errorf("always_allow %q names a device, so it cannot list other rules", x.Rule)
	case x.Rule == RuleAppLogs && x.App == "" && x.AppName == "":
		return fmt.Errorf("always_allow %q has no app or app_name", x.Rule)
	}
	return nil
}

// Standing reports whether the person always allows an agent what rule
// (RulePhoneSettings or RuleAppLogs) asks about on a device, and for
// RuleAppLogs in the app. It does not while every action asks
// (PresetEvery).
func (e *Engine) Standing(rule, agent, device string, app screen.App) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if !standingRule(rule) || agent == "" || device == "" || e.presetLocked() == PresetEvery {
		return false
	}
	for _, x := range e.file.AlwaysAllow {
		if x.standing() && x.Rule == rule && x.Agent == agent && x.Device == device &&
			(rule != RuleAppLogs || x.matches(app)) {
			return true
		}
	}
	return false
}

// CanKeepStanding reports whether KeepStanding would keep an answer: not
// while every action asks, nor while rules.yaml has an edit that cannot
// be used (FileError).
func (e *Engine) CanKeepStanding(rule, agent, device string, app screen.App) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.standingLocked(rule, agent, device, "", app)
	return ok && e.rulesDisk.err == nil
}

// KeepStanding keeps an Always allow for an agent on a device (and for
// RuleAppLogs in an app), and saves the rules file. deviceName is how
// Settings names the phone. ErrNotExemptable means it cannot be kept; the
// engine applies it even when saving fails, but not while rules.yaml has
// an edit that cannot be used, which it returns (FileError).
func (e *Engine) KeepStanding(rule, agent, device, deviceName string, app screen.App) (Exemption, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.syncLocked(&e.rulesDisk); err != nil {
		return Exemption{}, err
	}
	x, ok := e.standingLocked(rule, agent, device, deviceName, app)
	if !ok {
		return Exemption{}, ErrNotExemptable
	}
	for i := range e.file.AlwaysAllow {
		y := &e.file.AlwaysAllow[i]
		if !y.standing() || y.Rule != x.Rule || y.Agent != x.Agent || y.Device != x.Device ||
			(x.Rule == RuleAppLogs && !y.sameApp(x)) {
			continue
		}
		changed := false
		if y.DeviceName != x.DeviceName && x.DeviceName != "" {
			y.DeviceName, changed = x.DeviceName, true
		}
		if y.App == "" && x.App != "" {
			y.App, changed = x.App, true
		}
		if y.AppName == "" && x.AppName != "" {
			y.AppName, changed = x.AppName, true
		}
		if !changed {
			return *y, nil
		}
		return *y, e.saveRulesLocked()
	}
	e.file.AlwaysAllow = append(e.file.AlwaysAllow, x)
	return x, e.saveRulesLocked()
}

// standingLocked is the answer KeepStanding would keep.
func (e *Engine) standingLocked(rule, agent, device, deviceName string, app screen.App) (Exemption, bool) {
	agent, device = strings.TrimSpace(agent), strings.TrimSpace(device)
	if !standingRule(rule) || agent == "" || device == "" || e.presetLocked() == PresetEvery {
		return Exemption{}, false
	}
	x := Exemption{Rule: rule, Agent: agent, Device: device, DeviceName: strings.TrimSpace(deviceName)}
	if rule == RuleAppLogs {
		x.App, x.AppName = clonePackage(strings.TrimSpace(app.ID)), strings.TrimSpace(app.Name)
		if x.App == "" && x.AppName == "" {
			return Exemption{}, false
		}
	}
	return x, true
}
