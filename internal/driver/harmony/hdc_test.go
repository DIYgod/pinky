package harmony

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestExplicitHDCTakesPrecedence(t *testing.T) {
	name := "hdc"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("test executable"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PINKY_HDC", path)
	got, err := findHDC()
	if err != nil || got != path {
		t.Fatalf("findHDC = %q, %v", got, err)
	}
	t.Setenv("PINKY_HDC", filepath.Join(t.TempDir(), name))
	if _, err := findHDC(); err == nil {
		t.Fatal("explicit missing tool ignored")
	}
}

func TestRunHDCRejectsZeroExitFailure(t *testing.T) {
	t.Setenv("PINKY_HDC_TEST_HELPER", "1")
	for _, mode := range []string{"stdout", "stderr", "success"} {
		out, err := runHDC(context.Background(), os.Args[0], "-test.run=^TestHDCProcess$", "--", mode)
		if mode == "success" {
			if err != nil || strings.TrimSpace(string(out)) != "test-phone USB Unauthorized localhost" {
				t.Fatalf("success = %q, %v", out, err)
			}
		} else if err == nil {
			t.Errorf("%s: zero-exit failure accepted", mode)
		}
	}
}

func TestHDCProcess(t *testing.T) {
	if os.Getenv("PINKY_HDC_TEST_HELPER") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "stdout":
		fmt.Fprintln(os.Stdout, "[Fail] server unavailable")
	case "stderr":
		fmt.Fprintln(os.Stderr, "[Error] server unavailable")
	case "success":
		fmt.Fprintln(os.Stdout, "test-phone USB Unauthorized localhost")
	default:
		os.Exit(2)
	}
	os.Exit(0)
}
