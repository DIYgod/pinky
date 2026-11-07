package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/cmd/pinky/locales"
)

// agentProgram is the program that agents on this computer run to reach
// this Pinky: its own executable, by its full path. No other program can
// stand in for a path, as one named pinky can on the PATH (coreutils has a
// pinky command, and Homebrew's coreutils links it), and agents started
// from the Dock or the Start menu find it without the terminal's PATH,
// where the cask's pinkyctl and the Linux packages' are.
func agentProgram() string {
	exe, err := os.Executable()
	if err != nil {
		return "pinkyctl"
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	// macOS runs an app opened from a disk image or the Downloads folder
	// from a copy of its own, gone once it quits; the app belongs in
	// Applications.
	if runtime.GOOS == "darwin" && strings.Contains(exe, "/AppTranslocation/") {
		exe = "/Applications/Pinky.app/Contents/MacOS/" + filepath.Base(exe)
	}
	return exe
}

// shellWord quotes s for the shell people paste a command into, when it
// needs it: in single quotes on macOS and Linux, in double quotes on
// Windows.
func shellWord(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'$`\\!*?[]{}()<>|&;#~=%,") {
		return s
	}
	if runtime.GOOS == "windows" {
		return `"` + s + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// agentCommand is the command line that starts Pinky's MCP server over
// stdio for an agent on this computer.
func agentCommand() string { return shellWord(agentProgram()) + " mcp" }

// connectAgents is the Add an agent part of Settings › Agents: the command
// that adds Pinky to Claude Code, and the one other agents run.
func (a *app) connectAgents(c *mg.Context) {
	p := pal(c)
	cmd := agentCommand()
	mg.Column(c).Gap(8).Label(locales.ConnectTitle()).Children(func() {
		mg.Column(c).Children(func() {
			mg.Text(c, locales.ConnectTitle()).FontSize(15).FontWeight(650)
			mg.Text(c, locales.ConnectIntro()).
				FontSize(13.5).TextColor(p.ink2).Margin(2, 0, 0, 0)
		})
		a.commandBlock(c, "Claude Code", locales.ConnectClaudeHint(), "claude mcp add pinky -- "+cmd,
			locales.RemoteCommandLabel(), locales.ConnectCopyClaude())
		a.commandBlock(c, locales.ConnectOtherTitle(), locales.ConnectOtherHint(), cmd,
			locales.ConnectCommandLabel(), locales.ConnectCopyOther())
	})
}

// commandBlock shows a command under a title and a hint, with a button
// that copies it; label names the command and copyLabel the button.
func (a *app) commandBlock(c *mg.Context, title, hint, cmd, label, copyLabel string) {
	p := pal(c)
	mg.Column(c).Gap(6).Padding(10, 0).Children(func() {
		mg.Row(c).Gap(12).AlignItems(mg.Center).Children(func() {
			mg.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				mg.Text(c, title).FontWeight(600)
				mg.Text(c, hint).FontSize(13.5).TextColor(p.ink2)
			})
			if button(c, "", btnOpt{small: true, icon: icCopy, label: copyLabel}).Clicked() {
				a.copy(c, cmd, locales.RemoteCopiedCommand())
			}
		})
		mg.Text(c, cmd).Font("monospace").FontSize(12).Padding(10, 12).Radius(8).Background(p.bg2).Border(1, p.line).
			Selectable().Label(label)
	})
}
