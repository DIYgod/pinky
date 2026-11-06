package companion

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Runner runs adb commands against one device. ExecRunner shells out to the
// adb binary; the Android driver can supply its own implementation (for
// example over the adb server protocol) when it integrates the companion.
type Runner interface {
	// Shell runs cmd with the device's sh and returns its standard output.
	// A non-zero exit status is an error.
	Shell(ctx context.Context, cmd string) (string, error)
	// Forward forwards a free local TCP port to a device socket such as
	// "localabstract:pinky_companion" and returns the local address.
	Forward(ctx context.Context, remote string) (string, error)
	// RemoveForward removes a forward that Forward created.
	RemoveForward(ctx context.Context, local string) error
	// Install installs or replaces an APK, like `adb install -r`.
	Install(ctx context.Context, apk string) error
}

// ExecRunner implements Runner with the adb command-line tool.
type ExecRunner struct {
	ADB    string // path to adb; empty means FindADB()
	Serial string // the device's adb serial
}

func (r *ExecRunner) adb(ctx context.Context, args ...string) ([]byte, error) {
	bin := r.ADB
	if bin == "" {
		bin = FindADB()
	}
	full := append([]string{"-s", r.Serial}, args...)
	cmd := exec.CommandContext(ctx, bin, full...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = strings.TrimSpace(string(out))
		}
		return out, fmt.Errorf("adb %s: %w: %s", strings.Join(args, " "), err, msg)
	}
	return out, nil
}

// Shell implements Runner.
func (r *ExecRunner) Shell(ctx context.Context, cmd string) (string, error) {
	out, err := r.adb(ctx, "shell", cmd)
	return string(out), err
}

// Forward implements Runner.
func (r *ExecRunner) Forward(ctx context.Context, remote string) (string, error) {
	out, err := r.adb(ctx, "forward", "tcp:0", remote)
	if err != nil {
		return "", err
	}
	port := strings.TrimSpace(string(out))
	if _, err := strconv.Atoi(port); err != nil {
		return "", fmt.Errorf("adb forward: unexpected output %q", port)
	}
	return "127.0.0.1:" + port, nil
}

// RemoveForward implements Runner.
func (r *ExecRunner) RemoveForward(ctx context.Context, local string) error {
	_, port, ok := strings.Cut(local, ":")
	if !ok {
		return fmt.Errorf("adb forward: bad local address %q", local)
	}
	_, err := r.adb(ctx, "forward", "--remove", "tcp:"+port)
	return err
}

// Install implements Runner.
func (r *ExecRunner) Install(ctx context.Context, apk string) error {
	out, err := r.adb(ctx, "install", "-r", apk)
	if err != nil {
		return err
	}
	if !strings.Contains(string(out), "Success") {
		return fmt.Errorf("adb install: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// FindADB returns the adb to use: $PINKY_ADB, then the adb of an installed
// Android SDK (so Pinky shares Android Studio's adb server), then adb on
// PATH.
func FindADB() string {
	if p := os.Getenv("PINKY_ADB"); p != "" {
		return p
	}
	name := "adb"
	if runtime.GOOS == "windows" {
		name = "adb.exe"
	}
	var sdks []string
	for _, env := range []string{"ANDROID_HOME", "ANDROID_SDK_ROOT"} {
		if v := os.Getenv(env); v != "" {
			sdks = append(sdks, v)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		switch runtime.GOOS {
		case "darwin":
			sdks = append(sdks, filepath.Join(home, "Library", "Android", "sdk"))
		case "linux":
			sdks = append(sdks, filepath.Join(home, "Android", "Sdk"))
		case "windows":
			if v := os.Getenv("LOCALAPPDATA"); v != "" {
				sdks = append(sdks, filepath.Join(v, "Android", "Sdk"))
			}
		}
	}
	for _, sdk := range sdks {
		p := filepath.Join(sdk, "platform-tools", name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// shellQuote quotes s for the device's sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
