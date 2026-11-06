# Integration

What the main session did to put the five parts together, what was checked on real phones, and what is still open. Written on 2026-11-07.

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
