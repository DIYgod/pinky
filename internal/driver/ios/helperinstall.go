package ios

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Installing the WebDriverAgent helper on a phone: `pinky helper install`.
//
// It does what the person otherwise does in Xcode, with the WDA release
// Pinky builds for Simulators (wdaVersion, the source archive in Pinky's
// WebDriverAgent folder): build the runner signed with the person's team,
// give it Pinky's name and icon (helperbrand.go), sign the runner app
// again with the same certificate and entitlements (branding changes
// files its signature seals), and install it by running WDA's test once,
// as Product › Test does, until WDA prints ServerURLHere.
//
// The team and the bundle id default to the helper already on the phone,
// so the new one replaces it in place and keeps the provisioning profile
// that signs it. Xcode first signs with what is on this Mac; only when
// that has no profile for the helper and the phone does it ask Apple for
// one (-allowProvisioningUpdates), unless LocalSigning says not to.

// HelperInstall is what InstallHelper installs where.
type HelperInstall struct {
	// Dir is Pinky's WebDriverAgent folder, which keeps the source
	// archive the Simulators' builds use too.
	Dir string
	// Phone is the phone's UDID or name; "" for the only one.
	Phone string
	// Team is the Apple developer team to sign with; "" for the team of
	// the helper on the phone, else the only one this Mac can sign for.
	Team string
	// BundleID is the helper's bundle id, without ".xctrunner"; "" for
	// the one on the phone when the team is the same, else a new one.
	BundleID string
	// LocalSigning keeps Xcode to the certificates and profiles already
	// on this Mac: it never registers the phone or makes a profile.
	LocalSigning bool
	// Out gets what InstallHelper does, for the person.
	Out io.Writer
	// Client downloads the source; nil for http.DefaultClient.
	Client *http.Client
}

// helperStartTimeout bounds installing the built helper and waiting for
// WDA to answer: the phone may first ask for its passcode to allow
// automation, which XCTest waits 60 s for.
const helperStartTimeout = 4 * time.Minute

// helperMark is what WDA prints once it serves.
const helperMark = "ServerURLHere->"

// InstallHelper builds the helper, brands and signs it, and installs it on
// the phone, as described above.
func InstallHelper(ctx context.Context, o HelperInstall) error {
	out := o.Out
	if out == nil {
		out = io.Discard
	}
	client := o.Client
	if client == nil {
		client = http.DefaultClient
	}
	if runtime.GOOS != "darwin" {
		return errors.New("installing the iPhone helper needs a Mac with Xcode")
	}
	x, err := findXcode()
	if err != nil {
		return fmt.Errorf("installing the iPhone helper needs Xcode: %w", err)
	}
	team := strings.ToUpper(strings.TrimSpace(o.Team))
	if team != "" && !teamID.MatchString(team) {
		return fmt.Errorf("%q is not a team ID: it has 10 letters and digits (Xcode › Settings › Accounts shows it)", o.Team)
	}
	if o.BundleID != "" && !strings.Contains(strings.ToLower(o.BundleID), "webdriveragentrunner") {
		return errors.New("keep WebDriverAgentRunner in the bundle id: Pinky knows its helper by it")
	}
	phones, err := listPhones(ctx, x)
	if err != nil {
		return err
	}
	ph, err := pickPhone(phones, o.Phone)
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Phone: %s (%s, iOS %s)\n", ph.Name, ph.UDID, ph.OS)
	if ph.DeveloperMode == "disabled" {
		return fmt.Errorf("Developer Mode is off on %s: turn it on in Settings › Privacy & Security › Developer Mode (the phone restarts), then run this again", ph.Name)
	}

	// The helper already on the phone: the new one keeps its team and id.
	runner, rerr := phoneRunner(ph.UDID)
	switch {
	case rerr != nil:
		fmt.Fprintf(out, "Could not look for a helper on the phone (%v); going on.\n", rerr)
	case runner.BundleID == "":
		fmt.Fprintln(out, "The phone has no helper yet.")
	default:
		fmt.Fprintf(out, "Helper on the phone: %s, team %s\n", runner.BundleID, orUnknown(appIDTeam(runner.AppID)))
	}
	if team == "" {
		team = appIDTeam(runner.AppID)
	}
	if team == "" {
		team, err = onlyTeam(devTeams(ctx))
		if err != nil {
			return err
		}
	}
	bid := helperBundleID(o.BundleID, team, runner)
	if runner.BundleID != "" && runner.BundleID != bid+".xctrunner" {
		fmt.Fprintf(out, "The new helper gets its own bundle id, %s.xctrunner; the phone keeps %s until you remove it.\n", bid, runner.BundleID)
	}

	// The source: Pinky's archive of the release, downloaded once.
	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp("", "pinky-helper-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(tmp, "src")
	archive := filepath.Join(o.Dir, "WebDriverAgent-"+wdaVersion+".tar.gz")
	if err := unpackSource(archive, src); err != nil {
		os.RemoveAll(src)
		fmt.Fprintf(out, "Downloading WebDriverAgent %s…\n", wdaVersion)
		if err := downloadSource(ctx, client, archive); err != nil {
			return err
		}
		if err := unpackSource(archive, src); err != nil {
			os.Remove(archive)
			return err
		}
	}

	// Build, signed for the phone.
	dd := filepath.Join(tmp, "dd")
	logPath := filepath.Join(o.Dir, "helper-install.log")
	logf, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logf.Close()
	fmt.Fprintf(out, "Building WebDriverAgent %s with Xcode %s for team %s, bundle id %s. This takes a minute or two.\n", wdaVersion, x.Version, team, bid)
	tail, err := xcodeRun(ctx, x, logf, helperBuildArgs(src, dd, ph.UDID, team, bid, false), nil)
	if err != nil && ctx.Err() == nil && !o.LocalSigning && needsProvisioning(tail) {
		fmt.Fprintln(out, "No profile on this Mac signs the helper for this phone; letting Xcode get one from Apple (it may register the phone with your team).")
		tail, err = xcodeRun(ctx, x, logf, helperBuildArgs(src, dd, ph.UDID, team, bid, true), nil)
	}
	if err != nil {
		return xcodeFailed(ctx, "building the helper", err, tail, logPath)
	}
	products := filepath.Join(dd, "Build", "Products")
	app := runnerAppIn(products)
	if app == "" || xctestrun(products) == "" {
		return fmt.Errorf("Xcode built WebDriverAgent but made no runner app (%s)", logPath)
	}
	profile, err := embeddedProfile(app)
	if err != nil {
		return fmt.Errorf("reading the profile Xcode signed the helper with: %w", err)
	}
	if !profile.runsOn(ph.UDID) {
		return fmt.Errorf("the profile Xcode signed the helper with, %q, does not include %s; run this without --local-signing, or add the phone to your team in Xcode", profile.Name, ph.Name)
	}

	// Pinky's name and icon, then the signature again.
	if _, err := brandRunner(ctx, x, app); err != nil {
		return fmt.Errorf("giving the helper Pinky's name and icon: %w", err)
	}
	if err := resignApp(ctx, x, app); err != nil {
		return fmt.Errorf("signing the helper again: %w", err)
	}

	// Install and start it once, as Product › Test does.
	fmt.Fprintf(out, "Installing it on %s and starting it once. Keep the phone unlocked; if it asks for its passcode to allow automation, enter it there.\n", ph.Name)
	run, cancel := context.WithTimeout(ctx, helperStartTimeout)
	defer cancel()
	tail, err = xcodeRun(run, x, logf, helperTestArgs(products, ph.UDID, filepath.Join(tmp, "run")), []string{"TEST_RUNNER_USE_IP=127.0.0.1"})
	if err != nil {
		if errors.Is(err, errHelperUp) {
			fmt.Fprintf(out, "Installed %q on %s; WebDriverAgent answered and was stopped again.\n", helperDisplayName, ph.Name)
			fmt.Fprintf(out, "Its signature lasts until %s. Pinky starts the helper whenever it opens the phone.\n", helperDate(profile.Expires))
			return nil
		}
		if run.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("WebDriverAgent did not answer within %s of starting on %s (%s)%s", helperStartTimeout, ph.Name, logPath, hintText(helperHint(tail)))
		}
		return xcodeFailed(ctx, "installing and starting the helper", err, tail, logPath)
	}
	return fmt.Errorf("the helper's test ended before WebDriverAgent answered: %s (%s)%s", xcodeErrors(tail), logPath, hintText(helperHint(tail)))
}

// teamID is an Apple developer team's ID.
var teamID = regexp.MustCompile(`^[A-Z0-9]{10}$`)

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// appIDTeam is the team of an application identifier, "<team>.<bundle id>".
func appIDTeam(appID string) string {
	team, _, ok := strings.Cut(appID, ".")
	if !ok || !teamID.MatchString(team) {
		return ""
	}
	return team
}

// helperBundleID is the bundle id to build the helper with: the one asked
// for, else the phone's helper's when it is the team's (iOS replaces an
// app only with one of the same team), else one of Pinky's for the team.
func helperBundleID(want, team string, r wdaRunner) string {
	if want != "" {
		return strings.TrimSuffix(strings.TrimSpace(want), ".xctrunner")
	}
	if r.BundleID != "" && appIDTeam(r.AppID) == team {
		return strings.TrimSuffix(r.BundleID, ".xctrunner")
	}
	return "pinky." + strings.ToLower(team) + ".WebDriverAgentRunner"
}

// phoneRunner is the helper installed on a phone, read over usbmuxd.
func phoneRunner(udid string) (wdaRunner, error) {
	e, err := findEntry(udid, false)
	if err != nil {
		return wdaRunner{}, err
	}
	return findRunnerApp(e)
}

// helperPhone is a phone as devicectl lists it.
type helperPhone struct {
	UDID, Name, OS string
	DeveloperMode  string // "enabled", "disabled", or "" when unknown
	Reachable      bool   // devicectl can reach it now
}

// listPhones lists the iPhones and iPads paired with this Mac.
func listPhones(ctx context.Context, x xcode) ([]helperPhone, error) {
	f, err := os.CreateTemp("", "pinky-devices-*.json")
	if err != nil {
		return nil, err
	}
	f.Close()
	defer os.Remove(f.Name())
	cmd := exec.CommandContext(ctx, x.tool("devicectl"), "list", "devices", "--quiet", "--json-output", f.Name())
	cmd.Env = x.env()
	if b, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("listing phones with devicectl: %v: %s", err, strings.TrimSpace(string(b)))
	}
	b, err := os.ReadFile(f.Name())
	if err != nil {
		return nil, err
	}
	return parsePhones(b)
}

// parsePhones reads devicectl's JSON: the physical iOS devices paired with
// this Mac.
func parsePhones(b []byte) ([]helperPhone, error) {
	var doc struct {
		Result struct {
			Devices []struct {
				Hardware struct {
					UDID     string `json:"udid"`
					Platform string `json:"platform"`
					Reality  string `json:"reality"`
				} `json:"hardwareProperties"`
				Device struct {
					Name          string `json:"name"`
					OS            string `json:"osVersionNumber"`
					DeveloperMode string `json:"developerModeStatus"`
				} `json:"deviceProperties"`
				Connection struct {
					Pairing string `json:"pairingState"`
					Tunnel  string `json:"tunnelState"`
				} `json:"connectionProperties"`
			} `json:"devices"`
		} `json:"result"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("reading devicectl's list: %w", err)
	}
	var out []helperPhone
	for _, d := range doc.Result.Devices {
		if d.Hardware.Platform != "iOS" || d.Hardware.Reality != "physical" || d.Hardware.UDID == "" {
			continue
		}
		if d.Connection.Pairing != "" && d.Connection.Pairing != "paired" {
			continue
		}
		out = append(out, helperPhone{
			UDID: d.Hardware.UDID, Name: d.Device.Name, OS: d.Device.OS,
			DeveloperMode: d.Device.DeveloperMode,
			Reachable:     d.Connection.Tunnel != "unavailable",
		})
	}
	return out, nil
}

// pickPhone finds the phone a command names by UDID or name, or the only
// one within reach.
func pickPhone(phones []helperPhone, want string) (helperPhone, error) {
	fold := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, "’", "'")) }
	var match []helperPhone
	var names []string
	for _, p := range phones {
		names = append(names, fmt.Sprintf("%s (%s)", p.Name, p.UDID))
		if want == "" && p.Reachable || want != "" && (p.UDID == want || fold(p.Name) == fold(want)) {
			match = append(match, p)
		}
	}
	switch {
	case len(phones) == 0:
		return helperPhone{}, errors.New("no iPhone is paired with this Mac: plug it in with a cable and tap Trust on the phone")
	case len(match) == 1 && !match[0].Reachable:
		return helperPhone{}, fmt.Errorf("%s is not within reach: plug it in, or put it on this Mac's Wi-Fi", match[0].Name)
	case len(match) == 1:
		return match[0], nil
	case len(match) == 0 && want == "":
		return helperPhone{}, fmt.Errorf("no iPhone is within reach: plug it in, or put it on this Mac's Wi-Fi; paired: %s", strings.Join(names, ", "))
	case len(match) == 0:
		return helperPhone{}, fmt.Errorf("no paired phone is %q; paired: %s", want, strings.Join(names, ", "))
	}
	return helperPhone{}, fmt.Errorf("several phones are within reach; name one: %s", strings.Join(names, ", "))
}

// devTeam is a team this Mac holds a development certificate for.
type devTeam struct{ ID, Name string }

// devTeams lists the teams of the development certificates in the
// keychain that can sign.
func devTeams(ctx context.Context) ([]devTeam, error) {
	ids, err := exec.CommandContext(ctx, "/usr/bin/security", "find-identity", "-v", "-p", "codesigning").Output()
	if err != nil {
		return nil, fmt.Errorf("listing this Mac's signing certificates: %w", err)
	}
	var pems []byte
	for _, name := range []string{"Apple Development", "iPhone Developer"} {
		b, _ := exec.CommandContext(ctx, "/usr/bin/security", "find-certificate", "-a", "-p", "-c", name).Output()
		pems = append(pems, b...)
	}
	return teamsOf(validIdentities(string(ids)), pems), nil
}

// identityLine is a line of `security find-identity`: its SHA-1 and name.
var identityLine = regexp.MustCompile(`^\s*\d+\)\s+([0-9A-F]{40})\s+"(.*)"`)

// validIdentities reads `security find-identity -v`: the signing
// identities by their certificate's SHA-1.
func validIdentities(out string) map[string]string {
	ids := map[string]string{}
	for _, l := range strings.Split(out, "\n") {
		if m := identityLine.FindStringSubmatch(l); m != nil {
			ids[m[1]] = m[2]
		}
	}
	return ids
}

// teamsOf reads the teams of the development certificates in pems whose
// identities can sign (ids): a certificate's team is its subject's OU.
func teamsOf(ids map[string]string, pems []byte) []devTeam {
	seen := map[string]bool{}
	var out []devTeam
	for {
		var blk *pem.Block
		blk, pems = pem.Decode(pems)
		if blk == nil {
			break
		}
		sum := sha1.Sum(blk.Bytes)
		name, ok := ids[strings.ToUpper(hex.EncodeToString(sum[:]))]
		if !ok || !(strings.HasPrefix(name, "Apple Development:") || strings.HasPrefix(name, "iPhone Developer:")) {
			continue
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil || len(c.Subject.OrganizationalUnit) == 0 {
			continue
		}
		t := devTeam{ID: c.Subject.OrganizationalUnit[0]}
		if len(c.Subject.Organization) > 0 {
			t.Name = c.Subject.Organization[0]
		}
		if !seen[t.ID] {
			seen[t.ID] = true
			out = append(out, t)
		}
	}
	slices.SortFunc(out, func(a, b devTeam) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// onlyTeam is the team to sign with when nothing names one: the only one
// this Mac can sign for.
func onlyTeam(teams []devTeam, err error) (string, error) {
	if err != nil {
		return "", err
	}
	switch len(teams) {
	case 0:
		return "", errors.New("this Mac has no Apple Development certificate: add your Apple ID in Xcode › Settings › Accounts (a free one works), or name a team with --team")
	case 1:
		return teams[0].ID, nil
	}
	var names []string
	for _, t := range teams {
		names = append(names, fmt.Sprintf("%s (%s)", t.ID, t.Name))
	}
	return "", fmt.Errorf("this Mac can sign for several teams; name one with --team: %s", strings.Join(names, ", "))
}

// helperBuildArgs builds WDA's runner for the phone, signed for team with
// bundle id bid. updates lets Xcode ask Apple for a profile and register
// the phone.
func helperBuildArgs(src, dd, udid, team, bid string, updates bool) []string {
	args := []string{"build-for-testing",
		"-project", filepath.Join(src, "WebDriverAgent.xcodeproj"),
		"-scheme", "WebDriverAgentRunner",
		"-destination", "id=" + udid,
		"-derivedDataPath", dd}
	if updates {
		args = append(args, "-allowProvisioningUpdates", "-allowProvisioningDeviceRegistration")
	}
	return append(args,
		"CODE_SIGN_STYLE=Automatic",
		"DEVELOPMENT_TEAM="+team,
		"PRODUCT_BUNDLE_IDENTIFIER="+bid)
}

// helperTestArgs installs the build in products on the phone and runs
// WDA's test, which serves until it is stopped.
func helperTestArgs(products, udid, dd string) []string {
	return []string{"test-without-building",
		"-xctestrun", xctestrun(products),
		"-destination", "id=" + udid,
		"-derivedDataPath", dd,
		"-collect-test-diagnostics", "never",
		"-test-timeouts-enabled", "NO"}
}

// errHelperUp ends a test run once WDA printed helperMark.
var errHelperUp = errors.New("WebDriverAgent answered")

// xcodeRun runs xcodebuild with args, its output to log, and returns the
// output's last lines. A run that prints helperMark is stopped then and
// returns errHelperUp.
func xcodeRun(ctx context.Context, x xcode, log io.Writer, args, env []string) (string, error) {
	tail := &lastLines{max: 64 << 10}
	mark := &markWriter{mark: []byte(helperMark), found: make(chan struct{})}
	cmd := exec.Command(x.tool("xcodebuild"), args...)
	cmd.Env = append(x.env(), env...)
	cmd.Stdout = io.MultiWriter(log, tail, mark)
	cmd.Stderr = cmd.Stdout
	// Its own process group: a Ctrl-C reaches Pinky, which stops it with
	// SIGTERM; on SIGINT a test run collects diagnostics for minutes.
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("starting xcodebuild: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var result error
	select {
	case err := <-done:
		return tail.String(), err
	case <-mark.found:
		result = errHelperUp
	case <-ctx.Done():
		result = ctx.Err()
	}
	cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		<-done
	}
	return tail.String(), result
}

// markWriter closes found once mark went through it, across writes.
type markWriter struct {
	mu    sync.Mutex
	mark  []byte
	carry []byte
	found chan struct{}
	seen  bool
}

func (w *markWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.seen {
		return len(p), nil
	}
	b := append(w.carry, p...)
	if bytes.Contains(b, w.mark) {
		w.seen = true
		close(w.found)
		return len(p), nil
	}
	keep := min(len(b), len(w.mark)-1)
	w.carry = append([]byte(nil), b[len(b)-keep:]...)
	return len(p), nil
}

// xcodeFailed words a failed xcodebuild for the person.
func xcodeFailed(ctx context.Context, what string, err error, tail, logPath string) error {
	if ctx.Err() != nil {
		return fmt.Errorf("%s: %w", what, ctx.Err())
	}
	return fmt.Errorf("%s failed: %s (%s)%s", what, xcodeErrors(tail), logPath, hintText(helperHint(tail)))
}

func hintText(h string) string {
	if h == "" {
		return ""
	}
	return "\n" + h
}

// needsProvisioning reports whether a build failed for want of a profile
// that Xcode could get from Apple.
func needsProvisioning(out string) bool {
	l := strings.ToLower(out)
	return containsAny(l, "no profiles for", "requires a provisioning profile",
		"doesn't include the currently selected device", "does not include the currently selected device",
		"no provisioning profile", "provisioning profile doesn't include", "to register the device")
}

// helperHint says what the person can do about a failure that Xcode's
// output shows, or "".
func helperHint(out string) string {
	l := strings.ToLower(out)
	switch {
	case containsAny(l, "explicitly trusted", "untrusted developer", "not trusted", "verify the developer"):
		return "On the phone, trust your developer certificate in Settings › General › VPN & Device Management, then run this again."
	case containsAny(l, "developer mode"):
		return "Turn on Developer Mode in Settings › Privacy & Security › Developer Mode (the phone restarts), then run this again."
	case containsAny(l, "automation mode", "enable ui automation"):
		return "Enter the phone's passcode on the phone when it asks to allow automation, then run this again."
	case containsAny(l, "is locked", "passcode protected", "device locked", "unlock "):
		return "Unlock the phone and keep it unlocked until the helper starts, then run this again."
	case containsAny(l, "no account for team", "no accounts", "sign in with your apple id", "not signed in"):
		return "Add your Apple ID in Xcode › Settings › Accounts, or name another team with --team."
	case needsProvisioning(out), containsAny(l, "no signing certificate", "provisioning profile"):
		return "Xcode found no way to sign the helper for this phone. Run this without --local-signing to let Xcode get a profile, or check your team in Xcode › Settings › Accounts."
	}
	return ""
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// embeddedProfile reads the provisioning profile Xcode put in an app.
func embeddedProfile(app string) (provisioningProfile, error) {
	b, err := os.ReadFile(filepath.Join(app, "embedded.mobileprovision"))
	if err != nil {
		return provisioningProfile{}, err
	}
	return parseProfile(b)
}

// resignApp signs app again, after branding, with the certificate and
// entitlements Xcode signed it with. The frameworks and the test bundle
// inside keep their own signatures: branding changes none of them.
func resignApp(ctx context.Context, x xcode, app string) error {
	tmp, err := os.MkdirTemp("", "pinky-cert-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	prefix := filepath.Join(tmp, "cert")
	cmd := exec.CommandContext(ctx, "/usr/bin/codesign", "-d", "--extract-certificates="+prefix, app)
	cmd.Env = x.env()
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("reading its certificate: %v: %s", err, strings.TrimSpace(string(b)))
	}
	der, err := os.ReadFile(prefix + "0")
	if err != nil {
		return fmt.Errorf("Xcode did not sign it: %w", err)
	}
	sum := sha1.Sum(der)
	for _, args := range [][]string{
		resignArgs(strings.ToUpper(hex.EncodeToString(sum[:])), app),
		{"--verify", "--deep", "--strict", app},
	} {
		cmd := exec.CommandContext(ctx, "/usr/bin/codesign", args...)
		cmd.Env = x.env()
		if b, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("codesign %s: %v: %s", args[0], err, strings.TrimSpace(string(b)))
		}
	}
	return nil
}

// resignArgs sign app with the identity whose certificate has this SHA-1,
// keeping its bundle id, entitlements and flags.
func resignArgs(identity, app string) []string {
	return []string{"--force", "--sign", identity,
		"--preserve-metadata=identifier,entitlements,flags,runtime",
		"--generate-entitlement-der",
		"--timestamp=none",
		app}
}
