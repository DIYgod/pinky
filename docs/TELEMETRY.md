# Usage statistics, crash reports and feedback

Pinky can send three things to its developers: anonymous **usage statistics** (counts), **crash reports** after Pinky quits unexpectedly, and **feedback** the person writes. This page lists everything each one holds, where it is kept and how to turn it off. The code is in [`internal/telemetry`](../internal/telemetry).

What never leaves the computer, in any of them: what is on a phone's screen, what is typed, app names, phone names, serial numbers and other phone identifiers, PINs, file names, task names and anything else about the person. Usage statistics are counters with fixed names; nothing the person or a phone produces becomes a name.

## Turning it off

- **Settings › Privacy › Share anonymous usage statistics** turns usage statistics off. Turning it off also forgets what was counted and not yet sent.
- **`DO_NOT_TRACK=1`** in the environment (any value but `0`, `false`, `no` or `off`) stops usage statistics whatever the setting, and stops crash reports from going out without asking.
- **`PINKY_TELEMETRY_URL=-`** sends nothing at all: no usage statistics, no crash reports, no feedback.
- **Development builds** (`go run ./cmd/pinky`, `go build`: version `0.0.0-dev`) send nothing unless `PINKY_TELEMETRY_URL` names a backend. `--fake` (scripted fake phones) counts nothing.

Usage statistics are on by default. On the first run a note at the top of the window says so, with **What's shared**, **Turn off** and **OK**; nothing is sent until the person has answered it (or turned the setting on in Settings). A daemon that runs without the app (`pinky mcp` starts one when no Pinky is running) keeps counting but sends nothing until the note has been answered in the app.

## Usage statistics

About once a day (at most once every 24 hours, and only while Pinky runs), Pinky posts one report:

```json
{
  "install_id": "8c1f0e6b2a7d4e5f9a0b1c2d3e4f5a6b",
  "version": "0.2.0",
  "os": "macos",
  "os_version": "27.2",
  "arch": "arm64",
  "period_start": "2026-11-07T09:00:00Z",
  "period_end": "2026-11-08T09:12:00Z",
  "counts": {
    "app.starts": 2,
    "phones.android.wifi": 1,
    "phones.ios.usb": 1,
    "phones.max_online": 2,
    "runs.started": 5,
    "runs.agent.claude_code": 5,
    "runs.done": 4,
    "tools.phone_tap": 31,
    "results.settled": 52,
    "needs.approval.asked": 2,
    "needs.approval.allow_once": 1,
    "needs.approval.deny": 1
  }
}
```

**Settings › Privacy › What's shared** shows the report as it would go out at that moment.

- `install_id` is a random number made on the first run. It keeps one computer's reports together; it is not tied to the person, their Apple ID, the computer's name or its hardware. Deleting `telemetry.json` makes a new one.
- `os_version` is macOS's version, Windows' version and build, or a Linux distribution and its version (from `/etc/os-release`).
- Crash reports and feedback carry no install id, so they cannot be matched to usage statistics.

### The counters

Each counter counts within the report's period. `<platform>` is `android`, `ios` or `other`; the words in a name come from the lists below, never from a phone or the person.

| Counter | What it counts |
| --- | --- |
| `app.starts`, `daemon.starts` | Pinky started, as the app or as the background daemon |
| `crashes.app`, `crashes.daemon` | crashes found at the next start (the crash report itself is separate, below) |
| `exits.unclean.app`, `exits.unclean.daemon` | runs that ended without quitting and without a crash: killed, force-quit, the power went out |
| `phones.<platform>.usb`, `.wifi`, `.other` | different phones seen, by connection |
| `phones.<platform>.os.<major>` | different phones seen, by the first number of their system version (`17`, `27`) |
| `phones.<platform>.backend.<name>` | different phones seen, by how Pinky drives them: `scrcpy`, `companion`, `adb`, `coredevice`, `wda`, `mirroring`, `other` |
| `phones.<platform>.state.<state>` | different phones that waited for setup: `unauthorized` (not trusted yet), `needs_setup` |
| `phones.max_online` | the most phones connected at once |
| `runs.started`, `.done`, `.stopped`, `.failed`, `.paused` | agent tasks, and how they ended (`paused`: the person took over) |
| `runs.agent.<agent>` | agent tasks by agent product: `claude_code`, `claude`, `codex`, `cursor`, `windsurf`, `vs_code`, `gemini_cli`, `goose`, `cline`, `zed`, `opencode`, `continue`, `mcp_inspector`, `agent` (no name given) or `other` |
| `runs.remote`, `runs.background` | agent tasks over HTTP (remote agents), and on a background display |
| `agents.connected.<agent>` | MCP sessions, by agent product as above |
| `agents.scope.<mode>` | scopes the person gave agents: `everything`, `read_only`, `watch_only`, `apps` |
| `tools.<tool>` | tool calls, by tool: `phone_connect`, `phone_tap`, … (Pinky's own tool names; anything else is `other`) |
| `results.<status>` | tool results, by status: `settled`, `uncertain`, `blocked_by_policy`, `approval_denied`, `approval_pending`, `stale`, `user_took_over`, `paused`, `stopped`, `device_locked`, `ok` (a result without a status); by error code: `error.device_offline`, `error.lease_held`, `error.scope_denied`, `error.not_found`, `error.unsupported`, `error.invalid_argument`, `error.internal`, `error.other` |
| `needs.<kind>.asked` | what Pinky asked the person: `approval`, `new_agent` (an agent's first connection), `handoff`, `unlock` |
| `needs.approval.rule.<rule>` | approvals by the rule that asked, for Pinky's built-in rules (`payments`, `messages`, `commits`, `installs_and_links`, `calls`, `settings_changes`, `every_action`, …); the person's own rules count as `custom` |
| `needs.<kind>.<answer>` | how the person answered: `allow_once`, `allow_task`, `allow_always`, `deny`, `take_over`, or `error` |
| `needs.<kind>.unanswered` | needs that went away without an answer (timed out, or the agent left) |
| `unlock.<result>` | unlocking a phone from the computer: `ok`, `wrong_pin`, `no_pin`, `cancelled`, `no_keychain`, `unavailable`, `locked`, `offline`, `not_found`, `timeout`, `unsupported`, `failed` |
| `person.take_over`, `.hand_back`, `.stop`, `.stop_all` | the person taking a phone over, handing it back, stopping a task, and Stop all |
| `pairing.qr.started`, `.paired`, `.cancelled`; `pairing.code.paired` | pairing a phone over Wi-Fi, by QR code or with a code; a failure counts as `pairing.qr.<error>` or `pairing.code.<error>` |
| `companion.installed`, `companion.install.<error>` | installing the Android companion app |
| `background.turned_on`, `.turned_off` | background displays switched in Pinky |
| `history.exported`, `history.export.<error>`, `history.wiped` | exporting activity, and deleting all of it |

`<error>` is one of `cancelled`, `timeout`, `unsupported`, `offline`, `locked`, `not_found`, `unavailable` or `failed`: errors are sorted into these kinds and their messages are never kept.

A phone counts once a period in each bucket. To tell phones apart, Pinky keeps a salted hash of each phone's id in `telemetry.json`; the hashes and the salt never leave the computer, and the report carries only how many phones each bucket saw.

## Crash reports

Each Pinky process that runs the gateway (the app, or `pinky daemon`) asks the Go runtime to copy any crash (an unrecovered panic or a fatal error) to a file of its own in the data folder (`runtime/debug.SetCrashOutput`). A clean exit removes the file. At the next start Pinky finds the files of processes that are gone; those with a crash in them become crash reports, the others count as `exits.unclean.*`.

The app then asks: **Pinky quit unexpectedly … Send a crash report to help fix it?**, with **Show the report**, **Don't Send**, **Send Report** and **Send crash reports automatically from now on** (also in Settings › Privacy). A report holds:

| Field | |
| --- | --- |
| `version`, `process` | Pinky's version, and `app` or `daemon` |
| `os`, `os_version`, `arch`, `go_version` | the computer's system |
| `started`, `crashed` | when the run started and crashed |
| `reason` | the crash's first line, such as `panic: runtime error: index out of range [3] with length 3`. For fatal errors the runtime prints its message before the copy starts, so the reason is the function that failed, or the line from `daemon.log` for a daemon `pinky mcp` started |
| `stack` | what the runtime printed (up to 256 KB): function names, Pinky's source files and line numbers |

The home folder, the user's name, e-mail and IP addresses, long hex strings (tokens) and similar are taken out of the reason and the stack before the report is saved, so what the dialog shows is what is sent. Reports wait in the data folder until sent or dismissed; at most 10 are kept, none longer than 30 days. A crash report never goes out without the person's answer unless they chose to send them automatically, and not then while `DO_NOT_TRACK` is set.

## Feedback

**Help › Send Feedback…**, **Send Feedback…** in the menu bar icon's menu, and **Send feedback…** under Settings' sections open a form: a message, an e-mail address if the person would like an answer (optional), and **Attach diagnostics** (on by default), with **Review** to read exactly what is attached:

- Pinky's version, the computer's system, the usage and crash report settings;
- the settings that change how Pinky behaves (the approval preset, read-only mode, unlocking from the computer, hiding codes, recording, how long activity is kept, remote agents, developer mode) and the video decoder;
- each connected phone's platform, system version, model, connection, backend and state;
- the last 300 lines of `pinky.log` and `daemon.log`.

Phone ids and the names people give their phones, the home folder, the user's name, e-mail and IP addresses, long hex strings, adb's Wi-Fi names for phones, and the values of log fields that may name apps, tasks, files or what a screen showed (`app=`, `task=`, `name=`, `path=`, …) are replaced with placeholders such as `<phone-1>` and `<redacted>`. **Copy** puts the message and diagnostics on the clipboard instead, to send another way. Feedback is sent when the person presses Send, whatever the usage setting.

## Where it is kept

In Pinky's data folder (macOS `~/Library/Application Support/Pinky`, Linux `~/.local/share/pinky`, Windows `%LOCALAPPDATA%\Pinky`, or `$PINKY_HOME/data`):

| File | |
| --- | --- |
| `telemetry.json` | the install id, the counters since the last report, the hashes of the phones seen, when the note was answered and the last report went out |
| `crashes/<process>-<start>-<pid>.run` | one per running process; its crash, if it crashes |
| `crashes/<id>.json` | a crash report waiting for the person's answer |
| `crashes/<id>.unclean` | a run that ended without quitting, until the next count |

All are readable only by the user. Settings › Privacy's choices are `UsageStats` and `CrashReports` in `settings.json`.

## Where it goes

Reports are posted as JSON over HTTPS to the backend's `/v1/usage`, `/v1/crash` and `/v1/feedback`, with `User-Agent: Pinky/<version> (<os>; <arch>)`. A report that fails to go out is tried again later (after an hour, doubling up to a day); one the backend refuses (HTTP 4xx) is dropped. The backend sees the computer's IP address, as any server does; Pinky's own backend does not store it.

The backend's address is `telemetry.Endpoint` in [`internal/telemetry/telemetry.go`](../internal/telemetry/telemetry.go) (a build may set it with `-ldflags "-X github.com/DIYgod/pinky/internal/telemetry.Endpoint=…"`), or `PINKY_TELEMETRY_URL`. The client talks to it through the `telemetry.Backend` interface, so another backend can replace it.
