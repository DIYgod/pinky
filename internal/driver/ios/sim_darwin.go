package ios

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// simArch is the CPU Simulators run on: arm64 on Apple silicon, even for a
// Pinky that runs under Rosetta.
func simArch() string {
	if v, err := unix.SysctlUint32("hw.optional.arm64"); err == nil && v == 1 {
		return "arm64"
	}
	return "x86_64"
}

// ownGroup starts cmd in a process group of its own, so signals sent to
// Pinky's group, such as a terminal's Ctrl-C, do not reach it.
func ownGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
