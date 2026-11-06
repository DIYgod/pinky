package android

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

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

// CompanionInstall says what InstallCompanion did.
type CompanionInstall struct {
	APK       string // the file installed or compared
	Installed bool   // false when the phone already had exactly this APK
}

// InstallCompanion installs or updates the companion app from the APK that
// FindCompanionAPK finds, unless the phone already has exactly that file.
// It is for setup, outside tasks: the driver never installs anything by
// itself. On phones whose Google Play Protect checks adb installs, the
// phone asks the person to allow an app it does not know, and the install
// waits for the answer.
func (d *Device) InstallCompanion(ctx context.Context) (CompanionInstall, error) {
	apk, err := FindCompanionAPK()
	if err != nil {
		return CompanionInstall{}, err
	}
	c := companion.New(adbRunner{c: d.client, serial: d.info.ID})
	defer c.Close()
	installed, err := c.InstallIfChanged(ctx, apk)
	if err != nil {
		return CompanionInstall{APK: apk}, err
	}
	// Let the next call look at the companion afresh.
	d.comp.mu.Lock()
	d.comp.checked = false
	d.comp.retryAt = time.Time{}
	d.comp.mu.Unlock()
	return CompanionInstall{APK: apk, Installed: installed}, nil
}
