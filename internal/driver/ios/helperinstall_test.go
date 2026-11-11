package ios

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestHelperBundleID(t *testing.T) {
	owned := wdaRunner{BundleID: "com.me.WebDriverAgentRunner.xctrunner", AppID: "ABCDE12345.com.me.WebDriverAgentRunner.xctrunner"}
	for _, c := range []struct {
		name, want, team string
		r                wdaRunner
		bid              string
	}{
		{"asked", "com.x.WebDriverAgentRunner.xctrunner", "ABCDE12345", owned, "com.x.WebDriverAgentRunner"},
		{"the phone's, same team", "", "ABCDE12345", owned, "com.me.WebDriverAgentRunner"},
		{"the phone's, other team", "", "ZZZZZ99999", owned, "pinky.zzzzz99999.WebDriverAgentRunner"},
		{"the phone's, team unknown", "", "ABCDE12345", wdaRunner{BundleID: owned.BundleID}, "pinky.abcde12345.WebDriverAgentRunner"},
		{"none on the phone", "", "ABCDE12345", wdaRunner{}, "pinky.abcde12345.WebDriverAgentRunner"},
	} {
		if got := helperBundleID(c.want, c.team, c.r); got != c.bid {
			t.Errorf("%s: %q, want %q", c.name, got, c.bid)
		}
	}
	// Pinky's own id is the one it prefers among several runners.
	if !strings.Contains(strings.ToLower(helperBundleID("", "ABCDE12345", wdaRunner{})), "pinky") {
		t.Error("Pinky's bundle id does not say pinky")
	}
	for in, want := range map[string]string{
		"ABCDE12345.com.me.x": "ABCDE12345",
		"com.me.x":            "",
		"":                    "",
		"ABCDE12345":          "",
	} {
		if got := appIDTeam(in); got != want {
			t.Errorf("appIDTeam(%q) = %q, want %q", in, got, want)
		}
	}
}

// devicectlList is `devicectl list devices --json-output`, cut down.
const devicectlList = `{"info":{"outcome":"success"},"result":{"devices":[
 {"hardwareProperties":{"udid":"00008110-0001","platform":"iOS","reality":"physical","deviceType":"iPhone"},
  "deviceProperties":{"name":"Ann’s iPhone","osVersionNumber":"27.0.1","developerModeStatus":"enabled"},
  "connectionProperties":{"pairingState":"paired","tunnelState":"disconnected"}},
 {"hardwareProperties":{"udid":"00008103-0002","platform":"iOS","reality":"physical","deviceType":"iPad"},
  "deviceProperties":{"name":"Ann’s iPad","osVersionNumber":"26.1","developerModeStatus":"disabled"},
  "connectionProperties":{"pairingState":"paired","tunnelState":"unavailable"}},
 {"hardwareProperties":{"udid":"9C300DBA","platform":"iOS","reality":"simulated","deviceType":"iPhone"},
  "deviceProperties":{"name":"iPhone 18 Pro","osVersionNumber":"27.0"},
  "connectionProperties":{"pairingState":"paired","tunnelState":"disconnected"}},
 {"hardwareProperties":{"udid":"00008020-0003","platform":"watchOS","reality":"physical"},
  "deviceProperties":{"name":"Watch"},"connectionProperties":{"pairingState":"paired"}},
 {"hardwareProperties":{"udid":"00008110-0004","platform":"iOS","reality":"physical"},
  "deviceProperties":{"name":"Not paired"},"connectionProperties":{"pairingState":"unpaired"}}
]}}`

func TestParsePhones(t *testing.T) {
	phones, err := parsePhones([]byte(devicectlList))
	if err != nil {
		t.Fatal(err)
	}
	want := []helperPhone{
		{UDID: "00008110-0001", Name: "Ann’s iPhone", OS: "27.0.1", DeveloperMode: "enabled", Reachable: true},
		{UDID: "00008103-0002", Name: "Ann’s iPad", OS: "26.1", DeveloperMode: "disabled"},
	}
	if !slices.Equal(phones, want) {
		t.Errorf("phones:\n got %+v\nwant %+v", phones, want)
	}
	if _, err := parsePhones([]byte("not json")); err == nil {
		t.Error("no error for a list devicectl did not write")
	}
}

func TestPickPhone(t *testing.T) {
	phones, _ := parsePhones([]byte(devicectlList))
	for _, c := range []struct {
		want, udid, err string
	}{
		{"", "00008110-0001", ""}, // the only one within reach
		{"00008110-0001", "00008110-0001", ""},
		{"ann's iphone", "00008110-0001", ""}, // a straight quote and any case
		{"Ann’s iPad", "", "not within reach"},
		{"Bob", "", `no paired phone is "Bob"`},
	} {
		p, err := pickPhone(phones, c.want)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err %v, want %q", c.want, err, c.err)
			}
			continue
		}
		if err != nil || p.UDID != c.udid {
			t.Errorf("%q: %s, %v; want %s", c.want, p.UDID, err, c.udid)
		}
	}
	two := append(slices.Clone(phones), helperPhone{UDID: "x", Name: "Other", Reachable: true})
	if _, err := pickPhone(two, ""); err == nil || !strings.Contains(err.Error(), "several") {
		t.Errorf("two within reach: %v", err)
	}
	if _, err := pickPhone(nil, ""); err == nil || !strings.Contains(err.Error(), "paired") {
		t.Errorf("none: %v", err)
	}
	if _, err := pickPhone(phones[1:], ""); err == nil || !strings.Contains(err.Error(), "within reach") {
		t.Errorf("none within reach: %v", err)
	}
}

// devCert makes a certificate as Apple issues development ones: the team
// in the subject's OU. It returns it as PEM and its SHA-1.
func devCert(t *testing.T, cn, team, org string) ([]byte, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: cn, OrganizationalUnit: []string{team}, Organization: []string{org}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha1.Sum(der)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), strings.ToUpper(hex.EncodeToString(sum[:]))
}

func TestDevTeams(t *testing.T) {
	a, aSum := devCert(t, "Apple Development: Ann (P1)", "TEAMAAAAA1", "Ann Ltd")
	a2, a2Sum := devCert(t, "Apple Development: Ann (P2)", "TEAMAAAAA1", "Ann Ltd")
	b, bSum := devCert(t, "iPhone Developer: Bob (P3)", "TEAMBBBBB2", "Bob")
	gone, _ := devCert(t, "Apple Development: Old (P4)", "TEAMCCCCC3", "Old") // not valid any more
	dist, distSum := devCert(t, "Apple Distribution: Ann", "TEAMDDDDD4", "Ann Ltd")
	out := fmt.Sprintf(`  1) %s "Apple Development: Ann (P1)"
  2) %s "Apple Development: Ann (P2)"
  3) %s "iPhone Developer: Bob (P3)"
  4) %s "Apple Distribution: Ann"
  5) 7ACF33FFAA902E783A8A6C348796F0FB31FF5F2E "Developer ID Application: Ann (TEAMAAAAA1)"
     5 valid identities found
`, aSum, a2Sum, bSum, distSum)
	ids := validIdentities(out)
	if len(ids) != 5 || ids[aSum] != "Apple Development: Ann (P1)" {
		t.Fatalf("identities = %v", ids)
	}
	pems := slices.Concat(a, a2, b, gone, dist)
	got := teamsOf(ids, pems)
	want := []devTeam{{"TEAMAAAAA1", "Ann Ltd"}, {"TEAMBBBBB2", "Bob"}}
	if !slices.Equal(got, want) {
		t.Errorf("teams = %v, want %v", got, want)
	}
	if _, err := onlyTeam(got, nil); err == nil || !strings.Contains(err.Error(), "TEAMAAAAA1 (Ann Ltd), TEAMBBBBB2 (Bob)") {
		t.Errorf("two teams: %v", err)
	}
	if team, err := onlyTeam(got[:1], nil); team != "TEAMAAAAA1" || err != nil {
		t.Errorf("one team: %q, %v", team, err)
	}
	if _, err := onlyTeam(nil, nil); err == nil || !strings.Contains(err.Error(), "Accounts") {
		t.Errorf("no team: %v", err)
	}
}

func TestHelperArgs(t *testing.T) {
	args := helperBuildArgs("/s", "/dd", "UDID", "TEAMAAAAA1", "com.me.WebDriverAgentRunner", false)
	want := []string{"build-for-testing", "-project", "/s/WebDriverAgent.xcodeproj", "-scheme", "WebDriverAgentRunner",
		"-destination", "id=UDID", "-derivedDataPath", "/dd",
		"CODE_SIGN_STYLE=Automatic", "DEVELOPMENT_TEAM=TEAMAAAAA1", "PRODUCT_BUNDLE_IDENTIFIER=com.me.WebDriverAgentRunner"}
	if !slices.Equal(args, want) {
		t.Errorf("local signing:\n got %q\nwant %q", args, want)
	}
	if args := helperBuildArgs("/s", "/dd", "UDID", "T", "b", true); !slices.Contains(args, "-allowProvisioningUpdates") || !slices.Contains(args, "-allowProvisioningDeviceRegistration") {
		t.Errorf("with updates: %q", args)
	}
	r := resignArgs("ABC", "/a.app")
	if r[len(r)-1] != "/a.app" || !slices.Contains(r, "--preserve-metadata=identifier,entitlements,flags,runtime") || r[slices.Index(r, "--sign")+1] != "ABC" {
		t.Errorf("resign: %q", r)
	}
}

func TestHelperHint(t *testing.T) {
	for out, want := range map[string]string{
		"Unable to launch com.me.WebDriverAgentRunner.xctrunner because it has an invalid code signature, inadequate entitlements or its profile has not been explicitly trusted by the user.": "VPN & Device Management",
		"error: Developer Mode disabled":                                  "Developer Mode",
		"Timed out while enabling automation mode.":                       "allow automation",
		"The device is passcode protected.":                               "Unlock the phone",
		`error: No Account for Team "ABCDE12345".`:                        "Xcode › Settings › Accounts",
		`error: No profiles for 'com.me.WebDriverAgentRunner' were found`: "--local-signing",
		"** BUILD FAILED ** something else":                               "",
	} {
		got := helperHint(out)
		if want == "" && got != "" || !strings.Contains(got, want) {
			t.Errorf("%q: %q, want %q", out, got, want)
		}
	}
	if !needsProvisioning(`error: No profiles for 'x' were found: Xcode couldn't find any iOS App Development provisioning profiles matching 'x'.`) {
		t.Error("a missing profile does not ask for one")
	}
	if needsProvisioning("error: Developer Mode disabled") {
		t.Error("Developer Mode asks for a profile")
	}
}

func TestMarkWriter(t *testing.T) {
	w := &markWriter{mark: []byte(helperMark), found: make(chan struct{})}
	for _, s := range []string{"Test Suite started\n", "2026-11-11 ServerURL", "Here->http://127.0.0.1:8100<-ServerURLHere\n"} {
		select {
		case <-w.found:
			t.Fatalf("found before %q", s)
		default:
		}
		if n, err := w.Write([]byte(s)); n != len(s) || err != nil {
			t.Fatal(n, err)
		}
	}
	select {
	case <-w.found:
	default:
		t.Fatal("the mark split across writes was not found")
	}
	w.Write([]byte(helperMark)) // again: no second close
}
