# Gateway, MCP and local API

Branch `claude/gateway-core-da93b5`. This covers the packages the main session owned in [WORKING.md](../WORKING.md): `internal/gateway` (except the shared `api.go`), `internal/policy`, `internal/approval`, `internal/history`, `internal/mcp` and `cmd/pinky`, plus two new packages, `internal/api` (the local socket, listed in DESIGN's repository layout) and `internal/demo` (scripted fake phones). No real phone was used.

## What works

Everything below runs and is tested against fake phones; real drivers plug in later (see "Integration").

| Area | What it does |
| --- | --- |
| Gateway | `gateway.New(Options{Providers, DataDir, …})` discovers devices every 2 s through providers (no platform branches), opens them, and implements `gateway.API` for the client. Agents use `Session` methods instead, which are not in `API` |
| Pipeline | lease and scope → policy → approval → execute → settle (`driver.Settler` when the driver has it, else polling `Observe` until two reads match, 3 s limit) → read and diff → record. One `screen.Refs` per device; results are `screen.Diff` lines, or the whole screen on an app change |
| Leases | One MCP session can hold several phones. A lease ends on `phone_release`, session close, 10 idle minutes, Stop, or after 60 s offline. Another session gets `lease_held` with the holder's agent and task. A drop mid-action returns `uncertain`, then `device_offline`; a phone back within 60 s keeps its lease |
| Person controls | `TakeOver`, `HandBack`, `Stop`, `StopAll`, mirror input (`Touch`, `Key`, `Text`; taps and swipes are emulated when a driver lacks `Toucher`), lock state (from `Screen.Locked` and `driver.LockReporter`), client events |
| Policy | Allow, Ask, Deny; commit detection (`commit`, `payment`, `send_message`, `install_app`, `open_url`, `call`, plus `change_setting`) with word tables in 11 languages; YAML rules, strictest wins; a bundled, editable app table; blocked apps hidden from agents; password and one-time-code fields refused (rule `credentials`); read-only mode; per-task app list; digit codes in notifications masked |
| Approvals | 60 s from opening, then Deny; calls block 45 s, then `approval_pending` with a single-use id bound to session, device, lease, tool and arguments; decisions between calls are kept; on Allow the target and screen text are checked again (`stale`); the phone takes no other action meanwhile (reads still work); only the client's `Decide` answers |
| History | SQLite through `modernc.org/sqlite` with the tables from DESIGN (devices, agents, sessions, tasks, steps, approvals, media); a JPEG before and after each step in `media/`; `WipeHistory` |
| MCP | The 14 tools from DESIGN on the official Go SDK v1.8.0, with progress notifications when a request has a `progressToken`. Reference: [docs/mcp.md](../mcp.md); agent guide: [docs/SKILL.md](../SKILL.md) |
| Local API | A Unix socket only the user can reach (0700 folder, 0600 socket, peer uid checked), one gateway per user (flock on `pinky.lock`), JSON-RPC control plus MCP sessions |
| Commands | `pinky daemon`, `pinky mcp` (stdio shim that starts the daemon when none runs), `pinky devices`, `--fake`; hidden helpers `pinky needs` and `pinky decide` (answers only on a fake gateway) |

## How to run

```sh
go build ./... && go vet ./... && go test ./...
```

`go test ./...` includes two end-to-end tests in `cmd/pinky` that build the binary and run `pinky daemon --fake` and `pinky mcp` as separate processes (`go test -short` skips them). They use `/tmp/pinky-e2e-*` because a Unix socket path must fit in 104 bytes on macOS; the folders are removed afterwards.

Try it by hand with fake phones (a "Pixel 9 (fake)" and an "iPhone 17 (fake)": home screen, Dinely with a $12.80 payment, Chatter, Settings, a blocked Google Wallet, and an account screen with a password field):

```sh
go build -o /tmp/pinky ./cmd/pinky
export PINKY_HOME=/tmp/pinky-try        # keeps everything out of your real Pinky folders
/tmp/pinky daemon --fake &
/tmp/pinky devices
claude mcp add pinky-fake --env PINKY_HOME=/tmp/pinky-try -- /tmp/pinky mcp --fake
```

Then ask the agent to "reorder last week's lunch on the fake Pixel". The Pay tap waits for approval; answer it from another terminal:

```sh
/tmp/pinky needs
/tmp/pinky decide <id> allow "enjoy"
```

Without `--fake`, the daemon starts with no device providers (none are registered on this branch) and says so.

Folders: `$PINKY_HOME/{config,data,run}` when set; otherwise `~/Library/Application Support/Pinky` on macOS, XDG folders on Linux. Config holds `rules.yaml`, `apps.yaml` (both written with defaults on first start) and `agent-token` (0600). Data holds `pinky.db`, `media/` and `daemon.log`; a `--fake` gateway keeps its history in `data/fake`.

## Integration (for the main session)

- **Real drivers.** Append factories to `providerFactories` in `cmd/pinky/providers.go`; `realProviders` skips one that fails to start. `--fake` uses only `demo.World()`.
- **Desktop app.** Run `gateway.New` in the app process and use it as `gateway.API`. Methods the client needs that are not in `API` (I did not change the interface, since the client session may have its own implementation of it): `SetReadOnly(deviceID, on)` (empty id = every phone), `SetTaskApps`, `MediaPath(ctx, mediaID)` for `StepView.Before/After`, `WipeHistory`, `Agents`, `SetAgentScope`, `Policy()` (rules, `Reload`), `PersonTouched(deviceID)`, `Flush`, `Refresh`.
- **Needs.** `NeedApproval` covers Ask actions, `phone_request_approval` and an agent's first connection (rule `New agent`). New: `NeedHandoff` (`phone_handoff`). `NeedUnlock` ids are `unlock:<device id>` and show only while an agent task waits on a locked phone. `Decide` on a handoff: allow = hand back with the note, Deny = decline, TakeOver = take over (the handoff stays open until `HandBack`). `Decide(…, TakeOver)` on an action approval calls `TakeOver`. `AllowTask` grants the rule for the rest of the task; `AllowAlways` currently does the same.
- **Unlock.** `Options.Unlock` is the hook for entering the saved PIN after Touch ID; without it `Unlock` returns `ErrNoUnlock`.
- **Touches on the phone itself.** `driver.go` has no way for a driver to report them, so I added `Gateway.PersonTouched(deviceID)`. Either wire the Android driver's getevent signal to it, or add an optional driver interface (for example `TouchWatcher`) that the gateway subscribes to.
- **Socket clients.** A desktop app that attaches to a running daemon cannot approve over the socket: `decide`, `hand_back`, `unlock` and `wipe_history` need user presence (Touch ID or Windows Hello), which is not built. Only a gateway with fake phones accepts them.
- **Tuning for real phones.** `Options.PollInterval` (250 ms) and `SettleMax` (3 s) drive settling when a driver has no `Settler`; each poll is a full `Observe`. Every step stores a screenshot before and after (the "before" reuses the previous "after" when the screen did not change); `NoScreenshots` or `--no-screenshots` turns that off if `Screenshot` is slow.

## Decisions I made, and why

Nobody was available to ask, so these are mine; each is easy to change.

1. **The MCP server runs inside the gateway, and `pinky mcp` only copies bytes** between stdio and the socket. One connection is then exactly one MCP session, so session close, cancellation and progress work without extra plumbing, and the tools exist once.
2. **Socket protocol.** The first line is a hello that picks the role (`mcp` with the agent token, or `control`); control is line-delimited JSON-RPC 2.0. MCP connections must present the token from `agent-token`.
3. **The socket cannot approve.** `decide`, `hand_back`, `unlock` and `wipe_history` are refused unless the gateway runs only fake phones. Any process of the same user can open the socket, and DESIGN's threat model says approval methods accept only the in-process UI or a client that proves user presence. `stop`, `stop_all` and `take_over` are allowed: they only take power away from agents.
4. **First connection approval** is a regular approval card (rule `New agent`). Allowing grants `Options.DefaultScope` (everything); narrower scopes need `SetAgentScope` until the client has a scope picker. Approved agents are remembered by name in the `agents` table. A `--fake` gateway trusts agents without asking.
5. **Text-only tool results**, no `structuredContent` or output schema. Some clients show only structured content when it exists, which would hide the screen, and sending both doubles the tokens. The first line (`status · key: value …`) is easy to parse.
6. **Two extra error codes**: `invalid_argument` (bad arguments, a misused `approval_id`, no phone connected) and `internal` (cancelled calls, gateway failures). DESIGN's five did not cover these.
7. **`phone_connect` takes an optional `apps` list**, the task's allow-list. DESIGN has the list (M1) but not where it comes from. An agent can only narrow itself with it, which protects against prompt injection later in the task; the client can change it with `SetTaskApps`.
8. **`user_took_over` vs `paused`**: the first call after a takeover returns `user_took_over`, later ones `paused`, until the person hands back.
9. **Kept decisions.** A decision made while no call waits is kept for the timeout again (60 s) from the decision, not only until the original deadline, so an answer at second 59 still reaches the agent. A later call with an expired id opens a new approval instead of failing.
10. **`intent` is not part of the approval binding**: agents rephrase it on retries, and it does not change what the action does.
11. **Handoffs are approvals of kind `handoff`** (new `NeedHandoff` in `api.go`) with a 15-minute timeout. A handoff opened while the phone is locked ends by itself on unlock.
12. **Stop** ends the lease and bars that session from the phone, so a looping agent cannot reconnect; **Stop all** bars the sessions it stopped from every phone. A new session (the agent restarted) may connect.
13. **Task states**: an agent that leaves or idles without `phone_release` shows as done, not failed; the exact outcome (`disconnected`, `expired`, `offline`, `replaced`, `stopped`, …) is in history.
14. **Blocked apps**: the agent sees `app: blocked app` with no elements and no screenshot; their notifications, toasts and dialogs are dropped; their steps carry no screenshots or screen text. `home` and launching another app still work so the agent can leave.
15. **Read-only mode** also refuses typing outside search fields, as DESIGN's definition implies; it comes from `rules.yaml` (`read_only`, `read_only_devices`) and `SetReadOnly`.
16. **Policy extensions**: a `change_setting` kind (switches and Remove/Reset/Forget… buttons in Settings) for DESIGN's "change account or security settings"; categories beyond finance, messaging and stores (passwords, social, browsers, phone, settings, system) because the defaults need them; the app table matches display names too (iOS backends may not know bundle ids) and `com.bank.*` prefixes; `apps.yaml` adds to and excludes from the bundled table, so table updates still arrive.
17. **Code masking** applies to notifications that mention a code (whole words such as "code", "PIN", "OTP", and CJK keywords) and masks runs of 4–8 digits.
18. **A line break typed into a chat or social app counts as sending** (`send_message`), since many keyboards send on Enter.
19. **Credential fields** are recognized by the password state, or by labels and resource ids (password words; one-time-code words, minus promo, postal and invite codes).
20. **Screenshots** are JPEG (quality 75, scaled down by a whole factor to at most 1280 px), as the task allowed; DESIGN says WebP, which needs a non-stdlib encoder.
21. **Wipe** deletes sessions, tasks, steps, approvals and media, and keeps agents and devices, which are settings.
22. **Swipe direction** is where the finger moves (`up` shows content below), stated in the tool description.
23. **`phone_type` with `ref`** must target a text field; it taps it first to focus. Typing into a button could otherwise tap a commit without the commit check.
24. **Several phones online and no `device`**: `phone_connect` returns `invalid_argument` with the list, rather than picking one.

## Differences from DESIGN

- Not built here: the desktop app mode (`pinky` alone prints a message), Streamable HTTP, the remote API, video recording, retention limits, encryption, HTML and JSON export, Allow for this task and Always allow beyond what is above (M2), `phone_files` (returns `unsupported`), the scope picker.
- `pinky mcp` starts `pinky daemon`, not the desktop app in tray mode, since the app does not exist yet.
- One agent token for all agents, rather than one per agent; agents are told apart by their MCP client name, and the `agents.token_hash` column stays empty.
- Takeover by touches on the phone itself needs a driver signal (see Integration).
- Screenshots are JPEG, not WebP.

## Known issues

- Commit detection is word-based. It catches the common buttons in 11 languages, but a commit with an unusual label (an icon without a label, a slider in a language without a table) is missed, and some navigation buttons (Checkout, Confirm in a wizard) ask needlessly. `rules.yaml` can add label rules.
- The status-bar exclusion for the stale check is a heuristic (systemui ids, `statusbar` in the path, elements in the top 4.5 % of the screen). Screens with a ticking clock or countdown in the app will always come back `stale`.
- Settling by polling costs one `Observe` every 250 ms on drivers without `Settler`. Each `Observe` has a 20 s limit.
- DESIGN wants a pending status for a `phone_type` that would block longer than 45 s (long text on iPhone). Typing runs to completion here; the iOS driver's typing speed decides whether this matters.
- Windows: builds, but untested; the single-gateway lock there only checks whether the socket answers.
- A `--fake` gateway accepts approvals from any local process; that is intended (fake phones only) but worth knowing.
- `pinky needs` and `pinky decide` are development aids and not documented in `pinky help`.

## Shared contracts

- `internal/gateway/api.go`: added `NeedHandoff NeedKind = "handoff"`. Nothing else changed.
- `internal/screen`, `internal/driver/driver.go`, `internal/driver/fake`: unchanged.
- `go.mod` gains `github.com/modelcontextprotocol/go-sdk v1.8.0`, `modernc.org/sqlite v1.61.1`, `go.yaml.in/yaml/v3 v3.0.6`, `golang.org/x/sys`; other branches add their own requires, so expect a `go.mod` merge and a `go mod tidy`.

## Tests

| Package | Covers |
| --- | --- |
| `internal/policy` | commit words in 11 languages and non-commits, default rules for every kind, blocked apps by id, name and prefix, read-only, task and agent scopes, rules file parsing and strictest-wins, files created 0600, `apps.yaml` overrides, code masking, notification filtering |
| `internal/approval` | decide while waiting, pending then a kept decision, timeout as Deny, expiry, binding mismatch, single use, withdraw, cancelled waits |
| `internal/history` | round trip of every table, search, paths with spaces, JPEG downscaling, reopen, wipe |
| `internal/gateway` | approval allow (with history and media), deny, timeout, pending then repeat (busy phone, mismatched and reused ids), stale (screen text and target), take over and handoff, take over during an approval, stop and stop all (cancelling a slow action), lease conflict, session close, MCP-style cancellation, read-only, blocked app, credential fields, send message, batch, offline and reconnect, idle expiry, new-agent approval, locked phone and unlock handoff, wait_for and diffs, `Settler`, watch-only scope, task app list, unclaimed approvals, events |
| `internal/mcp` | over the SDK's in-memory transport: the 14 tools and their schemas, a full flow with approval and progress notifications, pending and errors, session close releasing the lease, batch and screenshots, handoff |
| `internal/api` | single gateway per user, socket and token modes, MCP over the socket with the token, control methods and the trust rule |
| `cmd/pinky` | end to end with real processes: `daemon --fake`, a refused second daemon, `devices`, two `pinky mcp` agents (approval answered through `needs`/`decide`, credentials, `lease_held`, iPhone `recents` unsupported, release, history), stdin close releasing leases, SIGTERM; and `pinky mcp --fake` starting the daemon itself |
