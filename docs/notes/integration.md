# Integration

What the main session did to put the parts together, what was checked on real phones, and what is still open. Rounds one and two were integrated in the night of 2026-11-06; round three on 2026-11-07, see [Round three](#round-three).

## Merges

The five branches merged into `main` cleanly except `go.mod` and `go.sum`, which were combined by hand and tidied: `claude/gateway-core-da93b5`, `claude/android-driver-1aff78`, `claude/android-companion-76e6fd`, `claude/ios-driver-ba7421`, `claude/desktop-client-380250`. Build, vet and every test passed after each merge.

A second round on the same branches merged cleanly too:

| Branch | Second round |
| --- | --- |
| Android driver | The companion inside the driver (reads in about 70 ms instead of 2.2 s, typing through `set_text`, toasts and notifications as events, settle on video and accessibility events), `InstallCompanion`, and `PersonWatcher` over `getevent` |
| iOS driver | Element frames from `remoteAXService` (every Calculator key has a position; OCR only as a fallback), provider checks cached, `Touch` in video pixels |
| Gateway | Runs on both real phones (device tests), Settings presets, code hiding and blocked apps in the policy, history screenshots off the agent's path, a person's scope for a new agent kept |
| Client | Handoff and New agent cards, the gateway's extras (step screenshots, agents, read-only, wiping history), `Options.Hidden`, App Nap off, `video.FrameStream`, mirror states |

## Shared contract changes

- `driver.Toucher`: points are in the pixels of the picture the device's `Stream` sends (at `MaxSize` 0), because that is all the mirror knows. Drivers whose video differs from the screen convert: an iPhone 13 mini sends 1136x2464 video for a 1125x2436 screen.
- `driver.PersonWatcher` (new, optional): a channel that fires when the person uses the phone itself. The gateway listens on every device that has it and takes over for the person (`watchPerson` → `PersonTouched`), so a running agent task pauses. `internal/demo` devices implement it for tests.
- `gateway.NeedHandoff` came from the gateway branch.

## Wiring in `cmd/pinky`

- **Providers** (`providers.go`): Android and iOS. The Android provider starts an adb server when none runs; the iOS provider is skipped when usbmuxd is missing. `PINKY_DEVICES=id1,id2` limits Pinky to those phones, for development while other tools use the rest.
- **The iOS provider ignores Wi-Fi devices** unless `ios.Provider.Wireless` is set: the gateway opens everything it lists, and usbmuxd lists every iPad and iPhone ever paired over the network.
- **App mode** (`app.go`): `pinky` with no command runs the gateway, the agent socket and the client window in one process, so approvals are answered in the app (the socket refuses them for real phones). Settings persist in `config/settings.json`; read-only mode reaches the gateway; blocked apps shown in Settings come from the deny rules in `rules.yaml`. The log goes to `data/pinky.log` as well as stderr.
- **The MCP shim** starts the app on macOS when no gateway runs (`pinky app --background`: menu bar only, the window opens when something needs the person), instead of a headless daemon that could not answer approvals for real phones. Fake phones and other platforms still get `pinky daemon`.
- **Settings** (`app.go`): the preset, code hiding, read-only mode and blocked apps are read from the policy (`rules.yaml`, `apps.yaml`) at start and written back through its API when Settings changes them; the rest (unlock, retention, recording, developer mode) stays in `settings.json`.
- **Demo phones have video**: `internal/demo` devices encode their drawn screen with `video.FrameStream`, so `pinky --fake` has a mirror that takes touches. `SetVideo(false)` turns it off for tests.
- **`pinky companion install [phone]`** installs or updates the Android companion from the APK `android.FindCompanionAPK` finds (inside Pinky.app after `make app`). Pinky never installs it on its own.
- **Packaging**: `cmd/pinky/mygo.json` and `make app` build `Pinky.app` (binary `Contents/MacOS/Pinky`, version from `mygo.json`) and a `.dmg`; the companion APK is copied into `Contents/Resources` when built. The `.dmg` is made before that copy, so only the `.app` carries the APK.
- **Checks**: the `e2e` build tag links MyGo's automation socket (`MYGO_AUTOMATION=/path/sock`), which can read, press and capture the app's windows even while the Mac's screen is locked. `scripts/mcpcall` drives `pinky mcp` as an agent: tools in order in one session, with `$ref:LABEL` resolved from the screens seen so far.

## Fixes made here

- **The Mi 10 Pro's mirror stayed black.** Its encoder tags H.264 with the YCgCo matrix, which swscale refuses to convert, so every `Convert` failed with "Operation not supported" while VideoToolbox decoded fine. `Decoder.Convert` now retries the picture as BT.709 and keeps doing so for that stream. `TestDecodeYCgCoStream` tags a test stream with `h264_metadata=matrix_coefficients=8` to reproduce it.
- **A provider wrapper must be a pointer**: the gateway compares providers, and a struct with a map field panics in `==`.
- **Read-only from the client never reached the gateway**: the gateway's `SetReadOnly` began returning an error in the second round, and the client's optional interface still had the old signature, so its type assertion failed without a word. The interface now matches (and `cmd/pinky` applies the setting as well).
- **Status bar OCR**: on the iPhone, OCR read the clock and the signal and battery icons as text ("5:05イ", "三の") that changed between reads and showed up in every diff. The gateway now leaves OCR lines centered in the top 6% of the screen out of what agents see.

## Checked on real phones

All through `scripts/devlock.sh`, with Calculator, Settings (read only) and the home screens only; captures were deleted after viewing.

| What | Result |
| --- | --- |
| `pinky devices` with the real providers | Mi 10 Pro and the iPhone listed, both online, in 1.4 s |
| App window, iPhone | Live HEVC mirror of the home screen |
| App window, Mi 10 Pro | Live H.264 mirror, correct colors after the YCgCo fix |
| Agent → `pinky mcp` → app → Mi 10 Pro | First connection asked "New agent" in the app; Allow answered it. Calculator AC, 6, ×, 7, = gave 42 with correct diffs; home; release |
| Agent → `pinky mcp` → app → iPhone | Connected, launched Calculator, read the screen |
| Claude Code (`claude -p`) on the Mi 10 Pro, through the app, companion in the driver | Asked to work out 6 × 7 in the Calculator only: connected, launched it, saw the old result and used `phone_batch` to clear and tap 6 × 7 =, read 42, pressed home and released, in 37 s and 7 turns |
| Agent → `pinky mcp` → app → iPhone, second round | Calculator 6, Multiply, 7, Equals by ref gave 42, 1.8–2.4 s a tap |
| Claude Code (`claude -p`) on the iPhone | Asked for 12 × 12 in the Calculator only: cleared it, entered 1 2 × 1 2 = in one `phone_batch`, read 144, pressed home and released, in 43 s |
| Claude Code (`claude -p`) with fake phones | Found last week's order, reordered, reached the payment, which Pinky held for approval; with no answer it was denied after 60 s and Claude stopped without paying. It also used `phone_request_approval` on its own to ask which order was lunch |

Timing on the Mi through the app, the same Calculator flow:

| | `uiautomator` (first merge) | Companion in the driver (second round) |
| --- | --- | --- |
| `phone_connect` | 2.7 s | 1.0 s |
| `phone_launch` | 10.8 s | 1.1 s (already in front) to 2.4 s |
| `phone_tap` | 8.3 s | 1.8–2.2 s |
| `phone_observe` | 2.2 s | 73 ms |

With `uiautomator`, each step settled (median 1.3 s), read the screen (2.2 s), took a history screenshot (0.75 s) and read the screen again to make sure no blocked app came to the front (2.2 s). With the companion the reads are about 70 ms; what is left is mostly the settle (the Calculator animates for about 0.9 s, then 0.4 s of quiet) and the screenshot. The companion labels elements the way TalkBack reads them, so the Calculator's keys are 清除, 乘 and 等于 on this phone rather than AC, × and =; the owner's own floating accessibility button shows up as `button "Mika"`. The accessibility setting was back to exactly the owner's service after every run.

## Open issues found tonight

- **Two lost taps (seen once).** In one run through the app on the Mi, taps on 7 and = did nothing: the gateway sent them to the right points (history has the targets) and the phone did not react. Three later runs through the app and two in-process runs did not reproduce it.
- **iPhone elements without positions** (first round): fixed in the second round by frames from `remoteAXService`.
- **The app under automation while the Mac is locked.** The floating approval panel captured blank, and pressing Allow on a payment card did not answer it, although the same press worked for the "New agent" and "Agent request" cards. The client session traced it to MyGo: a locked screen makes windows occluded, MyGo then holds their frames, and a press only counts at the next build. `ui.Tester` builds every frame, and `ui/realgateway_test.go` and the client's `TestGateway*` tests show these flows work against the real gateway. Watch them with the screen on.

## Decisions

1. Merge the session branches into `main` without asking: the owner asked for everything to be built overnight and checked in the morning.
2. Only USB iPhones by default, until Pinky has its own pairing flow for Wi-Fi.
3. The desktop app hosts the gateway (as DESIGN says), and the shim starts the app on macOS.
4. Keep the companion APK out of git and the Go binary for now: `make companion` builds it and `make app` bundles it.

## Round three

After the second round the owner asked for the window checked with the screen on, a decision on typing on an iPhone with Pinyin active, the companion app shipped inside Pinky.app, no keeping phones awake (burn-in, heat), and everything else that was still missing. The plan, the decisions and who did what are in [round3.md](round3.md).

### The window with the screen on

With the Mac unlocked, [`scripts/screencheck.py`](../../scripts/screencheck.py) drove the `e2e` build through MyGo's automation socket and acted as the agent over MCP, on `emulator-5560`: the New agent card (Approve), an approval in the window (Deny), the floating panel with its screenshot and countdown (Allow), clicking the mirror to take over ("You're in control"), Hand back and Stop all. All ten checks passed, and the captures looked right. It found one bug: with no phone added, and on Activity, Settings and Add phone, the window has no Needs you column, so an agent's first connection waited unseen until it timed out; the floating panel now shows approvals whenever the window cannot (a793b8e). Computer-use access to the window was declined, so the check uses the automation socket only, which acts inside Pinky's windows.

### Merges

Six branches merged into `main` without conflicts: `claude/platform-5568b2`, `claude/android-companion-76e6fd`, `claude/android-driver-1aff78`, `claude/ios-driver-ba7421`, `claude/gateway-core-da93b5`, `claude/desktop-client-380250`. Build, vet and tests passed after the last one except two client tests that still expected the gateway's stubs.

### Fixes made while integrating

- **The client's tests** expected the gateway's round-three stubs; the merged gateway records, exports and gives the demo phones unlocking, background work and files, so the client shows those controls (`TestGatewayRound3`, `TestGatewayLockedHandoff`).
- **The agent token** reaches the window (`ui.Options.AgentToken`), which Settings › Agents › Remote agents shows.
- **`PINKY_DEVICES` hid Wi-Fi pairing**: the filter now passes `driver.Pairer` through and lets a paired phone in (`TestDeviceFilterKeepsPairing`).
- **A wrong PIN** now carries `driver.ErrLocked` as well as `gateway.ErrWrongPIN`, so the client words it.
- **Key frames on shared streams**: the gateway shares one stream among the mirror and the recorder, so drivers answer its key frame requests: iPhones with an RTCP FIR, Android phones by restarting the encoder (scrcpy's reset-video). Without it a background task's recording on Android was empty: scrcpy sends key frames only every 10 s, and the recorder drops what comes before the first readable screen.
- **PIN stores say whether they work** (`secret.Supported`), so the client no longer probes the keychain with a delete.
- **`pinky quit`** ends the running app or daemon over the socket; Windows has no signal for it, and the smoke run uses it on every system.
- **Background work is the default on Android 13 and later** (`driver.BackgroundDefaulter`): there the virtual display keeps working while the phone's own screen is off and locked, which answers the owner's worry about keeping screens on. The person's choice in Settings wins and is kept.
- **An empty background display read as unreadable**: the gateway opens the display before the agent picks an app, and accessibility reports a display without windows as one it cannot see, so every task moved to the phone's locked main screen. The driver now shows it as an empty screen until an app starts, and `phone_connect` tells the agent to open apps with `phone_launch` (also in `docs/SKILL.md`).
- **The gateway's emulator tests** unlock through the gateway (`PINKY_TEST_PIN`), keep the recording test on the main screen, and check display 0's own power state: the phone as a whole stays awake while a background display is open.
- **Background displays went to sleep** about 10 s after the agent's last input while the phone was locked: Android gives every display group the lock screen's short timeout, so the app on the display slept and its tree read empty. Claude Code, which thinks longer than that between steps, then saw an empty screen and asked for a handoff. The Android driver now sends a key that does nothing (`KEYCODE_UNKNOWN`) to an open background display every 4 s without other input, which keeps that display's group awake and leaves the phone's own screen off (1437a55, the Android driver session).
- **The companion's idle shutdown never fired** while something kept dumping the screen: each `uiautomator dump` makes Android reconnect every accessibility service, and the app restarted its three-minute count at each reconnect. The companion now keeps counting across rebinds, and of several drivers using it, the last to leave turns it off (the companion and Android driver sessions).
- **Screenshots of background displays**: Android cannot screencap a virtual display, so approval cards, history and `phone_observe`'s screenshot had no picture during background tasks. The gateway now decodes one from the display's shared video, from a key frame it asks for, so it shows the screen as it is now (0.6–1.1 s on the emulator).

### Checked

| What | Result |
| --- | --- |
| `make check`, `go vet` with the `device` and `e2e` tags, Windows and Linux builds | Pass |
| CI (`.github/workflows/ci.yml`) on Linux, Windows and macOS, with the smoke run | See the run for the commit on GitHub |
| Gateway on the Android 16 emulator, main screen | Unlocked through the gateway with the PIN in 2.8 s; a 39 s recording (987 frames, 19 step marks) with nothing written while a blocked app was up |
| Gateway on the Android 16 emulator, background display, phone off and locked | Clock on the display by taps, 400 frames recorded, the phone's own display stayed off |
| Remote agents | `pinky daemon --fake --http`: 401 without the token; Claude Code over Streamable HTTP opened the Calculator on the fake Pixel and released it (24 s, $0.21) |
| `pinky quit` | Ends the daemon and the app |
| Pinky.app | FFmpeg's four libraries in `Contents/Frameworks`, the companion APK and FFmpeg's license in `Resources`, ad hoc signature valid, `.dmg` 13 MB |
| Claude Code on the emulator through the app, the emulator off and locked | The first runs found the two bugs above. After the fixes Claude worked on the background display (opened the Clock, started and paused the stopwatch, read the time, released) in 94 s for $0.46; the phone's own screen stayed off and locked |

The phones were not available for end-to-end runs: the iPhone's screen was off and locked, the Mi 10 Pro locked after someone pressed its power key at 10:35, and the Pixel is still offline. The sessions' own real-phone checks are in their notes: the iPhone's 16 tests (typing by paste with Pinyin active, Chinese and emoji, the clipboard put back), and the Calculator on a background display of the Mi (unlocked, main screen untouched).

### The Pixel, back at noon

The Pixel 1 (Android 10) came back on USB. The Android driver's device tests pass on it (observe, screenshots, the Calculator, typing and the clipboard, the stream and settle, Pinky's own input not counted as the person's). Two fixes came from it:

- **No background work below Android 11**: an app runs on a virtual display there, but the companion cannot read another display's windows before API 30, so the driver says so (`driver.BackgroundSupporter`) and the client offers no switch.
- **English app names on a phone in another language**: `phone_launch "Calculator"` found nothing on the Pixel, whose apps have Chinese names (计算器). The gateway and the driver now also match the parts of the package or bundle id.

A Claude Code run on the Pixel stopped when the person watcher reported a finger on its screen: the owner had touched it, so the takeover worked as designed.

### With the owner back

- **The Mi 10 Pro's companion** was updated (build 130, replacing round one's build signed with another key; its service on next to the owner's Mika). Claude Code then worked out 8 × 9 in the Calculator on a background display in 38 s ($0.21, about 1.4 s a tap), and the Calculator task the owner had open went back to the main screen afterwards.
- **The iPhone over USB**: Claude Code typed "Wi-Fi" into Settings' search by paste with Pinyin active, read the first result, went home and released the phone, in 45 s ($0.25).
- **Signing and notarization**: `make release` signed Pinky.app with the NATURAL SELECTION LABS Developer ID (team 492J8Q67PF, hardened runtime, timestamp) and Apple notarized the `.dmg` (accepted and stapled); Gatekeeper accepts both as "Notarized Developer ID". The signed app loads FFmpeg from its own Frameworks.

### Unlocking the iPhone from the computer

- **Nobody knew the feature was there.** The owner locked the iPhone and Pinky said nothing: idle phones' lock state was not read, and the offer to save a PIN came only once an agent hit the lock. The gateway now reads idle phones' lock state every 6 s, and a locked phone's page shows a card that offers to save its PIN, or to unlock it once one is saved (fc835f4).
- **Unlock failed with "the phone did not accept the saved PIN"**, though the PIN was right and nothing had been typed: the passcode pad was looked for through an accessibility session made before the phone locked, which did not see it, and the failure was kept for 30 s, so the next tries failed in a second. A diagnostic that types nothing showed the pad about 2 s after the swipe in a session made on the lock screen. The driver now looks in a fresh session, retries after errors and waits up to 5 s; `driver.ErrPINNotEntered` tells "nothing was entered" from a refused PIN; and after a refused PIN the client offers no further tries until the phone is unlocked on it or the PIN changes (0db9973).
- **Then it worked**: Unlock with Touch ID from the card unlocked the iPhone; and with the iPhone locked, Claude Code asked for help, the owner clicked Unlock with Touch ID (4 s), and Claude finished the Calculator task (76 s, $0.27).

### Pairing the Mi over Wi-Fi

- **adb no longer saw the network.** Its server had run since two days before and its mDNS discovery listed nothing, while macOS's Bonjour saw the Mi's wireless debugging service; restarting the server brought discovery back, and it reconnected the Mi over Wi-Fi at once (this Mac had paired before). Pinky does not restart adb by itself, since other tools share it; the pairing step now says what to check after 30 s (171b99b).
- **One phone showed twice**, over USB and over Wi-Fi, with another id on Wi-Fi. Over Wi-Fi a phone's id is now its hardware serial (`ro.serialno`, the serial adb gives the USB connection), its connections are listed once, and USB is used while it works (2e73598).
- **Then it worked**: the owner paired the Mi by QR code from Add phone, and the new release lists the Mi once.

### Known limits found in round three

- **A screenshot is one instant.** Content that blinks (Clock's paused stopwatch) may show blank; the element list still has its text.
- **Apps that hide text from accessibility** (Clock's running stopwatch hides its time) need a screenshot to read it; the agent guide says to ask for one when the list is not enough.
