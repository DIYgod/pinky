package main

import (
	"context"
	"path/filepath"
	"time"

	"github.com/DIYgod/pinky"
	"github.com/DIYgod/pinky/assets"
	"github.com/DIYgod/pinky/internal/agents"
	"github.com/DIYgod/pinky/internal/api"
	"github.com/DIYgod/pinky/internal/mcp"
	"github.com/DIYgod/pinky/ui"
)

// agentSetup finds the agents on this computer and adds this Pinky to them
// from Settings › Agents (internal/agents): copies of the files it changes,
// and the MCP bundle it opens in Claude Desktop, go in <data>/agents.
func agentSetup(dirs api.Dirs) *agents.Setup {
	s := &agents.Setup{
		Env: agents.SystemEnv(), Server: ui.AgentServer(), Skill: pinky.Skill,
		Dir:    filepath.Join(dirs.Data, "agents"),
		Bundle: agents.BundleOptions{Version: version, Icon: assets.Icon256},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if tools, err := mcp.Tools(ctx, version); err == nil {
		s.Bundle.Tools = agents.BundleTools(tools)
	}
	return s
}
