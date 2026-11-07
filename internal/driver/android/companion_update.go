package android

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
)

// UpdateCompanion implements driver.CompanionUpdater: it installs the
// bundled companion APK over an older installed build, as `pm install -r`
// does, without asking anyone, and keeps the app's accessibility service
// as it was. Android keeps enabled_accessibility_services across an update
// of the same package; when the service was on and is not listed or does
// not answer afterwards, it is turned on again through TurnOn, the path
// SetUpCompanion uses. A service that was off, or on only for the driver's
// own reads, is left to the driver. It refuses with driver.ErrNeedsPerson
// when the phone would show a dialog (Google Play Protect checks adb
// installs) or the installed app is signed with another key, and does
// nothing while the driver has the service on for a task's reads.
func (d *Device) UpdateCompanion(ctx context.Context) (driver.CompanionUpdate, error) {
	var res driver.CompanionUpdate
	c := companion.New(adbRunner{c: d.client, serial: d.info.ID})
	defer c.Close()
	before, installed, err := c.InstalledVersion(ctx)
	if err != nil {
		return res, wrapErr(err)
	}
	if !installed {
		return res, fmt.Errorf("the companion app is not installed: %w", driver.ErrUnsupported)
	}
	apk, err := FindCompanionAPK()
	if err != nil {
		return res, fmt.Errorf("%w: %w", err, driver.ErrUnsupported)
	}
	bundled, err := apkVersion(apk)
	if err != nil {
		return res, err
	}
	res.From, res.To = versionLine(before), versionLine(bundled)
	if before.Code <= 0 || before.Code >= bundled.Code {
		res.To = res.From
		res.ServiceOn, _ = c.Enabled(ctx)
		return res, nil
	}
	if d.companionForTask() {
		return res, errCompanionBusy
	}
	if asks, why := installAsks(ctx, adbRunner{c: d.client, serial: d.info.ID}); asks {
		return res, fmt.Errorf("%s: %w", why, driver.ErrNeedsPerson)
	}
	wasOn, err := c.Enabled(ctx)
	if err != nil {
		return res, wrapErr(err)
	}
	// The app's process goes away during the install: close the driver's
	// link to it first, so its reads fall back cleanly, and look at the
	// app afresh afterwards.
	d.companionOff("updating the app")
	if err := c.Install(ctx, apk); err != nil {
		if strings.Contains(err.Error(), "INSTALL_FAILED_UPDATE_INCOMPATIBLE") {
			return res, fmt.Errorf("the installed companion is signed with another key and has to be replaced: %w", driver.ErrNeedsPerson)
		}
		return res, wrapErr(err)
	}
	res.Updated = true
	d.cfg.logf("android: %s: updated the companion %s -> %s (service on before: %v)", d.info.ID, res.From, res.To, wasOn)
	k := &d.comp
	k.mu.Lock()
	k.checked = false
	k.retryAt = time.Time{}
	k.mu.Unlock()
	if !wasOn {
		return res, nil
	}
	// It was on: it should still be listed and answer once Android has
	// bound the new build. TurnOn adds Pinky's entry only when it is
	// missing and waits until the app answers.
	if err := c.TurnOn(ctx); err != nil {
		return res, fmt.Errorf("updated the companion, but its service did not come back on: %w", wrapErr(err))
	}
	res.ServiceOn = true
	return res, nil
}

// versionLine is a companion version as people read it: "0.1.0 (75)".
func versionLine(v companion.Version) string {
	switch {
	case v.Name != "" && v.Code > 0:
		return fmt.Sprintf("%s (%d)", v.Name, v.Code)
	case v.Code > 0:
		return fmt.Sprintf("build %d", v.Code)
	}
	return v.Name
}

// installAsks reports whether an install over adb may show the person a
// dialog on the phone: Google Play Protect checks such installs where
// Google Play services are installed, unless verifier_verify_adb_installs
// or package_verifier_enable is 0 (unset means on). Reading the settings
// changes nothing.
func installAsks(ctx context.Context, run companion.Runner) (bool, string) {
	out, err := run.Shell(ctx, "if pm path com.google.android.gms >/dev/null 2>&1; then echo gms=1; else echo gms=0; fi; "+
		"echo adb=$(settings get global verifier_verify_adb_installs); echo verifier=$(settings get global package_verifier_enable)")
	if err != nil {
		return true, "could not read the phone's install checks"
	}
	v := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, val, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			v[k] = strings.TrimSpace(val)
		}
	}
	if v["gms"] != "1" || v["adb"] == "0" || v["verifier"] == "0" {
		return false, ""
	}
	return true, "Google Play Protect checks installs over USB on this phone"
}

// errCompanionBusy: the driver has the service on for a task's reads, which
// end within a minute of the task; the update waits for that.
var errCompanionBusy = errors.New("android: the driver is using the companion app; try again in a minute")

var _ driver.CompanionUpdater = (*Device)(nil)
