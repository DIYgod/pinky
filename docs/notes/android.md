# Android driver

Status: working on the Mi 10 Pro (LineageOS, Android 17); partly verified on the Pixel (Android 10) before it dropped off USB. Branch `claude/android-driver-1aff78`.

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
- **Type:** printable ASCII is typed as key events (`inject_text`, 300 bytes per message). Anything else, including newlines (which typed would press Enter), is set as the clipboard and pasted with one `set_clipboard` message, so **non-ASCII typing replaces the phone's clipboard** until the companion app's `ACTION_SET_TEXT` takes over. Without `Append`, Ctrl+A selects the field's text first so the new text replaces it; empty text with Ctrl+A then Delete clears the field. `Submit` presses Enter.
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
- `Locked` costs one adb shell round trip; `Observe` already fills `Screen.Locked`.
- The driver does not refuse input on a locked phone; the gateway decides (`device_locked`).
- `Config.StayAwake` turns on scrcpy's stay-awake (it changes `stay_on_while_plugged_in` for the session and scrcpy restores it). It is off by default because Pinky's test rules forbid changing settings.

## How to run

```sh
go build -o /tmp/pinky-android/androidctl ./internal/driver/android/cmd/androidctl
A=/tmp/pinky-android/androidctl
scripts/devlock.sh 3e8be48e $A -s 3e8be48e observe
scripts/devlock.sh 3e8be48e $A -s 3e8be48e launch Calculator
scripts/devlock.sh 3e8be48e $A -s 3e8be48e tap 151 1990
scripts/devlock.sh 3e8be48e $A -s 3e8be48e type hello
scripts/devlock.sh 3e8be48e $A -s 3e8be48e key back
scripts/devlock.sh 3e8be48e $A -s 3e8be48e stream -seconds 5 -out /tmp/pinky-android/x.h264
scripts/devlock.sh 3e8be48e $A -s 3e8be48e run launch:com.android.settings swipe:540,1800,540,900 key:back
scripts/devlock.sh 3e8be48e $A -s 3e8be48e bg -app com.android.calculator2 tap:151,1990
```

`run` performs steps with person-like pacing and prints, for each, the action time, the settle time and the frames seen; set `ANDROIDCTL_TRACE=1` to list every video packet with its PTS gap and whether it counted as a change. `devices` needs no lock.

Tests:

```sh
go test ./internal/driver/android/...          # unit tests, hand-written fixtures
scripts/devlock.sh 3e8be48e env PINKY_DEVICE=3e8be48e go test -tags device ./internal/driver/android/...
```

The device tests use only the home screen, Settings search (no setting is changed) and the Calculator, and put the clipboard back after the paste test without logging it.

## Measurements on the Mi 10 Pro

(Filled in below from `androidctl` runs.)

## What works where

(Filled in below.)

## Not done, and open questions

(Filled in below.)
