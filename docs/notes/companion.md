# Android companion

The companion is a small Kotlin app with an accessibility service, plus its Go client. It gives the Android driver a fast element tree, events (window changes, toasts, notifications, content changes), text input with `ACTION_SET_TEXT` and a settle signal. The wire protocol is in [companion-protocol.md](companion-protocol.md).

Status, 2026-11-07: built and tested on the Pixel (Android 10) and on an Android 16 emulator. **Not verified on the Mi 10 Pro**: installing the APK there brought up a Google Play Protect dialog, and per the safety rules nobody answered it (see [What does not work yet](#what-does-not-work-yet)).

## What is where

| Path | What |
| --- | --- |
| `android/companion/` | Gradle project (wrapper included). Package `app.pinky.companion`, minSdk 29, targetSdk 36, compileSdk 36.1 (the newest platform in the local SDK). AGP 9.2.1 with its built-in Kotlin, Gradle 9.4.1, no AndroidX. The debug APK is about 2.7 MB. |
| `.../PinkyAccessibilityService.kt` | The service: starts the socket server when Android connects it, feeds events to the hub, turns itself off after 3 minutes without a connection |
| `.../CompanionServer.kt` | Abstract socket `@pinky_companion`, peer check, one thread per connection |
| `.../RequestHandler.kt` | Parses requests; runs everything that touches the tree on one worker thread |
| `.../TreeDumper.kt` | Windows and nodes to JSON, with per-window timing |
| `.../TextSetter.kt` | Target resolution and `ACTION_SET_TEXT` |
| `.../EventHub.kt` | Event conversion, content-change folding, subscriber queues, `wait_idle` |
| `.../AppLabels.kt` | App display names, loaded in the background |
| `.../MainActivity.kt` | Debug page (own process `:debug`): status, a text field, a password field, toast and notification buttons |
| `res/drawable/ic_launcher_*.xml` | Adaptive icon converted by hand from the brand SVGs (`pinky-android-foreground.svg`, `pinky-android-background.svg`) into vector drawables; the SVG transforms are kept as groups, and the body's radial gradient is drawn in its unit box so it stretches the way SVG's `objectBoundingBox` does |
| `internal/driver/android/companion/` | Go client: `Client` (Ping, Tree, Dump, SetText, Events, WaitSettled, Enable, Disable, ForceDisable, Install, InstallIfChanged, Installed), tree-to-screen conversion, `Runner` (adb) |
| `internal/driver/android/companion/cmd/companionctl` | A command-line tool for trying all of it by hand and for the benchmark |

No shared contract changed. The client implements `driver.Settler`.

## How to build and run

```sh
cd android/companion
export ANDROID_HOME=~/Library/Android/sdk
export JAVA_HOME="/Applications/Android Studio.app/Contents/jbr/Contents/Home"
./gradlew assembleDebug          # app/build/outputs/apk/debug/app-debug.apk
```

By hand, through the device lock (every command from `ping` on turns the service on for its run and restores the setting afterwards; `-keep` leaves it on):

```sh
go build -o /tmp/pinky-companionctl ./internal/driver/android/companion/cmd/companionctl
ID=FA6C20303860
scripts/devlock.sh $ID /tmp/pinky-companionctl -s $ID install android/companion/app/build/outputs/apk/debug/app-debug.apk
scripts/devlock.sh $ID /tmp/pinky-companionctl -s $ID tree            # the screen as agents see it
scripts/devlock.sh $ID /tmp/pinky-companionctl -s $ID tree -raw       # the raw JSON tree
scripts/devlock.sh $ID /tmp/pinky-companionctl -s $ID settext -id app.pinky.companion:id/debug_input "你好"
scripts/devlock.sh $ID /tmp/pinky-companionctl -s $ID events toast window
scripts/devlock.sh $ID /tmp/pinky-companionctl -s $ID bench 10        # companion tree vs uiautomator dump
scripts/devlock.sh $ID /tmp/pinky-companionctl -s $ID disable         # cleanup after a crash: removes Pinky's entry
```

`install` skips the install when the phone already has the same APK (compared by SHA-256); `install -force` always installs.

Tests:

```sh
go test ./internal/driver/android/companion/                    # synthetic data only
scripts/devlock.sh $ID env PINKY_DEVICE=$ID go test -tags device -v ./internal/driver/android/companion/
```

The device test installs the APK if it changed, turns the service on, reads Settings and the Calculator, writes Chinese and emoji into the debug page (by resource id, by point with append, by node handle), checks the password field and a `not_found` error, waits for a toast event, then turns the service off and checks that `enabled_accessibility_services` is exactly what it was. `PINKY_COMPANION_APK` overrides the APK path.

## Turning the service on and off

- `Enable` reads `enabled_accessibility_services`, appends only `app.pinky.companion/.PinkyAccessibilityService` and keeps every other entry, then pings until the service answers (up to 10 s). If it never answers, Enable restores the setting itself.
- `Disable` removes the entry again. When nothing else changed, it writes back exactly the original text (or deletes the key if it did not exist), even if Android rewrote the list in its own order or short form meanwhile. When the person changed something else during the task (say, turned TalkBack on), only Pinky's entry goes and their change stays.
- If Pinky's service was already listed when Enable ran (the person turned it on, or an earlier run crashed), Enable leaves the setting alone and Disable does not turn it off. The service's own idle watchdog covers the crash case: after 3 minutes without any connection it calls `disableSelf()`. `ForceDisable` removes Pinky's entry whoever added it.
- `accessibility_enabled` is never written. On the Pixel, Android set it from 0 to 1 when the service started and back to 0 when it stopped.
- This setting is the only one Pinky changes, and only its own entry, as docs/WORKING.md requires.

## Results

### Pixel (FA6C20303860), Android 10

| Check | Result |
| --- | --- |
| `adb install -r` | Works, no prompt |
| Enable, ping | Answers 1 to 2 s after the setting changes; `null` before, `null` after |
| Tree: home screen, Settings, Calculator, debug page | Works. Settings: 12 elements ("网络和互联网, WLAN、移动网络、流量使用和热点" and so on, rows merged into one button each). Calculator: 25 elements (digits by text, operators by description: "乘", "等于") |
| `set_text` Chinese and emoji into the debug page | Works: "你好，Pinky 世界 🎉" read back and verified; the debug page's echo (another process) shows 13 characters |
| Events | Toast with CJK text, notification (title and text) |
| Tree time, phone side | Settings 53 to 60 ms when the screen has not changed, 167 ms on the first read after it changed; Calculator 42 to 46 ms |

### Android 16 emulator (API 36, Google Play image, arm64 on the Mac)

The Pixel lost power before the final test run and the Mi 10 Pro was blocked, so the device test suite ran on a temporary emulator (created for this, then deleted).

| Check | Result |
| --- | --- |
| Device test suite | Passes: Settings, every `set_text` case (resource id, point with append, node handle, password field, `not_found`), toast event. Calculator skipped: the image has none. |
| Enable | Answers 230 ms after the setting changes |
| Notification event | Works once the app may post notifications (granted inside the emulator only) |
| `wait_idle` after starting an activity | Settled after 1.0 s |
| `include_not_important` | Settings: 29 nodes, 3.4 ms median; with it on, 60 nodes, 4.8 ms (`uiautomator dump`: 63 nodes) |
| Keyboard | The input method window reports only the keyboard's area (`[0,1111,1080,1920]`), so elements under the keyboard are dropped and lists above it keep their visible part |

### Tree speed against `uiautomator dump` (step 0, item 5)

Round trip from the computer: the companion's time includes adb forwarding and JSON decoding; `uiautomator dump` is `uiautomator dump <file> && cat <file>` through `adb shell`, the way a driver without the companion has to read it. Ten samples each, taken while the same app stayed in front (`companionctl bench 10`).

| Device | Screen | Companion tree | `uiautomator dump` | Ratio |
| --- | --- | --- | --- | --- |
| Pixel, Android 10 | Settings | median 70 ms (phone side 63 ms), 31 nodes | median 2118 ms (min 1983, max 2892), 33 nodes | 30x |
| Emulator, Android 16 | Settings | median 4 ms (phone side 2 ms), 29 nodes | median 1975 ms (min 1946, max 2004), 63 nodes | ~500x |
| Emulator, Android 16 | Launcher | median 6 ms, 15 nodes | median 1956 ms, 30 nodes | ~300x |
| Mi 10 Pro, Android 17 | | not measured (blocked) | not measured | |

`uiautomator dump` pays about 2 s for starting `app_process`, connecting a UiAutomation and waiting for the app to go idle, whatever the screen. The companion pays one round trip to the app for the root of each window (20 to 40 ms on the Pixel, under 1 ms on the emulator) and reads the rest from Android's node cache. By default it walks only views that are important for accessibility; `uiautomator dump` walks all views, which roughly doubles the node count but adds no labels or actions.

## Decisions

- **Abstract socket with a peer check.** Simplest transport for `adb forward`, nothing on disk, gone when the service stops. Any app can connect to an abstract socket, so the server accepts only root, shell (adbd) and its own uid; anything else gets `forbidden`.
- **The debug page runs in its own process.** In the service's own process, Android's accessibility cache did not refresh a text view that changed after `set_text` (the field itself refreshed, the echo next to it did not). Every real app is another process, so the debug page is one too.
- **Only what agents need, by default.** Views that are not important for accessibility, the keyboard's keys and the status and navigation bars are left out: they add nodes and time but no labels or actions, and the status bar clock would change every minute and show up in every diff. Each can be turned on (`include_not_important`, `ime`, `bars`). Windows that are not walked still count for occlusion.
- **Elements, not nodes.** A clickable row without its own label takes its descendants' text as one label ("Network & internet, Wi‑Fi, …") and their switch or checkbox state, the way a screen reader reads it; the texts are not listed again. A target merges only while it covers at most half the screen and holds at most 12 texts, so a clickable root never swallows the screen. A target with its own label keeps its children's texts as separate lines. Unlabeled targets are listed only when small and mostly on screen.
- **Labels and roles.** Label: content description, else text; inputs show their text, else hint, else description; labels are trimmed, whitespace collapsed, capped at 1000 runes (merged ones at 500). Roles map class names onto the screen model's roles (`EditText` to input, `Switch`/`ToggleButton` to switch, `CheckBox`/`RadioButton`/`CheckedTextView` to checkbox, `TabView` to tab, scrollables and list classes to list, clickables to button, images to image), and an app's role description ("Tab", "Switch", "link") wins when present. States follow the order of the constants in `internal/screen`; `editable` is not repeated on inputs.
- **Paths** are the window type and package, then each ancestor's short class name and resource id name (`application:com.android.settings>FrameLayout>RecyclerView#recycler_view`). No sibling indices, so refs survive rows being inserted above.
- **Occlusion.** An element whose center lies under a higher window (keyboard, dialog, status bar) is cut to its largest uncovered part, or dropped when nothing useful is left.
- **Password text never leaves the phone.** The app omits `text` for password fields (with "speak passwords" on, some versions expose the real characters), `set_text` does not read it back, and the client ignores it even if one arrives.
- **Foreground app.** The top-most focused or active application window, or a focused full-screen system window (the notification shade, the lock screen). App names load in the background (the first lookup of a package loads its resources, 100 ms or more on the Pixel), so the first tree after an app opens may use the window title instead.
- **Active window root through the cache.** `AccessibilityWindowInfo.getRoot()` bypasses the node cache and drops what it prefetched, so every child costs another round trip; `getRootInActiveWindow()` keeps the prefetched nodes. Together with not walking the bars, this took the Pixel's Settings tree from 58 to 169 ms down to 53 to 60 ms.
- **Node handles** are `AccessibilityNodeInfo.hashCode()` (the view and window ids), stable while the view lives, so `set_text` can target exactly the node a tree showed.
- **Submit on Android 10.** `ACTION_IME_ENTER` exists from Android 11. Below that, `set_text` reports `submitted: false` and the caller presses Enter itself; the client does not inject keys behind the driver's back.
- **Events** are folded for content changes (one per 100 ms per connection, with a count) and queued per subscriber, so a slow reader never blocks the service. Times are converted to the host's clock using the skew `ping` measures.
- **Idle watchdog** of 3 minutes without any connection, changeable with `config`. It also turns off a service the person enabled by hand, which matches "accessibility on only during tasks".
- **Runner interface.** The client needs only `Shell`, `Forward`, `RemoveForward` and `Install`. `ExecRunner` uses the adb command line (the SDK's adb first, as the design says); the Android driver can pass its own adb implementation when the main session integrates the two.

## What does not work yet

- **Mi 10 Pro not verified.** `adb install -r` there opened Google Play Protect's "Send app for a security check?" dialog (always send / send this time / don't send) at about 01:44, and the install waits for it. Per the safety rules it was left alone and the phone was not used again; the Android driver session was told. The owner needs to answer it once; after that, `install` skips unchanged APKs, so it should not come back for the same build. The companion was never enabled there, and `enabled_accessibility_services` there is unchanged (the owner's Mika service). To finish: answer the dialog, then run the device test and `companionctl bench 10` on Settings and the Calculator (`com.android.calculator2/.Calculator`).
- **The Pixel went offline** at about 02:00 and is no longer on the USB bus. Most likely its battery ran flat: Settings showed it discharging while plugged in, at 21% and seven minutes later 17%, with the screen kept on by the tests. It needs charging before more tests. The APK changes made after that (toast text, one refactor in the tree dumper) ran on the emulator only.
- **Play Protect on other phones.** Any phone with Google Play and "verify apps over USB" will ask the person once for an unknown APK. Setup should explain it, and a release build signed with a stable key would help Play recognize the app.
- **`uiautomator dump` turns the companion off while it runs** (Android unbinds every other accessibility service for a UiAutomation), so a driver must not mix the two; the client reconnects once by itself afterwards.
- **Screen off.** With the screen off, Android lists only the status bar window, so the tree is empty. The driver should check the power state itself.
- **Not tested:** virtual displays (the `display` parameter exists for scrcpy's background mode), WebView, Compose and Flutter apps, landscape on a real phone, toasts and notifications with the companion running for hours, and `include_not_important` on Android below 13 (it cannot clear the node cache there).
- **Not done:** the APK is not shipped with the Go binary (`*.apk` is ignored by git). The main session should decide between building it in CI and embedding it with `go:embed`, or downloading it from GitHub releases. No release signing config exists yet.
- **Possible speedups:** non-active windows (dialogs, the shade) still pay a cache-bypassing root fetch per tree; a client could also keep one subscription and request trees only after `content` events.

## Integration notes for the driver

1. When a task starts: `InstallIfChanged(apk)`, then `Enable`. When it ends, or after about a minute idle: `Disable`.
2. `Observe`: `Tree` gives a `screen.Screen` without refs; add `ScreenEvent()` results from an `Events` subscription to `Screen.Events`, filtered by the gateway's policy.
3. `Type`: `SetText(Target{Point: &center, ID: element.ID}, text, SetTextOptions{Append, Submit})`, or `Target{}` for the focused field. When `Submit` was asked and `Submitted` is false, press Enter through scrcpy.
4. Settle: the client implements `driver.Settler` (`WaitSettled`, 300 ms of quiet), which can back up the frame-size detector.
5. Fall back to `uiautomator dump` only when the companion is not available, never while it is on.
