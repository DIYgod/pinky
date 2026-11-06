# Pinky

Let AI agents use your phone, with you in the loop.

Pinky lets any agent (Claude Code, Codex, Cursor or a script) operate a real iPhone or Android phone over MCP, while you watch on your computer, approve risky steps, take over and review what happened. Approvals are enforced by Pinky's gateway, not by instructions to the agent.

**Status:** pre-alpha. Nothing here is ready for daily use yet.

## Layout

```text
cmd/pinky/                    app, daemon and MCP shim in one binary
internal/screen/              the screen format agents read
internal/driver/              driver interfaces; android/ and ios/ implement them
internal/gateway/             sessions, leases, the action pipeline, the client API
internal/policy/, approval/   rules and approvals
internal/history/             task history (SQLite)
internal/mcp/                 the MCP server
internal/video/, ui/          the desktop client (MyGo native UI)
android/companion/            the Android companion app (Kotlin)
docs/                         design and notes
```

## Build

Requires Go 1.27.3 or later.

```sh
go build ./...
go test ./...
```

## Documents

- [Design](docs/DESIGN.md)
- [Working on Pinky](docs/WORKING.md)

## License

Apache-2.0
