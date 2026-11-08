//go:build darwin

package ui

import (
	"os/exec"
	"strings"
	"testing"
)

// TestKeepMacAwake checks with pmset that the Mac is kept from idle sleep
// while an agent task runs, and only then.
func TestKeepMacAwake(t *testing.T) {
	if _, err := exec.LookPath("pmset"); err != nil {
		t.Skip("no pmset")
	}
	assertions := func() string {
		out, err := exec.Command("pmset", "-g", "assertions").Output()
		if err != nil {
			t.Skip("pmset -g assertions:", err)
		}
		return string(out)
	}
	if strings.Contains(assertions(), awakeReason) {
		t.Skip("another test process holds the assertion")
	}
	keepMacAwake(true)
	held := assertions()
	keepMacAwake(false)
	if !strings.Contains(held, awakeReason) {
		t.Fatalf("no %q among the assertions:\n%s", awakeReason, held)
	}
	for _, line := range strings.Split(held, "\n") {
		if strings.Contains(line, awakeReason) && !strings.Contains(line, "PreventUserIdleSystemSleep") {
			t.Errorf("the assertion does not prevent idle sleep: %s", line)
		}
		if strings.Contains(line, awakeReason) && strings.Contains(line, "PreventUserIdleDisplaySleep") {
			t.Errorf("the assertion keeps the display on: %s", line)
		}
	}
	if strings.Contains(assertions(), awakeReason) {
		t.Error("the assertion stays after the tasks end")
	}
}

// TestKeepMacAwakeAway checks with pmset that the Mac is kept from idle
// sleep while a phone is connected through Tailscale, and only then.
func TestKeepMacAwakeAway(t *testing.T) {
	if _, err := exec.LookPath("pmset"); err != nil {
		t.Skip("no pmset")
	}
	assertions := func() string {
		out, err := exec.Command("pmset", "-g", "assertions").Output()
		if err != nil {
			t.Skip("pmset -g assertions:", err)
		}
		return string(out)
	}
	if strings.Contains(assertions(), awayReason) {
		t.Skip("another test process holds the assertion")
	}
	keepMacAwakeAway(true)
	held := assertions()
	keepMacAwakeAway(false)
	if !strings.Contains(held, awayReason) {
		t.Fatalf("no %q among the assertions:\n%s", awayReason, held)
	}
	for _, line := range strings.Split(held, "\n") {
		if strings.Contains(line, awayReason) && !strings.Contains(line, "PreventUserIdleSystemSleep") {
			t.Errorf("the assertion does not prevent idle sleep: %s", line)
		}
		if strings.Contains(line, awayReason) && strings.Contains(line, "PreventUserIdleDisplaySleep") {
			t.Errorf("the assertion keeps the display on: %s", line)
		}
	}
	if strings.Contains(assertions(), awayReason) {
		t.Error("the assertion stays after the phone is back")
	}
}
