# Pinky MCP reference

Pinky's gateway serves one MCP server, `pinky`, with 18 tools. Agents launch `pinkyctl mcp` (Pinky's executable with the argument `mcp`), a stdio shim that forwards to the running gateway over its user-only Unix socket (and starts the gateway first when none runs: the Pinky app on macOS, `pinky daemon` elsewhere). Agents that cannot start a command can use Streamable HTTP instead (below). The agent-facing guide is [SKILL.md](SKILL.md); the design is in [DESIGN.md](DESIGN.md), section "Agent interface".

## Connecting an agent

### With a click

Settings › Agents › Connect an agent lists the agents Pinky finds on this computer, each with Connect, and connects one with a click. Until some agent can reach Pinky (its settings have Pinky, or one has connected), a card in the sidebar offers the same: Connect when Pinky found one agent, Choose… for several, and where to get one (Claude Code, Claude Desktop) when there is none; the Tasks list says no agent is connected.

Connect adds an MCP server named `pinky` to the agent's own settings, which starts this Pinky by its full path with the argument `mcp` (as `/Applications/Pinky.app/Contents/MacOS/Pinky mcp`): no other program can stand in for a path, and agents started from the Dock or the Start menu find it without the terminal's `PATH`.

- Pinky changes nothing until the person clicks Connect (or Update, Fix or Remove) for that agent.
- Where the agent has a command for its MCP servers, Pinky runs it, found on the `PATH` of the person's login shell and where installers put it: `claude mcp add --scope user pinky -- <path> mcp`, `codex mcp add pinky -- <path> mcp`, `copilot mcp add pinky -- <path> mcp`, `gemini mcp add --scope user pinky <path> mcp`, and `… mcp remove` for Remove. Otherwise Pinky changes the settings file itself, in Pinky's entry only: the rest of the file stays as it was, comments, order and formatting included. Pinky reads the file again after the change and refuses one that would alter anything else, and it leaves a file with a mistake in it alone: Settings says what is wrong where, with Show the file.
- Before any change Pinky keeps a copy of the file as it was in its data folder, `agents/backups/<agent>/<time>-<file>` (the newest ten for each agent), and Settings offers to show it.
- It installs Pinky's skill, [SKILL.md](SKILL.md), as `pinky/SKILL.md` in the folder of skills the agent reads: `~/.claude/skills` for Claude Code, `~/.agents/skills` for the agents that share it ([agentskills.io](https://agentskills.io)). Remove takes it out, from `~/.agents/skills` once no agent that reads it has Pinky. A `pinky` skill that is not Pinky's (its front matter names another) stays as it is.
- An agent is Connected when its entry starts this Pinky (or `pinkyctl`, when that is on the `PATH`) and its skill is this Pinky's. Settings marks an entry that starts a Pinky that is no longer there (the app moved), `pinky` (coreutils' command on many computers, below), another copy of Pinky or something else under the name `pinky`, and an old skill; Update sets it right. The sidebar's card offers Fix for the first two, which leave the agent without Pinky.
- Settings reads the agents again every 10 s while it shows them, and the card every 30 s, so an agent installed meanwhile shows up.

| Agent | Found by | Settings | Changed with | Skills |
| --- | --- | --- | --- | --- |
| Claude Code | `claude`, or `~/.claude.json` | `~/.claude.json` (or `$CLAUDE_CONFIG_DIR/.claude.json`): `mcpServers`, user scope | `claude mcp add` and `remove`, else the file | `~/.claude/skills` |
| Claude Desktop | Claude.app, or its folder | Pinky's extension (below); an entry in `claude_desktop_config.json` (`mcpServers`, in `~/Library/Application Support/Claude`, `%APPDATA%\Claude` or MSIX's folder, `~/.config/Claude`) is set right there | opens the extension in Claude Desktop, which asks to install it | — |
| Codex (CLI, IDE extension, ChatGPT app) | `codex` (also inside ChatGPT.app), or `~/.codex` | `~/.codex/config.toml` (or `$CODEX_HOME`): `[mcp_servers.pinky]` | `codex mcp add` and `remove`, else the file | `~/.agents/skills` |
| Cursor | Cursor.app, or `~/.cursor/mcp.json` | `~/.cursor/mcp.json`: `mcpServers` | the file | `~/.agents/skills` |
| VS Code | the app, or its User folder | Copilot's global `~/.copilot/mcp-config.json` (or `$COPILOT_HOME`): `mcpServers`, `"type": "local"`; an entry in VS Code's `User/mcp.json` (`servers`) is set right there | the file | `~/.agents/skills` |
| Copilot CLI | `copilot` | the same as VS Code | `copilot mcp add` and `remove`, else the file | `~/.agents/skills` |
| Windsurf, now Devin Desktop | the app, `~/.config/devin`, `devin` or `~/.codeium/windsurf` | Devin's `~/.config/devin/mcp_config.json` (`%APPDATA%\devin`), or Windsurf's `~/.codeium/windsurf/mcp_config.json`: `mcpServers` | the file | `~/.agents/skills`; Windsurf's own `~/.codeium/windsurf/skills` |
| Gemini CLI | `gemini` (`~/.gemini` is Antigravity's too) | `~/.gemini/settings.json` (or `$GEMINI_CLI_HOME/.gemini`): `mcpServers` | `gemini mcp add` and `remove`, else the file | `~/.agents/skills` |

Claude Code, Codex, Copilot CLI and Gemini CLI read their MCP servers when a session starts, so Settings says to start a new session; Cursor, Windsurf and Claude Desktop's settings file when they open (quit and open them again). VS Code starts a new server at the next message.

Claude Desktop keeps its settings in memory while it runs and writes them back whole, so a change to `claude_desktop_config.json` made while it runs may be lost. Connect therefore installs Pinky as an extension: it writes `Pinky.mcpb` into `<data>/agents/` (the extension below, made to start this Pinky) and opens it in Claude Desktop (`open -b com.anthropic.claudefordesktop` on macOS), which asks the person to install it. Settings then finds it among Claude Desktop's extensions (`Claude Extensions/*/manifest.json` named `pinky`) and shows it connected, or turned off; only Claude Desktop removes it, in Settings › Extensions. An entry that the person added to `claude_desktop_config.json` comes first in Claude Desktop, so Pinky changes that one instead, and says to quit and open Claude Desktop again.

`PINKY_AGENTS_PATH`, for tests of the app, is the whole search path for agents' commands, and leaves out the login shell, the system's application folders and every command outside that path.

Not yet: Antigravity (`~/.gemini/config/mcp_config.json`), opencode, Zed, Kiro, Cline, Factory Droid and Qwen Code; add them by hand. How it is built and what was tried: [notes/agents.md](notes/agents.md).

### Claude Desktop extension

Each release has `pinky-<version>.mcpb`, an [MCP bundle](https://github.com/modelcontextprotocol/mcpb): opened with a double-click, or dragged into Claude Desktop's Settings › Extensions, it installs as the extension Pinky, with no settings file to change. The bundle does not carry Pinky: it starts the Pinky installed on the computer, `/Applications/Pinky.app` or `~/Applications/Pinky.app` on a Mac (then the cask's `pinkyctl`), `%LOCALAPPDATA%\Programs\Pinky\Pinky.exe` on Windows, so install Pinky first. Its setting Pinky takes another path to Pinky's program on a Mac. `go run ./scripts/release mcpb -version <version> -out <file>` makes it; the release workflow adds it to every release. The one Settings › Agents opens names this Pinky's path instead.

### By hand

Settings › Agents › Add an agent by hand shows both commands below for the copy of Pinky that is open, by its own path, with buttons that copy them.

Claude Code:

```sh
claude mcp add --scope user pinky -- pinkyctl mcp
mkdir -p ~/.claude/skills/pinky && cp docs/SKILL.md ~/.claude/skills/pinky/SKILL.md
```

`--scope user` makes Pinky available in every folder; without it, Claude Code adds it to the folder it runs in only.

`pinkyctl` is the command that Homebrew's cask (`brew install --cask diygod/tap/pinky`) and the Linux packages put on the `PATH`. Without them, or for agents started from the Dock or the Start menu, which may not see the terminal's `PATH`, give the app's own executable instead: `/Applications/Pinky.app/Contents/MacOS/Pinky` on a Mac, `Pinky.exe` in `%LOCALAPPDATA%\Programs\Pinky` on Windows, `/opt/pinky/pinky` (the .deb) or `~/.local/pinky.app/pinky` (the .tar.gz's `install.sh`) on Linux.

The command used to be named `pinky`, which is also a coreutils command (on many Macs through Homebrew's coreutils, and on every Linux): an agent set up with `pinky mcp` that reports "Connection closed" runs coreutils' `pinky` (`pinky --version` says GNU coreutils). Set it up again with `pinkyctl mcp` or the app's path; Settings › Agents marks such an agent and sets it right with Update.

Any client that takes a JSON config:

```json
{ "mcpServers": { "pinky": { "command": "pinkyctl", "args": ["mcp"] } } }
```

To try Pinky without a phone, start an iOS Simulator or an Android emulator in the app (**Add phone › Virtual phone**; only the person can start one, agents have no tool for it), and agents use it like a phone. Or use `pinkyctl mcp --fake` (it starts `pinkyctl daemon --fake` when no gateway runs) or run `pinkyctl daemon --fake` yourself first.

### Over HTTP

For agents on another machine, in a container, or clients that speak only HTTP, Pinky serves the same tools over Streamable HTTP. It is off by default: turn on Settings › Remote agents in the app, or run `pinky daemon --http 127.0.0.1:7465`. The endpoint is `http://<address>/mcp`, on this computer only (`127.0.0.1:7465`) unless the person picks another address. Every request needs the agent token as a bearer token; `pinky token` prints it.

Claude Code:

```sh
claude mcp add --transport http pinky http://127.0.0.1:7465/mcp --header "Authorization: Bearer $(pinkyctl token)"
```

A JSON config:

```json
{ "mcpServers": { "pinky": { "type": "http", "url": "http://127.0.0.1:7465/mcp", "headers": { "Authorization": "Bearer <token>" } } } }
```

- Without the token, or with another one, requests get `401`; only upload and download links (below) need none. Browser pages from other origins get `403`, and so do requests to `127.0.0.1` that name another host (DNS rebinding).
- A remote agent's files are on its own machine, out of reach of the folder `phone_files` copies to the phone from and `phone_app` installs from. It uploads them into that folder instead (`phone_files { action: "upload" }`, [below](#uploading-a-file)): in the call, or to a one-time link at `http://<address>/upload/<token>`, which takes the file without the agent token.
- Screenshots, task reports and the recordings the person allows that Pinky saves for a remote agent (`phone_observe { save }`, `phone_release { export }`) stay on this computer too: the agent gets a one-time link for each, `http://<address>/download/<token>`, which it fetches without the agent token ([below](#saving-screenshots-and-reports)).
- Each MCP session over HTTP is one Pinky session, with the same first-connection approval and scopes as over stdio. The agent's name gets " (remote)" after it ("Claude Code (remote)"), so a remote agent is asked about on its own first connection and never gets the scope the person gave the agent of that name on this computer. A newer session of the same remote agent continues the phone its older sessions left ([below](#what-http-clients-may-do)). Turning Remote agents off ends every session at once, with its leases and open approvals.

#### What HTTP clients may do

Pinky is a stateful Streamable HTTP server: `initialize` returns an `Mcp-Session-Id`, and the requests after it carry that id. Clients that handle sessions loosely work too:

- **A new session for every call.** Some clients open a session for each tool call (initialize, then one `tools/call`) and never send an id twice, or start a new session for every turn of a conversation. Over HTTP, a newer session of the same agent (the same `clientInfo` name, so the same "(remote)" name in Pinky) continues the phone that its older sessions hold: the task goes on, with its open approvals (repeat the call with `approval_id` from the new session) and what the person allowed for it. A call that names the phone takes it over, and so does a call without `device` when the agent's older sessions hold exactly one phone; `phone_connect` with the same task name continues the task, another name starts a new one, and `phone_release` releases what the agent's older sessions held.
- **Isolation.** Only a newer session of the same remote agent takes a phone over, and only while the older session has no call running on it (else `lease_held`, "still busy with a call"). It never goes back: of two sessions of one agent that both want a phone, the newer one gets it, and the older one gets `lease_held`. Other agents, and agents on this computer, never take a phone this way; all of them need the agent token.
- **Closing sessions.** `DELETE`, or an hour without requests, ends a session. Its phone then waits 2 minutes for the agent's next session before its task ends as disconnected; `phone_release` ends it at once.
- **Stop.** When the person stops such an agent's task, the agent's later sessions may not use that phone for 10 minutes (any phone, after Stop all), so a client that reconnects for every call cannot simply go on.
- **No session id at all.** A client that cannot keep the `Mcp-Session-Id` (Grok Bot's connector sends fixed headers only, GitHub DIYgod/pinky#10) may send every request after `initialize` without it. Pinky then keeps a session of its own for that client, told apart by its `User-Agent`, starts it with what the client's own `initialize` asked (so the person sees and approves it by its name, "Grok (remote)"), and sends those requests into it: the phone, the task and its approvals carry from call to call, also when the client initializes again. Like any session it ends after an hour without requests; the next one continues the phone as above. A client that never initializes gets one too, as "Agent (remote)". Requests that carry an id always go to that session, so a client that keeps its id never shares a session with one that does not; two clients without ids and with the same `User-Agent` are one client to Pinky.
- **A `GET` without a session id** gets `405`: Pinky offers no stream outside a session, so carry on with `POST`.
- **A `GET` stream.** A client may keep its session's `GET` stream open as long as it likes; calls (`POST`) are served alongside it. Pinky sends little on it, and a proxy that cuts idle streams only makes the client open it again.
- **Protocol versions** 2024-11-05 to 2025-11-25. The sessionless 2026-07-28 protocol (per-request `_meta`) is not served yet.
- **Tunnels and reverse proxies** on this computer (Cloudflare Tunnel, `tailscale serve`, nginx) must send a `Host` of `127.0.0.1:<port>` or `localhost:<port>` (cloudflared: `originRequest.httpHostHeader`), as requests to `127.0.0.1` that name another host get `403`. Upload and download links use the `Host` the request had, or the proxy's `X-Forwarded-Host` and `X-Forwarded-Proto`; the tool's answer says to swap in the public address if the link shows Pinky's own.

- Protocol: the official Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.8.0), newline-delimited JSON-RPC over stdio; the SDK negotiates the protocol version.
- Server info: name `pinky`, title `Pinky`; the server sends short usage instructions at initialization.
- Identity: the agent's name in Pinky comes from the client's `clientInfo` (`claude-code` shows as "Claude Code"). The shim presents the agent token from `agent-token` in the config directory (mode 0600).
- Sessions: one MCP session is one Pinky session. Closing it (the agent exits, or closes stdin) ends its leases and withdraws its open approval panels without acting.

## Results

Every tool returns one text block (plus a JPEG image block for `phone_observe { screenshot: true }`). Results are text for models, not JSON:

```text
<status> · <device name> · approval_id: <id> · rule: <rule> · settle: timeout
note: "<the person's note>"
<one or two sentences>
<the screen, or the lines that changed>
```

- The first line is the status, or `error: <code>` for errors (then the MCP result also has `isError: true`). Details follow as `key: value`, separated by ` · `: `approval_id`, `rule`, `decision`, `settle`, `matched`, `holder`, `setup`, and `no change` when the screen did not change (never after a read that `settle_ms` cut short, which may come before the change).
- The screen is the format in DESIGN.md: a header line, then events (`toast:`, `notification:`, `dialog:`), then one element per line `[ref] role "label" @x,y state`, then `ocr` lines.
- `ocr` lines are text read from the screen's pixels that no element shows; they have no ref and are tapped by `x,y`. iPhones add them on every read that shows the agent the screen. Android phones add them where the element tree is empty or sparse over a web view or a canvas (mini-programs and other pages in super-apps' own web views, games, maps), and only there; `phone_observe { ocr: true }` asks for all the text no element shows. OCR uses Vision on macOS and Windows.Media.Ocr on Windows; a computer without it (Linux) marks those Android areas with the state `no_ocr` instead. The picture OCR reads stays in memory and is dropped once read: none is taken of a locked phone, nor of a blocked app the person has not allowed for the task.
- After an action, only the changes are sent: `+` added, `-` removed, `~` changed (including `~ screen …` for a changed header). A new app, size, orientation or lock state, or a diff longer than the screen, sends the whole screen.
- `phone_connect`, `phone_observe` and `phone_handoff` always send the whole screen.
- Files saved for the agent ([below](#saving-screenshots-and-reports)) follow the first lines, one `saved: <path> · <size>` line each; over HTTP the sentences give download links instead.
- A blocked app shows as `app: blocked app` with no elements; its toasts and dialogs are dropped, and no screenshot is returned. An app the person allowed for the task (see `phone_launch`) shows as usual, `ocr` lines included, but still without screenshots: OCR reads it from a picture the driver keeps in memory and drops.

### Statuses

| Status | Meaning |
| --- | --- |
| `settled` | The call ran and the screen stopped changing, or settling hit its limit, 3 s or the action's `settle_ms` (`settle: timeout`) |
| `uncertain` | The action may have happened (the driver failed mid-action, the phone dropped). Observe before anything else |
| `blocked_by_policy` | A rule denied the action; `rule` names it (`credentials` for password and code fields, `Read-only`, `Finance apps`, …) |
| `approval_denied` | The person said no, or nobody answered in time; `note` may say why |
| `approval_pending` | The call blocked for 45 s; repeat it with `approval_id` |
| `stale` | The screen changed while the approval waited, so nothing was done |
| `user_took_over` | The person took over; returned at once with the current screen. Later calls return `paused` until they hand back |
| `paused` | The person is in control. Wait with `phone_handoff` |
| `stopped` | The person pressed Stop; the lease is gone, and this session may not use that phone again |
| `device_locked` | The phone is locked. Call `phone_handoff` |
| `device_needs_setup` | The phone is connected, but Pinky cannot use it until the person does a step on it or on the computer, and nothing was done. `setup` names the step and the message says what to do: on an iPhone `trust`, `trust_record` (the iPhone trusts this computer, but Apple's device service could not replace the pairing record it holds on Windows: the message has the command an administrator runs; see [docs/notes/ios.md](notes/ios.md)), `developer_mode`, `unlock`, `passcode` (iOS asks for it before the WebDriverAgent helper may control the phone), `no_disk_image`, `disk_image`, `install_helper`, `connection` (the connection chosen in Settings › Phones does not work with the phone: the person chooses another), `unlock` (also an iPhone or iPad locked on Wi-Fi, which Pinky opens only once it is unlocked), `device_service` (the Mac's device service lost the phone on Wi-Fi: the person clicks Restart device service on the phone's page in Pinky); on Android `authorize` (allow USB debugging), `usb_permission`, `usb_driver`; `preparing` (Pinky prepares the phone: wait a few seconds, then observe) and `update` (the phone's iOS is too old: end the task) are no steps to hand off. Otherwise call `phone_handoff` asking for the step: it ends by itself once the phone is ready, with the whole screen. After `usb_driver` the phone comes back under another id: call `phone_connect` again. The lease waits with the phone instead of ending after 60 s |

### Errors

| Error | Meaning |
| --- | --- |
| `device_offline` | The phone is not connected. A lease survives 60 s of disconnection |
| `lease_held` | Another session holds the phone; `holder` names its agent and task |
| `scope_denied` | The app is outside the agent's scope or the task's app list |
| `not_found` | The ref is gone, no installed app matches, or no app opens the link |
| `unsupported` | The phone or backend cannot do this, such as `recents` on iPhone, or `phone_files` `push` on iPhone; or a link several apps open, with none set as the default |
| `invalid_argument` | Bad or missing arguments, a misused `approval_id`, or no phone connected in this session |
| `internal` | The call was cancelled, or the gateway failed |

## Common arguments

Every action tool (`phone_tap`, `phone_type`, `phone_swipe`, `phone_pinch`, `phone_key`, `phone_launch`) takes:

| Argument | Type | Meaning |
| --- | --- | --- |
| `device` | string | The phone, by id, name or a unique id prefix. Required when the session holds more than one phone |
| `intent` | string | One sentence on why, shown on the approval panel and in history |
| `approval_id` | string | From `approval_pending`: repeat the same call with it to keep waiting |
| `settle_ms` | 0–3000 | The most to wait for the screen to stop changing before it is read; default 3000. See below |

`settle_ms` is for screens that never stop moving (games, video, live maps), where every action otherwise waits the whole 3 s and returns `settle: timeout`. A lower limit returns sooner, and 0 reads the screen right after the action.

- Below about 500 ms the result always says `settle: timeout`: an iPhone waits at least 500 ms before it calls the screen settled, and an Android phone needs 400 ms without a change.
- A read it cuts short may come before the action's effect shows (an iPhone starts to change about a third of a second after a tap). The result says so in its message and never says `no change`; observe before repeating the action. History keeps no picture after such a step when the read matched the one before it.
- The next action on the phone waits out the rest of the 3 s, or until the screen settles, before it reads the screen it is judged by, so the rules never judge it against a screen from before this one took effect. `settle_ms` therefore saves time between calls made seconds apart; in `phone_batch` and in calls made in parallel, only the last action gains.
- `phone_launch` and `home` still wait up to 3 s for the app in front to change; `settle_ms` limits only the wait after that.
- It is ignored when the person approves the action, allowed its rule for the task, or always allows it in the app: such an action waits the whole 3 s.
- It is not part of what an approval binds: an `approval_id` may be repeated with another `settle_ms`.

`phone_observe`, `phone_apps`, `phone_wait_for` and `phone_release` take `device`; `phone_files`, `phone_app`, `phone_request_approval` and `phone_handoff` take `device` and `approval_id` (and `phone_files` and `phone_app` take `intent`). Unknown arguments are rejected.

## Tools

### phone_connect

Leases a phone for a task and returns its first screen.

| Argument | Type | Meaning |
| --- | --- | --- |
| `task` | string, required | One line naming the task, shown to the person |
| `device` | string | Optional when exactly one phone is online, or the session already holds one, or none is online and exactly one is connected but needs setup |
| `apps` | string[] | Optional app allow-list for the task (names or ids). Launching another app returns `scope_denied`; typing into one asks the person. Launchers and system UI are always allowed |
| `read_only` | boolean | Run the task read-only, whatever the agent's scope: anything the person's rules would ask about is refused (`blocked_by_policy`, rule `Read-only`), and typing works only in search fields. Connecting again for the same task never widens it. A new agent's approval card then offers read-only access first |

The first connection of an agent the person has not approved yet returns `approval_pending` (rule `New agent`); repeat with `approval_id`. Another session's phone returns `lease_held`. Calling again with the same task keeps the lease; another task name ends the previous task (outcome `replaced`) and starts a new one. After the person pressed Stop, the session cannot connect to that phone again. While the person is in control of the phone (they took over and have not handed back, whichever task was running), the lease starts paused and the result is `paused`: call `phone_handoff`. A phone that is connected but waits for a step (Trust, Allow USB debugging, Developer Mode, …) is leased all the same and returns `device_needs_setup`, without a screen, so `phone_handoff` can ask the person for the step; other calls return `device_needs_setup` until the phone is ready.

### phone_release

Ends the task and frees the phone.

| Argument | Type | Meaning |
| --- | --- | --- |
| `outcome` | `done` \| `failed` \| `partial` \| `cancelled` | Default `done` |
| `summary` | string | For the person's history |
| `export` | boolean | Also save the task's report (HTML and JSON) for a bug report or a pull request, and its screen recording if the person allows it, [below](#saving-screenshots-and-reports) |
| `device` | string | Default: every phone the session holds |
| `approval_id` | string | From an `approval_pending` result of an export: repeat the same call with it to keep waiting for the person's answer |

### phone_observe

Returns the whole current screen.

| Argument | Type | Meaning |
| --- | --- | --- |
| `wait_ms` | 0–30000 | Wait until the screen differs from what the agent last saw (then let it settle), or until this passes |
| `screenshot` | boolean | Add a JPEG screenshot at the phone's full resolution, in the screen's pixel space (1125×2436 on an iPhone X-sized screen). Some clients show the model a smaller copy of tool images: to keep or pass on the picture, use `save`. None while the phone is locked or a blocked app is in front |
| `save` | string | Save that screenshot as a file for a bug report or a pull request, such as `onboarding-1.png`, and return its path ([below](#saving-screenshots-and-reports)). The image itself comes back only with `screenshot: true` |
| `ocr` | boolean | Add all the text the pixels show that no element does, as `ocr` lines, however complete the list looks (Android adds such lines by itself only where the list is empty or sparse). On a computer without OCR, the result says so |

Works while the person is in control and while an approval is open.

### phone_tap

| Argument | Type | Meaning |
| --- | --- | --- |
| `ref` | integer | The element to tap |
| `x`, `y` | integers | A point instead of a ref (both or neither) |
| `long_press` | boolean | Press and hold |

The policy checks every element under the finger, not only the smallest: tapping a price label inside a Pay button counts as tapping Pay.

### phone_type

| Argument | Type | Meaning |
| --- | --- | --- |
| `text` | string, ≤ 16384 chars | The text; may be empty with `submit` |
| `ref` | integer | The field (tapped first to focus it); default the focused field. Must be a text field |
| `append` | boolean | Keep the field's text |
| `submit` | boolean | Press Enter afterwards |

Typing into password and one-time-code fields is always refused (`blocked_by_policy`, rule `credentials`). So is typing without `ref` when no field reports focus and the screen has such a field.

### phone_swipe

| Argument | Type | Meaning |
| --- | --- | --- |
| `direction` | `up` \| `down` \| `left` \| `right` | Where the finger moves; `up` shows the content below |
| `ref` | integer | Swipe inside this element (with `direction`) |
| `from`, `to` | `{x, y}` | Exact points instead of a direction |
| `duration_ms` | 80–1200 | Default 300 |

### phone_pinch

| Argument | Type | Meaning |
| --- | --- | --- |
| `ref` | integer | Pinch around the middle of this element (a map, photo or page), with both fingers inside it |
| `x`, `y` | integers | A point instead of a ref (both or neither); default the middle of the screen |
| `scale` | 0.25–4 | The fingers end this many times as far apart as they start: above 1 zooms in (2 shows the content about twice as large), below 1 zooms out (0.5 about half as large) |
| `rotation` | -180–180 | Also turn both fingers around the centre by this many degrees, clockwise when positive. Without `scale`, the pinch only turns |
| `duration_ms` | 200–2000 | Default 500 |

The fingers stay inside the element and clear of the screen's edges, so the edges' system gestures never start; a pinch with too little room for two fingers (a small element, a point at the edge) returns `invalid_argument`. The app decides how far its content follows the fingers: observe, and pinch again if it is not enough. The rules judge a pinch like a swipe, with the elements under both fingers. Android phones pinch on their own screen and on the background display; iPhones pinch through the WebDriverAgent helper. An iPhone through Device Hub (iOS 27 without the helper) returns `unsupported` and does nothing.

### phone_key

| Argument | Type | Meaning |
| --- | --- | --- |
| `key` | `back` `home` `recents` `enter` `tab` `delete` `forward_delete` `escape` `dpad_up` `dpad_down` `dpad_left` `dpad_right` `page_up` `page_down` | Keys the phone lacks return `unsupported` |

`home` always works, even in a blocked app, except on an iPhone below iOS 27 without the WebDriverAgent helper (see Limits).

### phone_launch

| Argument | Type | Meaning |
| --- | --- | --- |
| `app` | string, required | Package name, bundle id, or name (the app's name, its home screen name or a well-known one; part of a name is not enough); or a link: `https://…`, an app's own link (`myapp://…`), `tel:`, `sms:`, `mailto:` and the like ([Opening a link](#opening-a-link)) |
| `language` | string | Open the app in this language, a BCP 47 tag (`ja`, `zh-Hans`, `pt-BR`), without changing the phone's. A region in the tag sets the region too |
| `region` | string | Open the app with this region's dates, numbers and currency (`JP`, `DE`, `419`). Alone, the app keeps the phone's language |

Opening a blocked banking or wallet app, or one the person blocked, asks the person like an Ask action (rule `Finance apps` or `Blocked by you`; up to 45 s, then `approval_pending`). If they allow it, it stays allowed for the rest of the task: the agent may open it and read its screen, and everything else the rules ask about there still asks; Pinky keeps no screenshots, recording or screen text of it (its `ocr` lines come from a picture kept in memory only, and no screenshot is returned), and Settings do not change. If they deny it, later launches of it in the task return `blocked_by_policy` at once. Password managers return `blocked_by_policy` without asking. Apps outside the agent's scope or the task's list return `scope_denied`; unknown apps return `not_found`. A name finds an app only by its id, its name, its home screen name or a well-known name (微信 for WeChat): a name that is only part of other apps' names (微信 in 微信读书) finds nothing, and `not_found` lists those apps as similar. The task's `apps` list in `phone_connect` takes names the same way, and its message lists similar apps for a name it could not place.

With `language` or `region`, the launch changes the phone's settings for that app, as [phone_device](#phone_device) does for the whole phone, under the same rule `Phone settings`: on a real phone it asks the person (besides what the rules say about the launch itself), read-only agents get `scope_denied` and read-only mode `blocked_by_policy`. A running app restarts, since apps read their language when they start. Pinky puts the language back when the task ends, or on `phone_device { reset: ["language"] }`. History says "Opened Settings in ja-JP", or "with region JP". A link keeps the language of the app it opens in: `language` or `region` with a link is `invalid_argument`.

- **Android 13 and later** set the app's own language (`cmd locale set-app-locales`), as the phone's Settings › Apps › Language would; the app changes at once. Earlier versions return `unsupported`.
- **iPhones and iOS Simulators** pass `-AppleLanguages` and `-AppleLocale` as launch arguments, after quitting the app if it runs. The app keeps that language until it quits, so putting it back quits the app if it still runs (with the WebDriverAgent helper; through CoreDevice, without it, the app stays in that language until it quits).

#### Opening a link

A link opens in the app the phone would open it in, and only there. Pinky finds that app before anything opens and judges the link as a launch of it: blocking the app, or leaving it out of the agent's scope or the task's list, refuses its links, and the person's allowing a blocked app for the task covers its links too. The link also meets the rules a tap on the same thing would. Every link counts as opening a link (rule `Installs and links`, which asks when the person chose to approve more than payments); `tel:` and `facetime:` links count as a call too, `sms:` and `mailto:` as sending a message, and payment links as a payment, which asks under `Payments` whatever the person chose: `upi:`, `alipays:`, `paypal:`, `bitcoin:` and the like, and WeChat Pay's `weixin://wxpay/…`. The approval and the task's history name the link and the app it opens in. `file:`, `content:`, `data:`, `intent:`, `android-app:`, `javascript:`, `itms-services:` and Shortcuts links (`shortcuts:`, and any link that would open in Shortcuts) return `blocked_by_policy` with rule `Unsafe links`. A link no app on the phone opens returns `not_found`. One that several apps open, with none set as the default so that the phone would ask which, returns `unsupported` and names them: launch the app you mean and open the link from inside it.

On Android a link opens in the app the phone set for it, on the background display when the task runs there; while the agent works in an app clone it opened, a link that app opens opens in the clone. On an iPhone a web link opens in Safari, whichever browser the person made the default, and an app's own link opens in the app that declares its scheme (Apple's own app when it is one of them). Through the WebDriverAgent helper links need WebDriverAgent 4.11 or later: an older one returns `unsupported` until the person installs it again.

### phone_apps

| Argument | Type | Meaning |
| --- | --- | --- |
| `query` | string | Only apps whose name or id contains it |

Lists installed apps inside the agent's scope, user apps first, one per line: `Name (id)`, with `· system` for system apps. An iPhone app whose home screen shows another name than its own (`WeChat · 微信 (com.tencent.xin)`) lists both. Blocked apps are never listed.

### phone_batch

| Argument | Type | Meaning |
| --- | --- | --- |
| `steps` | array, ≤ 50 | Each step has `tool` (`tap`, `type`, `swipe`, `pinch`, `key`, `launch`, with or without the `phone_` prefix) and that tool's arguments, plus its own `intent`, `approval_id` and `settle_ms` |
| `intent` | string | For steps without their own |

Steps run in order, each settling first, and the batch stops at the first step that is not `settled` (`approval_pending`, `uncertain`, an error); a step whose screen kept moving (`settle: timeout`) does not stop it. The result's first line is the last step's status; then `step N:` blocks follow with each step's status and changes. To finish a batch that stopped at `approval_pending`, repeat that step alone as its own tool call with the `approval_id`.

### phone_wait_for

| Argument | Type | Meaning |
| --- | --- | --- |
| `text` | string, required | Matched without case in element labels and new events. In languages that put spaces between words it matches whole words only (`Stop` does not match `Stopwatch`); Chinese, Japanese, Korean and Thai text matches anywhere in a label |
| `gone` | boolean | Wait for it to disappear |
| `timeout_ms` | 0–45000 | Default 10000 |

Returns `settled` with `matched: true` or `matched: false` and the screen changes.

### phone_files

Copies files between the phone's Pinky folder (`/sdcard/Pinky` on Android, the only place on the phone agents may reach) and a folder on the computer, `<data>/files/<phone id>/`, which the result names. Agents read and write that folder with their own file tools; agents that cannot (on another computer, over HTTP) upload files into it. Needs a `phone_connect` lease like the other tools. An iPhone has no Pinky folder: there `list` and `upload` work on the computer's folder only, for apps to install with `phone_app`, and `pull` and `push` return `unsupported`.

| Argument | Type | Meaning |
| --- | --- | --- |
| `action` | `list` \| `pull` \| `push` \| `upload` | `list` (default) shows both sides; `pull` copies from the phone; `push` copies to the phone; `upload` saves a file the agent sends in the computer's folder. With `content` and no `action`, `upload` |
| `paths` | string[] | Files or folders, relative to the Pinky folder. Default: everything; for `push`, the files that are new or changed on the computer. For `upload`: the one file's name, such as `photo.jpg` or `Pictures/photo.jpg` |
| `content` | string | For `upload`: the file's bytes in base64 (standard or URL alphabet, padding and line breaks optional, or a `data:…;base64,` URL), up to 10 MB |
| `replace` | boolean | For `upload`: replace a different file of the same name |
| `intent` | string | For `push`: one sentence on why, shown on the approval panel |
| `approval_id` | string | From `approval_pending`: repeat the same call with it |

The result lists the folder, then one line per file: `path · size · modified · where`, where `where` is `both` (same size and time), `phone`, `computer` or `differs`. `pull` and `push` list the files they copied and skip files that are the same on both sides; copied files keep their modification time.

- `push` asks the person first (rule `Files`), like an Ask action: up to 45 s, then `approval_pending`. "Allow for this task" covers later pushes in the task. Read-only mode refuses it (`blocked_by_policy`, rule `Read-only`), and read-only and watch-only agents get `scope_denied`: they may only list and pull.
- Paths that leave the folder (`..`, absolute paths) are `invalid_argument`; files that are not there are `not_found`. Symbolic links in the computer's folder are not copied, and a pull replaces a link in its way instead of writing through it.
- Each call is a step in the task's history ("Copied 2 files from the phone: a.txt, b.png").
- Files pushed to Android show in the phone's gallery and photo pickers: Pinky asks the media scanner for them on Android 10 and earlier, and later versions find them by themselves.

#### Uploading a file

`upload` writes one file into the computer's folder and nothing to the phone, so it asks nothing; the `push` after it asks the person as usual. It is the first half of a push and follows its rules: read-only and watch-only agents get `scope_denied`, and read-only mode `blocked_by_policy` (rule `Read-only`).

- **Names** are paths inside the folder; missing folders are made. `invalid_argument` for `..`, absolute paths, names that start with a dot, control characters, `< > : " | ? *`, names that end in a space or a dot, Windows device names (`CON`, `NUL`, `COM1`, `LPT1`…, with any extension), names over 255 bytes and paths over 1000 bytes: the file is made on this computer, which may run Windows, and then on the phone.
- **A name already taken:** the same bytes settle without change; a different file is `invalid_argument` unless `replace: true`. A symbolic link in the way is replaced, never written through, and a folder on the way that is a link is refused.
- **`content`** carries up to 10 MB, checked before anything is decoded or written. Over HTTP Pinky takes requests of up to 16 MiB for it, as over stdio (the SDK's own HTTP default is 4 MiB). A model has to write every byte of it out, so it suits small files and agents that call tools from code.
- **Without `content`, over HTTP,** Pinky checks the upload (scope, name, a taken name) and returns a link instead: `http://<address>/upload/<token>`, at the address the agent reached Pinky at (or the `X-Forwarded-Proto` and `X-Forwarded-Host` of a proxy in front of it). The agent sends the file as the request body with `PUT` or `POST`, from its own shell: `curl --upload-file photo.jpg '<link>'`. A link works once, for 10 minutes, for files up to 200 MB, and only for its session, phone, name and `replace`; it ends with the session. It needs no agent token: its own random 128-bit token is the key. Forms (`multipart/form-data`) get `415`, larger files `413` (or `400` once past the limit when no length was sent), and unknown, used or expired links `404`. The answer is the text the tool would return, with `200`, or `400`, `403`, `503` or `500`.
- **Without `content`, over stdio,** the call is `invalid_argument` and names the folder: an agent on this computer writes the file there itself.
- Each upload is a step in the task's history: "Received “photo.jpg” (1.2 MB) from the agent".

### phone_app

Installs, updates, uninstalls or stops (force-quits) an app: for an agent that builds an app, puts it on the phone, runs it and checks it.

| Argument | Type | Meaning |
| --- | --- | --- |
| `action` | `install` \| `uninstall` \| `stop`, required | `install` installs an app from files on the computer, or updates the installed app and keeps its data; `uninstall` removes an app and its data; `stop` force-quits it |
| `paths` | string[] | For `install`: one `.apk`, the split APKs of one app, one `.ipa` or one `.app` folder, relative to the phone's folder on the computer (the one `phone_files` names), such as `["app-debug.apk"]` |
| `app` | string | For `uninstall` and `stop`: an installed app's name or id, found as `phone_launch` finds it |
| `intent` | string | One sentence on why, shown on the approval panel and in history |
| `approval_id` | string | From `approval_pending`: repeat the same call with it |

- **Files.** Only files in the phone's folder on the computer install: paths that leave it (`..`, absolute paths) are `invalid_argument`, and so are symbolic links on the way; missing files are `not_found`. An agent on another computer uploads the file first with `phone_files { action: "upload" }`. Pinky reads the package before it asks and installs from a copy that it reads again, so the file cannot change after the person said yes.
- **Android** takes an `.apk`, or the split APKs of one app (one base and its splits, one signer), and installs it for the user the phone's screen runs as. Test-only builds (Android Studio's Run) install; downgrades and an app signed with another key do not, and the message says what to do.
- **iPhone** takes a development-signed `.ipa` or `.app` folder, over the cable or Wi-Fi, with or without the WebDriverAgent helper. Before asking, Pinky refuses (`invalid_argument`) a build for the Simulator, one without a provisioning profile, an expired profile, an App Store profile, and a profile that does not list this iPhone, naming its UDID; what the iPhone itself refuses after the transfer (a signature it does not accept, an iOS too old for the app) comes back in the same plain words. iOS Simulators return `unsupported`.
- **Approvals.** `install` and `uninstall` always ask the person, whatever they chose in Settings › Safety (rules `Installing from the computer` and `Uninstalling apps`): up to 45 s, then `approval_pending`. The panel names the app, its id and version, who signed it, the files and, for an update, the version it replaces. "Allow for this task" covers installing the same app id signed by the same signer again (the build, install, check loop asks once), or uninstalling the same app again; there is no "Always allow". `stop` does not ask, except while every action asks.
- **Refused** (`blocked_by_policy`, with the rule): Pinky's own apps (`Pinky's own apps`), system apps (`System apps`), finance apps, password managers and apps the person blocked, for all three actions, judged for an install by the new app's id and name. Read-only mode refuses all three (`Read-only`); read-only and watch-only agents, and apps outside the agent's scope or the task's `apps`, get `scope_denied`.
- The result says what happened (`Installed “Dinely” 1.3 (14) (com.example.dinely). Open it with phone_launch.`), and each call is a step in the task's history: "Installed “Dinely” 1.3 (14) from “app-debug.apk”", "Updated “Dinely” to 1.4 (15) from “app-debug.apk”", "Uninstalled “Dinely”", "Stopped “Dinely”". `stop` also returns the screen's changes.

### phone_logs

Reads one app's own log lines or crash reports, so an agent that tests an app on the phone can see why it failed. Needs a `phone_connect` lease like the other tools; it changes nothing on the phone (annotated read-only). Design and privacy decisions: [notes/app-logs.md](notes/app-logs.md).

| Argument | Type | Meaning |
| --- | --- | --- |
| `app` | string, required | Package name, bundle id, or name, found as for `phone_launch` |
| `kind` | `logs` \| `crashes` | `logs` (default): the app's log lines; `crashes`: its crash reports, with Android's not-responding (ANR) reports and the iPhone's watchdog ends |
| `since` | string | Where to start: an RFC 3339 time, a date and time or a time of day on the computer's clock (`14:05` is the last 14:05 that passed), or a duration back (`10m`, `2h`, `1d`). Default: when the task started |
| `filter` | string | For `logs`: only lines whose tag or text contains it, ignoring case |
| `level` | `verbose` \| `debug` \| `info` \| `warn` \| `error` \| `fatal` | For `logs`: only lines of this level or more severe |
| `limit` | 0–500 | Lines (default 200, at most 500) or crash reports (default 3, at most 10) |
| `intent` | string | One sentence on why, shown on the approval panel |
| `approval_id` | string | From `approval_pending`: repeat the same call with it |

The result's message says what it holds ("120 log lines of Example since 14:05:00, oldest first."); the lines or reports follow it.

- **Log lines** are the newest that match, shown oldest first, one per line: `14:05:31.204 4321 E Tag: text`, the time on the computer's clock, the process id, the level (`V D I W E F`), the tag (Android's tag, an iPhone line's subsystem and category or its image) and the text. A line over 2,000 bytes is cut. When more lines matched than `limit` or 64 KB allow, the message says how many older lines were left out; narrow with `since`, `filter` or `level`.
- **Crash reports** come newest first, each with a heading: `── crash · 2026-11-09 14:05:33 · pid 4321 · com.example.app`, its kind (`crash`, `native crash`, `ANR`, `watchdog`), a one-line summary, then the report: on Android the drop box entry (an ANR cut down to the app's own threads), on an iPhone the `.ips` report cut down to the exception, the crashed thread's backtrace and the app's own binary images. When they pass 64 KB the newest is cut short and the others keep their first lines; the message says so.
- **Only that app's own processes.** Lines of other processes, the system's included, are never returned, so system lines about the app (Android's "Process … has died") are not either; the crash reports carry what matters of them.
- **Scope.** An app outside the agent's scope or the task's `apps` is `scope_denied`; watch-only agents get `scope_denied` for every app. Read-only agents and read-only mode may read logs.
- **Refused apps.** Blocked apps (even one the person allowed for the task), finance apps and password managers (even with those rules off), the phone's own system apps, Pinky Companion and the iPhone's WebDriverAgent helper return `blocked_by_policy` without asking; the rule is the app's rule, or `App logs`.
- **Asking.** The first read of an app in a task asks the person (rule `App logs`, "Read the logs of Example?"), like an Ask action: up to 45 s, then `approval_pending`. "Allow for this task" covers that app's logs and crash reports until the task ends; there is no "Always allow". A development build does not ask: an Android app that is debuggable, or an iPhone app signed for development (`get-task-allow`, as Xcode installs it). Denied: `approval_denied` with the person's note.
- **iPhones keep no log history Pinky can read.** Pinky starts listening to the app at the first `phone_logs` call for it in a task and keeps listening until the task ends (up to 20,000 lines), so the first call returns little; the message says from when Pinky listens. Make the app do what you want to see, then call again. Pinky finds a new process of the app within a second, sooner when `phone_launch` started it, so the very first lines of a fresh start can be missing. Only what the app logs through the system log reaches it (`os_log`, `Logger`, `NSLog`), not `print`; levels are `debug`, `info`, `error` and `fatal` (a fault). Crash reports have no such limit: the phone keeps them. iOS Simulators return `unsupported`.
- **Android** reads logcat (the main, system and crash buffers) for the app's user id, and crash reports from the drop box (`data_app_crash`, `data_app_native_crash`, `data_app_anr`) and the crash buffer. An app that shares its user id with others is `unsupported`, as their lines cannot be told apart. What a phone still holds depends on its buffers: a busy phone may have dropped older lines.
- Each call is a step in the task's history, with what was read and never the text: "Read 120 log lines of Example", "Read 1 crash report of Example".

### phone_device

The switches developers flip to test an app: the screen's rotation, light or dark appearance, and a simulated location. Without arguments it reads them; read-only and watch-only agents may only read.

| Argument | Type | Meaning |
| --- | --- | --- |
| `rotation` | `portrait` \| `landscape` \| `upside_down` \| `reverse_landscape` \| `auto` | Turn the screen. `landscape` is turned to the left, the top on the left; `auto` (Android) turns with how the phone is held |
| `appearance` | `light` \| `dark` | Light or dark appearance (dark mode) |
| `location` | `{latitude, longitude}` | Make every app on the phone see this place |
| `reset` | string[] | Put back sooner what this task changed: `rotation`, `appearance`, `location`, `language` (apps opened in another language with `phone_launch`), or `all` |
| `intent` | string | One sentence on why, shown on the approval panel |
| `approval_id` | string | From `approval_pending`: repeat the same call with it |

The result has one line per switch, after the screen's changes: `rotation: landscape (was portrait)`, `appearance: dark`, `location: none`, or `location: unsupported · <why>`. Several switches in one call are read first, so one the phone lacks fails the call before anything changes, then set in the order rotation, appearance, location.

- **Asking.** On a real phone a change asks the person (rule `Phone settings`; up to 45 s, then `approval_pending`), with Allow once and Allow for this task, which covers the task's later changes and language launches; there is no Always allow. On an emulator or a Simulator it asks only when the person chose to approve more than payments in Settings › Safety. Read-only agents get `scope_denied`, read-only mode `blocked_by_policy` (rule `Read-only`). `reset` never asks.
- **Putting back.** When the task ends, however it ends (released, stopped, the session gone), Pinky puts back every change, the last made first, as steps after "Finished" in history: "Put the appearance back to light", "Stopped the simulated location". A switch the person changed meanwhile stays as they set it ("Left the appearance as the person set it"), and a phone offline at the end keeps the change ("Could not put …").
- **Each change is a step** in history: "Set the screen's rotation to landscape", "Set the location to 35.6812, 139.7671".
- **Rotation** turns what the app in front allows: an app that only runs upright stays upright, though the phone counts as turned and is turned back.

| | Rotation | Appearance | Location |
| --- | --- | --- | --- |
| Android emulator | yes | yes | yes, through the emulator's console; it cannot stop a simulated location, so one set on an emulator that had none stays |
| Android phone | yes | yes | `unsupported` |
| iOS Simulator, iPhone with the WebDriverAgent helper | yes, but not `auto` | yes | yes |
| iPhone without the helper | `unsupported` | `unsupported` | `unsupported` |

### phone_request_approval

| Argument | Type | Meaning |
| --- | --- | --- |
| `summary` | string, required | The question as the person reads it |
| `details` | string | More context |

Returns `settled` with `decision: allow`, or `approval_denied`, with the person's `note`. It grants nothing: later steps the person's rules ask about, such as payments, still ask, and so does your next question. Like action approvals it blocks up to 45 s and holds the phone while open.

### phone_handoff

| Argument | Type | Meaning |
| --- | --- | --- |
| `reason` | string, required | What the person should do |

Shows a Needs you card and waits until the person hands control back; returns `settled` with their `note` and the whole screen. If they already handed back before the call, it returns at once. A handoff opened while the phone waits for a step (`device_needs_setup`) ends by itself once Pinky has opened the phone, with `note: "The phone is ready."`; if the person hands back before that, the result is `device_needs_setup` again, with their note. A handoff opened while the phone is locked ends by itself when the person unlocks from the computer, if unlocking is all its `reason` asks for ("Please unlock the phone"); one that asks for more ("Unlock the phone, then sign in to Wise") stays open until the person hands back, and so does any handoff after an unlock on the phone itself. The person may decline (`approval_denied`); with no answer it ends after 15 minutes. Blocks up to 45 s at a time, then `approval_pending`.

## Saving screenshots and reports

An agent that writes a bug report or a pull request can keep what it saw as files: `phone_observe { save: "onboarding-1.png" }` saves a screenshot, and `phone_release { export: true }` the task's report. Both write into the phone's folder on the computer, `<data>/files/<phone id>/` (the one `phone_files` uses), and nothing to the phone, so they ask nothing, except for a screen recording: read-only and watch-only agents may save, in read-only mode too. They work on every phone, iPhones included. A Pinky without a data folder returns `unsupported`.

- **A screenshot** is saved at the phone's full resolution: PNG, or JPEG for a name that ends in `.jpg` or `.jpeg`; a name without an extension gets `.png`. Names follow the rules of uploads ([above](#uploading-a-file)), other extensions are `invalid_argument`, and a file of that name is replaced. It is the screenshot `screenshot: true` returns, covered the same way: one-time codes and blocked apps' notifications under gray boxes, and none while the phone is locked or a blocked app is in front, an app the person allowed for the task included (Pinky keeps no picture of those). Then the result says why, ending "Nothing was saved."
- **The report** is written as the task ends: `pinky-task-<date>-<time>.html`, a page of the task's steps with the screenshots Pinky kept for its history (covered the same way), and a `.json` file with the same as data, as Activity's Export… writes them.
- **The screen recording** goes with the report only when the person allows it, as it covers neither one-time codes nor blocked apps' notifications. When the task was recorded, `phone_release { export: true }` first opens a card (rule `Recordings`, "Give Claude Code the screen recording of “Reorder lunch”?") and blocks up to 45 s, then returns `approval_pending`; repeat the same call with `approval_id`. The task keeps running meanwhile, and other actions on the phone return `approval_pending` with the same id. The card asks at every export: it offers no Allow for this task or Always allow, and no preset or rule answers it, for read-only and watch-only agents too. Taking over the phone leaves it open. If the person allows it, the recording is saved next to the report as `pinky-task-<date>-<time>-<task id>.mp4`, and the report plays it. If they decline or don't answer within 60 seconds, the task ends with the report alone and the result is `settled` all the same, not an error: its message says the recording was not given and why, with the person's `note` if they wrote one. If the lease ends while the card is open (the person pressed Stop), the result is `stopped` and nothing is saved. A task without a recording (recording off in Settings › Privacy, or nothing recorded yet) asks nothing.
- **On this computer** the result names each file on a `saved:` line; the agent reads or copies it with its own file tools.
- **Over HTTP** the result gives a command for each file instead: `curl -fsS -o 'onboarding-1.png' 'http://<address>/download/<token>'`, at the address the agent reached Pinky at, as for upload links. A link works once, for 10 minutes, and needs no agent token: its own random 128-bit token is the key. Unlike an upload link it outlives the session, so a client that closes its session right after `phone_release` still gets the files; turning Remote agents off ends every link. Unknown, used or expired links, and files no longer in the folder, get `404`, and methods other than `GET` get `405`. Files come as attachments, the report as `application/octet-stream`, so it opens from the agent's disk, never from Pinky's address.
- Each save is a step in the task's history: "Saved a screenshot as “onboarding-1.png”", "Saved the task's report for the agent" or "Saved the task's report and screen recording for the agent", after the card's own step.

## Approvals

- Which actions ask is the person's choice in Settings › Safety: payments only (the default); payments, messages, deletions and other commits, calls, installs, links that leave the app and settings changes; or every action. Their own rules may ask about more, and blocked apps and credential fields are refused whatever they chose.
- An Ask action opens an approval panel in the client and the call blocks for up to 45 s, sending progress notifications if the request carried a `progressToken` (at the start, then every 5 s, with the time left to answer).
- The person has 60 s from when the panel opens (configurable with `pinky daemon --approval-timeout`). No answer means Deny (`note: "No answer in 60 s."`).
- After 45 s the call returns `approval_pending` with the id. The panel stays open. A decision made while no call waits is kept for the next call with that id, for as long as the timeout again; later the id is gone and a call with it asks anew.
- The id is single-use and bound to the session, device, lease, tool and arguments (`intent` excluded). Another call with it returns `invalid_argument`.
- While an approval is open on a phone, other actions on it return `approval_pending` with that approval's id; reads still work.
- Before acting on Allow, Pinky reads the screen again and compares the target's role, label, states and position (a few pixels of tolerance) and the screen's text and states (status bar excluded; OCR text only when the screen has no element text). Any change returns `stale`.
- "Allow for this task" covers the rules that asked, in that app, for the rest of the task. An action that another rule also asks about still asks. `phone_request_approval` has no such answer: each question asks the person.
- "Always allow" keeps the answer in the rules file: in that app, the rule that asked, and the rules that asked with it, no longer ask about the steps that rule asks about, in later tasks too, until the person takes it back in Settings › Safety. The approved call's message says `from now on "Payments" no longer asks in Dinely`; a later such action returns `settled` at once, its message starting `No approval needed: the person always allows "Payments" in Dinely.` It never lifts a deny, read-only mode, the agent's scope or the task's `apps`, and it does not apply while every action asks. The panel offers it for actions only: not for `phone_request_approval`, `phone_files`, `phone_app`, `phone_launch`, `phone_logs` or `phone_device`.
- An MCP cancellation of the blocked call, a closed session, Stop and Take over all withdraw the panel without acting.
- Only the client's `Decide` answers approvals. No tool can.

## Progress notifications

Sent only when the request carries `_meta.progressToken`: while waiting for an approval or handoff (at the start and every 5 s), for `phone_observe` with `wait_ms`, and for `phone_wait_for` (every 5 s). `progress` and `total` are seconds.

## Limits

- `phone_device` changes are kept in memory: if Pinky quits during a task, or the phone is offline when it ends, they stay. An Android emulator's location is read back as the last place Pinky sent it, so one set in the emulator's extended controls meanwhile is replaced when the task ends.
- iPhones below iOS 27 without the WebDriverAgent helper (iOS 17.4 to 26, nothing installed) are read through the accessibility inspector and OCR and can launch apps, nothing more: `phone_tap`, `phone_swipe`, `phone_type` and `phone_key` return `unsupported`, with a sentence naming the helper; `phone_pinch` returns `unsupported` too. With the helper, everything works; reading the screen takes 0.5 to 3 s, and about 7 s on a home screen.
- iPhones through Device Hub (iOS 27 and later without the helper) cannot pinch: `phone_pinch` returns `unsupported` before it asks the person or touches the phone. With the WebDriverAgent helper chosen for the phone in Settings › Phones, they pinch.

| Limit | Value |
| --- | --- |
| Longest block of one call | 45 s |
| Approval timeout | 60 s |
| Handoff timeout | 15 min |
| Settle limit | 3 s, or less with `settle_ms` |
| Idle lease | 10 min |
| Reconnect grace | 60 s |
| `text` | 16384 characters |
| `phone_batch` steps | 50 |
| `phone_logs` result | 500 lines or 10 crash reports, 64 KB |
