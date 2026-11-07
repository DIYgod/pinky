package ui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	mg "github.com/egoist/mygo/ui"

	"github.com/DIYgod/pinky/ui/fakeapi"
)

func TestShellWord(t *testing.T) {
	plain := "/Applications/Pinky.app/Contents/MacOS/Pinky"
	if got := shellWord(plain); got != plain {
		t.Errorf("shellWord(%q) = %q, want it as it is", plain, got)
	}
	cases := map[string]string{
		"/Users/me/My Apps/Pinky.app/Contents/MacOS/Pinky": `'/Users/me/My Apps/Pinky.app/Contents/MacOS/Pinky'`,
		"/tmp/it's/pinky": `'/tmp/it'\''s/pinky'`,
	}
	if runtime.GOOS == "windows" {
		cases = map[string]string{
			`C:\Users\Jane Doe\AppData\Local\Programs\Pinky\Pinky.exe`: `"C:\Users\Jane Doe\AppData\Local\Programs\Pinky\Pinky.exe"`,
		}
	}
	for in, want := range cases {
		if got := shellWord(in); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAgentCommand(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip(err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	cmd := agentCommand()
	if !strings.HasSuffix(cmd, " mcp") || !strings.Contains(cmd, filepath.Base(exe)) {
		t.Errorf("agentCommand() = %q, want this executable (%s) and mcp", cmd, exe)
	}
	if strings.Contains(agentProgram(), "pinky ") || agentProgram() == "pinky" {
		t.Errorf("agentProgram() = %q: a name on the PATH, which another program can take", agentProgram())
	}
}

func TestConnectAgents(t *testing.T) {
	ta := newTestApp(t, fakeapi.OnePhone)
	ta.sync()
	ta.page, ta.set.sec = pageSettings, secAgents
	tt := mg.NewTester(ta.view, 1280, 820)
	claude := "claude mcp add pinky -- " + agentCommand()
	// The commands show under their labels; copying them gives their text.
	for _, want := range []string{"Add an agent", "Command to add Pinky to Claude Code", "Command that starts Pinky for an agent"} {
		if !hasText(tt, want) {
			t.Fatalf("Settings › Agents lacks %q: %q", want, tt.Texts())
		}
	}
	for label, want := range map[string]string{
		"Copy the command for Claude Code":  claude,
		"Copy the command for other agents": agentCommand(),
	} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		if tt.Clipboard() != want {
			t.Errorf("%s copied %q, want %q", label, tt.Clipboard(), want)
		}
	}
}
