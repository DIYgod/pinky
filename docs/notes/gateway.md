# Gateway, MCP and local API

Branch `claude/gateway-core-da93b5`. This covers the packages the main session owned in [WORKING.md](../WORKING.md): `internal/gateway` (except the shared `api.go`), `internal/policy`, `internal/approval`, `internal/history`, `internal/mcp` and `cmd/pinky`, plus two new packages, `internal/api` (the local socket, listed in DESIGN's repository layout) and `internal/demo` (scripted fake phones). The first round used fake phones only; the second ran the gateway on the Mi 10 Pro and the iPhone (see "Real phones") and added the Settings API.

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
| History | SQLite through `modernc.org/sqlite` with the tables from DESIGN (devices, agents, sessions, tasks, steps, approvals, media); a JPEG before and after each step in `media/`, taken in the background (see "What the agent waits for"); `WipeHistory` |
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

Real phones (build tag `device`): `internal/gateway/device_test.go` runs the gateway in-process with the real provider for one phone and drives a session like an agent: Calculator (6 × 7 = 42, one tap at a time and as one `phone_batch`), Settings (scroll, open a sub-page, back, home), a Settings switch that must ask and is denied, ref stability, take over, hand back, Stop mid-action, and the stored screenshots. Every input passes `guardDevice` (`guard_test.go`), which refuses anything outside Calculator keys and Settings navigation and any tap on a switch, whatever the gateway decides, and stops without input when the phone shows anything unexpected or is locked. Build once, then hold the phone's lock only for the run:

```sh
go test -c -tags device -o /tmp/pinky-gw/gateway.test ./internal/gateway
scripts/devlock.sh 3e8be48e env PINKY_DEVICE=3e8be48e /tmp/pinky-gw/gateway.test -test.run 'TestDevice$' -test.v
scripts/devlock.sh 00008110-001E30C9262A801E env PINKY_DEVICE=00008110-001E30C9262A801E /tmp/pinky-gw/gateway.test -test.run 'TestDevice$' -test.v
```

Folders: `$PINKY_HOME/{config,data,run}` when set; otherwise `~/Library/Application Support/Pinky` on macOS, XDG folders on Linux. Config holds `rules.yaml`, `apps.yaml` (both written with defaults on first start) and `agent-token` (0600). Data holds `pinky.db`, `media/` and `daemon.log`; a `--fake` gateway keeps its history in `data/fake`.

## Real phones

On 2026-11-07, the gateway ran in-process with the real drivers through the device test above, one phone at a time under its lock, using only the home screen, the Calculator and Settings. Last runs: the Mi at 04:06, the iPhone at 04:09; both passed every part.

| | Mi 10 Pro (Android 17, LineageOS) | iPhone 13 mini (iOS 27.0.1) |
| --- | --- | --- |
| Connect (first screen) | 2.2 s | 2.1 s (15 s once, while OCR loaded in a new process) |
| Calculator 6 × 7 = 42, tap by tap | Yes, by ref | Yes: digits by ref; ×, = by x and y (they have no position, see below) |
| The same five taps as one `phone_batch` | 21.6 s, 4.3 s a step | 11.9 s, 2.4 s a step |
| Settings: open, sub-page, back, scroll, home | Yes (Display) | Yes (Bluetooth) |
| A Settings switch asks, Deny, unchanged | Yes: Adaptive brightness asked under "Settings changes" after 2.7 s; still on afterwards (also `settings get system screen_brightness_mode` = 1 before and after) | Yes: Airplane Mode asked after 2.0 s; unchanged |
| Refs on two reads of one screen | 32 of 32 kept | 26 of 26 kept |
| Take over, then `user_took_over`, then `paused`; hand back | Yes | Yes |
| Stop while an action reads the screen | `stopped`, no input reached the phone | Same |
| History | 22 steps, 41 screenshot references, all readable JPEG | 22 steps, 39 |
| Lock state (`LockReporter`) | Unlocked, 55 to 70 ms | Unlocked, 17 to 32 ms |
| Read (`Observe`), median | 2.24 s (`uiautomator dump`) | 1.40 s (walk and OCR); 0.7 s in the Calculator |
| Settle (`Settler`), median | 1.25 s, never hit the 3 s limit | 1.19 s; hit the limit 4 times in 19 (launches, scrolls, the home screen) |
| Screenshot, median | 0.35 s | 0.34 s |
| Action from call to result, median | 6.1 s (read 2.2, settle 1.3, screenshot 0.35, read 2.2) | 6.0 s |

What the runs changed in the gateway:

- **Third-party launchers count as system apps.** The Mi's launcher is Lawnchair (`app.lawnchair.play`), which the app table did not know; agent scopes and task app lists would have treated its home screen as an app outside the list. Lawnchair, Niagara, Smart Launcher, Microsoft Launcher, POCO, Hyperion, Kvaesitso and some vendor launchers are added.
- **History screenshots no longer cost a screen read.** The check that no blocked app was captured used one more `Observe` per screenshot, 2.2 s a step on the Mi. History and approval-panel screenshots now come right before the step's own read, which checks them; only screenshots an agent receives keep the extra read. (Later the same night history screenshots left the agent's wait altogether; see "What the agent waits for".)
- **`phone_batch` steps start from the previous step's read** when it settled less than a second before, which saves one read a step (Mi: 4.3 s instead of 6.1 s a step). Separate tool calls always read again, since the agent's thinking time leaves room for the screen to change.
- **The read limit is 60 s** (was 20 s): the iPhone's first read took 15 s while OCR loaded.
- **A ref that is on the screen without a position gets one more read** before `not_found`, because OCR placement on the iPhone varies from read to read.
- Switch approvals read "Change “Adaptive brightness”?" instead of "Tap …".
- Defaults stay: `SettleMax` 3 s fits both phones' settle times; `PollInterval` only matters for drivers without a `Settler` and for `phone_wait_for`.

A note for whoever runs the device test: one Mi run reported "the switch changed after Deny". It was the test's own check comparing a different element with the same label; the switch had not changed (the setting read 1 before and after, and the guard saw no tap on it). The check now compares the same element.

## What the agent waits for

After the Android driver moved to the companion app (a read takes 70 ms instead of 2.2 s), the gateway's own share of each action became visible: an action waited for a read, a screenshot for the step's "before" picture whenever the screen had changed, the input, settle, a screenshot for the "after" picture, and a read. Now no screenshot is on that path:

- **The result returns once the read after the action is done.** The history screenshot is taken in the background after that read. The next action reuses it as its "before" picture when the screen still matches: same app, size, lock and keyboard, and the same tree elements by role, label and state outside the status bar (refs, positions and OCR lines do not count, since OCR placement varies between reads). Nothing takes a screenshot before an action any more. Other screens shown to the agent (`phone_connect`, `phone_observe`, `phone_wait_for`, a hand-back) get a background screenshot the same way, and a screenshot the agent asked for is reused too. Code: `internal/gateway/shots.go`.
- **`phone_launch` skips the read before it** when the policy allows the app (that depends on the app and the task, not on the screen), the driver reports locks (`LockReporter`, so `d.locked` is current), and no step's picture waits for a read (see below). A launch the policy refuses reads nothing either. A launch that asks still reads first, for the approval panel.
- So an action now waits for: a read (none for such a launch, or for a `phone_batch` step that starts from the previous step's read), the input, settle, and a read. The approval panel still takes its screenshot at once, since the person needs it; so does `phone_observe { screenshot: true }`, which also reads again to check it.

**The blocked-app guarantee.** A background screenshot is bracketed by two reads, like the screenshots agents get. It is kept only when the read just before it and a later read, started after the screenshot returned, show the same app, which is not blocked, on an unlocked phone, with no input from the agent or the person in between. Input means any agent action, input on the mirror, a touch on the phone, Take over, an unlock from the computer, or the phone going offline. The later read is the one the next call makes anyway (an action reads before it acts); when the task ends first, the gateway reads once more in the background, except after Stop, when it leaves the phone alone. Otherwise the screenshot is dropped, including after 10 minutes without a read. The app is checked against the rules again when the screenshot is kept, and a kept screenshot is written to history only when a step shows it.

Why two reads: the old after-screenshot came before the read that checked it, so an app that opened during the screenshot was caught. A screenshot taken after a read, with no read after it, would miss that, and payment flows do hand over to a banking app after a static "Redirecting…" screen that can pass for settled. `TestShotOfBlockedAppNeverStored` covers both cases; `shots_test.go` also covers the drops below, the reuse, the task-end read and the launch reads.

**What history gives up:**

- In `phone_batch`, the next step's input comes before any read, so the steps in between have no pictures; the first step's "before" and the last step's "after" remain.
- A call that comes before the previous screenshot returned (0.3 to 0.7 s) drops it: that step has no "after" and the next no "before". Agents think for longer; the device test, paced at 0.4 s, lost a few (table below).
- An action whose screen changed by itself since the agent's last result has no "before" picture, and neither does a launch at the start of a task.
- A locked screen gets no screenshot (notification previews on it may come from blocked apps).

**Measured** with the device test, the same harness for both builds (before: 33299a0; after: this change; drivers from main 1564080), one phone at a time under its lock (40 to 50 s a run on the Mi, about 2 minutes on the iPhone):

| | Mi 10 Pro before | after | iPhone 13 mini before | after |
| --- | --- | --- | --- | --- |
| Action, median of 14 | 1.80 s | **1.44 s** | 5.39 s | **4.97 s** |
| Tap, median of 6 | 1.81 s | **1.44 s** | 3.17 s | **2.86 s** |
| Key (home, back), median of 4 | 1.60 s | **0.94 s** | 7.54 s | **6.45 s** |
| Swipe (2) | 2.09, 2.37 s | **1.60, 1.61 s** | 7.23, 6.57 s | **6.41, 5.85 s** |
| Launch (Calculator, Settings) | 2.77, 1.64 s | **1.55, 1.49 s** | 4.95, 7.01 s | **3.07, 4.74 s** |
| `phone_batch` of 5 taps | 8.74 s, 1.75 s a step | **6.99 s, 1.40 s a step** | 12.03 s, 2.41 s a step | **10.83 s, 2.17 s a step** |
| Settle, median | 1.25 s | 1.25 s | 1.03 s (3 of 19 hit 3 s) | 1.43 s (4 of 19 hit 3 s) |
| Read, median | 0.07 s | 0.07 s | 1.38 s | 1.35 s |
| Screenshots taken (median) | 22 (0.36 s), all in the wait | 18 (0.36 s), none in the wait | 35 (0.34 s), all in the wait | 19 (0.36 s), none in the wait |
| Settled actions with a before / after picture | 19 / 19 of 19 | 13 / 14 of 19 | 19 / 19 of 19 | 13 / 14 of 19 |

The parent session's figures from the desktop app on the uiautomator driver (`phone_launch` 10.8 s, `phone_tap` 8.4 s) also included a second read per screenshot, which the previous round had already removed. On the Mi, settle is now most of an action (1.25 s of a 1.44 s tap); the rest is the two 70 ms reads and the input. On the iPhone, two reads of about 1.4 s and settle (1 to 1.4 s, and the full 3 s on the home screen and after scrolls) make up most of an action. Before, nearly every iPhone action also took two screenshots: the old reuse compared the whole screen text, and the read after an action (with OCR lines) never matched the read before the next one (without them). Settle varied between the two runs (median 1.03 s with 3 timeouts before, 1.43 s with 4 after), which hides part of the gain in the medians; the whole test took 123.6 s instead of 134.7 s. The first launch of a task skips its read: the Calculator launch went from 4.95 s to 3.07 s on the iPhone and from 2.77 s to 1.55 s on the Mi.

Of the 11 pictures missing in each "after" run, 8 belong to the middle steps of the `phone_batch`. The other 3 are most likely the test's 0.4 s pacing (the next call read the screen before the previous screenshot had returned) or a screen that changed between calls; the run does not log why a screenshot was dropped. `TestBeforeReusesTheAfterPicture` checks that steps with a pause between them keep both pictures.

## Driver issues seen from the gateway

Found while running the gateway on the phones; not changed here, since the driver sessions own that code.

iPhone 13 mini (iOS driver):

- **OCR placement changes between two reads of the same still screen.** The Settings "Battery" row had a position in one read and none in the next (twice in three runs); the Calculator's "7" key lost its position once "6×7" showed on the display (the text "7" was on screen twice). An agent then gets `not_found` for a ref it has just seen. The gateway now reads once more before answering `not_found`, which helps when the placement flips back; a steadier placement (keep a row's position while the screen does not scroll, or skip ambiguous matches more carefully) would fix it at the source.
- **Positions can be wrong, not only missing.** Airplane Mode was placed at y 627 in one read and at y 1511 in the next, with no input in between (Settings had just opened). A tap on a misplaced ref lands on whatever is really there; the gateway checks every element under the finger against the policy, but an element without a position cannot be checked. This matters most of all the iOS findings.
- **A swipe that did not scroll**: in one run a swipe up through the middle of Settings (562,1948 to 562,488 in 300 ms) settled at the 3 s limit and the next two reads still showed the top of the list; the same swipe on the Mi scrolls Settings to its end. In other runs the list did move. After a scroll the list also kept changing for seconds after the settle signal (43 elements, then 38, rows losing their positions).
- **Keys that show a symbol never get a position**: the Calculator's Multiply, Divide, Add, Subtract, Equals, Clear, Delete, Per cent and Point have labels that OCR cannot match to their glyphs (×, ÷, +, −, =, C, ⌫, %, .), and digits 1, 3 and 0 were missed in some reads. Agents have to tap by x and y from a screenshot. A small table of symbol labels in the OCR matcher would place most of them.
- The Calculator's display reads `other "StandardInputView;value:6×7"`: the identifier and value joined, instead of the value as the label.
- OCR turns the status bar into lines such as `ocr "3:42イ"` and `ocr "三の"` (clock and signal icons) on every screen; dropping OCR lines inside the status bar would save agents the noise.
- go-ios logs `ERROR no connection dispatcher registered for global channel module=go-ios/dtx_codec` on stderr when the device opens.
- The first read in a new process took 15 s once (OCR loading); later processes read in 0.7 to 2 s. The gateway's read limit is now 60 s.
- Settling hits the 3 s limit after most launches and scrolls and on the home screen (widgets), so those actions take 6 to 7 s.

Mi 10 Pro (Android driver):

- Each read is a `uiautomator dump` of 2.2 s, and an action needs two (before and after), so 4.4 s of the median 6.1 s per action is reading. The companion app's tree (about 30 ms) is not wired into the driver yet; that is the biggest speed-up available.
- No toasts or notifications reach `Screen.Events` without the companion (known).

## Settings API (for the client)

Everything Settings › Safety shows maps onto the policy engine (`g.Policy()`) and two gateway helpers. Each setter saves its file at once (`rules.yaml` or `apps.yaml` in the config directory, mode 0600, written atomically); agents cannot reach any of it.

| Client setting (`ui.Settings`) | Read | Change |
| --- | --- | --- |
| `Preset int` (1, 2, 3) | `g.Policy().Preset()` returns `policy.Preset` | `g.Policy().SetPreset(policy.Preset(n)) error` |
| `HideCodes bool` | `g.Policy().HideCodes()` | `g.Policy().SetHideCodes(on) error` |
| `ReadOnly bool` | `g.Policy().ReadOnly("")` | `g.SetReadOnly("", on) error` (a device id instead of `""` sets one phone until restart) |
| `BlockedApps []BlockedApp{Name, Kind}` | `g.BlockedApps(ctx) []policy.BlockedApp` | `g.SetAppBlocked(screen.App{ID: id, Name: name}, blocked) error` |

- `policy.Preset`: `PresetCommits` (1, the default: payments, messages, deletions and other commits ask, and so do calls, installs, links that leave the app and settings changes), `PresetPayments` (2: only payments ask), `PresetEvery` (3: every tap, type, swipe, key and launch asks), `PresetNone` (0: only the rules in the file). Blocked apps and the credentials rule apply under every preset. With read-only on and preset 3, looking around still asks (it is not denied), and commits are denied.
- `HideCodes` masks four-to-eight-digit codes in notifications that mention a code. It does not blur screenshots: agents get screenshots only on request (`phone_observe { screenshot: true }`), and blocked apps never.
- `policy.BlockedApp{ID, Name, Category, Rule, ByPerson}`: `g.BlockedApps` returns the apps the person blocked, plus the apps on the connected phones that a rule blocks (banking, wallets, password managers), sorted by name. For the client's `Kind`, use `policy.CategoryLabel(b.Category)` ("Finance", "Passwords", "Blocked by you"); for its name, `b.Name`, or `b.ID` when the name is unknown (an app blocked by package name that no phone has).
- `SetAppBlocked(app, true)` adds the app to the `blocked` category in `apps.yaml` (rule "Blocked by you"); the client's free-text field can pass `screen.App{Name: text}`, or `{ID: text}` when the text looks like a package or bundle id. `SetAppBlocked(app, false)` removes it, and for a bundled banking or password app adds it to `exclude`. It returns `policy.ErrBlockedByRule` when a rule in `rules.yaml` names the app.
- `rules.yaml` gets `preset: 1` on first run. A rules file from before presets that spells out the six old default Ask rules becomes preset 1 without them; any other old file keeps exactly its rules (preset 0).

## Integration (for the main session)

- **Real drivers.** Append factories to `providerFactories` in `cmd/pinky/providers.go`; `realProviders` skips one that fails to start. `--fake` uses only `demo.World()`.
- **Desktop app.** Run `gateway.New` in the app process and use it as `gateway.API`. Methods the client needs that are not in `API` (I did not change the interface, since the client session may have its own implementation of it): `SetReadOnly(deviceID, on)` (empty id = every phone), `SetTaskApps`, `MediaPath(ctx, mediaID)` for `StepView.Before/After`, `WipeHistory`, `Agents`, `SetAgentScope`, `Policy()` (rules, `Reload`), `PersonTouched(deviceID)`, `Flush`, `Refresh`.
- **Needs.** `NeedApproval` covers Ask actions, `phone_request_approval` and an agent's first connection (rule `New agent`). New: `NeedHandoff` (`phone_handoff`). `NeedUnlock` ids are `unlock:<device id>` and show only while an agent task waits on a locked phone. `Decide` on a handoff: allow = hand back with the note, Deny = decline, TakeOver = take over (the handoff stays open until `HandBack`). `Decide(…, TakeOver)` on an action approval calls `TakeOver`. `AllowTask` grants the rules that asked, in that app, for the rest of the task; `AllowAlways` currently does the same.
- **Unlock.** `Options.Unlock` is the hook for entering the saved PIN after Touch ID; without it `Unlock` returns `ErrNoUnlock`. A successful `Unlock` also answers the handoffs the agent opened while the phone was locked.
- **In control without a task.** `DeviceInControl` can now show with no task ("You're in control") after the agent left; the device card needs a Hand back button in that state.
- **Touches on the phone itself.** `driver.go` has no way for a driver to report them, so I added `Gateway.PersonTouched(deviceID)`. Either wire the Android driver's getevent signal to it, or add an optional driver interface (for example `TouchWatcher`) that the gateway subscribes to.
- **Socket clients.** A desktop app that attaches to a running daemon cannot approve over the socket: `decide`, `hand_back`, `unlock` and `wipe_history` need user presence (Touch ID or Windows Hello), which is not built. Only a gateway with fake phones accepts them.
- **Tuning for real phones.** `Options.PollInterval` (250 ms) and `SettleMax` (3 s) drive settling when a driver has no `Settler`; each poll is a full `Observe`. History screenshots are taken in the background and never delay an agent (see "What the agent waits for"); `NoScreenshots` or `--no-screenshots` turns them off. `Flush` waits for screenshots in progress as well as history writes. A driver whose `Observe` takes a screenshot anyway (the iOS driver's OCR placement does) could hand that image to the gateway through an optional interface and save the second screenshot; none exists yet.

## Decisions I made, and why

Nobody was available to ask, so these are mine; each is easy to change.

1. **The MCP server runs inside the gateway, and `pinky mcp` only copies bytes** between stdio and the socket. One connection is then exactly one MCP session, so session close, cancellation and progress work without extra plumbing, and the tools exist once.
2. **Socket protocol.** The first line is a hello that picks the role (`mcp` with the agent token, or `control`); control is line-delimited JSON-RPC 2.0. MCP connections must present the token from `agent-token`.
3. **The socket cannot approve.** `decide`, `hand_back`, `unlock` and `wipe_history` are refused unless the gateway runs only fake phones. Any process of the same user can open the socket, and DESIGN's threat model says approval methods accept only the in-process UI or a client that proves user presence. `stop`, `stop_all` and `take_over` are allowed: they only take power away from agents.
4. **First connection approval** is a regular approval card (rule `New agent`). Allowing grants `Options.DefaultScope` (everything), unless the person picked a scope on the card: the client calls `SetAgentScope` (which also approves the agent) right before `Decide`, and the gateway keeps that scope when the agent picks up the answer. Since `SetAgentScope` approves by itself, call it only on Allow. Approved agents are remembered by name in the `agents` table. A `--fake` gateway trusts agents without asking.
5. **Text-only tool results**, no `structuredContent` or output schema. Some clients show only structured content when it exists, which would hide the screen, and sending both doubles the tokens. The first line (`status · key: value …`) is easy to parse.
6. **Two extra error codes**: `invalid_argument` (bad arguments, a misused `approval_id`, no phone connected) and `internal` (cancelled calls, gateway failures). DESIGN's five did not cover these.
7. **`phone_connect` takes an optional `apps` list**, the task's allow-list. DESIGN has the list (M1) but not where it comes from. An agent can only narrow itself with it, which protects against prompt injection later in the task; the client can change it with `SetTaskApps`.
8. **`user_took_over` vs `paused`**: the first call after a takeover returns `user_took_over`, later ones `paused`, until the person hands back. Person control belongs to the phone, not the task: releasing, renaming the task, idling out or another session connecting does not end it, and `phone_connect` then returns `paused`. Only Hand back ends it. Input on the mirror takes over only while an agent task runs; the Take over button always does.
9. **Kept decisions.** A decision made while no call waits is kept for the timeout again (60 s) from the decision, not only until the original deadline, so an answer at second 59 still reaches the agent. A later call with an expired id opens a new approval instead of failing.
10. **`intent` is not part of the approval binding**: agents rephrase it on retries, and it does not change what the action does.
11. **Handoffs are approvals of kind `handoff`** (new `NeedHandoff` in `api.go`) with a 15-minute timeout. A handoff opened while the phone is locked ends by itself only when the person unlocks from the computer (`Unlock`); after an unlock on the phone the person hands back explicitly, since they may be doing what the agent asked.
12. **Stop** ends the lease and bars that session from the phone, so a looping agent cannot reconnect; **Stop all** bars the sessions it stopped from every phone. A new session (the agent restarted) may connect. Take over and Stop cancel the driver call in progress, and every action checks the lease, person control and open approvals again right before the driver acts.
13. **Task states**: an agent that leaves or idles without `phone_release` shows as done, not failed; the exact outcome (`disconnected`, `expired`, `offline`, `replaced`, `stopped`, …) is in history.
14. **Blocked apps**: the agent sees `app: blocked app` with no elements and no screenshot; their notifications are dropped, and toasts and dialogs read while one is in front are discarded; their steps carry no screenshots or screen text. A screenshot an agent receives is followed by a read, and dropped when the foreground app changed or is blocked; a history screenshot is kept only when a read before it and a read after it show the same app, with no input in between (see "What the agent waits for"). `home` and launching another app still work so the agent can leave.
15. **Read-only mode** also refuses typing outside search fields, as DESIGN's definition implies; it comes from `rules.yaml` (`read_only`, `read_only_devices`) and `SetReadOnly`.
16. **The policy checks every element under the finger** and the strictest verdict wins, so a price label inside a Pay button still counts as Pay.
17. **Allow for this task** grants every rule that asked, in that app; another rule that also asks (or the same rule in another app) still asks. Always allow does the same until the rules editor exists.
18. **Typing without `ref`** is refused (rule `credentials`) when no field reports focus and the screen has a password or code field.
19. **Policy extensions**: a `change_setting` kind (switches and Remove/Reset/Forget… buttons in Settings) for DESIGN's "change account or security settings"; categories beyond finance, messaging and stores (passwords, social, browsers, phone, settings, system) because the defaults need them; the app table matches display names too (iOS backends may not know bundle ids) and `com.bank.*` prefixes; `apps.yaml` adds to and excludes from the bundled table, so table updates still arrive.
20. **Code masking** applies to notifications that mention a code (whole words such as "code", "PIN", "OTP", and CJK keywords) and masks runs of 4–8 digits.
21. **A line break typed into a chat or social app counts as sending** (`send_message`), since many keyboards send on Enter.
22. **Credential fields** are recognized by the password state, or by labels and resource ids (password words; one-time-code words, minus promo, postal and invite codes).
23. **Screenshots** are JPEG (quality 75, scaled down by a whole factor to at most 1280 px), as the task allowed; DESIGN says WebP, which needs a non-stdlib encoder.
24. **Wipe** deletes sessions, tasks, steps, approvals and media, and keeps agents and devices, which are settings.
25. **Swipe direction** is where the finger moves (`up` shows content below), stated in the tool description.
26. **`phone_type` with `ref`** must target a text field; it taps it first to focus. Typing into a button could otherwise tap a commit without the commit check.
27. **Several phones online and no `device`**: `phone_connect` returns `invalid_argument` with the list, rather than picking one.
28. **Presets are rules generated from a number** in `rules.yaml` (`preset: 1`), added before the file's own rules; the file stays the one source of truth, as DESIGN asks, and keeps working when edited by hand. Preset 2 means what the client's text says: only payments ask, so sends, deletions, installs, calls and settings changes go through. Preset 3 asks before every tap, type, swipe, key and launch; with read-only on, looking around still asks rather than being denied.
29. **"Hide codes" is the notification code mask** (it does not blur screenshots); **blocked apps** the person adds go to a `blocked` category in `apps.yaml` with a built-in deny rule, and unblocking a bundled banking app excludes it by id and by name.

## Differences from DESIGN

- Not built here: the desktop app mode (`pinky` alone prints a message), Streamable HTTP, the remote API, video recording, retention limits, encryption, HTML and JSON export, Allow for this task and Always allow beyond what is above (M2), `phone_files` (returns `unsupported`), the scope picker.
- `pinky mcp` starts `pinky daemon`, not the desktop app in tray mode, since the app does not exist yet.
- One agent token for all agents, rather than one per agent; agents are told apart by their MCP client name, and the `agents.token_hash` column stays empty.
- Takeover by touches on the phone itself needs a driver signal (see Integration).
- Screenshots are JPEG, not WebP.

## Known issues

- Commit detection is word-based. It catches the common buttons in 11 languages, but a commit with an unusual label (an icon without a label, a slider in a language without a table) is missed, and some navigation buttons (Checkout, Confirm in a wizard) ask needlessly. `rules.yaml` can add label rules.
- The status-bar exclusion for the stale check is a heuristic (systemui ids, `statusbar` in the path, elements in the top 4.5 % of the screen). Screens with a ticking clock or countdown in the app will always come back `stale`.
- Settling by polling costs one `Observe` every 250 ms on drivers without `Settler`. Each `Observe` has a 60 s limit. A screenshot an agent asks for is followed by one more `Observe` to make sure no blocked app came to the front; history screenshots wait for the next read instead, and the approval panel's screenshot is checked only by the read just before it.
- History pictures are missing for the steps in the middle of a `phone_batch`, after a call that follows the previous result within about half a second, and for screens that changed by themselves between calls (see "What the agent waits for").
- Take over and Stop cancel the driver call in progress through its context; a driver that ignores the context finishes the call anyway (the result then says it may have happened).
- DESIGN wants a pending status for a `phone_type` that would block longer than 45 s (long text on iPhone). Typing runs to completion here; the iOS driver's typing speed decides whether this matters.
- Windows: builds, but untested; the single-gateway lock there only checks whether the socket answers.
- A `--fake` gateway accepts approvals from any local process; that is intended (fake phones only) but worth knowing.
- `pinky needs` and `pinky decide` are development aids and not documented in `pinky help`.

## Review

An independent review of the pipeline, approvals and socket (after the first commit) found these, all fixed in the second round with regression tests in `internal/gateway/regression_test.go`:

- After Take over, an agent could get control back by connecting with another task name, or after release, idling out or a new session. Person control now belongs to the phone until Hand back.
- An action that had passed the checks still ran if the person took over or pressed Stop while its screen was being read. Every action now checks again right before the driver acts, and Take over cancels the call in progress.
- Tapping a child element inside a Pay button (a price label) skipped the payment check. The policy now checks every element under the finger.
- A blocked app's toasts and dialogs could reach the agent after it left the app, or through `phone_wait_for`; a screenshot could catch a blocked app that came to the front between the read and the shot. Both are closed.
- Approvals opened at the moment of a Stop or Take over could stay open with no owner and block the phone. They are now withdrawn.
- "Allow for this task" on one rule skipped other rules that also asked. Grants now need every asking rule, per app.
- Smaller: history steps of one approval used by two concurrent calls, a data race on the agent approval flag and on the device's provider, approvals opened after `Close`, the daemon exiting on a transient `Accept` error, typing with unknown focus near a password field, stale checks ignoring element states.

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
| `internal/gateway` | approval allow (with history and media), deny, timeout, pending then repeat (busy phone, mismatched and reused ids), stale (screen text and target), take over and handoff, take over during an approval, stop and stop all (cancelling a slow action), lease conflict, session close, MCP-style cancellation, read-only, blocked app, credential fields, send message, batch, offline and reconnect, idle expiry, new-agent approval, locked phone and unlock from the computer, wait_for and diffs, `Settler`, watch-only scope, task app list, unclaimed approvals, events, wipe. Regression tests from a review: takeover kept across tasks and sessions, take over and Stop while an action is in flight, no orphan approvals, blocked-app events and screenshots, a price label inside a Pay button, per-app task grants, one approval id used twice, agent approval race, typing with unknown focus. Background screenshots (`shots_test.go`): the result does not wait, "before" reuses "after", a blocked app is never stored (also when it opens during the screenshot), drops on the next input and on the person's input, the read at task end (not after Stop), and when a launch reads first |
| `internal/mcp` | over the SDK's in-memory transport: the 14 tools and their schemas, a full flow with approval and progress notifications, pending and errors, session close releasing the lease, batch and screenshots, handoff |
| `internal/api` | single gateway per user, socket and token modes, MCP over the socket with the token, control methods and the trust rule |
| `internal/gateway` (tag `device`) | the real Mi 10 Pro and iPhone, see "Real phones" |
| `cmd/pinky` | end to end with real processes: `daemon --fake`, a refused second daemon, `devices`, two `pinky mcp` agents (approval answered through `needs`/`decide`, credentials, `lease_held`, iPhone `recents` unsupported, release, history), stdin close releasing leases, SIGTERM; and `pinky mcp --fake` starting the daemon itself |
