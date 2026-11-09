//go:build !windows

package harmonyos

import "os/exec"

func hideConsole(*exec.Cmd) {}
