// Package harmony discovers native HarmonyOS phones through the SDK's hdc.
// Android-based HarmonyOS phones continue to use the Android provider.
package harmony

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// findHDC does not download software or change the SDK/server configuration.
// PINKY_HDC also lets an app launched outside a terminal find an SDK tool.
func findHDC() (string, error) {
	if path := os.Getenv("PINKY_HDC"); path != "" {
		return exec.LookPath(path)
	}
	if path, err := exec.LookPath("hdc"); err == nil {
		return path, nil
	}
	name := "hdc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if exe, err := os.Executable(); err == nil {
		if path, err := exec.LookPath(filepath.Join(filepath.Dir(exe), "..", "Helpers", name)); err == nil {
			return path, nil
		}
	}
	for _, key := range []string{"HDC_SDK_PATH", "HDC_HOME", "OHOS_SDK_HOME", "HOS_SDK_HOME"} {
		base := os.Getenv(key)
		if base == "" {
			continue
		}
		for _, sub := range []string{"", "toolchains", "default/openharmony/toolchains", "default/HarmonyOS/toolchains"} {
			if path, err := exec.LookPath(filepath.Join(base, filepath.FromSlash(sub), name)); err == nil {
				return path, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

func findFFmpeg() (string, error) {
	if path := os.Getenv("PINKY_FFMPEG"); path != "" {
		return exec.LookPath(path)
	}
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path, nil
	}
	if runtime.GOOS == "darwin" {
		for _, candidate := range []string{"/opt/homebrew/bin/ffmpeg", "/usr/local/bin/ffmpeg"} {
			if path, err := exec.LookPath(candidate); err == nil {
				return path, nil
			}
		}
	}
	return "", exec.ErrNotFound
}

// runHDC bounds commands even when a discovery caller has no deadline. It
// never kills or restarts an existing server shared with other SDK tools.
func runHDC(ctx context.Context, path string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.WaitDelay = time.Second
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	// hdc can print a failure and still exit successfully. Do not mistake
	// that text for a device id.
	failed := func(b []byte) bool {
		return bytes.Contains(b, []byte("[Fail]")) || bytes.Contains(b, []byte("[Error]"))
	}
	if err != nil || failed(out) || failed(stderr.Bytes()) {
		if err == nil {
			err = errors.New("hdc rejected the command")
		}
		return nil, fmt.Errorf("hdc: %w: %s", err, strings.TrimSpace(string(out)+stderr.String()))
	}
	return out, nil
}

// shellCommand quotes arguments for the device shell. HDC joins shell
// arguments; host exec argument separation alone does not protect them.
func shellCommand(args ...string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = "'" + strings.ReplaceAll(arg, "'", "'\\''") + "'"
	}
	return strings.Join(quoted, " ")
}
