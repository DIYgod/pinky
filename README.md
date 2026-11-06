# Pinky

Let AI agents use your phone, with you in the loop.

Pinky lets any agent (Claude Code, Codex, Cursor or a script) operate a real iPhone or Android phone over MCP, while you watch on your computer, approve risky steps, take over and review what happened. Approvals are enforced by Pinky's gateway, not by instructions to the agent.

**Status:** pre-alpha. Claude Code has used real phones through it end to end (an Android phone over USB and an iPhone on iOS 27), but expect rough edges. See [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) for what works today and how to try it.

## Try it

Requirements: macOS (Windows and Linux build but are untested), Go 1.27.3 or later, FFmpeg 9 from Homebrew for the live mirror (`brew install ffmpeg`), Android platform tools (`adb`) for Android phones, and Xcode for iPhones (it prepares a phone for development once).

```sh
make app                                   # build/pinky/<os>-<arch>/Pinky.app
open build/pinky/darwin-arm64/Pinky.app    # or: go run ./cmd/pinky
```

Then connect your agent. For Claude Code:

```sh
claude mcp add pinky -- "$PWD/build/pinky/darwin-arm64/Pinky.app/Contents/MacOS/Pinky" mcp
mkdir -p ~/.claude/skills/pinky && cp docs/SKILL.md ~/.claude/skills/pinky/SKILL.md
```

Ask the agent to do something on your phone. The first time it connects, Pinky asks you to allow it.

No phone at hand? `go run ./cmd/pinky --fake` runs the app with two scripted phones (a payment to approve, a blocked banking app, a password field).

### Phones

- **Android:** turn on USB debugging, plug the phone in and accept the computer's key. Pinky works over adb and a bundled scrcpy server; nothing has to be installed on the phone. The optional companion app reads the screen in about 70 ms instead of 2 s: `make companion && make app`, then `pinky companion install` with the phone plugged in (the phone may ask you to allow an unknown app once).
- **iPhone:** iOS 27 or later, Developer Mode on, connected over USB and trusted once in Xcode's Devices window. Nothing is installed on the phone.

## Layout

```text
cmd/pinky/                    app, daemon and MCP shim in one binary
internal/screen/              the screen format agents read
internal/driver/              driver interfaces; android/ and ios/ implement them
internal/gateway/             sessions, leases, the action pipeline, the client API
internal/policy/, approval/   rules and approvals
internal/history/             task history (SQLite)
internal/mcp/                 the MCP server
internal/api/                 the local socket for agents and clients
internal/demo/                scripted fake phones
internal/video/, ui/          the desktop client (MyGo native UI, FFmpeg decoding)
android/companion/            the Android companion app (Kotlin)
scripts/                      device locks and an MCP call script for checks
docs/                         design, agent guide, MCP reference and notes
```

## Build and test

```sh
make check          # build (CGO_ENABLED=0), vet, test
make app            # Pinky.app and a .dmg
make companion      # the Android companion APK (Android SDK and JDK needed)
make e2e            # a binary with MyGo's automation socket, for UI checks
```

Tests that need a real phone use the `device` build tag; see [docs/WORKING.md](docs/WORKING.md).

## Documents

- [Acceptance checklist](docs/ACCEPTANCE.md)
- [Design](docs/DESIGN.md)
- [Agent guide (SKILL.md)](docs/SKILL.md) and [MCP reference](docs/mcp.md)
- [Working on Pinky](docs/WORKING.md)
- Notes: [integration](docs/notes/integration.md), [gateway](docs/notes/gateway.md), [Android](docs/notes/android.md), [companion](docs/notes/companion.md), [iOS](docs/notes/ios.md), [client](docs/notes/client.md)

## License

Apache-2.0
