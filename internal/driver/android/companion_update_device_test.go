//go:build device

package android

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
)

// TestDeviceCompanionUpdate updates the companion app the way automatic
// updates do, on an emulator: an older build with its service off is
// updated and the service stays off; an older build with the service on is
// updated and the service is on again and answers. It also checks what
// Android itself does with the service across `pm install -r`. It needs an
// older build signed with the same key (PINKY_COMPANION_OLD) and the one
// Pinky bundles (PINKY_COMPANION_APK). It installs apps and turns Pinky's
// own service on and off, so it runs on emulators only, and it leaves the
// bundled build installed and the setting as it found it:
//
//	scripts/devlock.sh emulator-5562 env PINKY_DEVICE=emulator-5562 \
//	  PINKY_COMPANION_OLD=old.apk PINKY_COMPANION_APK=new.apk \
//	  go test -tags device -run TestDeviceCompanionUpdate -v ./internal/driver/android/
func TestDeviceCompanionUpdate(t *testing.T) {
	serial := os.Getenv("PINKY_DEVICE")
	if !strings.HasPrefix(serial, "emulator-") {
		t.Skip("emulators only: it installs apps and turns Pinky's service on and off")
	}
	oldAPK, newAPK := os.Getenv("PINKY_COMPANION_OLD"), os.Getenv("PINKY_COMPANION_APK")
	if oldAPK == "" || newAPK == "" {
		t.Skip("PINKY_COMPANION_OLD and PINKY_COMPANION_APK not set")
	}
	oldV, err := companion.APKVersion(oldAPK)
	if err != nil {
		t.Fatal(err)
	}
	newV, err := companion.APKVersion(newAPK)
	if err != nil {
		t.Fatal(err)
	}
	if oldV.Code >= newV.Code {
		t.Fatalf("the old build (%d) is not older than the new one (%d)", oldV.Code, newV.Code)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	p := NewProvider(Config{Logf: t.Logf})
	d, err := p.OpenDevice(ctx, serial)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	run := adbRunner{c: d.client, serial: serial}
	c := companion.New(run)
	defer c.Close()

	shell := func(cmd string) string {
		t.Helper()
		out, err := run.Shell(ctx, cmd)
		if err != nil {
			t.Fatalf("%s: %v", cmd, err)
		}
		return strings.TrimSpace(out)
	}
	setting := func() string { return shell("settings get secure enabled_accessibility_services") }
	listed := func() bool { return strings.Contains(setting(), companion.Package+"/") }
	installed := func() int {
		v, ok, err := c.InstalledVersion(ctx)
		if err != nil || !ok {
			t.Fatalf("installed version: %v, %v", ok, err)
		}
		return v.Code
	}
	// installOld puts the older build back, which `pm install -r` refuses:
	// -d allows it for debuggable builds such as the companion's.
	installOld := func() {
		t.Helper()
		f, err := os.Open(oldAPK)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		var b [6]byte
		rand.Read(b[:])
		remote := "/data/local/tmp/pinky-companion-old-" + hex.EncodeToString(b[:]) + ".apk"
		if err := d.client.Push(ctx, serial, f, remote, 0o644, time.Now()); err != nil {
			t.Fatal(err)
		}
		if out := shell("pm install -r -d -t " + remote + "; rm -f " + remote); !strings.Contains(out, "Success") {
			t.Fatalf("install the old build: %s", out)
		}
		if got := installed(); got != oldV.Code {
			t.Fatalf("installed %d, want the old build %d", got, oldV.Code)
		}
	}

	orig := setting()
	t.Logf("before: build %d, enabled_accessibility_services=%q", installed(), orig)
	defer func() {
		// Leave the bundled build with the setting as found.
		rctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if v, _, _ := c.InstalledVersion(rctx); v.Code != newV.Code {
			if err := c.Install(rctx, newAPK); err != nil {
				t.Errorf("restore the bundled build: %v", err)
			}
		}
		cmd := "settings put secure enabled_accessibility_services '" + strings.ReplaceAll(orig, "'", `'\''`) + "'"
		if orig == "null" || orig == "" {
			cmd = "settings delete secure enabled_accessibility_services"
		}
		if _, err := run.Shell(rctx, cmd); err != nil {
			t.Errorf("restore the setting: %v", err)
		}
	}()
	t.Setenv("PINKY_COMPANION_APK", newAPK)

	// 1. Off stays off.
	installOld()
	if listed() {
		if err := c.ForceDisable(ctx); err != nil {
			t.Fatal(err)
		}
	}
	before := setting()
	if st, err := d.CompanionState(ctx); err != nil || st != driver.CompanionOutdated {
		t.Fatalf("CompanionState = %v, %v; want outdated", st, err)
	}
	res, err := d.UpdateCompanion(ctx)
	if err != nil {
		t.Fatalf("UpdateCompanion (service off): %v", err)
	}
	if !res.Updated || res.ServiceOn || res.To != versionLine(newV) || res.From != versionLine(oldV) {
		t.Errorf("UpdateCompanion (service off) = %+v", res)
	}
	if got := installed(); got != newV.Code {
		t.Errorf("installed %d after the update", got)
	}
	if after := setting(); after != before {
		t.Errorf("the update changed the setting: %q -> %q", before, after)
	}
	if res, err := d.UpdateCompanion(ctx); err != nil || res.Updated {
		t.Errorf("a second UpdateCompanion = %+v, %v; want nothing to do", res, err)
	}

	// 2. What Android does with an enabled service across pm install -r.
	installOld()
	if err := c.TurnOn(ctx); err != nil {
		t.Fatalf("turn the service on: %v", err)
	}
	if err := c.Install(ctx, newAPK); err != nil {
		t.Fatal(err)
	}
	kept := listed()
	answers := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && kept; time.Sleep(500 * time.Millisecond) {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, err := c.Ping(pctx)
		cancel()
		if err == nil {
			answers = true
			break
		}
	}
	t.Logf("after pm install -r over an enabled service: still listed %v, answers %v", kept, answers)

	// 3. On stays on.
	installOld()
	if !listed() {
		if err := c.TurnOn(ctx); err != nil {
			t.Fatal(err)
		}
	}
	res, err = d.UpdateCompanion(ctx)
	if err != nil {
		t.Fatalf("UpdateCompanion (service on): %v", err)
	}
	if !res.Updated || !res.ServiceOn {
		t.Errorf("UpdateCompanion (service on) = %+v", res)
	}
	if !listed() {
		t.Errorf("the service is off after the update: %q", setting())
	}
	if st, err := d.CompanionState(ctx); err != nil || st != driver.CompanionOn {
		t.Errorf("CompanionState after the update = %v, %v; want on", st, err)
	}
}
