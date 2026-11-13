# Usage statistics, crash reports and feedback

Pinky sends its developers anonymous **usage statistics** (counts) and **crash reports** after it quits unexpectedly, and lets the person send **feedback** as a GitHub issue. This page lists everything each one holds, where it is kept and how to turn it off. The client is in [`internal/telemetry`](../internal/telemetry); the backend that receives usage statistics and crash reports is the Cloudflare Worker in [`worker/`](../worker).

What never leaves the computer in usage statistics and crash reports: what is on a phone's screen, what is typed, app names, phone names, serial numbers and other phone identifiers, PINs, file names, task names and anything else about the person. Usage statistics are counters with fixed names; nothing the person or a phone produces becomes a name.

## Turning it off

- **Settings › Privacy › Share anonymous usage statistics and crash reports** turns both off. Turning it off also forgets what was counted and not yet sent; crash reports then stay on the computer.
- **`DO_NOT_TRACK=1`** in the environment (any value but `0`, `false`, `no` or `off`) stops both, whatever the setting.
- **`PINKY_TELEMETRY_URL=-`** sends nothing.
- **Development builds** (`go run ./cmd/pinky`, `go build`: version `0.0.0-dev`) send nothing unless `PINKY_TELEMETRY_URL` names a backend. With `--fake` (scripted fake phones) nothing is counted; crash reports still go out.

The setting is on by default, and Pinky shows no note about it on the first run: Settings › Privacy, this page and the README say so. **What's shared** in Settings › Privacy shows the usage report as it would go out at that moment.

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

- `install_id` is a random number made on the first run. It keeps one computer's reports together; it is not tied to the person, their Apple ID, the computer's name or its hardware. Deleting `telemetry.json` makes a new one.
- `os_version` is macOS's version, Windows' version and build, or a Linux distribution and its version (from `/etc/os-release`).
- Crash reports carry no install id, so they cannot be matched to usage statistics.

### The counters

Each counter counts within the report's period. `<platform>` is `android`, `ios` or `other`, and `android_emulator` or `ios_simulator` for virtual phones, which count apart from phones; the words in a name come from the lists below, never from a phone or the person.

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
| `results.<status>` | tool results, by status: `settled`, `uncertain`, `blocked_by_policy`, `approval_denied`, `approval_pending`, `stale`, `user_took_over`, `paused`, `stopped`, `device_locked`, `device_needs_setup`, `ok` (a result without a status); by error code: `error.device_offline`, `error.device_paused`, `error.lease_held`, `error.scope_denied`, `error.not_found`, `error.unsupported`, `error.invalid_argument`, `error.internal`, `error.other` |
| `needs.<kind>.asked` | what Pinky asked the person: `approval`, `new_agent` (an agent's first connection), `handoff`, `unlock` |
| `needs.approval.rule.<rule>` | approvals by the rule that asked, for Pinky's built-in rules (`payments`, `messages`, `commits`, `installs_and_links`, `calls`, `settings_changes`, `phone_settings`, `every_action`, …); the person's own rules count as `custom` |
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

Each Pinky process that runs the gateway (the app, or `pinky daemon`) asks the Go runtime to copy any crash (an unrecovered panic or a fatal error) to a file of its own in the data folder (`runtime/debug.SetCrashOutput`). A clean exit removes the file. At the next start Pinky finds the files of processes that are gone; those with a crash in them become crash reports, which go out at once while the setting is on, and the others count as `exits.unclean.*`. A report holds:

| Field | |
| --- | --- |
| `id`, `process` | the run (`app-<start>-<pid>`), and `app` or `daemon` |
| `version` | Pinky's version |
| `os`, `os_version`, `arch`, `go_version` | the computer's system |
| `started`, `crashed` | when the run started and crashed |
| `reason` | the crash's first line, such as `panic: runtime error: index out of range [3] with length 3` or `SIGSEGV: segmentation violation`. For fatal errors the runtime prints its message before the copy starts, so the reason is then the function that failed, or the line from `daemon.log` for a daemon that `pinky mcp` started |
| `stack` | what the runtime printed (up to 256 KB): function names, Pinky's source files and line numbers |

The home folder, the user's name, e-mail and IP addresses, long hex strings (tokens) and similar are taken out of the reason and the stack before the report is saved. With the setting off, reports stay in the data folder, where feedback's diagnostics mention the last one; at most 10 are kept, none longer than 30 days.

## Feedback

**Help › Send Feedback…**, **Send Feedback…** in the menu bar icon's menu, and **Send feedback…** under Settings' sections open a form: a title (optional), a message, and **Attach diagnostics** (on by default), with **Review** to read exactly what is attached. **Open on GitHub** opens a new issue in [DIYgod/pinky](https://github.com/DIYgod/pinky/issues) with all of it filled in ([the Feedback issue form](../.github/ISSUE_TEMPLATE/feedback.yml)); the person reads it there and submits it with their GitHub account. **Issues are public.** Nothing is sent until they submit it on GitHub.

Diagnostics hold:

- Pinky's version, the computer's system, whether usage statistics are on, and the last crash's reason when there is one;
- the settings that change how Pinky behaves (the approval preset, read-only mode, unlocking from the computer, hiding codes, recording, how long activity is kept, remote agents, developer mode) and the video decoder;
- each connected phone's platform, system version, model, connection, backend and state;
- Tailscale's state on the computer, the Android devices on its tailnet (online or not, which are Pinky's phones, by model, and whether Pinky reaches them), and each Android phone's **Use away from home** (on or off, and what keeps it from being set up); the tailnet's name and the devices' names are left out;
- the end of `pinky.log` and `daemon.log`, as much as fits in the issue's address (the last 300 lines at most).

Phone ids and the names people give their phones, the home folder, the user's name, e-mail and IP addresses, long hex strings, adb's Wi-Fi names for phones, and the values of log fields that may name apps, tasks, files or what a screen showed (`app=`, `task=`, `name=`, `path=`, …) are replaced with placeholders such as `<phone-1>` and `<redacted>`. A link to GitHub holds about 7 KB, so older log lines are left out when they do not fit; **Copy** puts the message and the whole diagnostics on the clipboard, to paste into the issue or send another way.

## Where it is kept

In Pinky's data folder (macOS `~/Library/Application Support/Pinky`, Linux `~/.local/share/pinky`, Windows `%LOCALAPPDATA%\Pinky`, or `$PINKY_HOME/data`):

| File | |
| --- | --- |
| `telemetry.json` | the install id, the counters since the last report, the hashes of the phones seen, when the last report went out |
| `crashes/<process>-<start>-<pid>.run` | one per running process; its crash, if it crashes |
| `crashes/<id>.json` | a crash report not sent yet |
| `crashes/<id>.unclean` | a run that ended without quitting, until it is counted |

All are readable only by the user. Settings › Privacy's choice is `UsageStats` in `settings.json`.

## Where it goes

Usage statistics and crash reports are posted as JSON over HTTPS to the backend's `/v1/usage` and `/v1/crash`, with `User-Agent: Pinky/<version> (<os>; <arch>)`. A report that fails to go out is tried again later (after an hour, doubling up to a day); one the backend refuses (HTTP 4xx) is dropped.

The backend is a Cloudflare Worker with a D1 database ([`worker/`](../worker)), at `https://pinky-telemetry.rss3.workers.dev`. It checks each report's shape and size, stores it, and keeps usage reports for 400 days and crash reports for 180. It does not store IP addresses or any request header, and has Workers Logs off; Cloudflare sees the address of the connection, as any server would, and the Worker uses it for a minute to limit how often one address may report.

The backend's address is `telemetry.Endpoint` in [`internal/telemetry/telemetry.go`](../internal/telemetry/telemetry.go) (a build may set it with `-ldflags "-X github.com/DIYgod/pinky/internal/telemetry.Endpoint=…"`), or `PINKY_TELEMETRY_URL`. The client talks to it through the `telemetry.Backend` interface, so another backend can replace it.

## Checking it

Automated: `go test ./internal/telemetry ./ui` (the crash tests run the test binary again as a child that panics, hits a fatal error, exits or hangs), `go test -tags e2e -run TestTelemetryInTheApp ./cmd/pinky` (the app over MyGo's automation socket with a fake backend: Send Feedback's GitHub link, a SIGQUIT crash sent at the next start, a clean quit that leaves nothing; it needs the screen unlocked), and `node --test` plus `./smoke.sh` in `worker/` ([worker/README.md](../worker/README.md)).

By hand, with a release build (`make app`; development builds send nothing):

- [ ] Settings › Privacy shows **Share anonymous usage statistics and crash reports** (on) and **What's shared**, which shows the report with counters such as `app.starts`.
- [ ] `DO_NOT_TRACK=1 build/pinky/darwin-arm64/Pinky.app/Contents/MacOS/Pinky`: the switch reads *Off: DO_NOT_TRACK is set on this computer*.
- [ ] Help › **Send Feedback…**, write something, **Open on GitHub**: the browser opens a new issue in DIYgod/pinky with the Feedback form filled in (the message, the version line, the diagnostics). This needs the form on `main`, a `feedback` label for it to apply, and, for people other than the repository's members, the repository to be public.
- [ ] `kill -QUIT $(pgrep -x Pinky)`, then open Pinky again: within a minute `worker/report.sh crashes` lists the crash, `SIGQUIT: quit`.
- [ ] A day after the first run, `worker/report.sh` lists this computer's install among the installs, with its counters.
