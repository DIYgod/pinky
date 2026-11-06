# Integration

What the main session did to put the five parts together, what was checked on real phones, and what is still open. Written on 2026-11-07.

## Merges

The five branches merged into `main` cleanly except `go.mod` and `go.sum`, which were combined by hand and tidied: `claude/gateway-core-da93b5`, `claude/android-driver-1aff78`, `claude/android-companion-76e6fd`, `claude/ios-driver-ba7421`, `claude/desktop-client-380250`. Build, vet and every test passed after each merge.

## Shared contract changes

- `driver.Toucher`: points are in the pixels of the picture the device's `Stream` sends (at `MaxSize` 0), because that is all the mirror knows. Drivers whose video differs from the screen convert: an iPhone 13 mini sends 1136x2464 video for a 1125x2436 screen.
- `driver.PersonWatcher` (new, optional): a channel that fires when the person uses the phone itself. The gateway listens on every device that has it and takes over for the person (`watchPerson` → `PersonTouched`), so a running agent task pauses. `internal/demo` devices implement it for tests.
- `gateway.NeedHandoff` came from the gateway branch.

## Wiring in `cmd/pinky`

- **Providers** (`providers.go`): Android and iOS. The Android provider starts an adb server when none runs; the iOS provider is skipped when usbmuxd is missing. `PINKY_DEVICES=id1,id2` limits Pinky to those phones, for development while other tools use the rest.
- **The iOS provider ignores Wi-Fi devices** unless `ios.Provider.Wireless` is set: the gateway opens everything it lists, and usbmuxd lists every iPad and iPhone ever paired over the network.
- **App mode** (`app.go`): `pinky` with no command runs the gateway, the agent socket and the client window in one process, so approvals are answered in the app (the socket refuses them for real phones). Settings persist in `config/settings.json`; read-only mode reaches the gateway; blocked apps shown in Settings come from the deny rules in `rules.yaml`. The log goes to `data/pinky.log` as well as stderr.
- **The MCP shim** starts the app on macOS when no gateway runs, instead of a headless daemon that could not answer approvals for real phones. Fake phones and other platforms still get `pinky daemon`.
- **Packaging**: `cmd/pinky/mygo.json` and `make app` build `Pinky.app` (binary `Contents/MacOS/Pinky`, version from `mygo.json`) and a `.dmg`; the companion APK is copied into `Contents/Resources` when built. The `.dmg` is made before that copy, so only the `.app` carries the APK.
- **Checks**: the `e2e` build tag links MyGo's automation socket (`MYGO_AUTOMATION=/path/sock`), which can read, press and capture the app's windows even while the Mac's screen is locked. `scripts/mcpcall` drives `pinky mcp` as an agent: tools in order in one session, with `$ref:LABEL` resolved from the screens seen so far.

## Fixes made here

- **The Mi 10 Pro's mirror stayed black.** Its encoder tags H.264 with the YCgCo matrix, which swscale refuses to convert, so every `Convert` failed with "Operation not supported" while VideoToolbox decoded fine. `Decoder.Convert` now retries the picture as BT.709 and keeps doing so for that stream. `TestDecodeYCgCoStream` tags a test stream with `h264_metadata=matrix_coefficients=8` to reproduce it.
- **A provider wrapper must be a pointer**: the gateway compares providers, and a struct with a map field panics in `==`.

## Checked on real phones

All through `scripts/devlock.sh`, with Calculator, Settings (read only) and the home screens only; captures were deleted after viewing.

| What | Result |
| --- | --- |
| `pinky devices` with the real providers | Mi 10 Pro and the iPhone listed, both online, in 1.4 s |
| App window, iPhone | Live HEVC mirror of the home screen |
| App window, Mi 10 Pro | Live H.264 mirror, correct colors after the YCgCo fix |
| Agent → `pinky mcp` → app → Mi 10 Pro | First connection asked "New agent" in the app; Allow answered it. Calculator AC, 6, ×, 7, = gave 42 with correct diffs; home; release |
| Agent → `pinky mcp` → app → iPhone | Connected, launched Calculator, read the screen |
| Claude Code (`claude -p`) with fake phones | Found last week's order, reordered, reached the payment, which Pinky held for approval; with no answer it was denied after 60 s and Claude stopped without paying. It also used `phone_request_approval` on its own to ask which order was lunch |

Timing on the Mi with `uiautomator` (before the companion is wired in): about 8.3 s per tap and 10.8 s per launch. Each step settles (median 1.3 s), reads the screen (2.2 s), takes a history screenshot (0.75 s) and reads the screen again to make sure no blocked app came to the front (2.2 s). The companion's 30 ms tree removes most of this; moving the history screenshot off the agent's path removes the rest.

## Open issues found tonight

- **Two lost taps (seen once).** In one run through the app on the Mi, taps on 7 and = did nothing: the gateway sent them to the right points (history has the targets) and the phone did not react. Three later runs through the app and two in-process runs did not reproduce it.
- **iPhone elements without positions.** The inspector gives labels but no frames, and OCR does not read symbols, so Calculator's ×, =, 1, 3 and 0 have no coordinates. The iOS session is working on frames from `remoteAXService`.
- **The app under automation while the Mac is locked.** MyGo draws only when asked, so the floating approval panel captures blank, and pressing Allow on a payment card did not answer it, although the same press worked for the "New agent" and "Agent request" cards and `ui/realgateway_test.go` shows Allow works with the real gateway. Check both with the screen on.
- **The fake phones have no video**, so their mirrors say "No picture: not supported by this device or backend".

## Decisions

1. Merge the session branches into `main` without asking: the owner asked for everything to be built overnight and checked in the morning.
2. Only USB iPhones by default, until Pinky has its own pairing flow for Wi-Fi.
3. The desktop app hosts the gateway (as DESIGN says), and the shim starts the app on macOS.
4. Keep the companion APK out of git and the Go binary for now: `make companion` builds it and `make app` bundles it.
