package policy

import (
	"fmt"
	"strings"

	"github.com/DIYgod/pinky/internal/screen"
)

// Operations on whole apps (phone_app). The app acted on is Input.Launch:
// for an install, the app the package holds. docs/notes/apps.md explains
// the answers.
const (
	OpInstall   Op = "install"
	OpUninstall Op = "uninstall"
	OpStop      Op = "stop"
)

// Built-in rules about whole apps.
const (
	// RuleInstall asks before an install or update from the computer,
	// unless the person always allows the agent to install builds of the
	// app by the same signer on the phone (Engine.Standing).
	RuleInstall = "Installing from the computer"
	// RuleUninstall asks before every uninstall.
	RuleUninstall = "Uninstalling apps"
	// RulePinkyApps refuses to touch Pinky Companion and the iPhone's
	// helper (an XCTest runner, see IsPinkyApp), which give Pinky its access
	// to the phone.
	RulePinkyApps = "Pinky's own apps"
	// RuleSystemApps refuses to touch apps the phone ships with.
	RuleSystemApps = "System apps"
	// RuleFinance and RulePasswords name the bundled rules that block
	// finance apps and password managers; installs, uninstalls and stops
	// are refused for them even when the rules file no longer blocks them.
	RuleFinance   = "Finance apps"
	RulePasswords = "Password managers"
)

// AppChange reports whether the op installs, uninstalls or stops an app.
func (op Op) AppChange() bool {
	return op == OpInstall || op == OpUninstall || op == OpStop
}

// StandaloneApp is the package of the standalone prototype
// (android/standalone, docs/notes/standalone-phone.md): Pinky running on
// the phone itself, whose screen and controls hold the person's approvals
// and Stop. Its screen is blocked outright (Blocked, Evaluate) and no task
// can lift that, so an agent can never press its controls.
const StandaloneApp = "app.pinky.standalone"

// pinkyApps are Pinky Companion's package names and the standalone
// prototype's.
var pinkyApps = []string{"app.pinky.companion", "app.pinky.companion.dev", StandaloneApp}

// IPhoneApp begins the bundle ids of Pinky's iPhone app (ios/), which, like
// the standalone prototype, holds the person's approvals and Stop on its
// screen. Its extensions' ids begin with it too, and so do the ids SideStore
// and AltStore give it (<id>.<their team>).
const IPhoneApp = "cc.diygod.pinky"

// iPhoneTeamApp reports whether a bundle id, in lower case, has the shape
// desktop Pinky gives the apps it builds with the person's own team, since
// a team cannot sign another team's ids: "pinky.", the team id (ten letters
// and digits), then the end or a dot and more. helperBundleID in
// internal/driver/ios names Pinky Helper so, and the iPhone app is to
// follow it once desktop Pinky builds it. Other ids that merely begin with
// "pinky.", Android packages among them, are not Pinky's.
func iPhoneTeamApp(id string) bool {
	rest, ok := strings.CutPrefix(id, "pinky.")
	if !ok || len(rest) < 10 || len(rest) > 10 && rest[10] != '.' {
		return false
	}
	for _, c := range rest[:10] {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// IsStandalone reports whether the app is Pinky running on the phone
// itself: the standalone prototype on Android, Pinky's iPhone app on iOS.
// XCTest runners are not, whatever their ids. Pinky Helper built with the
// person's team is pinky.<team id>.WebDriverAgentRunner.xctrunner, the
// shape iPhoneTeamApp matches, but its screen holds no approvals or Stop,
// so agents may act while it is in front; IsPinkyApp still counts it.
func IsStandalone(app screen.App) bool {
	id := strings.ToLower(clonePackage(app.ID))
	if strings.HasSuffix(id, ".xctrunner") {
		return false
	}
	return id == StandaloneApp || id == IPhoneApp || strings.HasPrefix(id, IPhoneApp+".") || iPhoneTeamApp(id)
}

// IsPinkyApp reports whether the app is part of Pinky on the phone: Pinky
// Companion, Pinky on the phone itself (IsStandalone), or the iPhone's
// helper. The helper is WebDriverAgent's XCTest
// runner, and every runner counts, whatever its bundle id: Pinky Helper keeps
// the id it was first installed with, which may be anything ending in
// .xctrunner (iPhone Use's cc.diygod.iphoneuse.wda.xctrunner, a --bundle-id of
// `pinky helper install`, Xcode's <prefix>.WebDriverAgentRunner.xctrunner),
// and a phone only runs XCTest runners for development. The cost is that
// agents cannot uninstall or stop a developer's own UI test runner, which the
// person still can.
func IsPinkyApp(app screen.App) bool {
	id := strings.ToLower(clonePackage(app.ID))
	for _, p := range pinkyApps {
		if id == p {
			return true
		}
	}
	return strings.HasSuffix(id, ".xctrunner") || IsStandalone(app)
}

// appChangeLocked decides an install, uninstall or stop. Unlike the screen
// steps, the agent cannot be watched doing them: the apps that hold money,
// passwords or Pinky itself are refused outright, a task's unblock lifts
// nothing, read-only refuses all three, and an install or uninstall always
// asks.
func (e *Engine) appChangeLocked(in Input) Verdict {
	app := in.Launch
	cats := e.apps.Categories(app)
	verb := map[Op]string{OpInstall: "install over", OpUninstall: "uninstall", OpStop: "stop"}[in.Op]
	deny := func(rule, why string) Verdict {
		return Verdict{Decision: Deny, Rule: rule, Reason: fmt.Sprintf("Agents cannot %s %s: %s", verb, appName(app), why)}
	}
	switch {
	case IsPinkyApp(app):
		return deny(RulePinkyApps, "it gives Pinky its access to the phone.")
	case contains(cats, CatSystem):
		return deny(RuleSystemApps, "it is part of the phone's system.")
	case contains(cats, CatPasswords):
		return deny(RulePasswords, "it is a password manager.")
	case contains(cats, CatFinance):
		return deny(RuleFinance, "it is a finance app.")
	case contains(cats, CatBlocked):
		return deny(RuleBlocked, "you blocked it for agents.")
	case len(in.ScopeApps) > 0 && !appIn(app, in.ScopeApps):
		return Verdict{Decision: Deny, Rule: RuleAgentScope, Scope: true,
			Reason: fmt.Sprintf("%s is outside this agent's scope.", appName(app))}
	case len(in.TaskApps) > 0 && !appIn(app, in.TaskApps):
		return Verdict{Decision: Deny, Rule: RuleTaskApps, Scope: true,
			Reason: fmt.Sprintf("%s is not in this task's app list (%s).", appName(app), strings.Join(in.TaskApps, ", "))}
	}
	var asks []string
	switch in.Op {
	case OpInstall:
		asks = append(asks, RuleInstall)
	case OpUninstall:
		asks = append(asks, RuleUninstall)
	}
	// The person's own rules about the app apply as to any action: a deny
	// refuses, an ask asks too.
	for _, r := range e.rulesLocked() {
		if !r.matches(app, cats, nil, in) {
			continue
		}
		switch r.Decision {
		case Deny:
			return Verdict{Decision: Deny, Rule: r.Name, Reason: fmt.Sprintf("%s is blocked by the rule %q.", appName(app), r.Name)}
		case Ask:
			if !contains(asks, r.Name) {
				asks = append(asks, r.Name)
			}
		}
	}
	if in.ReadOnly || e.readOnlyLocked(in.Device) {
		return Verdict{Decision: Deny, Rule: RuleReadOnly,
			Reason: "Read-only mode is on: agents cannot install, uninstall or stop apps."}
	}
	if len(asks) == 0 {
		return Verdict{Decision: Allow}
	}
	v := Verdict{Decision: Ask, Rule: asks[0], Asks: asks}
	switch v.Rule {
	case RuleInstall:
		v.Reason = "Installing or updating an app from the computer needs the person's approval."
	case RuleUninstall:
		v.Reason = "Uninstalling an app deletes its data on the phone and always needs the person's approval."
	default:
		v.Reason = reason(v, app)
	}
	// Always allow answers lift the person's own asks, never the two above:
	// the gateway keeps an install's Always allow per agent, phone and
	// signer (standing.go), and an uninstall asks every time.
	return e.liftLocked(v, app)
}
