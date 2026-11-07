package companion

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Install installs or updates the companion app from an APK file, like
// `adb install -r`. Pinky may install and remove only its own apps.
//
// On phones with Google Play Protect checking adb installs, an install of
// an app Play does not know shows a "Send app for a security check?"
// dialog on the phone and waits for the person; prefer InstallIfChanged.
func (c *Client) Install(ctx context.Context, apk string) error {
	return c.run.Install(ctx, apk)
}

// InstallIfChanged installs the APK unless the phone already has exactly
// this file, compared by SHA-256. It reports whether it installed.
func (c *Client) InstallIfChanged(ctx context.Context, apk string) (bool, error) {
	local, err := fileSHA256(apk)
	if err != nil {
		return false, err
	}
	if remote, err := c.InstalledSHA256(ctx); err == nil && remote == local {
		return false, nil
	}
	if err := c.Install(ctx, apk); err != nil {
		return false, err
	}
	return true, nil
}

// Uninstall removes the companion app, Pinky's own package and nothing
// else. Call ForceDisable first: when an uninstalled package's service is
// listed in enabled_accessibility_services, Android rewrites the setting,
// and the person's other entries with it.
func (c *Client) Uninstall(ctx context.Context) error {
	out, err := c.run.Shell(ctx, "pm uninstall "+Package)
	if err != nil {
		return err
	}
	if !strings.Contains(out, "Success") {
		return fmt.Errorf("pm uninstall %s: %s", Package, strings.TrimSpace(out))
	}
	return nil
}

var (
	versionNameRE = regexp.MustCompile(`(?m)^\s*versionName=(\S+)`)
	versionCodeRE = regexp.MustCompile(`(?m)\bversionCode=(\d+)`)
)

// Installed returns the installed companion's version name, or ok=false
// when the app is not installed.
func (c *Client) Installed(ctx context.Context) (version string, ok bool, err error) {
	v, ok, err := c.InstalledVersion(ctx)
	return v.Name, ok, err
}

// Version is an app version: the name people read and the code Android
// compares.
type Version struct {
	Name string
	Code int
}

// InstalledVersion returns the installed companion's version, and false
// when the app is not installed.
func (c *Client) InstalledVersion(ctx context.Context) (Version, bool, error) {
	out, err := c.run.Shell(ctx, "dumpsys package "+Package)
	if err != nil {
		return Version{}, false, err
	}
	v, ok := parsePackageVersion(out)
	return v, ok, nil
}

// parsePackageVersion reads `dumpsys package <pkg>` ("versionCode=68
// minSdk=29 targetSdk=36", "versionName=0.1.0"); the first package entry
// wins, which is the installed one even while an update is staged. A
// missing package prints neither line.
func parsePackageVersion(out string) (Version, bool) {
	var v Version
	found := false
	if m := versionCodeRE.FindStringSubmatch(out); m != nil {
		v.Code, _ = strconv.Atoi(m[1])
		found = true
	}
	if m := versionNameRE.FindStringSubmatch(out); m != nil {
		v.Name = m[1]
		found = true
	}
	return v, found
}

// versionAsset is the file the build writes into the APK with its version,
// so it can be read without the Android SDK.
const versionAsset = "assets/pinky-companion.json"

// ErrNoVersion means an APK carries no version file: a build from before
// the version file was added (2026-11-07).
var ErrNoVersion = errors.New("the APK has no " + versionAsset)

// APKVersion reads the version a companion APK carries in
// assets/pinky-companion.json, written by the same build as the manifest.
func APKVersion(apk string) (Version, error) {
	z, err := zip.OpenReader(apk)
	if err != nil {
		return Version{}, fmt.Errorf("read %s: %w", apk, err)
	}
	defer z.Close()
	for _, f := range z.File {
		if f.Name != versionAsset {
			continue
		}
		r, err := f.Open()
		if err != nil {
			return Version{}, err
		}
		defer r.Close()
		var v struct {
			Package     string `json:"package"`
			VersionName string `json:"version_name"`
			VersionCode int    `json:"version_code"`
		}
		if err := json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&v); err != nil {
			return Version{}, fmt.Errorf("%s in %s: %w", versionAsset, apk, err)
		}
		if v.Package != Package || v.VersionCode <= 0 {
			return Version{}, fmt.Errorf("%s in %s names %q version %d, not Pinky's companion", versionAsset, apk, v.Package, v.VersionCode)
		}
		return Version{Name: v.VersionName, Code: v.VersionCode}, nil
	}
	return Version{}, fmt.Errorf("%s: %w", apk, ErrNoVersion)
}

// InstalledSHA256 hashes the installed base APK on the phone.
func (c *Client) InstalledSHA256(ctx context.Context) (string, error) {
	out, err := c.run.Shell(ctx, "pm path "+Package)
	if err != nil {
		return "", err
	}
	var path string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(strings.TrimSpace(line), "package:"); ok && strings.HasSuffix(p, "/base.apk") {
			path = p
		}
	}
	if path == "" {
		return "", fmt.Errorf("%s is not installed", Package)
	}
	out, err = c.run.Shell(ctx, "sha256sum "+shellQuote(path))
	if err != nil {
		return "", err
	}
	sum, _, _ := strings.Cut(strings.TrimSpace(out), " ")
	if len(sum) != 64 {
		return "", fmt.Errorf("sha256sum: unexpected output %q", out)
	}
	return strings.ToLower(sum), nil
}

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
