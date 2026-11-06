package companion

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"regexp"
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
	if remote, err := c.installedSHA256(ctx); err == nil && remote == local {
		return false, nil
	}
	if err := c.Install(ctx, apk); err != nil {
		return false, err
	}
	return true, nil
}

var versionNameRE = regexp.MustCompile(`(?m)^\s*versionName=(\S+)`)

// Installed returns the installed companion's version name, or ok=false
// when the app is not installed.
func (c *Client) Installed(ctx context.Context) (version string, ok bool, err error) {
	out, err := c.run.Shell(ctx, "dumpsys package "+Package)
	if err != nil {
		return "", false, err
	}
	m := versionNameRE.FindStringSubmatch(out)
	if m == nil {
		return "", false, nil
	}
	return m[1], true, nil
}

// installedSHA256 hashes the installed base APK on the phone.
func (c *Client) installedSHA256(ctx context.Context) (string, error) {
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
