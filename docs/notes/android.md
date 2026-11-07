# Android driver

Status, round three (2026-11-07): everything the round asked for is built and checked on two emulators, `emulator-5560` (Android 16) and `emulator-5562` (Android 10, standing in for the Pixel). The Mi 10 Pro (LineageOS, Android 17) was used read-only and with the Calculator only, until it locked at about 10:36 with someone apparently at it ([below](#the-mi-10-pro-in-round-three)). The Pixel is still offline. Branch `claude/android-driver-1aff78`, not pushed this round.

## What was built

| Package | What it does |
| --- | --- |
| `internal/driver/android/adb` | A client for the adb server protocol: `host:version`, `host:devices-l`, `host:track-devices-l`, `host-serial:<serial>:features`, `host:transport:<serial>`, `shell:` (v1, interleaved output), `shell,v2,raw:` (stdout, stderr, exit code), `exec:` (binary-safe), `sync:` (push, pull, `STAT`/`LST2`, `LIST`/`LIS2`, several requests on one connection), local sockets, forwards, and wireless debugging: `host:mdns:check`, `host:mdns:services`, `host:pair:`, `host:connect:`. It reuses the running server; it never kills or restarts it. `StartServer` exists for a caller that finds no server; nothing calls it automatically. |
| `internal/driver/android/scrcpy` | The bundled scrcpy 5.0.1 server (`server/`, with its Apache-2.0 license and checksum) and its protocol: `key=value` options, the video stream headers, control messages, device messages, `start_app` results read from the server log, and `list_apps`. The options that would keep a screen awake or press POWER do not exist and are dropped from raw extras. |
| `internal/driver/android` | The driver. `Provider` (also `driver.Pairer`), `Device` (also `Toucher`, `LockReporter`, `Clipboard`, `Settler`, `PersonWatcher`, `Waker`, `Unlocker`, `Files`, `Backgrounder`, `CompanionInstaller`), and `Background`, the device a background display gives (`Toucher`, `LockReporter`, `Clipboard`, `Settler`, `Files`). |
| `internal/driver/android/companion` | The Go client for the companion app (owned by this session since round three; the app itself is the companion session's). Round three added `TurnOn`, `KeepEnabled`, `EnabledForTask`, `Uninstall`, `InstalledVersion`, `APKVersion`, `Info.Features` and a display for `set_text`. |
| `internal/driver/android/cmd/androidctl` | A debug command for every feature, with timings. |

Shared contracts: none changed by this session. `cmd/pinky/companion.go` (gateway-core's) now calls `SetUpCompanion`, because `InstallCompanion` had to take the contract's signature.

### How it works

- **One scrcpy session per device** carries input, the H.264 stream and settle detection. It starts on first use (about 0.5 s on the Mi), restarts by itself on the next call after it dies, and connects to the server's abstract socket straight through the adb server, so no host port is used. Each run pushes its own jar, which the server deletes once it starts; a server that crashed before that (the Android 10 emulator's failing encoder left six) has it removed by the driver when the session ends, and `Close` waits up to 3 s for that.
- **Server options Pinky always sets:** `audio=false`, `tunnel_forward=true`, `clipboard_autosync=false`, `power_on=false`. Never: `stay_awake`, `power_off_on_close`, `turn_screen_off`, `screen_off_timeout` (the options are gone from the code, and `Options.Extra` drops them). The phone's own screen timeout and lock always apply.
- **When the video encoder fails,** the device runs a session without video instead. The Android 10 emulator's encoder fails at once (`Capture/encoding error: IllegalStateException`), and so does the image's own `screenrecord` (`Encoder failed (err=-38)`), with and without SwiftShader. The first video session of a device waits up to a second for its first frame; if the server reports an encoder failure instead, the driver logs it once and starts a control-only session: taps, keys, typing, the clipboard and unlocking keep working, touches go in display pixels (the size from `dumpsys window displays`, `cur=WxH`), settle uses the companion's events (300 ms quiet) or a fixed 1 s wait, and `Stream` and background displays return `driver.ErrUnsupported`. `Stream` tries video again after a minute.
- **Input** (taps of 50 ms, long presses of the phone's `long_press_timeout` plus 400 ms, swipes, raw touches, keys) goes through scrcpy's control messages. **One gesture at a time per session:** a session restart (a `Stream` with other options) waits for the gesture in progress plus 150 ms for the server to read it, and a gesture that finds its session replaced before it began goes to the new one. This fixed the lost taps (below).
- **Wake (`driver.Waker`).** `ScreenOn` reads display 0's power state from the display power controllers (`dumpsys display`: the `mScreenState` that follows `mDisplayId=0`; Android 10 has one controller). The overall wakefulness and the device idle controller's `mScreenOn` both say "on" while a background display keeps its own display group awake, so neither is used. `Wake` presses `KEYCODE_WAKEUP` (never POWER) when the screen is off and waits until it is on; it never unlocks. Actions keep waking the screen as before.
- **Unlock (`driver.Unlocker`).** Wake, then `wm dismiss-keyguard`, which shows the PIN pad on a secure lock screen. The alternate bouncer of phones with an under-display fingerprint sensor (`alternate_bouncer`, `udfps_touch_overlay`) gets a tap above the sensor, which brings up the PIN pad; otherwise a swipe up from 70 % of the height is the fallback, but never on a screen that shows digit keys the driver cannot read (on some pads a touch on a key enters its digit). The driver enters nothing until a tree read (the companion's, or uiautomator's when the companion is off) shows one of these, and then enters the PIN through scrcpy, never in a shell command line (where other processes could read it), and never logs it:
  - **AOSP's PIN view or password field** (`pinEntry`, `passwordEntry`), or a focused password field in SystemUI (with digit keys for a PIN): the PIN goes in as key codes, as one gesture, 80 ms apart. A 300 ms pause follows, then Enter unless the phone already unlocked itself (Android's auto-confirm).
  - **Otherwise, a PIN pad read from its keys' labels and layout** (`pinpad.go`), for OEM lock screens and SystemUI's Compose bouncer: ten keys in SystemUI or a keyguard package (MIUI's is `com.android.keyguard`), each labelled with one digit in its text or description ("2", "2 ABC", "2\nABC", the word "Two" as on vivo's pads, Arabic-Indic or full-width digits), laid out as a phone keypad (three evenly spaced columns, 1 to 9 in three evenly spaced rows and one key below the middle column; the digits may be scrambled), no key bigger than its cell, all on one window, none under a window above it, exactly one such grid on the screen, and in the same place in two reads in a row: a pad still sliding in reads a few dozen pixels off, and gets 2 s from when it first shows to settle. A key without a label may take its digit from its resource id (`key0`, `VivoPinkey0`) only when every key with both a label and such an id agrees, because LineageOS's scrambled pad keeps the ids in place and moves the labels. The PIN goes in as taps on the keys' centers, 150 ms apart, with a lock check before each digit after the fourth (a pad that checks the PIN as it is typed may have unlocked, and further taps would land on the screen behind). If the phone is still locked 0.6 s after the last digit, the driver reads the pad again and, when it is unchanged and shows a confirm key in its bottom row or just below it (`key_enter`, `btn_letter_ok`, "OK", "Enter", "确定", "✓" and the like), taps it after one more lock check; pads without one check the PIN by themselves.

  Then up to 4 s for the lock screen to go. Still locked: `driver.ErrLocked`, never retried; an error after the first digit went in is `ErrLocked` too, since those digits would spoil another try. Nothing entered: `driver.ErrPINNotEntered` (no PIN pad, a pad the driver cannot read with confidence, keys disabled after too many attempts, a saved PIN that is not all digits on a PIN pad). A pattern lock is `ErrUnsupported`. Which lock screens are checked on a device and which only in tests: see [Unlocking other Android phones](integration.md#unlocking-other-android-phones).
- **The Pinky folder (`driver.Files`)** is `/sdcard/Pinky`, over the sync protocol: `ListFiles` walks it with `LIS2` (or `LIST` on phones without `ls_v2`), folders included, sorted, and creates it when missing; `Pull` and `Push` take paths relative to it. Paths are cleaned and refused with `driver.ErrNotFound` when absolute, with any `..`, a backslash, control characters or nothing left. Every component is checked with `LST2`/`STAT` (lstat), so a symbolic link is never followed. `/sdcard` refuses to create links on both emulators anyway. `Push` creates folders, keeps the modification time, checks the size, and on Android 10 tells the media scanner (Android 11 and later index files written through `/sdcard` by themselves).
- **Background displays (`driver.Backgrounder`).** `OpenBackground` opens a scrcpy virtual display at the main display's size without system decorations, with the keyboard kept on it, and starts the app. The device it returns streams, observes (through the companion), takes input and settles on that display only, and closes it on Close. **The person's tasks are never destroyed:** starting an app the person already has a task for moves that task to the virtual display (Android reuses it), so before every start the driver notes which tasks are elsewhere (`cmd activity stack list`), and Close moves those back to the main display (`cmd activity display move-stack <id> 0`) before the display goes and takes only the agent's own tasks with it. A task given back lands on top of the main display: Android has no shell command to put it lower. `DisplayOptions.KeepContent` (`androidctl bg -keep`) moves every task to the main display instead, also on top. From Android 13 the display is always unlocked and has its own display group, so it keeps working while the phone is off and locked ([below](#background-displays-while-the-phone-is-off-and-locked)); there `Background.Locked` is false, and the display gets a key that does nothing (`KEYCODE_UNKNOWN`) every 4 s without other input, which keeps its display group awake through the lock screen's 10 s timeout. Its reads turn the companion on again at once after a failure (there is no uiautomator to fall back on there) and retry once. `Type` uses the companion's `set_text` on that display when the app has the `displays` feature, and settle waits for that display's events too.
- **Companion app.** When `app.pinky.companion` is installed, the first Observe or Type turns its service on for the driver's reads (`Enable`) unless it is on already, and a minute without driver calls, or Close, turns it off again, restoring the setting exactly. A service the person turned on (`InstallCompanion` does that) stays on. Several drivers (two Pinky processes, Pinky and `androidctl`) can share it: a driver closing leaves the service on while another client is connected, and the last one to leave turns it off, exactly as found when it turned it on itself, or by removing only Pinky's entry when another driver had turned it on. A service the person turned on for good (Install; the app then keeps `idle_disable_ms` 0) stays on. Events are tracked per display (`display` in each event; -1 counts for every display).
- **The companion installer (`driver.CompanionInstaller`).** `CompanionState`: `missing` (or `no_apk` when Pinky has no APK either), `outdated` when the installed versionCode is lower than the bundled APK's (`assets/pinky-companion.json` in the APK, which the companion build writes since round three; versionCode is the commit count), `on` when the service is listed and answers a ping, else `off`. While the driver has the service on only for its own reads, the state is `off`: the person has not turned it on. `InstallCompanion` installs when the phone has none or an older build. A build signed with another key (any build from before round three, such as the Mi's) cannot be updated in place: the driver turns it off, uninstalls Pinky's own package and installs the new one. It then turns the service on for good (`TurnOn`: only Pinky's entry is added, every other entry stays), waits for it to answer, and stops the app's idle backstop (`idle_disable_ms` 0), which would otherwise turn the service off after three minutes without a connection. From build 73 the app keeps that setting across restarts (a reboot, an update, a uiautomator dump) and forgets it when its entry leaves the setting (the person turns the service off, `Disable`, `ForceDisable`), so the driver's own on-demand `Enable` keeps the three-minute backstop. Checked on `emulator-5560` with build 73: turned on through the installer, the service was still listed and answering 3 min 24 s later with no connection. Play Protect may make the phone ask the person; the call waits up to its context.
- **Pairing (`driver.Pairer`, on the Provider).** `StartPairing` makes a random service name (`pinky-xxxxxxxxxx`) and password, `QR()` returns `WIFI:T:ADB;S:<name>;P:<password>;;`, and `Wait` polls the adb server's mDNS services for the phone's `_adb-tls-pairing._tcp` service of that name, pairs with `host:pair:<password>:<addr>`, and waits up to 30 s for the phone to come online. The server may connect by itself (serial `adb-<serial>-<suffix>._adb-tls-connect._tcp`); otherwise the driver connects to the phone's `_adb-tls-connect._tcp` service, found by the guid `host:pair` reports, or by the address it paired from. `PairWithCode` does the same with the address and six-digit code the phone shows. A real adb server (platform-tools 37) answers `host:pair` with OKAY and then a second OKAY or FAIL once the pairing is over; `Pair` reads both forms.
- **Person input** (`PersonInput`): `getevent` on the touchscreens and hardware-key devices from `getevent -lp`: direct multi-touch devices, or, on a phone without any (the emulators' virtio touchscreens have no input properties), its multi-touch devices; devices with power, volume, home, back, menu or recents keys; never `uinput` devices. Injected input never reaches /dev/input, so it is not reported. At most one signal per 300 ms.
- **Observe:** the companion's tree when it is on, else `uiautomator dump`; lock and keyboard state from the window manager in parallel. The screen conversion lists an input method window's elements whenever the app sends its nodes (build 70 sends them unasked for a keyboard sheet that covers the screen, such as Gboard's stylus tutorial), and hides what lies under a higher window by its touchable `region` when the app sends one (Android 13+), else by its bounds: Gboard's stylus toolbar spans the lower half of the screen but covers only a narrow strip. When a tree is still empty while the keyboard shows (older builds, such as the Mi's), the driver reads again with the keyboard's window, then falls back to uiautomator for that read.

### Settle detection

As in round two: a video packet counts as a change unless it is a refinement (85 to 125 ms after the previous capture), a key frame or configuration, or tiny; the screen has settled when no change came for 400 ms (limit 3 s). While the companion is on, its window and content events must also have been quiet for 300 ms; since round three only the events of the display being settled count (and those whose display is not known). Without video: the companion's events, or a fixed 1 s.

## Using it from the gateway

```go
p := android.NewProvider(android.Config{Logf: log.Printf}) // driver.Provider and driver.Pairer
dev, _ := p.Open(ctx, id)                                  // *android.Device
dev.(driver.Waker).Wake(ctx)                                // before an agent's step on the main screen
bg, _ := dev.(driver.Backgrounder).OpenBackground(ctx, driver.BackgroundOptions{App: "com.android.settings"})
```

- **Do not wake the phone for an agent working on a background display:** that would turn the screen on, which is what background work avoids. `Background` does not implement `Waker` on purpose.
- `Background.Locked()` is false from Android 13, and so is its `Screen.Locked`; the phone's own `Device.Locked()` still says whether the phone is locked. Agents on a background display are not stopped by the lock screen: the gateway's policy (blocked apps, approvals) is all that stands between them and the phone's apps.
- `Unlock(ctx, pin)` returns nil, `ErrLocked` (the PIN went in, all or part of it, and the phone is still locked: do not try again), `ErrPINNotEntered` (nothing was entered: no PIN pad, one the driver cannot read with confidence, attempts refused), `ErrUnsupported` (pattern, characters it cannot type) or `ErrOffline`. Errors never contain the PIN.
- `Files` paths are slash-separated and relative to `/sdcard/Pinky`; `ErrNotFound` for anything outside or missing. `Background` serves the same folder.
- `CompanionState` costs one `dumpsys package` (7 to 80 ms), plus `settings get` and a ping when the app is current. `InstallCompanion` may take minutes on a phone where Play Protect asks.
- `func (d *android.Device) SetUpCompanion(ctx context.Context, force bool) (android.CompanionInstall, error)` does what `InstallCompanion` does and reports `APK`, `Bundled` and `Before` (versions), `Installed` and `Replaced`; `force` also installs over the same versionCode when the file differs. `android.FindCompanionAPK()` looks at `$PINKY_COMPANION_APK`, `pinky-companion.apk` next to the executable, `../Resources/pinky-companion.apk` (macOS bundle), then the debug build under the working directory.
- `Stream` returns `ErrUnsupported` on a phone whose encoder fails; input still works there.
- **`cmd/pinky`'s `onlyProvider` (`PINKY_DEVICES`) hides `driver.Pairer`:** it embeds `driver.Provider` only, so `Gateway.StartPairing` finds no pairer while that variable is set. gateway-core may want to forward it.

## How to run

```sh
go build -o /tmp/pinky-android/androidctl ./internal/driver/android/cmd/androidctl
A=/tmp/pinky-android/androidctl
scripts/devlock.sh emulator-5560 $A -s emulator-5560 screen          # true or false
scripts/devlock.sh emulator-5560 $A -s emulator-5560 wake
scripts/devlock.sh emulator-5560 env ANDROIDCTL_PIN=1234 $A -s emulator-5560 unlock   # or the PIN on stdin
scripts/devlock.sh emulator-5560 $A -s emulator-5560 files
scripts/devlock.sh emulator-5560 $A -s emulator-5560 push notes.txt docs/notes.txt
scripts/devlock.sh emulator-5560 $A -s emulator-5560 pull docs/notes.txt /tmp/pinky-android/notes.txt
scripts/devlock.sh emulator-5560 $A -s emulator-5560 companion-state
scripts/devlock.sh emulator-5560 env PINKY_COMPANION_APK=app-debug.apk $A -s emulator-5560 companion-install
scripts/devlock.sh emulator-5560 $A -s emulator-5560 bg -app com.google.android.deskclock state sleep:5000 state observe
```

`bg` runs steps on a background display; its `state` step prints whether the phone's own screen is on and locked. Never pass a PIN on the command line.

Tests:

```sh
go test ./internal/driver/android/...       # unit tests, fake adb servers and fixtures
scripts/devlock.sh emulator-5560 env PINKY_DEVICE=emulator-5560 PINKY_TEST_PIN=1234 \
  PINKY_COMPANION_APK=/path/to/app-debug.apk go test -tags device -v ./internal/driver/android/
```

On a phone the device tests use only the home screen, Settings search and the Calculator, and skip what needs an emulator. On emulators they also press power, unlock with `PINKY_TEST_PIN`, use the phone app's keypad where there is no Calculator, install the companion and change the accessibility setting (put back afterwards). `PINKY_TAPS` sets the taps per batch of `TestDeviceTapsLand` (40).

## Round three results

### Background displays while the phone is off and locked

This is the question behind the owner's keep-awake worry. **On the Android 16 emulator with a PIN, an app on a background display keeps running, drawing, taking taps and text, and streaming while the phone's own screen is off and locked.** Each run used the Clock's stopwatch or Settings on a scrcpy virtual display:

| Run | Result |
| --- | --- |
| Locked before the display opened (screen off, keyguard showing) | Stopwatch reset, started, read 7 s then 11 s, paused at 11 s, by taps on the display. 624 frames in 17.8 s (35 fps). The phone stayed screen off and locked throughout. |
| Locked while the work ran (power key 12 s in) | The stopwatch read 3 s before the lock and 15 s and 18 s after it, and paused on a tap after it. 794 frames in 25 s. |
| Idle past the screen timeout (timeout set to 15 s, no input for 35 s), phone locked before the display opened | The display's power group stayed awake (`wakefulness 1`, display 0 dozing), the stopwatch read 38 s, a tap still paused it, 1747 frames in 44 s (40 fps). |
| Idle while the phone locks with the display open (later, from main's report) | **Broke without a fix:** about 10 s after the last input, reads of the display turned empty (no app, no elements, 3 ms) and the app slept. With the keep-awake key, reads 8 s and 14 s into a pause and 35 s after the lock still showed the app; through the gateway, `TestEmulatorBackground` passes, and fails at its 8 s read without the key. |
| Typing (`TestDeviceBackgroundLocked`) | Settings on the display, search tapped, "pinky" typed with the companion's `set_text` on that display, read back, video flowing; the phone off and locked. |

Why: from Android 13 scrcpy creates its display with `VIRTUAL_DISPLAY_FLAG_ALWAYS_UNLOCKED` and `OWN_DISPLAY_GROUP`, so the keyguard does not cover it and the display has its own power state. `dumpsys power` shows a second power group that stays awake while group 0 dozes, and `dumpsys display` shows display 0 OFF and the virtual display ON.

**The lock screen's timeout applies to every display group.** While the keyguard shows and has focus, Android sets a 10 s user-activity timeout for all groups (`PowerManagerService: Going to sleep due to timeout ... activityTimeoutWM=10000`; `PowerGroup: Powering off display group due to timeout (groupId= 1, ... millisSinceLastUserActivity=10003)`). The background display's group then dozes, its activities go to sleep (`isSleeping=true`, `visibleRequested=false`, the app at `TOP_SLEEPING`), and accessibility still lists the window but cannot fetch its root, so the companion's tree is empty. The phone locked before the display opened kept working because focus was then on the display's app, not on the keyguard. Input on the virtual display counts as activity for its group only, so the driver sends `KEYCODE_UNKNOWN` there every 4 s when nothing else did; apps ignore that key. `KEYCODE_WAKEUP` sent to the virtual display does not help: it wakes the phone's own screen (group 0) and leaves group 1 dozing.

What it costs and means:

- **The phone's own screen stays off** (no burn-in), but the phone as a whole stays awake while the display is open: its CPU, the encoder while content changes (it sends nothing for a still screen), and the keep-awake key every 4 s. Close the display when the task ends; then the phone sleeps as usual.
- **The lock screen does not protect apps from an agent on a background display.** That is the point of the feature, and it moves all of the protection to the gateway's policy.
- **Android 10 to 12: not verified.** scrcpy sets neither flag before Android 13, so the display most likely sleeps with the phone and the keyguard may cover it. The Android 10 emulator cannot show it (its encoder fails, and a virtual display needs one) and the Pixel is offline.
- **The Mi:** a background display with the Calculator worked again (8 x 9 = 72 by taps, main screen untouched, the launcher still in front, the accessibility setting exactly as before). The locked case could not be tried: the Mi keeps its screen on while charging (`stay_on_while_plugged_in=15`, Developer options > Stay awake, not set by Pinky), and the rules forbid pressing its power key or changing settings.

**Recommendation:** make background work the default for Android 13 and later, with the display closed at the end of each task, and keep the main screen for Android 12 and earlier until a phone shows otherwise.

### The lost taps

Reproduced and fixed. `TestDeviceTapsLand` taps the phone app's 5 key in batches and counts the digits that arrive:

| Batch | Before the fix | After |
| --- | --- | --- |
| Paced 300 ms, back to back, video reset every 400 ms, a stream nobody reads, the companion turned off and on every 1.5 s, screenshots and reads meanwhile, the session restarted right before each tap | 0 lost | 0 lost |
| Another caller reopening the stream with other options every 350 ms while taps go on | **9 of 40 lost** (each failed with "session restarted with new options"; a write that beat the close could also vanish without an error) | 0 lost |
| Android 10: a stream request on a phone without video | 1 of 40 lost (the request closed the session before refusing) | 0 lost |

Totals after the fix: 320 taps on Android 16 and 280 on Android 10, none lost. The Mi's two lost taps (7 and = through the app) fit this: a session restart, or a session that died, while the taps were on their way. Which restart happened that night is not known; the fix covers restarts the driver makes, and taps on a session that dies return an error.

### Android 10 (emulator-5562, standing in for the Pixel)

- **scrcpy:** the server runs, but the emulator image's video encoder fails (so does its `screenrecord`), so the driver runs it without video. The real Pixel streamed fine in round one.
- **Companion:** builds from round three install and run (`set_text`, events; Android 10 has no IME action, so Enter goes through scrcpy). Typing ASCII and CJK passes through `set_text`.
- **uiautomator fallback:** used for the PIN pad while the companion was off.
- **Person watcher:** watches the virtio touchscreens (see above); a finger through the emulator console gives one signal, Pinky's taps none.
- **Unlock:** wrong PIN `ErrLocked`, right PIN 2.4 s with the companion.
- **Device tests:** all pass, or skip what needs video (stream, background displays) or a calculator.

### Timings (emulators unless said)

| What | Time |
| --- | --- |
| `ScreenOn` | 10 to 30 ms (Mi 11 to 170 ms) |
| `Wake` from asleep | 0.46 s |
| `Unlock` with the right PIN | 1.5 s with the companion (Android 16), 2.4 s (Android 10), 4.2 to 5.0 s with uiautomator reading the PIN pad |
| `Unlock` with a wrong PIN, until `ErrLocked` | about 7 s (the 4 s wait for the lock screen to go included) |
| `Unlock` by taps on a pad read from its labels, from the screen off | 3.3 to 3.8 s with the companion, 8 to 12.5 s with uiautomator; a wrong PIN, until `ErrLocked`, about 6 s (Android 10, 16 and 17 emulators, up to four running) |
| `ListFiles` (4 entries), push 300 KB, pull it back | 3 to 14 ms, 12 to 25 ms, 4 to 7 ms |
| `CompanionState` | 7 to 33 ms (Mi, read-only: 79 ms) |
| `InstallCompanion`: turn on only; replace a build signed with another key | 0.18 s; 0.39 s (no Play Protect on the emulators) |
| Background display ready | 0.35 to 0.6 s (Mi 1.1 s) |
| Control-only fallback after a failed encoder | one extra session start, about 0.13 s |

## What works where

| | Mi 10 Pro, Android 17 | Emulator, Android 16 | Emulator, Android 10 | Pixel, Android 10 |
| --- | --- | --- | --- | --- |
| Observe, Screenshot, input, launch, settle | Yes (rounds one and two; Calculator tests again today) | Yes | Yes, without video | Partly (round one) |
| Stream | Yes | Yes | No: the image's encoder fails | Yes (round one) |
| Wake, ScreenOn | ScreenOn read-only | Yes | Yes | |
| Unlock | Yes, by the owner from the computer (key events, past the fingerprint screen) | Yes, right and wrong PIN; also by taps on the pad read from its labels (so is an Android 17 emulator) | Yes; also by taps | Not tried |
| Files | Not tried | Yes | Yes | |
| Companion state and install | State only: `outdated` (build 1 vs 69) | Yes, all states | Yes, incl. replacing a build signed with another key | |
| Background display | Yes, Calculator, unlocked | Yes, also off and locked | No (needs video) | |
| Person input | Pinky's input gives no signal (round two) | Finger: one signal; Pinky: none | Same | |
| Pairing | Fake servers only; a real pairing needs someone at a phone | | | |

## The Mi 10 Pro in round three

- 09:25: read-only. Screen on and unlocked on the launcher, AC powered, `stay_on_while_plugged_in=15` (`mStayOn=true`): its screen never times out while charging. Other tools' files are in `/data/local/tmp` (AndroMeldAgent, frida-server, Mika), so the owner's own tooling may have set it. Reported to the main session at once.
- 09:40: `CompanionState` = `outdated`. A background display with the Calculator, 8 x 9 = 72; main screen untouched.
- 10:35: `TestDeviceCalculator`, `TestDeviceBackground` and `TestDevicePersonQuietWhileInjecting` (Calculator). The first two passed. The third counted **5 person signals** while Pinky gave input (round two: 0), and afterwards the Mi was **locked with its screen off** (`showing=true`, dozing), although Stay awake is on. The input dispatcher's recent queue had key events and two resets of input device 4 at that time. Pinky never sends the power key (no key code for it, no scrcpy option for it), so someone most likely pressed it and touched the screen: the watcher saw them. As the rules ask, the Mi was left as it was and not used again. The accessibility setting was back to the owner's service, and no `getevent`, `app_process` or Pinky file was left on it.

## Emulator state left behind

- Both: PIN `1234` (`locksettings set-pin`), `stay_on_while_plugged_in=0`, the accessibility setting as found (`null`), the companion session's build 73. `emulator-5560` is unlocked on its home screen; the companion session left `emulator-5562` locked and asleep. `screen_off_timeout` is 600000 on `emulator-5560`; on `emulator-5562` it went back to the image's maximum after the restart. Gboard's stylus tutorial was dismissed on `emulator-5560`.
- `emulator-5562` runs with `-gpu swiftshader_indirect` since a restart at 09:11 (it did not help the encoder).
- `/sdcard/Pinky` exists on both and is empty. No Pinky jar is left in `/data/local/tmp`.

## Decisions

Rounds one and two (kept):

1. **Direct abstract-socket connections, no `adb forward`.**
2. **A jar per session, deleted by the server.**
3. **Wake with `KEYCODE_WAKEUP`, never POWER.** Round three removed every scrcpy option that could press POWER or keep a screen awake.
4. **`clipboard_autosync=false`.**
5. **Settle on capture cadence, not size,** 400 ms quiet.
6. **Typing:** the companion's `set_text` first; keys when they reach the field, paste otherwise.
7. **Rows merged into one element,** the way TalkBack reads them.
8. **`App` is the read window's package.**
9. **Background displays without system decorations, keyboard kept local.**
10. **Companion settle: video and events both quiet, tracked locally.**
11. **`set_text` falls back only after a refusal.**
12. **Person input from real devices only** (no `uinput`), 300 ms apart.
13. **Notification text stays out of logs.**

Round three:

14. **Read display 0's power state, not the wakefulness:** a background display keeps the phone awake with its own screen off.
15. **Unlock only after seeing the PIN pad.** Typing digits at a lock screen that has not shown the pad could land some of them in it and count as a failed attempt, so the driver reads the tree first, even at 2 s with uiautomator. `wm dismiss-keyguard` brings up the pad on AOSP-style lock screens without a swipe that vendors place differently; the swipe is the fallback.
16. **The PIN goes as key codes through scrcpy.** Not `input text` (the PIN would be in a process list) and not `set_text` (it would pass through the companion app).
17. **Background `Locked` is false from Android 13,** so the gateway does not stop an agent on a background display because the phone is locked.
18. **A control-only session when the encoder fails,** rather than a driver that cannot tap at all on such a phone.
19. **Restarts wait for the gesture in progress** (one gesture at a time per session), instead of a second session for streams with other options, which would double the encoder's work on the phone.
20. **Replacing a build signed with another key is part of Install:** the person asked for the current companion, the old one cannot be updated, and it is Pinky's own app. Its service is turned off before the uninstall, so Android does not rewrite the person's accessibility setting.
21. **`CompanionState` reports `off` while the service is on only for the driver's reads,** so the client's Install card reflects what the person chose, not the driver's last minute.
22. **The bundled APK's version comes from `assets/pinky-companion.json`,** which the companion build writes, rather than parsing the binary manifest.
23. **Pairing finds the phone by its guid,** and connects by hand only when the adb server has not connected by itself after a second.
24. **Multi-touch devices count as touchscreens on a phone without a direct one** (the emulators): otherwise the watcher would see nothing there.
25. **A background display gives the person's tasks back and destroys only the agent's.** Destroying everything (scrcpy's default) lost the person's task when the agent used an app they had open; moving everything back put the agent's last app on top of the person's screen. Given back, the person's task also lands on top, which is the smaller harm.
26. **A no-op key keeps a background display awake** while it is open, rather than a wake lock (which would need a change to the bundled scrcpy server or the companion app) or `KEYCODE_WAKEUP` (which wakes the phone's own screen). It is sent only after 4 s without other input on the display.
27. **The last driver to leave turns the companion off,** so two Pinky processes (or Pinky and `androidctl`) do not cut each other off and the setting still goes back: the app's three-minute backstop did not turn the service off on `emulator-5560` in a four-minute wait with no client: build 73 restarted its count whenever Android bound the service again, which every `uiautomator dump` causes. Build 74 fixed that, but older builds (the Mi's) still have it, so the driver does not rely on the backstop.

## Not done, and open questions

- **Android 10 to 12 with a background display while locked:** expected not to work (see above); needs a real phone.
- **A wake lock instead of the keep-awake key:** the companion session could add a `keep_awake` command that puts a 1-pixel, untouchable accessibility overlay with `FLAG_KEEP_SCREEN_ON` on a given display, which recent Android versions turn into a wake lock for that display. Not built or tested; the key works.
- **A real Wi-Fi pairing:** needs someone at a phone. The adb server here has mDNS (`adb discovery`).
- **The Mi:** locked since about 10:36, probably by someone at it; Stay awake keeps its screen on while charging, which the owner may want off. It still has companion build 1; `InstallCompanion` there will replace it and Play Protect will ask.
- **The Pixel:** still offline.
- **Screenshot on a virtual display** returns `ErrUnsupported` (`screencap` rejects virtual display ids); decode a frame from the display's stream.
- **Screens that never stop moving** reach the settle limit; the phone app's favorites page on the Android 16 emulator is one.
- **uiautomator unbinds every accessibility service** while it dumps; with the companion on, the driver never runs it.
