# Android driver

Status: working on the Mi 10 Pro (LineageOS, Android 17) and an Android 16 emulator; partly verified on the Pixel (Android 10) before it dropped off USB during the night. Branch `claude/android-driver-1aff78`.

## What was built

| Package | What it does |
| --- | --- |
| `internal/driver/android/adb` | A client for the adb server protocol: `host:version`, `host:devices-l`, `host:track-devices-l`, `host:transport:<serial>`, `shell:` (v1, interleaved output), `shell,v2,raw:` (stdout, stderr, exit code), `exec:` (binary-safe), `sync:` push, pull and stat, local sockets, and `forward` / `killforward` / `list-forward`. It reuses the running server; it never kills or restarts it. `StartServer` exists for a caller that finds no server; nothing calls it automatically. |
| `internal/driver/android/scrcpy` | The bundled scrcpy 5.0.1 server (`server/`, with its Apache-2.0 license and checksum) and its protocol: `key=value` options, the video stream headers, control messages, device messages, `start_app` results read from the server log, and `list_apps`. |
| `internal/driver/android` | The driver: `Provider`, `Device`, and the optional `Toucher`, `LockReporter`, `Clipboard` and `Settler`. Also `Device.OpenBackground` for a virtual display (`Background` implements `driver.Device` too) and `Device.VideoStats` for diagnostics. |
| `internal/driver/android/cmd/androidctl` | A debug command for every feature, with timings. |

No shared contract was changed.

### How it works

- **One scrcpy session per device.** It starts on first use (about 0.5 s on the Mi, 0.9 s on the Pixel), carries input, the H.264 stream and settle detection, and restarts by itself on the next call after it dies. The client connects to the server's abstract socket straight through the adb server (`localabstract:scrcpy_<scid>`), so no host port or `adb forward` is needed. Each run pushes its own jar (`/data/local/tmp/pinky-scrcpy-<scid>.jar`), which the server deletes once it starts, so nothing stays on the phone.
- **Server options Pinky always sets:** `audio=false`, `tunnel_forward=true`, `clipboard_autosync=false` (the phone's clipboard is read only when asked), and `power_on=false`: scrcpy would press POWER to wake the screen, which toggles it. Pinky wakes the screen itself with `KEYCODE_WAKEUP`, which can only turn it on: before an action, if the screen was not seen on in the last 10 s, it checks `dumpsys power` and wakes it if needed.
- **Stream** turns scrcpy packets into `driver.Packet` (Annex B, PTS, `Config`, `KeyFrame`). A new stream asks the encoder for a fresh configuration and key frame (`reset_video`) and starts at the configuration packet. Options that differ from the running session restart it. A consumer that falls 256 packets behind loses packets until it catches up, then gets a fresh key frame.
- **Input:** taps (finger down 50 ms), long presses (the phone's `long_press_timeout` plus 400 ms, at least 800 ms), swipes (a move every 8 ms) and raw touches go through scrcpy's touch messages, mapped from screen pixels onto the video frame. Keys map onto Android key codes; `recents` is `KEYCODE_APP_SWITCH`.
- **Type:** printable ASCII is typed as key events (`inject_text`, 300 bytes per message), unless the keyboard is a composing one (pinyin and other CJK input methods) and is showing, because such a keyboard turns letters into a composition. That text, and anything that is not printable ASCII (CJK, emoji, newlines, which typed would press Enter), is set as the clipboard and pasted with one `set_clipboard` message, so **pasted typing replaces the phone's clipboard** until the companion app's `ACTION_SET_TEXT` takes over. Decision 6 below has the details. Without `Append`, Ctrl+A selects the field's text first so the new text replaces it; empty text with Ctrl+A then Delete clears the field. `Submit` presses Enter.
- **Launch and Apps:** the app list comes from the server's `list_apps` (launcher apps, named in the phone's language; 0.5 s on the Mi), cached for 5 minutes. `Launch` takes a package or a display name, matched ignoring case: exactly, then by prefix, then by substring; more than one hit is an error that lists them. The app starts with `start_app`, like a tap on its icon; the server's log says whether it started.
- **Lock state:** `dumpsys window policy` (`showing=` under `KeyguardServiceDelegate` on Android 10 and 17; older field names too). `Locked` is true while the lock screen shows, even if an app such as an alarm covers it.

### Observe

`uiautomator dump /dev/tty` (falling back to a temporary file on phones where that fails) gives the element tree; `dumpsys` gives the lock and keyboard state in parallel.

- Roles come from class names: inputs (`EditText` and friends), switches, checkboxes and radio buttons, tabs (`TabView`, bottom navigation items), lists (anything scrollable, or a known list class), buttons (button classes, or anything clickable), images, text, else `other`.
- Labels: the text, else the content description, else the last part of the resource id. Inputs show their content, or their hint while empty; password fields never show their content. Whitespace is collapsed and labels are cut at 200 characters.
- A clickable container without its own label takes the labels of its plain children (up to six, 160 characters, joined with ` · `) and the check state of a plain switch or checkbox inside it, the way TalkBack reads a row. So a Settings row reads `switch "Wi-Fi · Connected" checked` instead of three loose lines. Containers larger than 40% of the screen take nothing, so a full-screen tap target cannot swallow the page. Children that repeat their parent's label are dropped.
- Only nodes that are clickable, long-clickable, checkable, scrollable or editable, or that carry text, are kept. Bounds are clipped to the screen; nodes outside it are dropped.
- States: focused, checked, selected, scrollable, disabled, editable (inputs), password.
- `Path` joins the ancestors' class names and resource ids, without sibling indices, so refs survive unrelated layout changes.
- `App` is the package of the dumped window (so the notification shade shows as System UI), named from the app list. `Width` and `Height` are `wm size` turned by the dump's rotation, the same pixel space as `screencap`. `Keyboard` comes from `mInputShown` (and `mImeWindowVis` on new versions).
- `ObserveOptions.OCR` is ignored; OCR runs on the computer. No events (toasts, notifications) are reported; that needs the companion app.

### Settle detection

The design said "encoded frames shrink to idle size". On a real phone they do not: after every change scrcpy's encoder re-encodes the last frame about ten times, 100 ms apart, to sharpen it (Android's repeat-previous-frame option), and those refinement packets are often 20 to 80 KB at 1080x2340. A size threshold kept every action "changing" for over a second.

Their timing gives them away instead. Real changes (animations, scrolling, transitions) arrive at the display's refresh rate (every 11 ms on the Mi's 90 Hz panel); refinements arrive exactly 100 ms after the previous frame. So a packet counts as a change unless it is a refinement (85 to 125 ms after the previous capture time, by the phone's PTS, which transport jitter does not affect), a key frame or configuration (encoder restarts and the periodic key frame), or tiny (below width x height / 8000 bytes, 315 bytes on the Mi, for a blinking cursor). The screen has settled once no change arrived for 400 ms; the limit is 3 s by default. Both are in `Config`.

Limits: an app that reacts more than 400 ms after a tap looks settled before it reacts; a screen that never stops moving (video, a live wallpaper, a spinner) always hits the limit, which the gateway reports as `settle: timeout`.

## Using it from the gateway

```go
p := android.NewProvider(android.Config{Logf: log.Printf}) // driver.Provider
infos, _ := p.List(ctx)            // reads each phone's model and name once per connection
dev, _ := p.Open(ctx, infos[0].ID) // open each phone once; every open device runs its own scrcpy session
dev.(driver.Settler).WaitSettled(ctx, 0) // 0 means Config.SettleMax (3 s)
```

- Errors that mean the phone or the adb server is gone wrap `driver.ErrOffline`; an unknown app or package wraps `driver.ErrNotFound`; an ambiguous app name is a plain error listing the candidates; unmapped keys wrap `driver.ErrUnsupported`.
- `Provider.OpenDevice` returns the concrete `*android.Device` for `OpenBackground` and `VideoStats`.
- `Locked` costs one adb shell round trip (about 45 ms); `Observe` already fills `Screen.Locked`.
- The driver does not refuse input on a locked phone; the gateway decides (`device_locked`).
- `Config.StayAwake` turns on scrcpy's stay-awake (it changes `stay_on_while_plugged_in` for the session and scrcpy restores it). It is off by default because Pinky's test rules forbid changing settings.

## How to run

```sh
go build -o /tmp/pinky-android/androidctl ./internal/driver/android/cmd/androidctl
A=/tmp/pinky-android/androidctl
scripts/devlock.sh 3e8be48e $A -s 3e8be48e observe
scripts/devlock.sh 3e8be48e $A -s 3e8be48e launch com.android.calculator2   # or the name the phone shows for it
scripts/devlock.sh 3e8be48e $A -s 3e8be48e tap 151 1990
scripts/devlock.sh 3e8be48e $A -s 3e8be48e tap 669 2209 -long
scripts/devlock.sh 3e8be48e $A -s 3e8be48e type hello
scripts/devlock.sh 3e8be48e $A -s 3e8be48e key back
scripts/devlock.sh 3e8be48e $A -s 3e8be48e stream -seconds 5 -out /tmp/pinky-android/x.h264
scripts/devlock.sh 3e8be48e $A -s 3e8be48e run launch:com.android.settings swipe:540,1800,540,900 key:back
scripts/devlock.sh 3e8be48e $A -s 3e8be48e bg -app com.android.calculator2 -out /tmp/pinky-android/bg.h264 'type:6*7' key:enter
```

`bg` opens a virtual display, starts the app there and runs the steps on it; decode the recorded stream with `ffmpeg -i bg.h264 -vf setparams=colorspace=bt709 f%03d.png` to see it. `run` performs steps with person-like pacing and prints, for each, the action time, the settle time and the frames seen; set `ANDROIDCTL_TRACE=1` to list every video packet with its PTS gap and whether it counted as a change. `devices` needs no lock.

Tests:

```sh
go test ./internal/driver/android/...          # unit tests, hand-written fixtures
scripts/devlock.sh 3e8be48e env PINKY_DEVICE=3e8be48e go test -tags device ./internal/driver/android/...
```

The device tests use only the home screen, Settings search (no setting is changed) and the Calculator, and put the clipboard back after the paste test without logging it.

## Measurements on the Mi 10 Pro

1080x2340, 90 Hz, USB. From `androidctl run` and the device tests, 2026-11-07.

| What | Result |
| --- | --- |
| Action to settled, median | **1.31 s** over 21 actions (min 0.40 s, max 1.53 s): 11 Calculator taps, Home, Back, two launches, four Settings scrolls. An earlier run of 19 gave the same median. A Calculator tap animates for about 0.86 s, then 0.4 s of quiet. The first action after opening a device adds about 0.5 s to start the scrcpy session. |
| `uiautomator dump` (whole `Observe`) | 2.11 to 2.27 s on the home screen, Calculator and Settings (32 to 40 elements). Almost all of it is uiautomator waiting for 1 s of UI idle plus its JVM start; it cannot be made faster without the companion app. |
| Video | H.264 at native 1080x2340, 8 Mbit/s target. While in use (the 21 actions over 31 s): 45.7 fps and 5.8 Mbit/s on average; the busiest second 88 fps and 27.9 Mbit/s (scrolling Settings). Calculator taps alone: 56 fps, 2.8 Mbit/s. A still home screen: 4.7 fps, 1.2 Mbit/s, from status-bar updates and their refinements; a truly still screen sends nothing. |
| Session start | 0.47 to 0.53 s (push, start, connect); 0.9 s on the Pixel. |
| Other | `screencap -p` 0.75 s; app list 0.53 s for 56 apps (Pixel 1.8 s for 22); a `dumpsys` query such as `Locked` 40 to 49 ms through the Go client; virtual display ready in 1.1 s. |

## What works where

| | Mi 10 Pro, Android 17 (LineageOS) | Pixel, Android 10 | Emulator, Android 16 |
| --- | --- | --- | --- |
| List devices, model, name, version | Yes | Yes | Yes |
| Observe in the screen format | Yes | Yes (home screen) | Yes |
| Screenshot, same size as Observe | Yes | Yes | Yes |
| Tap, long press, swipe | Yes (long press: holding Delete cleared the Calculator) | Taps yes (Calculator) | Taps and swipes yes |
| Keys (home, back, enter, ...) | Yes | Yes | Yes |
| Type ASCII | Yes (pasted while the Doubao keyboard shows; as keys otherwise) | **Failed** under the first rule: letters typed as keys did not reach the Settings search field (see below); not retested | Yes (Gboard, English: keys) |
| Type CJK (paste) and clipboard | Yes | Not reached | Yes |
| Launch Settings and Calculator by package or name | Yes | Yes | Settings yes (no Calculator app) |
| Stream and settle | Yes | Stream yes (scrcpy test); settle hit the 3 s limit on its home screen | Yes |
| Lock state | Yes (unlocked) | Yes (asleep, no lock screen) | Yes |
| Wake a sleeping screen | Not needed | Yes (`KEYCODE_WAKEUP`) | Not needed |
| Background mode (virtual display) | **Yes**: the Calculator opened on display 70+, 6 x 7 = 42 by taps and by typed keys, the main display stayed on the home screen, and the display and its app were gone after Close | Not tried | Not tried |

The emulator belonged to the companion session (its app was in front); after one short run under its lock its app was put back in front.

### The Pixel

The Pixel dropped off USB at about 01:45 during the typing test, while this session held its lock: adb said "closed" mid-command, then "device not found", and macOS stopped listing it as a USB device (the Mi and the iPhone stayed). Nothing sent could reboot or power it off (the driver had woken it with `KEYCODE_WAKEUP`, opened Settings search and typed). It had not come back by the end of the night. Likely causes: an old battery browning out with the screen on, a reboot that did not finish, or the cable. **Please check it in the morning.** A note is in the shared notes folder.

Before it dropped, typing "pinky" as key events into the Settings search left the field empty. The likeliest cause: on a Chinese-locale phone Gboard was in pinyin mode, so the letters went into a composition instead of the field. The rule was changed after that (paste while a composing keyboard shows), and the Mi, whose keyboard is ByteDance's Doubao pinyin keyboard, now types ASCII correctly either way. The Pixel still needs a run of `TestDeviceTypeAndClipboard`.

### A Play Protect dialog on the Mi

At about 01:44 the companion session's `adb install` of Pinky Companion made Google Play Protect show "Send app for security check?" on the Mi; that session left it alone, as the rules ask, and released the Mi's lock when its install returned at about 01:54. This session's tests were queued behind that lock, and their first action at about 01:54 was a Home key press, so if the dialog was still up, the Home press moved it out of view. Nothing on it was tapped: every later tap on the main display followed an observe or a launch, and the first observe a few minutes later showed the launcher. A read-only check at 02:08 found no Play Store or verifier activity left in the activity stack, no lock screen, and `app.pinky.companion` installed. Lesson for test scripts and for the gateway: observe before the first action on a phone someone else may have used.

## Decisions

Made without the owner, as the brief asked.

1. **Direct abstract-socket connections, no `adb forward`.** The adb server can open `localabstract:` on the device itself, so there is no host port to pick or clean up. The adb package still implements forwards for other users (the companion client).
2. **A jar per session, deleted by the server.** Pushing 734 KB takes about 50 ms over USB; in exchange nothing stays on the phone and concurrent server runs (a session plus `list_apps`) cannot delete each other's jar.
3. **`power_on=false`, wake with `KEYCODE_WAKEUP`.** scrcpy's own wake presses POWER, which toggles the screen and could turn it off in a race; WAKEUP can only turn it on. The check runs only when the screen has not been seen on for 10 s (40 ms).
4. **`clipboard_autosync=false`.** Otherwise scrcpy streams every clipboard change to the computer. Pinky reads the clipboard only when asked, which the design treats as an Ask action.
5. **Settle on capture cadence, not size.** See above. Quiet period 400 ms: 300 ms would shave 0.1 s off every action, but apps that start reacting after 300 to 400 ms would look settled too early.
6. **Typing: keys when they reach the field, paste otherwise.** Key events never touch the clipboard but a pinyin keyboard swallows them into a composition, and paste works in every text field but not in apps without one (the Calculator ignores it). So ASCII goes as keys unless the keyboard is a composing one (a known CJK input method, or any keyboard on a Chinese, Japanese or Korean phone except AOSP-style Latin ones) and is showing (40 ms check). Non-ASCII and newlines always paste. Pasting replaces the clipboard, and the driver does not put the old content back: restoring it races with the app reading the paste and could paste the person's own clipboard into a field. The companion app's `ACTION_SET_TEXT` removes the problem.
7. **Merging rows into one element.** The spec says label = text, else description, else resource id. A clickable row with no text of its own would then show as `button "item_layout"` next to loose text lines, so a row takes its children's labels (`button "Display · Dark theme, font size"`), which matches the design's own example (`button "Place order · $12.80"`), keeps commit detection on the element that is tapped, and halves the element count on list screens. Nodes with their own label keep it.
8. **`App` is the dumped window's package,** not the focused activity: what the agent sees (the notification shade shows as System UI).
9. **Background displays without system decorations, keyboard kept local.** With decorations, the taskbar appeared and hid during the first seconds and moved the Calculator's keys between taps (a tap meant for 6 hit 3). Without them the app fills the display. `display_ime_policy=local` keeps any soft keyboard on the virtual display, off the person's screen. `SystemDecorations` turns them back on.
10. **Background mode is a separate method** (`Device.OpenBackground`, returning a `driver.Device`) rather than a `StreamOptions` field, so the shared contract stays as it is and the gateway can treat the display as a device of its own.

## Not done, and open questions

- **Locked phone and virtual displays:** not tested, as agreed; to do with the owner. scrcpy creates its displays with the always-unlocked flag; whether apps there keep running while the phone is locked is step 0 item 6.
- **Observe and Screenshot on a virtual display** return `ErrUnsupported`: `uiautomator dump` reads only the main display, and `screencap` rejects virtual display ids (also with `-a`). The gateway can decode a frame from the display's stream (the design's plan for screenshots anyway), and the companion app can read other displays.
- **Toasts, notifications, dialogs** (`Screen.Events`) are not reported; uiautomator cannot see toasts. Companion app.
- **OCR:** `ObserveOptions.OCR` is ignored.
- **Takeover from touches on the phone** (`getevent`), **files** (`phone_files`; the adb package has push and pull), **Xiaomi's "USB debugging (Security settings)" check** and **wireless pairing** are not part of this task.
- **Screens that never stop moving** (video, live wallpapers, spinners, a busy status bar) always reach the settle limit; `uiautomator dump` fails on them after about 10 s ("could not get idle state"), which Observe reports as an error.
- An H.264 quirk for the client session: the Mi's encoder marks its stream with the YCgCo matrix, which FFmpeg 8 refuses to convert to RGB unless told the color space (`setparams=colorspace=bt709`). The desktop decoder may need the same override.
