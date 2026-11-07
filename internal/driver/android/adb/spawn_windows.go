package adb

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// serverCommand is the server's command, detached from any console as
// adb's own launch_server starts it: the pipe's handle is inherited, and
// its number goes on the command line.
func serverCommand(path, listen string, reply *os.File) (*exec.Cmd, error) {
	h := windows.Handle(reply.Fd())
	if err := windows.SetHandleInformation(h, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		return nil, err
	}
	cmd := exec.Command(path, "-L", listen, "fork-server", "server", "--reply-fd", strconv.FormatUint(uint64(h), 10))
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags:              windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
		AdditionalInheritedHandles: []syscall.Handle{syscall.Handle(h)},
	}
	return cmd, nil
}

// captureStderr: on Windows the server may keep the standard error it was
// given open for good, so it gets none.
const captureStderr = false

// hideWindow keeps a console program (adb.exe) that a windowed Pinky runs
// from flashing a console window.
func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW, HideWindow: true}
}
