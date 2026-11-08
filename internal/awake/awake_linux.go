package awake

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// On Linux, Hold runs systemd-inhibit, which holds a logind lock against
// idle handling and sleep while its command runs: the command is cat on a
// pipe from this process, so the lock ends when release closes the pipe,
// or when this process exits, however it exits. systemd-inhibit --list
// shows it, by Pinky, with the reason. logind may refuse the lock to a
// process outside a login session; Hold then returns its error.

// inhibitCheck is how long Hold waits for systemd-inhibit to fail, as it
// does at once when logind refuses the lock.
const inhibitCheck = 500 * time.Millisecond

func hold(reason string) (func(), error) {
	path, err := exec.LookPath("systemd-inhibit")
	if err != nil {
		return nil, fmt.Errorf("awake: no systemd-inhibit: %w", ErrUnsupported)
	}
	cmd := exec.Command(path, "--what=idle:sleep", "--who=Pinky", "--why="+reason, "--mode=block", "cat")
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("awake: %w", err)
	}
	var stderr lockedBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("awake: systemd-inhibit: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		in.Close()
		msg := strings.TrimSpace(stderr.String())
		if msg == "" && err != nil {
			msg = err.Error()
		}
		return nil, fmt.Errorf("awake: systemd-inhibit: %s", msg)
	case <-time.After(inhibitCheck):
	}
	return func() {
		in.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}, nil
}

// lockedBuffer keeps the first KiB a process writes, safe for the
// goroutine that copies it and a reader.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if room := 1024 - b.buf.Len(); room > 0 {
		b.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
