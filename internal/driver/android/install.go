package android

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/DIYgod/pinky/internal/driver"
	"github.com/DIYgod/pinky/internal/driver/android/adb"
	"github.com/DIYgod/pinky/internal/driver/android/companion"
)

// adbRunner lets the companion client use the driver's adb client instead
// of the adb command-line tool.
type adbRunner struct {
	c      *adb.Client
	serial string
}

// Shell implements companion.Runner; a non-zero exit status is an error.
func (r adbRunner) Shell(ctx context.Context, cmd string) (string, error) {
	res, err := r.c.Run(ctx, r.serial, cmd)
	if err != nil {
		return "", wrapErr(err)
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if msg == "" {
			msg = strings.TrimSpace(string(res.Stdout))
		}
		return string(res.Stdout), fmt.Errorf("adb shell %s: exit status %d: %s", firstWord(cmd), res.ExitCode, msg)
	}
	return string(res.Stdout), nil
}

// Forward implements companion.Runner.
func (r adbRunner) Forward(ctx context.Context, remote string) (string, error) {
	local, err := r.c.Forward(ctx, r.serial, "tcp:0", remote, false)
	if err != nil {
		return "", wrapErr(err)
	}
	return net.JoinHostPort("127.0.0.1", strings.TrimPrefix(local, "tcp:")), nil
}

// RemoveForward implements companion.Runner.
func (r adbRunner) RemoveForward(ctx context.Context, local string) error {
	_, port, err := net.SplitHostPort(local)
	if err != nil {
		return err
	}
	return wrapErr(r.c.RemoveForward(ctx, r.serial, "tcp:"+port))
}

// Install implements companion.Runner like `adb install -r`: it pushes the
// APK to a temporary file and installs it with the package manager.
func (r adbRunner) Install(ctx context.Context, apk string) error {
	f, err := os.Open(apk)
	if err != nil {
		return err
	}
	defer f.Close()
	var b [6]byte
	_, _ = rand.Read(b[:])
	remote := "/data/local/tmp/pinky-companion-" + hex.EncodeToString(b[:]) + ".apk"
	if err := r.c.Push(ctx, r.serial, f, remote, 0o644, time.Now()); err != nil {
		return wrapErr(err)
	}
	res, err := r.c.Run(ctx, r.serial, "pm install -r -t "+remote+"; status=$?; rm -f "+remote+"; exit $status")
	if err != nil {
		return wrapErr(err)
	}
	out := strings.TrimSpace(string(res.Stdout) + " " + string(res.Stderr))
	if res.ExitCode != 0 || !strings.Contains(out, "Success") {
		return fmt.Errorf("android: install %s: %s", filepath.Base(apk), out)
	}
	return nil
}

func firstWord(s string) string {
	if i := strings.IndexByte(s, ' '); i >= 0 {
		return s[:i]
	}
	return s
}

// errNoCompanionAPK means FindCompanionAPK looked everywhere and found none.
var errNoCompanionAPK = errors.New("android: no companion APK found")

// companionAPKCandidates lists where FindCompanionAPK looks, in order.
func companionAPKCandidates() []string {
	var c []string
	if p := os.Getenv("PINKY_COMPANION_APK"); p != "" {
		c = append(c, p)
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		c = append(c,
			filepath.Join(dir, "pinky-companion.apk"),
			// Pinky.app/Contents/MacOS/pinky -> Pinky.app/Contents/Resources
			filepath.Join(dir, "..", "Resources", "pinky-companion.apk"),
		)
	}
	if wd, err := os.Getwd(); err == nil {
		c = append(c, filepath.Join(wd, "android", "companion", "app", "build", "outputs", "apk", "debug", "app-debug.apk"))
	}
	return c
}

// FindCompanionAPK returns the companion APK to install: $PINKY_COMPANION_APK,
// then pinky-companion.apk next to the executable, then
// ../Resources/pinky-companion.apk relative to it (a macOS app bundle),
// then the debug build under the working directory
// (android/companion/app/build/outputs/apk/debug/app-debug.apk).
func FindCompanionAPK() (string, error) {
	cands := companionAPKCandidates()
	for _, p := range cands {
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
			return filepath.Clean(p), nil
		}
	}
	return "", fmt.Errorf("%w; looked in %s", errNoCompanionAPK, strings.Join(cands, ", "))
}

// CompanionInstall says what SetUpCompanion did.
type CompanionInstall struct {
	APK       string            // the bundled APK compared or installed
	Bundled   companion.Version // its version; zero when it has no version file
	Before    companion.Version // the installed version before; zero when it was missing
	Installed bool              // the APK was installed
	Replaced  bool              // an older build signed with another key was uninstalled first
}

// CompanionState implements driver.CompanionInstaller: missing (or no_apk
// when Pinky has no APK to install either), outdated when the installed
// versionCode is lower than the bundled APK's, on when the app's
// accessibility service is listed and answers, and off otherwise. While the
// driver has the service on only for its own reads, which it turns off
// again after a minute, the state is off: the person has not turned it on.
func (d *Device) CompanionState(ctx context.Context) (driver.CompanionState, error) {
	c := companion.New(adbRunner{c: d.client, serial: d.info.ID})
	defer c.Close()
	inst, installed, err := c.InstalledVersion(ctx)
	if err != nil {
		return "", wrapErr(err)
	}
	apk, apkErr := FindCompanionAPK()
	if !installed {
		if apkErr != nil {
			return driver.CompanionNoAPK, nil
		}
		return driver.CompanionMissing, nil
	}
	if apkErr == nil {
		if bundled, err := apkVersion(apk); err == nil && inst.Code > 0 && inst.Code < bundled.Code {
			return driver.CompanionOutdated, nil
		}
	}
	if d.companionForTask() {
		return driver.CompanionOff, nil
	}
	listed, err := c.Enabled(ctx)
	if err != nil {
		return "", wrapErr(err)
	}
	if !listed {
		return driver.CompanionOff, nil
	}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if _, err := c.Ping(pctx); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return driver.CompanionOff, nil
	}
	return driver.CompanionOn, nil
}

// InstallCompanion implements driver.CompanionInstaller with SetUpCompanion.
func (d *Device) InstallCompanion(ctx context.Context) error {
	_, err := d.SetUpCompanion(ctx, false)
	return err
}

// SetUpCompanion installs the bundled companion APK when the phone has none
// or an older build (or, with force, any build that differs from it), and
// turns its accessibility service on for good, keeping the person's other
// services. It is for setup only, when the person asks: the driver never
// installs anything by itself. A build from before round 3, signed with
// another key, cannot be updated in place; it is turned off, uninstalled
// (Pinky's own package only) and replaced. On phones whose Google Play
// Protect checks adb installs, the phone asks the person to allow the app,
// and the install waits for the answer, up to ctx.
func (d *Device) SetUpCompanion(ctx context.Context, force bool) (CompanionInstall, error) {
	var res CompanionInstall
	c := companion.New(adbRunner{c: d.client, serial: d.info.ID})
	defer c.Close()
	var installed bool
	var err error
	if res.Before, installed, err = c.InstalledVersion(ctx); err != nil {
		return res, wrapErr(err)
	}
	apk, apkErr := FindCompanionAPK()
	res.APK = apk
	if apkErr == nil {
		res.Bundled, _ = apkVersion(apk)
	}
	need := !installed || res.Bundled.Code > 0 && res.Before.Code < res.Bundled.Code
	if !need && force && apkErr == nil {
		local, err := fileSHA256(apk)
		if err != nil {
			return res, err
		}
		remote, err := c.InstalledSHA256(ctx)
		need = err != nil || remote != local
	}
	if need {
		if apkErr != nil {
			return res, fmt.Errorf("%w: %w", apkErr, driver.ErrUnsupported)
		}
		if err := d.installCompanionAPK(ctx, c, apk, &res); err != nil {
			return res, err
		}
	}
	if err := c.TurnOn(ctx); err != nil {
		return res, wrapErr(err)
	}
	// The driver's own link may have the service on for its reads; it must
	// leave it on now, and look at the app afresh.
	k := &d.comp
	k.mu.Lock()
	if k.client != nil {
		k.client.KeepEnabled()
	}
	k.checked = false
	k.retryAt = time.Time{}
	k.mu.Unlock()
	return res, nil
}

// installCompanionAPK installs the APK, replacing a build signed with
// another key.
func (d *Device) installCompanionAPK(ctx context.Context, c *companion.Client, apk string, res *CompanionInstall) error {
	err := c.Install(ctx, apk)
	if err != nil && strings.Contains(err.Error(), "INSTALL_FAILED_UPDATE_INCOMPATIBLE") {
		d.cfg.logf("android: %s: the installed companion is signed with another key; replacing it", d.info.ID)
		// Off first, so that Android does not rewrite the person's
		// accessibility setting when the package goes.
		d.companionOff("replacing the app")
		if err := c.ForceDisable(ctx); err != nil {
			return wrapErr(err)
		}
		if err := c.Uninstall(ctx); err != nil {
			return wrapErr(err)
		}
		res.Replaced = true
		err = c.Install(ctx, apk)
	}
	if err != nil {
		return wrapErr(err)
	}
	res.Installed = true
	d.cfg.logf("android: %s: installed the companion %s (%d)", d.info.ID, res.Bundled.Name, res.Bundled.Code)
	return nil
}

// companionForTask reports whether the driver has the service on for its
// own reads only.
func (d *Device) companionForTask() bool {
	k := &d.comp
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.on && k.client != nil && k.client.EnabledForTask()
}

// apkVersion reads a bundled APK's version, cached by path, size and time.
func apkVersion(apk string) (companion.Version, error) {
	fi, err := os.Stat(apk)
	if err != nil {
		return companion.Version{}, err
	}
	key := fmt.Sprintf("%s|%d|%d", apk, fi.Size(), fi.ModTime().UnixNano())
	apkVersionMu.Lock()
	defer apkVersionMu.Unlock()
	if v, ok := apkVersions[key]; ok {
		return v, nil
	}
	v, err := companion.APKVersion(apk)
	if err != nil {
		return v, err
	}
	apkVersions[key] = v
	return v, nil
}

var (
	apkVersionMu sync.Mutex
	apkVersions  = map[string]companion.Version{}
)

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var _ driver.CompanionInstaller = (*Device)(nil)
