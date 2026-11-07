package telemetry

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/DIYgod/pinky/internal/driver"
)

// The logs diagnostics end with, in the data folder, and how much of each.
var diagnosticLogs = []string{"pinky.log", "daemon.log"}

const (
	logTailBytes = 96 << 10
	logTailLines = 300
)

// Diagnostics is the text the person may attach to feedback: Pinky's
// version and system, the phones' platforms, models and systems, extra
// lines from the caller (settings, video), and the end of Pinky's logs.
// Phone ids and names, the home folder, the user name, e-mail and IP
// addresses, and log fields that may name apps, tasks or files are taken
// out (Redactor). The person reads it before sending.
func (c *Client) Diagnostics(phones []driver.Info, extra ...string) string {
	red := NewRedactor()
	for _, p := range phones {
		red.Phone(p.ID, p.Name, p.Model)
	}
	var logs []string
	var names []string
	if c != nil && c.opt.Dir != "" {
		for _, name := range diagnosticLogs {
			tail, err := readTail(filepath.Join(c.opt.Dir, name), logTailBytes, logTailLines)
			if err != nil || strings.TrimSpace(tail) == "" {
				continue
			}
			red.Learn(tail)
			logs = append(logs, tail)
			names = append(names, name)
		}
	}

	var b strings.Builder
	sys := System()
	version := DevVersion
	if c != nil && c.opt.Version != "" {
		version = c.opt.Version
	}
	fmt.Fprintf(&b, "Pinky %s · %s %s · %s · %s\n", version, OSName(sys.OS), sys.OSVersion, sys.Arch, sys.GoVersion)
	if c != nil {
		stats := "off"
		switch {
		case DoNotTrack():
			stats = "off (DO_NOT_TRACK)"
		case c.Usage():
			stats = "on"
		}
		crashes := "ask first"
		if c.AutoCrash() {
			crashes = "send automatically"
		}
		fmt.Fprintf(&b, "Usage statistics: %s · Crash reports: %s · Waiting crash reports: %d\n", stats, crashes, len(c.Crashes()))
	}
	for _, line := range extra {
		if line = strings.TrimSpace(line); line != "" {
			b.WriteString(red.String(line))
			b.WriteByte('\n')
		}
	}
	fmt.Fprintf(&b, "\nPhones (%d):\n", len(phones))
	if len(phones) == 0 {
		b.WriteString("  none connected\n")
	}
	for _, p := range phones {
		parts := []string{strings.TrimSpace(string(p.Platform) + " " + p.OSVersion)}
		for _, s := range []string{p.Model, p.Connection, p.Backend, string(p.State)} {
			if s = strings.TrimSpace(s); s != "" {
				parts = append(parts, s)
			}
		}
		b.WriteString("  " + red.String(strings.Join(parts, " · ")) + "\n")
	}
	for i, tail := range logs {
		fmt.Fprintf(&b, "\n%s, the last lines:\n", names[i])
		b.WriteString(red.String(strings.TrimRight(tail, "\n")))
		b.WriteByte('\n')
	}
	out := b.String()
	if len(out) > maxDiagnostics {
		out = "… (cut)\n" + out[len(out)-maxDiagnostics+10:]
	}
	return out
}

// readTail returns the last lines of a file, at most max bytes and lines,
// starting at a line.
func readTail(path string, max int64, lines int) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := int64(0)
	if info.Size() > max {
		start = info.Size() - max
	}
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", err
	}
	data, err := io.ReadAll(io.LimitReader(f, max))
	if err != nil {
		return "", err
	}
	if start > 0 {
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		}
	}
	all := strings.Split(strings.TrimRight(strings.ToValidUTF8(string(data), "?"), "\n"), "\n")
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return strings.Join(all, "\n"), nil
}
