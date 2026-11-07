# Round three

Started on 2026-11-07. The goal: build everything the acceptance checklist listed as not built, act on the decisions, and check it all. The owner is away from the phones, so nobody can answer a prompt on them (Play Protect, trust dialogs, permissions), and the Mac's screen may be locked.

## Decisions

1. **Typing on an iPhone.** Text goes through the pasteboard: the driver saves the person's clipboard, sets the text, presses Command-V and puts the clipboard back. The saved text stays in memory and is never logged or shown. This types English while Pinyin is active, and Chinese and emoji too. HID typing remains a fallback for when the pasteboard service is missing: ASCII only, switching to a Latin keyboard for the duration when a non-Latin one is active, otherwise `unsupported`.
2. **The companion app ships inside Pinky.app** and the `.dmg`. On an Android phone without it, or with an older one, the phone's page offers an Install button. Pinky never installs anything without that click. A debug keystore committed with the companion signs every build the same way, so a newer build installs over an older one from any machine.
3. **Phones are never kept awake.** The owner worries about burn-in and heat. Phones keep their own screen timeout and lock; the gateway wakes a phone (`driver.Waker`) before an agent's step. On Android, a background display lets an agent work while the phone's own screen stays off and locked, where the phone allows it; the Android driver checks this on the emulators and the Mi 10 Pro, and its result decides whether background work becomes the default for Android phones.
4. **The Mac stays awake while an agent task runs**, and only then. App Nap stays off.
5. **Unlock from the computer**: PINs live in the system keychain (`internal/secret`); each unlock needs Touch ID (or the Mac's password) and is logged in the task's history, never with the PIN. macOS first; Windows and Linux where their keychains allow it, otherwise the setting says it is not available there.
6. **Remote agents**: MCP over Streamable HTTP, off by default, at `127.0.0.1:7465` unless the person changes the address. The agent token is the bearer token, and the first-connection approval and scopes are the same as over stdio.
7. **`phone_files`**: Android only. The phone's `Pinky/` folder (`/sdcard/Pinky`) syncs with a local folder per phone (`<data>/files/<phone id>/`), by pull and push. Read-only and watch-only agents may only pull. iPhones return `unsupported`: the media folder AFC reaches is invisible on the phone.
8. **Screen recording** is on by default, as Settings already says: one MP4 per task, remuxed from the phone's stream without re-encoding, paused while a blocked app is in front, with a mark per step, and deleted by the retention limit.
9. **History**: retention in days (default 30, 0 keeps everything) and export as a self-contained HTML report plus JSON.
10. **Windows and Linux** are checked in GitHub Actions: build, vet, tests and a smoke run of the fake gateway, plus the window under Xvfb on Linux if MyGo allows.
11. **FFmpeg**: Pinky.app carries its own FFmpeg libraries (an LGPL build with the H.264 and HEVC decoders and VideoToolbox), so it runs on Macs without Homebrew.

## Contracts on main

- `internal/driver`: `Waker`, `Unlocker`, `Files` with `FileInfo`, `Backgrounder` with `BackgroundOptions`, `CompanionInstaller` with `CompanionState`, `Pairer` with `Pairing`.
- `internal/gateway/extras.go`: `StartPairing`, `PairWithCode`, `CompanionState` and `InstallCompanion` work; `Recording`, `SetRecording`, `SetRetention`, `ExportHistory`, `SetBackground`, `Background` and `AgentStream` are stubs for the gateway session. `DeviceView.Features` says which optional driver features a phone has.
- `internal/secret`: `Store`, `System()`, `PINAccount`, and `Memory` for tests. `System()` returns an unsupported store until the platform session lands the real ones.
- `internal/recording`: `Open(ctx, path, from)` plays a recording as a `driver.Stream` (stub).
- `ui.Settings`: `RemoteAgents`, `RemoteAddr` (`ui.DefaultRemoteAddr`).

## Who does what

| Session | Owns | Work |
| --- | --- | --- |
| android-driver | `internal/driver/android/**` (with the Go companion client) | `Waker`; `Unlocker` through adb (wake, swipe, PIN, Enter, verify, `ErrLocked` on a wrong PIN, never retry); `Files` on `/sdcard/Pinky`; `Backgrounder`, and whether a background display keeps working while the phone is off and locked (emulators, then the Mi 10 Pro with Calculator only); `CompanionInstaller` (state from the installed and bundled versions; install, then turn on its accessibility service, keeping the others); `Pairer` (QR text `WIFI:T:ADB;S:<name>;P:<password>;;`, the adb server's mDNS services, `host:pair`, `host:connect`); the two lost taps; Android 10 on `pinky-api29` |
| android-companion | `android/companion/**`, `docs/notes/companion*.md` | Tree, events and `set_text` for a given display (background displays); the committed debug keystore and a version the driver can compare; Android 10 on `pinky-api29`. Agree protocol changes with android-driver |
| ios-driver | `internal/driver/ios/**` | Typing as decided; `Waker` and `Unlocker` (wake with the Home button, never the side button; passcode through the HID keyboard; check with the lock state); iPhones over Wi-Fi if go-ios's tunnel allows; the first read after an app opens; Calculator's display as its value |
| gateway-core | `internal/gateway/**`, `internal/mcp/**`, `internal/history/**`, `internal/recording/**`, `cmd/pinky/**` | Wake before steps; unlock with `Options.PIN` replacing `Options.Unlock`; `phone_files`; one device stream shared by the mirror and the recorder; recordings; retention; export; background displays; Streamable HTTP; wiring all of it in `cmd/pinky` |
| desktop-client | `ui/**` | Save a PIN and unlock with Touch ID; pairing by QR code and by code; the companion's Install card; the background switch and the agent's display in the mirror; the recording player with step marks, export, retention and recording settings; Settings for remote agents with the URL and token; the Mac awake while tasks run; `fakeapi` for all of it |
| platform | `internal/secret/**`, `internal/video` library loading, `Makefile`, `.github/**`, packaging | Keychain with Touch ID on macOS (purego: Security and LocalAuthentication), Windows and Linux stores where possible; FFmpeg inside Pinky.app; the companion in the `.dmg`; CI on Windows and Linux with smoke runs, fixing what fails there |
| main | merges, `docs/ACCEPTANCE.md`, `docs/notes/integration.md` | The screen-on checks when the Mac is unlocked; merging; checks on emulators and phones; the app; the checklist |

## Rules

- Merge `main` into your branch first (`sync_with_base_branch`, or `git merge main`), and run `make check` before you report.
- Phones: [WORKING.md](../WORKING.md) still applies. Nobody can answer a prompt on them today. Do not install a new companion build on the Mi 10 Pro, never enter the owner's PIN or passcode, and the Pixel is offline.
- Emulators: `pinky-api36` is `emulator-5560` (Android 16) and `pinky-api29` is `emulator-5562` (Android 10, standing in for the Pixel 1). They are ours: set PINs, lock, install, reboot. Hold `scripts/devlock.sh emulator-5560 …` as for phones. Leave the AVD `Folo_Release_QA` alone (another project). Start them, if they are down, with `~/Library/Android/sdk/emulator/emulator -avd pinky-api36 -port 5560 -no-window -no-audio -no-boot-anim -no-snapshot-save` (and `pinky-api29 -port 5562`).
- Commit on your branch (Conventional Commits, English). Do not push, except the platform session, which pushes its own branch to run CI. Keep your notes under `docs/notes/` current. When done, report what changed, what you checked and how, and what is left.
