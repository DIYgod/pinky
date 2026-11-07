# Pinky MCP reference

Pinky's gateway serves one MCP server, `pinky`, with 14 tools. Agents launch `pinky mcp`, a stdio shim that forwards to the running gateway over its user-only Unix socket (and starts the gateway first when none runs: the Pinky app on macOS, `pinky daemon` elsewhere). Agents that cannot start a command can use Streamable HTTP instead (below). The agent-facing guide is [SKILL.md](SKILL.md); the design is in [DESIGN.md](DESIGN.md), section "Agent interface".

## Connecting an agent

Claude Code:

```sh
claude mcp add pinky -- pinky mcp
```

Any client that takes a JSON config:

```json
{ "mcpServers": { "pinky": { "command": "pinky", "args": ["mcp"] } } }
```

To try Pinky without a phone, use `pinky mcp --fake` (it starts `pinky daemon --fake` when no gateway runs) or run `pinky daemon --fake` yourself first.

### Over HTTP

For agents on another machine, in a container, or clients that speak only HTTP, Pinky serves the same tools over Streamable HTTP. It is off by default: turn on Settings › Remote agents in the app, or run `pinky daemon --http 127.0.0.1:7465`. The endpoint is `http://<address>/mcp`, on this computer only (`127.0.0.1:7465`) unless the person picks another address. Every request needs the agent token as a bearer token; `pinky token` prints it.

Claude Code:

```sh
claude mcp add --transport http pinky http://127.0.0.1:7465/mcp --header "Authorization: Bearer $(pinky token)"
```

A JSON config:

```json
{ "mcpServers": { "pinky": { "type": "http", "url": "http://127.0.0.1:7465/mcp", "headers": { "Authorization": "Bearer <token>" } } } }
```

- Without the token, or with another one, requests get `401`. Browser pages from other origins get `403`, and so do requests to `127.0.0.1` that name another host (DNS rebinding).
- Each MCP session over HTTP is one Pinky session, with the same first-connection approval and scopes as over stdio. The agent's name gets " (remote)" after it ("Claude Code (remote)"), so a remote agent is asked about on its own first connection and never gets the scope the person gave the agent of that name on this computer. Closing it (`DELETE`), an hour without requests, or turning Remote agents off ends it, with its leases and open approvals.

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

- The first line is the status, or `error: <code>` for errors (then the MCP result also has `isError: true`). Details follow as `key: value`, separated by ` · `: `approval_id`, `rule`, `decision`, `settle`, `matched`, `holder`, and `no change` when the screen did not change.
- The screen is the format in DESIGN.md: a header line, then events (`toast:`, `notification:`, `dialog:`), then one element per line `[ref] role "label" @x,y state`, then `ocr` lines.
- After an action, only the changes are sent: `+` added, `-` removed, `~` changed (including `~ screen …` for a changed header). A new app, size, orientation or lock state, or a diff longer than the screen, sends the whole screen.
- `phone_connect`, `phone_observe` and `phone_handoff` always send the whole screen.
- A blocked app shows as `app: blocked app` with no elements; its toasts and dialogs are dropped, and no screenshot is returned.

### Statuses

| Status | Meaning |
| --- | --- |
| `settled` | The call ran and the screen stopped changing, or settling hit its 3 s limit (`settle: timeout`) |
| `uncertain` | The action may have happened (the driver failed mid-action, the phone dropped). Observe before anything else |
| `blocked_by_policy` | A rule denied the action; `rule` names it (`credentials` for password and code fields, `Read-only`, `Finance apps`, …) |
| `approval_denied` | The person said no, or nobody answered in time; `note` may say why |
| `approval_pending` | The call blocked for 45 s; repeat it with `approval_id` |
| `stale` | The screen changed while the approval waited, so nothing was done |
| `user_took_over` | The person took over; returned at once with the current screen. Later calls return `paused` until they hand back |
| `paused` | The person is in control. Wait with `phone_handoff` |
| `stopped` | The person pressed Stop; the lease is gone, and this session may not use that phone again |
| `device_locked` | The phone is locked. Call `phone_handoff` |

### Errors

| Error | Meaning |
| --- | --- |
| `device_offline` | The phone is not connected. A lease survives 60 s of disconnection |
| `lease_held` | Another session holds the phone; `holder` names its agent and task |
| `scope_denied` | The app is outside the agent's scope or the task's app list |
| `not_found` | The ref is gone, or no installed app matches |
| `unsupported` | The phone or backend cannot do this, such as `recents` on iPhone, or `phone_files` on iPhone |
| `invalid_argument` | Bad or missing arguments, a misused `approval_id`, or no phone connected in this session |
| `internal` | The call was cancelled, or the gateway failed |

## Common arguments

Every action tool (`phone_tap`, `phone_type`, `phone_swipe`, `phone_key`, `phone_launch`) takes:

| Argument | Type | Meaning |
| --- | --- | --- |
| `device` | string | The phone, by id, name or a unique id prefix. Required when the session holds more than one phone |
| `intent` | string | One sentence on why, shown on the approval panel and in history |
| `approval_id` | string | From `approval_pending`: repeat the same call with it to keep waiting |

`phone_observe`, `phone_apps`, `phone_wait_for` and `phone_release` take `device`; `phone_files`, `phone_request_approval` and `phone_handoff` take `device` and `approval_id` (and `phone_files` takes `intent`). Unknown arguments are rejected.

## Tools

### phone_connect

Leases a phone for a task and returns its first screen.

| Argument | Type | Meaning |
| --- | --- | --- |
| `task` | string, required | One line naming the task, shown to the person |
| `device` | string | Optional when exactly one phone is online, or the session already holds one |
| `apps` | string[] | Optional app allow-list for the task (names or ids). Launching another app returns `scope_denied`; typing into one asks the person. Launchers and system UI are always allowed |

The first connection of an agent the person has not approved yet returns `approval_pending` (rule `New agent`); repeat with `approval_id`. Another session's phone returns `lease_held`. Calling again with the same task keeps the lease; another task name ends the previous task (outcome `replaced`) and starts a new one. After the person pressed Stop, the session cannot connect to that phone again. While the person is in control of the phone (they took over and have not handed back, whichever task was running), the lease starts paused and the result is `paused`: call `phone_handoff`.

### phone_release

Ends the task and frees the phone.

| Argument | Type | Meaning |
| --- | --- | --- |
| `outcome` | `done` \| `failed` \| `partial` \| `cancelled` | Default `done` |
| `summary` | string | For the person's history |
| `device` | string | Default: every phone the session holds |

### phone_observe

Returns the whole current screen.

| Argument | Type | Meaning |
| --- | --- | --- |
| `wait_ms` | 0–30000 | Wait until the screen differs from what the agent last saw (then let it settle), or until this passes |
| `screenshot` | boolean | Add a JPEG screenshot, in the screen's pixel space. None while the phone is locked or a blocked app is in front |

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

### phone_key

| Argument | Type | Meaning |
| --- | --- | --- |
| `key` | `back` `home` `recents` `enter` `tab` `delete` `forward_delete` `escape` `dpad_up` `dpad_down` `dpad_left` `dpad_right` `page_up` `page_down` | Keys the phone lacks return `unsupported` |

`home` always works, even in a blocked app, except on an iPhone below iOS 27 without the WebDriverAgent helper (see Limits).

### phone_launch

| Argument | Type | Meaning |
| --- | --- | --- |
| `app` | string, required | Package name, bundle id, or name (exact, then a unique part of it) |

Blocked apps return `blocked_by_policy`; apps outside the agent's scope or the task's list return `scope_denied`; unknown apps return `not_found`. A URL (`https://…`) counts as opening a link (rule `Installs and links`), which asks the person when they chose to approve more than payments.

### phone_apps

| Argument | Type | Meaning |
| --- | --- | --- |
| `query` | string | Only apps whose name or id contains it |

Lists installed apps inside the agent's scope, user apps first, one per line: `Name (id)`, with `· system` for system apps. Blocked apps are never listed.

### phone_batch

| Argument | Type | Meaning |
| --- | --- | --- |
| `steps` | array, ≤ 50 | Each step has `tool` (`tap`, `type`, `swipe`, `key`, `launch`, with or without the `phone_` prefix) and that tool's arguments, plus its own `intent` and `approval_id` |
| `intent` | string | For steps without their own |

Steps run in order, each settling first, and the batch stops at the first step that does not settle. The result's first line is the last step's status; then `step N:` blocks follow with each step's status and changes. To finish a batch that stopped at `approval_pending`, repeat that step alone as its own tool call with the `approval_id`.

### phone_wait_for

| Argument | Type | Meaning |
| --- | --- | --- |
| `text` | string, required | Matched without case in element labels and new events |
| `gone` | boolean | Wait for it to disappear |
| `timeout_ms` | 0–45000 | Default 10000 |

Returns `settled` with `matched: true` or `matched: false` and the screen changes.

### phone_files

Copies files between the phone's Pinky folder (`/sdcard/Pinky` on Android, the only place on the phone agents may reach) and a folder on the computer, `<data>/files/<phone id>/`, which the result names. Agents read and write that folder with their own file tools. Needs a `phone_connect` lease like the other tools. iPhones return `unsupported`.

| Argument | Type | Meaning |
| --- | --- | --- |
| `action` | `list` \| `pull` \| `push` | `list` (default) shows both sides; `pull` copies from the phone; `push` copies to the phone |
| `paths` | string[] | Files or folders, relative to the Pinky folder. Default: everything; for `push`, the files that are new or changed on the computer |
| `intent` | string | For `push`: one sentence on why, shown on the approval panel |
| `approval_id` | string | From `approval_pending`: repeat the same call with it |

The result lists the folder, then one line per file: `path · size · modified · where`, where `where` is `both` (same size and time), `phone`, `computer` or `differs`. `pull` and `push` list the files they copied and skip files that are the same on both sides; copied files keep their modification time.

- `push` asks the person first (rule `Files`), like an Ask action: up to 45 s, then `approval_pending`. "Allow for this task" covers later pushes in the task. Read-only mode refuses it (`blocked_by_policy`, rule `Read-only`), and read-only and watch-only agents get `scope_denied`: they may only list and pull.
- Paths that leave the folder (`..`, absolute paths) are `invalid_argument`; files that are not there are `not_found`. Symbolic links in the computer's folder are not copied, and a pull replaces a link in its way instead of writing through it.
- Each call is a step in the task's history ("Copied 2 files from the phone: a.txt, b.png").

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

Shows a Needs you card and waits until the person hands control back; returns `settled` with their `note` and the whole screen. If they already handed back before the call, it returns at once. A handoff opened while the phone is locked ends by itself when the person unlocks from the computer; after an unlock on the phone itself, the person hands back explicitly. The person may decline (`approval_denied`); with no answer it ends after 15 minutes. Blocks up to 45 s at a time, then `approval_pending`.

## Approvals

- Which actions ask is the person's choice in Settings › Safety: payments only (the default); payments, messages, deletions and other commits, calls, installs, links that leave the app and settings changes; or every action. Their own rules may ask about more, and blocked apps and credential fields are refused whatever they chose.
- An Ask action opens an approval panel in the client and the call blocks for up to 45 s, sending progress notifications if the request carried a `progressToken` (at the start, then every 5 s, with the time left to answer).
- The person has 60 s from when the panel opens (configurable with `pinky daemon --approval-timeout`). No answer means Deny (`note: "No answer in 60 s."`).
- After 45 s the call returns `approval_pending` with the id. The panel stays open. A decision made while no call waits is kept for the next call with that id, for as long as the timeout again; later the id is gone and a call with it asks anew.
- The id is single-use and bound to the session, device, lease, tool and arguments (`intent` excluded). Another call with it returns `invalid_argument`.
- While an approval is open on a phone, other actions on it return `approval_pending` with that approval's id; reads still work.
- Before acting on Allow, Pinky reads the screen again and compares the target's role, label, states and position (a few pixels of tolerance) and the screen's text and states (status bar excluded; OCR text only when the screen has no element text). Any change returns `stale`.
- "Allow for this task" covers the rules that asked, in that app, for the rest of the task. An action that another rule also asks about still asks. `phone_request_approval` has no such answer: each question asks the person.
- "Always allow" keeps the answer in the rules file: in that app, the rule that asked, and the rules that asked with it, no longer ask about the steps that rule asks about, in later tasks too, until the person takes it back in Settings › Safety. The approved call's message says `from now on "Payments" no longer asks in Dinely`; a later such action returns `settled` at once, its message starting `No approval needed: the person always allows "Payments" in Dinely.` It never lifts a deny, read-only mode, the agent's scope or the task's `apps`, and it does not apply while every action asks. The panel offers it for actions only: not for `phone_request_approval`, `phone_files` or `phone_launch`.
- An MCP cancellation of the blocked call, a closed session, Stop and Take over all withdraw the panel without acting.
- Only the client's `Decide` answers approvals. No tool can.

## Progress notifications

Sent only when the request carries `_meta.progressToken`: while waiting for an approval or handoff (at the start and every 5 s), for `phone_observe` with `wait_ms`, and for `phone_wait_for` (every 5 s). `progress` and `total` are seconds.

## Limits

- iPhones below iOS 27 without the WebDriverAgent helper (iOS 17.4 to 26, nothing installed) read the screen through the accessibility inspector and OCR, and can only press listed elements and launch apps: `phone_swipe`, `phone_type`, `phone_key`, `long_press` and taps at points with no listed element return `unsupported` (or `not_found` for the tap), with a sentence naming the helper. With the helper, everything works; reading the screen takes 0.5 to 3 s.

| Limit | Value |
| --- | --- |
| Longest block of one call | 45 s |
| Approval timeout | 60 s |
| Handoff timeout | 15 min |
| Settle limit | 3 s |
| Idle lease | 10 min |
| Reconnect grace | 60 s |
| `text` | 16384 characters |
| `phone_batch` steps | 50 |
