# iOS driver notes

Status of `internal/driver/ios`, the CoreDevice backend, and what step 0 found on a real iPhone. Measured on 2026-11-07 with an iPhone 13 mini (iPhone14,4) on iOS 27.0.1 (24A446), connected by USB to a Mac with macOS 27.2 and Xcode 27.2; the developer disk image was mounted and Developer Mode on. A second pass the same night added element frames through `remoteAXService`, the provider's inspection cache, and touches in video pixels. The third (round three, the next morning) added typing by paste, `Waker` and `Unlocker`, measured the phone over Wi-Fi, and read Calculator's display as its value.

## Step 0 results

| Item | Result | Numbers |
| --- | --- | --- |
| Tunnel without root | **Works.** A userspace tunnel in Pinky's own process, next to macOS's CoreDevice tunnel | Tunnel up in 0.25–0.35 s, RSD handshake 0.13–0.15 s, 85 services, MTU 16000 |
| Screen stream | **Works.** HEVC over RTP through the userspace tunnel, depacketized to Annex B | 1136x2464 HEVC Main, up to 60 fps, 1–3 Mbit/s typical (cap 6), first packet 0.6 s after the start request, keyframe 70 ms after a request, 0 packets lost over USB |
| Touch | **Works.** Tap, swipe, long press and raw touches through the universal HID service, on one long-lived session. Raw touches take video pixels; the video is the screen at 1:1 plus black padding | Touch-down to the first changed video frame: 100–131 ms, median 109 ms (still Settings page) |
| Keys | Home **works** (Indigo button event); Back is a left-edge swipe and **works**; Recents returns `unsupported` | |
| Text | **Works for any text**, pasted: the pasteboard is saved, set to the text, Command-V goes through the virtual keyboard, and once the field shows the text the pasteboard is put back. Checked in Settings' search with the Pinyin keyboard active: English, Chinese, emoji, appending, and the pasteboard byte for byte the same afterwards. Without the pasteboard service, keys: printable ASCII, switching a composing keyboard (Pinyin) to the Latin one with Control-Space and back | Paste: 0.26–0.46 s per call (the save 2–70 ms, the paste confirmed in the field 0.1–0.36 s, the restore 25–50 ms), about 1 s for the first call in a session. Keys: 4–5 s for "clock" with the switch |
| Element tree (pass/fail) | **Pass.** Through `remoteAXService`, the service Xcode 27's Device Hub uses: label, role, value and frame for every element on screen, nothing installed on the phone. (The inspector API, the first attempt, gives no frames on iOS 27, as go-ios found on 18.4.1.) | Act, settle, `Observe` in one session: Settings 33–261 ms (12 elements, all with frames), Calculator 340–481 ms (23; it waits for the push that carries the display's number), once 1.07 s, over 18 launches in round three (the second pass had seen 2.2 s and 3.0 s in 2 of 9; they did not come back). Without settling, right after a launch: 0.8–1.6 s, which the phone sets. A walk alone: 0.19 s (Settings), 0.29–0.31 s (Calculator), 0.32 s (home screen). Before, inspector and OCR: 1.35 s for Settings with 13 of 38 elements placed, 0.65–0.70 s for Calculator with 8 of 23, and 13.5 s for the first OCR in a process |
| OCR placement | **Fallback only.** macOS Vision through purego places elements by label when `remoteAXService` fails, and adds text the tree lacks when asked | Vision 0.1–0.4 s per screenshot (13.5 s the first time in a process); 9 of 10, 12 of 13, 18 of 26 elements placed on Settings, Clock, home screen |
| Screenshot | **Works**, two services with fallback | 1125x2436 PNG, 0.35 s (Settings) to 0.6–0.75 s (home screen, 5–7 MB) over USB |
| Wake, lock state | **Partly tested.** The screen's state (CoreDevice's backlight state) and the lock state read right on the awake, unlocked phone, and `Wake` leaves the app in front alone. Waking a dark screen and `Unlock` could not be tried: the side button is off limits, the test phone never turns its screen off by itself, and nobody may type its passcode. Unit tests cover the unlock steps; see "What the owner should try" | Screen state 12 ms |
| Wi-Fi | **Works, but too slow and lossy to be the default.** The same tunnel, video, HID, `remoteAXService` and pasteboard run through usbmuxd's network connection | See "Wi-Fi" below: screenshots 3.4–5.3 s, walks 1.7–2.3 s, 1.5–10 % of video packets lost |
| Windows and Linux | **Not tested.** The package cross-compiles without cgo for both | |

What these mean for the design:

- The userspace tunnel removes the "tunnel may need administrator rights" prerequisite on every OS: no utun, no wintun, no sudo. It still needs usbmuxd (built into macOS; Apple Devices on Windows; the `usbmuxd` package on Linux).
- The element tree, the part step 0 feared, works without WebDriverAgent: `remoteAXService` gives frames, and the phone even pushes the whole visible tree after every change, so `Observe` after an action usually costs tens of milliseconds. It needs `clientNeedsAccessibility`, which turns on the phone's accessibility runtime for the session (Device Hub does the same). OCR stays as the fallback.
- USB matters: an iPhone that also syncs over Wi-Fi is listed twice by usbmuxd, and a tunnel over Wi-Fi is several times slower and loses video packets (see "Wi-Fi"). The driver picks the USB entry; Wi-Fi works but stays off by default.

## What is built

`internal/driver/ios` implements `driver.Provider`, `driver.Device`, `driver.Toucher`, `driver.LockReporter`, `driver.Settler`, `driver.Waker` and `driver.Unlocker`.

| File | What |
| --- | --- |
| `tunnel.go` | Userspace CoreDevice tunnel: the `CoreDeviceProxy` lockdown service carries bare IPv6 packets; a gVisor netstack (TCP and UDP) runs them in-process. go-ios dials services through a 127.0.0.1 forwarder that speaks go-ios's userspace-tunnel preamble and only reaches the device's tunnel address |
| `conn.go` | Tunnel plus RSD handshake. The first handshake announces the host (`HandshakeAsHost`), once per tunnel, as iOS 27.2 requires |
| `coredevice.go` | CoreDevice RemoteXPC requests, always with protocol 629.3 / DDI version 2 (some features refuse the old version), with dial and request timeouts |
| `offer.go`, `display.go` | The media stream offer (adapted from go-ios, MIT) and start/stop. Payload type 100's feature list is `FLS;SW:1;` instead of Xcode's `FLS;VRAE:0;SW:1;`: VRAE:0 makes the encoder drop frames under motion (pymobiledevice3's measurement) |
| `rtp.go`, `sps.go` | RTP parsing; RFC 7798 HEVC (single NAL, AP, FU; PACI skipped) and RFC 6184 H.264 (single, STAP-A, FU-A) depacketizers; the codec is sniffed from the NAL header because payload type 100 carries HEVC. The display service's 14-byte slice trailer is stripped. SPS parsers for the picture size |
| `media.go` | The long-lived stream: access-unit assembly, `driver.Packet`s (a config packet before each keyframe, PTS from the 24 kHz RTP clock), RTCP receiver reports every second (the device stops after 10 s without them), a per-frame receipt like Xcode's, picture loss indications when a subscriber needs a keyframe (repeated every 600 ms until one comes), loss handling, fan-out to `Stream` subscribers |
| `hid.go`, `keys.go` | Universal HID touch reports (surface 257), Indigo button events (Home), a virtual HID keyboard registered with `createService`, ASCII to US-layout usages |
| `pasteboard.go`, `type.go` | The pasteboard service (PULL and SET); `Type` by paste with the pasteboard saved and put back, and the keys fallback with the Latin keyboard switch |
| `power.go` | `Waker` (screen state from the backlight, Home) and `Unlocker` |
| `device.go` | The session: one tunnel, one media stream, one HID connection; taps and swipes in screenshot pixels and raw touches in video pixels, mapped to the panel's 0..65535 range |
| `axp.go`, `rax.go`, `observe_rax.go` | `remoteAXService`: a keyed-archive reader and writer for AccessibilityPlatformTranslation's classes, the session (requests one at a time, the walk, the trees the phone pushes), and `Observe` from it (pushed tree or walk, roles, labels, frames in pixels, other processes' elements, the keyboard) |
| `ax.go`, `caption.go`, `observe.go` | The fallback: the inspector walk, caption parsing into label, role and states, OCR placement, keyboard detection, the foreground app |
| `ocr.go`, `ocr_darwin.go`, `ocr_other.go` | Vision text recognition through purego (no cgo); other OSes return `unsupported` |
| `screenshot.go`, `apps.go`, `lock.go`, `settle.go`, `provider.go`, `display_info.go` | The rest of the interface |
| `diag.go`, `cmd/iosctl` | Diagnostics and the debugging command |

Coordinates are screenshot pixels: 1125x2436 on an iPhone 13 mini, which is the size UIKit renders (375x812 points at 3x); the panel is 1080x2340 and the video 1136x2464. `Toucher` points are video pixels (the contract since 5e83552). Matching decoded frames against screenshots (row and column brightness profiles, correlation 0.998 and better on Settings and Calculator) showed the video is the screen at 1:1 in the top-left corner with black padding, 11 columns on the right and 28 rows at the bottom, so a video point is the same screen point and `Touch` only clamps points in the padding to the screen's edge. A video smaller than the screen would be scaled back up; no phone tested does that.

### Decisions

- **One media stream for the whole session.** HID input is dropped unless a stream from the same client runs, so `Open` starts the stream and keeps it; `Stream` returns a subscriber view that starts at a fresh keyframe, and closing it does not stop the device's stream. Restarting streams often can wedge the device's media daemon until a reboot (pymobiledevice3 reports it).
- **Own display, HID and CoreDevice clients on top of go-ios's transport** instead of go-ios's `display` and `hid` packages: go-ios discards the stream answer (SSRCs and the RTCP port, needed for receiver reports), refuses userspace tunnels for media, and its HID package has no buttons or keyboard.
- **go-ios from main** (`v1.3.3-0.20261103131045-6910f3b585ad`): the display and HID packages and the RSD host announcement are not in a release yet. Pin a release once one has them.
- **HID timestamps in 24 MHz ticks.** go-ios and pymobiledevice3 stamp reports in nanoseconds. With those, a 12 ms key press opened the press-and-hold accent menu (as if held for half a second) and swallowed the rest of the text; stamping in 24 MHz ticks, the phone's mach time unit, fixed it. So the phone reads mach ticks and nanoseconds make every interval look 41.7 times longer. Touch looked fine only because paging goes by distance; fling velocities were affected too.
- **Element frames from `remoteAXService`, the inspector and OCR as the fallback.** See "remoteAXService" below. `Observe` falls back when the service fails to connect or answer, and tries it again after 30 s.
- **A pushed tree when it is recent, else a walk.** The phone builds and pushes the screen's tree after every change, and requests wait while it does (1–4 s on screens with many images), so a walk right after an action is slow. `Observe` walks and waits for the push at once and takes whichever comes first. A push counts only if its first tree arrived at least 1 s after Pinky's last input or launch, the Observe comes within 6 s of that input, and the push's app is the frontmost app the walk's first request found: a push can arrive late, after the next input, and then describes the screen before it (a regression run caught a late home-screen push answering for Settings before this rule). After 6 s the walk alone answers, because the screen may have changed in ways the phone does not push (a timer's text).
- **Roles from AutomationType.** The walk and the second push give `XCUIElementType` (`Key` 20, `Icon` 44, `Cell` 75, ...), the same types XCUITest reports. A first push lacks it; then traits and the translator's own role decide (`AXPAttributeRole` 2 button, 3 switch, 7 image, 15 search field, as seen).
- **Roles from captions on the fallback path.** The audit daemon answers attribute queries (label, value, traits) only for the element holding the inspector focus, which would cost a round trip per element on top of the move. The walk keeps its moves pipelined and parses VoiceOver's caption ("Alarms, Button, Selected, Tab") with English and Simplified Chinese trait words; other languages get the whole caption as the label and the role `other`.
- **The provider asks a phone again only when needed.** The gateway lists devices every 2 s. `List` keeps each phone's answer until usbmuxd's entry for it changes (a new `DeviceID` on every attach) or, for a phone that is not online, for 30 s; an inspection cut short by its context is not kept. `Open` reuses the answer and refuses a phone reachable only over Wi-Fi unless `Wireless` is set.
- **Foreground app from the elements.** An element handle's first four bytes are the owning process id; the process list (`listprocesses`) and the installation proxy's bundle paths turn it into a bundle id and name. The daemon's app-state events came once and never again, so they are only a fallback.
- **Screenshots from the instruments service first**, CoreDevice's screen capture second. Both got stuck on the device at times (most likely after a test process was killed mid-request); the fallback and dial timeouts keep calls from hanging.
- **Lock state from lockdown's `PasswordProtected`**, the value `devicectl` reports as `passcodeRequired`. iOS 27's developer disk image does not implement CoreDevice's `getlockstate` (the feature is listed but `dtdeviceinfod` has no implementation).
- **Typing pastes** (the owner's decision). `Type` saves the pasteboard, sets it to the text, presses Command-V on the virtual keyboard, waits until the focused field's value changes (read through `remoteAXService`: the frontmost app's focused element, attribute 19, then its value), and puts the pasteboard back. It chooses before touching the field, so text it cannot type leaves the field as it was. Restoring waits for the paste because restoring first would paste the person's own clipboard into the field (and show it to the agent); without `remoteAXService` it waits 0.5 s. What is saved stays in memory for one call and is never logged or shown; `TypeStats` says only how it went.
- **Pinky's pasteboard writes stay on the phone.** The phone's daemon (the DDI's `dtpasteboardd`) writes with `UIPasteboard setItemProviders:localOnly:expirationDate:` and passes `localOnly` exactly when the SET carries `sourceMetadata` (disassembled from the DDI's `CoreDeviceUtilities`: `localOnly = sourceMetadata != nil`, the branch that also records "sync metadata"). pymobiledevice3 sends none, so its writes go out through Universal Clipboard and replace the clipboards of the owner's Mac and iPad. Pinky always sends `{nonce, pasteboardName, changeCount}`. Checked: the Mac's pasteboard change count (a number; its contents were never read) stayed the same through every write.
- **What the pasteboard keeps.** `Type` reads each item's first representation (`promiseSecondary`, always quick), then all of them (`allResolved`) for up to 3 s; if that read stalls (an app listing a type it cannot produce makes the daemon stall for about 64 s, pymobiledevice3 found with Notes) it keeps the first representations. The daemon refuses to read an item marked as coming from another device through Universal Clipboard (`com.apple.is-remote-clipboard`), a password (`org.nspasteboard.ConcealedType`) or transient (`org.nspasteboard.TransientType`, `de.petermaurer.TransientPasteboardType`, `org.nspasteboard.AutoGeneratedType`), with CoreDeviceError 26006; such a pasteboard cannot be saved, so `Type` empties it after pasting instead of leaving the text there. The test phone held a Universal Clipboard item when this round started. A pasteboard that changed since Pinky's own SET (someone copied, or a Universal Clipboard item arrived) is left as it is.
- **Keys only without the pasteboard service**: printable ASCII through the virtual keyboard. Before typing, a probe letter (deleted again) shows whether the active keyboard composes: Pinyin puts a candidate bar into the app's own tree (cells whose identifier is the candidate, "啊"), sometimes only after a few hundred milliseconds, so the probe looks for up to 1 s. If it composes, Control-Space switches to the other keyboard for the text and back afterwards, 300 ms after the last key (switching back at once let Pinyin take the English word just typed into a composition, "Cloc k", in one run); if the other keyboard composes too, the call returns `unsupported`. Latin keyboards capitalize the first letter of a field (the hardware keyboard's Auto-Capitalization setting), which keys cannot avoid; pasting is not affected. `TypeStats.Trace` records the probes.
- **Waking with Home, never the side button.** The screen's state is CoreDevice's display info `backlightState`: `activeOn` and `activeDimmed` count as on, `off` and `inactiveOn` (the always-on display of newer phones) as asleep. `Wake` does nothing on a screen that is on: Home would leave the app.
- **Unlocking.** `Unlock` wakes the screen, returns at once if lockdown says the phone is unlocked, swipes up from the bottom edge, and waits up to 3 s for the passcode pad (buttons for all ten digits, or a secure field for an alphanumeric passcode; it also stops there if Face ID unlocks the phone). Without the pad it returns `ErrLocked` having typed nothing. It types the passcode on the virtual keyboard 120 ms per key, checking before each key that lockdown still says locked (once it does not, the keys would go to the app in front), presses Return unless the passcode is four or six digits (those submit themselves), and waits up to 4 s for lockdown to say unlocked, else `ErrLocked`. It never types the passcode twice.
- **Wi-Fi stays off by default.** See "Wi-Fi" below. `Open` uses the USB entry unless `Info.Connection` is "wifi".
- **Values in identifiers.** Calculator's display has no label or value, only the identifier `StandardInputView;value:144`. An identifier with `;value:` gives its name as the identifier (stable, for refs) and the rest as the value, and such an element reads as text, so the display is `text "144"`.

## How to run

Hold the device lock for every command that touches the phone.

```sh
go build -o /tmp/pinky-iosctl ./internal/driver/ios/cmd/iosctl
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> rsd
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> observe -ocr
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> stream -seconds 10 -out /tmp/pinky-stream.hevc
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> latency -vertical      # on a still list such as Settings
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> tap 285 2283
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> swipe 900 1218 225 1218 -ms 300
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> key home
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> type -append "hello 你好"
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> launch Calculator
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> screenshot -out /tmp/pinky-shot.png
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> screen     # is the screen on
scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> locked     # lockdown only, no session
/tmp/pinky-iosctl mux                                               # usbmuxd's entries, no phone contacted
```

`iosctl unlock` reads a passcode from standard input and unlocks: only for the owner (see "What the owner should try").

`iosctl devices` lists every phone usbmuxd sees and opens a tunnel to each to check its services, so do not run it while other sessions use their phones. Keep captures under `/tmp/pinky-*` and delete them afterwards.

`iosctl observe` prints where the tree came from (`remoteax`, `remoteax-push` or `inspector`) and the timings; `ObserveStats.Trace` says what the remoteax path waited for. `iosctl type` prints how it typed (paste or keys) and what happened to the pasteboard, never the text. Each `iosctl` command is a new process with no earlier input, so it always walks; the device tests below measure the push path.

Tests: `go test ./internal/driver/ios/` runs the unit tests (RTP and SPS parsing, depacketizers, access-unit assembly, the offer against a captured one, the keyed-archive reader and writer, trees from pushes, elements and roles from the tree including other processes and the keyboard, caption parsing, keyboard detection, OCR placement, HID reports, the provider's cache, video-to-screen points) without a phone. The real-phone tests share one session and use only the home screen, Settings, Calculator and Clock:

```sh
PINKY_DEVICE=<udid> scripts/devlock.sh <udid> go test -tags device -count=1 -v ./internal/driver/ios/
```

All 16 pass on the iPhone 13 mini (inspect, stream, home screen observe, swipe and raw touch, apps, launch Settings by name, Calculator tap, screenshot, lock state, unsupported keys, latency, typing by paste into Settings' search with the pasteboard checked after each call, typing by keys with the keyboard switch, waking, a Calculator key tapped where Observe puts it and the display read back as `7`, Observe timing after launches). `TestDeviceConnection` measures one connection in a session of its own: `PINKY_CONN=usb` or `wifi`, run alone with `-run TestDeviceConnection`. `PINKY_NO_SETTLE=1` makes the timing test observe right after each launch; `PINKY_SHOTS=1` saves the typing test's screenshots under `/tmp/pinky-ios` (delete them afterwards).

## Details and data

### Tunnel

- The device gives each tunnel its own /64 (macOS's own tunnel was `fd3e:…`, ours `fd4f:…`), so both coexist. macOS 27's own tunnel is not routable from other processes (no utun route), so Pinky cannot borrow it.
- The device grants the MTU asked for (16000), which keeps per-packet overhead low. Over USB a 6.9 MB screenshot arrives in about 0.75 s including the phone's PNG encoding, so the userspace stack moves at least 9 MB/s; gVisor's TCP receive autotuning is on.
- `HandshakeAsHost` once per tunnel matters (go-ios's finding, not re-tested here): without the host's RemoteXPC version flags, services that send large replies (screenshots) crash on the device.

### Video

- The answer's `streamConfig`: `RxPayloadType` 100 (HEVC inside), `Framerate` 60, `KeyFrameInterval` 0 (no periodic keyframes), `RXMaxBitrate` 6000000, `RTCPTimeoutEnabled` with 10 s, `CustomWidth`x`CustomHeight` 1136x2448 while the SPS says 1136x2464 (the SPS is right; ffprobe agrees).
- The phone also creates a virtual "Wireless" display (id 2, 1136x2448) for the stream.
- RTCP is multiplexed on the receiver port; the phone sends a sender report about every second. Its NTP time against our arrival gives 55–95 ms one way, if both clocks agree.
- After a change the phone sends 60 fps for about 5–7 s, then stops sending frames until the screen changes again (a status bar clock tick restarts them); its RTCP sender reports keep coming every second throughout. While frames do come, a still screen gives identical frame sizes and motion varying ones (5–75 KB). The size of a still frame depends on the encoder's current bitrate: about 1–2 KB when idle, but after motion the encoder raises its bitrate and pads still frames to a constant 10 KB for seconds. So "no frames, or frames of one size" marks a still screen, not small frames; the settle detector uses that (step 0 item 4). The stream's watchdog counts RTCP too: only silence on both for 5 s means the phone stopped the stream.
- A subscriber that joins while frames have stopped gets the parameter sets and a fresh keyframe within about 25 ms of its request.
- Settle data from Settings: tapping "General" changed the first frame 331 ms after touch-down (the tap is held 60 ms, so about 270 ms after touch-up, including the app's own response); the push animation's frames were 58–74 KB; frame sizes became constant about 2.3 s after the tap. A fling in a list keeps the picture moving for about 2.5 s (measured as frame-to-frame pixel difference in the decoded video). Home screens with widgets (Photos, Clock) never look still.
- The audit daemon's screen and layout change notifications arrived in one session and not in others, so they are only a hint for settling.
- Without loss there are no keyframes after the first. Subscribers that join later ask for one; the phone ignores a request that comes right after a keyframe, hence the retry.
- Packet loss happened only over Wi-Fi and while large screenshots shared the tunnel; the gVisor UDP buffer never overflowed.

### Input

- Touch positions are 0..65535 on each axis of the panel in its native (portrait) orientation. Landscape is not handled yet: the mapping assumes `rot0`.
- `Key(back)` swipes from the left edge at mid-height over 250 ms. It starts at x=0 so the system gesture wins; a stray drag cannot select anything, but avoid screens where a drag at mid-height matters.
- The virtual keyboard makes iOS treat the phone as having a hardware keyboard while the session lasts; the software keyboard still showed in Settings until a key was pressed. It is registered at the first key, not at `Open` (registering takes about 0.35 s), and goes away when the HID connection closes. `Type` without `Append` sends Command-A and Delete first.
- Keys go through the active input method. On the test phone the Pinyin keyboard was active: "clock" typed as keys became a composition (Command-A did not select it, one Delete left "c lo c" with U+2006 spaces between the syllables), and Escape appeared to cancel it. That is why `Type` pastes (the owner's choice between pasting and switching keyboards); keys remain only as the fallback above. Command-V and Command-A work with Pinyin active as long as nothing is being composed.

### Pasteboard

- The service: `com.apple.coredevice.pasteboardservice` (DDI `dtpasteboardd`, launchd label `com.apple.coredevice.dtpasteboardd`, entitlement `com.apple.private.CoreDevice.canAccessPasteboard`), bare XPC dictionaries with `command`, replies on the server-to-client stream; requests need the wants-reply flag. Messages: `PULL {pasteboardName, dataPolicy}` → `PULL_REPLY {pasteboard: {items, metadata, sourceMetadata}}`; `SET {pasteboardName, items, sourceMetadata}` → `SET_REPLY` with the new snapshot. Items are `{types: [UTI], data: {UTI: {data}}}`; a promised representation comes as `{isPromised, isAvailable, size}`. Metadata is `{nonce (the phone's boot session), pasteboardName, changeCount}`, and a PULL returns the `sourceMetadata` of the SET that made the current contents, so Pinky can tell its own write.
- Errors come as `{error: {code, domain, userInfo}}` (CoreDeviceError). 26006: "The pasteboard contains type '…' indicating its contents are transient, confidential, or already synchronized by another mechanism, and was not synchronized."
- `SET` with no items empties the pasteboard. Timings over USB: the first request on a connection 0.55 s (the RemoteXPC handshake; `Open` now connects the service in the background), then PULL 2–35 ms, SET 8–30 ms.
- No paste permission prompt or banner appeared: Command-V from a keyboard counts as the person's paste.

### Keyboards

- The test phone's language is English (Singapore); lockdown's `com.apple.international` `Keyboard` names the default keyboard (`en_GB@sw=QWERTY;hw=Automatic`), not the active one, which was Pinyin (Simplified Chinese).
- With Pinyin active, a typed letter shows a candidate bar in the app's own tree (`Cell` elements labelled "啊, 叹词的啊" with the identifier "啊", and a "More suggestions" button). `AXPAttributeTextInputMarkedRange` (123) answered nothing; the field's value shows the uncommitted letters.
- Control-Space switched Pinyin to English (UK) and back; two presses restore the keyboard. After hardware key presses iOS hides the on-screen keyboard until the field is tapped again. A probe's walk right after a switch took 1.1–1.8 s (the phone was pushing a tree).

### Wi-Fi

With Finder's "Show this iPhone when on Wi-Fi", usbmuxd lists the phone twice (USB and Network). Opening the Network entry runs everything Pinky uses: the tunnel (CoreDeviceProxy through usbmuxd's network connection), video, HID, `remoteAXService`, screenshots and the pasteboard. The same session measured over each (`TestDeviceConnection`, two Wi-Fi runs):

| | USB | Wi-Fi |
| --- | --- | --- |
| Session open | 0.58 s | 1.5–2.6 s |
| Touch-down to changed frame | 73–154 ms | 6 ms–1.5 s (median 150–270 ms) |
| Observe, walk | 0.3 s (pushes 32–38 ms) | 1.7–2.3 s |
| Screenshot (home screen) | 0.67–0.78 s | 3.4–5.3 s |
| Type (paste) | 0.26–0.46 s | about 2 s |
| Video | 0 packets lost, no keyframe requests | 1.5–10 % of packets lost, 10–27 keyframe requests in 55 s |

Everything goes through TCP inside TCP over Wi-Fi (gVisor's TCP in the tunnel, inside usbmuxd's connection), so each round trip costs tens of milliseconds and a walk, which is one round trip per request, pays it 25–40 times; the video is UDP in the tunnel and the phone drops it when the tunnel backs up. So `Provider.Wireless` stays off: the mirror would stutter, agents would wait seconds per step, and turning it on would also make the gateway open every iPhone and iPad paired over Wi-Fi (the owner's iPad among them) and show screen sharing on them. Apple's own Wi-Fi path is a different tunnel (remote pairing over QUIC, pymobiledevice3's `remote start-tunnel` over Wi-Fi), not tried here; it is the way to make Wi-Fi good.

### Element tree (inspector, the fallback)

- The walk: `deviceInspectorFocusOnElement:` then `deviceInspectorMoveWithOptions:` (first, then next) in batches of 8, until an element repeats; it covers the visible app including off-screen cells of scroll views (Settings lists all 38 rows, only the visible ones get positions), but not the keyboard (another process) or the status bar. It does not scroll the app.
- Per element: caption (label, value, traits, hint as VoiceOver speaks them), accessibility identifier, an opaque handle. Captions are localized.
- Tried for frames on iOS 27, all empty: attributes `Frame`, `AXFrame`, `frame`, `accessibilityFrame`, `Bounds`, `Position`, `Size`, `ElementRect`, `AXAttribute-2003` and more; hit testing (`deviceFetchElementAtNormalizedDeviceCoordinate:` with an `NSValue` point) answers nothing; `deviceFetchSpecialElement:` 0 and 1 return the first and last element, not the app. The audit (`deviceBeginAuditTypes:`) reports rects only for flagged elements (8 "element detection" issues on the home screen, 3.9 s).
- Side effect found and fixed: the inspector focus draws a ring on the focused element (visible in the video) until `deviceInspectorPreviewOnElement:` is sent with null, which every walk now does.
- A null element handle in `deviceElement:valueForAttribute:` broke the daemon connection; never send one.

### remoteAXService

How it was found: Xcode's `AccessibilityAudit.framework` (in `Xcode.app/Contents/SharedFrameworks`) holds both ends. On the host, `AXAuditRemoteDevice` owns the DTX connection and serves as the transport of an `AXPHostCacheManager` (from the system's `AccessibilityPlatformTranslation.framework`): it sends what the manager hands it with `processDataFromHost:` and feeds what the phone sends through `processDataFromRemoteDevice:` back to it. On the phone, `AXAuditDevicesAppRemoteServer` does the reverse for the phone's `AXPTranslator_iOS`. The Objective-C runtime (through purego) listed the classes and methods, lldb disassembled the few methods that mattered, Apple's own `NSKeyedArchiver` showed the request format, and `AXPTranslatorRequest`'s `description` named the attribute numbers.

The protocol, as iOS 27.0.1 speaks it:

- DTX on the tunnel (`com.apple.accessibility.axAuditDaemon.remoteAXService`), global channel only. Send `_notifyOfPublishedCapabilities:`; `deviceAPIVersion` answers 26.
- `clientNeedsAccessibility:` with YES is required: without it requests get no answer. NO makes the phone close the connection, so it means goodbye. The phone then asks the host `hostAPIVersion` (Xcode answers 7, and so does Pinky).
- A request is an `NSKeyedArchiver` archive (secure coding) of `AXPTranslatorRequest` {`requestType`, `attributeType`, `actionType`, `clientType` (Xcode's cached-tree client type, 2), `parameters`, `translation` (`AXPTranslationObject` {`pid`, `objectID`, `isApplicationElement`, ...})}, sent as the `NSData` argument of `processDataFromHost:`. The answer comes as `processDataFromRemoteDevice:` with an archived `AXPTranslatorResponse` {`associatedRequestType`, `attribute`, `resultData`, `associatedTranslationObject`, `notification`, `error`}.
- Request types used: 2 one attribute (`attributeType`), 4 the frontmost app's element (18–19 ms), 5 several attributes with `parameters` {`attributes`: [numbers]} (the key `AXPAttributes` gives an empty answer). 11 is the tree dump the phone pushes; asked for by the host, it got no answer.
- Answers carry no request ID, so requests go one at a time: 5–60 ms each when the phone is idle, about 10 ms per element in a walk. A request for all 131 attributes at once got no answer within 2 s; a session with a request that timed out is dropped, since its late answer would be read as the next one's.
- Attribute numbers (from the description on macOS 27; the walk asks for the marked ones): 1 activation point, 3 attributed label, 8 children*, 9 children in navigation order*, 21 frame* (screen points), 23 help, 25 identifier*, 26 is element*, 27 enabled*, 28 focused*, 30 selected*, 32 visible*, 33 label*, 41 parent, 42 placeholder*, 45 role*, 53 value*, 61 automation type* (`XCUIElementType`), 62 image data (PNG), 64 value description*, 65 host application pid, 66 is remote element*, 69 remote element pid*, 77 traits*, 78 traits as text, 112 visible point, 114 braille frame, 124 responds to interaction, 128 memory address, 129 application orientation.
- Pushes: after `clientNeedsAccessibility` and after screen changes, 1.1–1.5 s later, the phone pushes `AXPTreeDumpTypeInitialDump` (the frontmost app's element and every element on screen with about 24 attributes: 21 KB for Settings, 36 KB for Calculator, 32 KB for the home screen), then `AXPTreeDumpTypeAdditionalData` (the same elements with about 60 attributes, AutomationType, identifiers and icon images included: 68 KB, 134 KB and 1.7 MB) and sometimes notifications. Not every change gets a push (Calculator's Clear did not). Decoding takes 0.3–4.5 ms.
- While the phone builds a push, requests wait. Walks measured 0.27–0.33 s on an idle Calculator and 1.08 s when one met a push; on screens whose second push carries many images, 2.9 s (home screen) and 4.6 s (Settings' search with the keyboard up). Hence the push rules above.
- What is listed: the elements on screen, in reading order (12–14 on Settings' first page, where the inspector walk found 38 rows including those scrolled out of view; 23–25 on Calculator; 26 on the home screen). Elements behind a sheet can be listed too (Settings' rows under its search suggestions).
- Other processes: the keyboard's keys (another process) come as children of the app, with frames relative to a host view listed just before them, which has traits bit 34 (`0x400000000`) and the keyboard's frame; the keys are `StaticText` labelled with one character. A home screen widget (Photos, another process) came with a frame relative to itself and no host; `Observe` leaves such elements out rather than place them wrongly. Attributes 66 and 69 stayed empty in both cases.
- Calculator's display has no label or value; its identifier carries the number (`StandardInputView;value:7`), which `Observe` shows as a text element with that value (`text "7"`, identifier `StandardInputView`). The identifier is not in a first push (that element has only its frame, traits and role there), so `Observe` waits up to 1.2 s for the second when a first push leaves an element unnamed; requests wait while the phone builds that push, so asking for the identifier would not be sooner. Calculator's keys are `Key` (20) with the keyboard-key trait, so the keyboard is recognized by its process (many one-character labels), not by that trait.
- The first read after an app opens, measured again in round three (18 launches): act, settle, `Observe` (what the gateway does) took 33–261 ms on Settings and 340–481 ms on Calculator (the wait for the push with the display's number), once 1.07 s; the 2–3 s seen in the second pass did not come back. `Observe` right after `Launch`, without settling, took 0.8–1.6 s: the phone's first answer (the frontmost app) alone took 0.73–0.83 s while the app started, and its push came 1.1–1.6 s after the launch.
- An app that has just launched can show its screen before it lists any element (a walk found only the app element once); `Observe` then waits up to 1.5 s for the push and walks again.

### OCR (fallback)

- Vision's accurate recognizer with `zh-Hans`, `zh-Hant`, `en-US`: 0.1–0.4 s per 1125x2436 screenshot once loaded. The first use on a Mac took 56 s (model compile), and runs with automatic language detection sometimes took 15 s, so detection is off.
- Placement matches labels to text in rounds (equal, truncated with an ellipsis, label inside a line, line inside the label) in reading order. Text that appears twice can be matched to the wrong element (a search field whose value equals a result title); elements without visible text (icons, dock apps, images) get no position.
- The on-screen keyboard is detected from OCR (five or more single letters in the lower half) and its keys are dropped from the output.

## What does not work yet

- Elements scrolled out of view are not listed (the phone lists what is on screen); agents scroll and observe again.
- Other processes' elements without a host view (home screen widgets) are left out. Web content in other apps (another process) is not tested.
- Elements behind a sheet can be listed with the sheet's (Settings' search).
- The pushed-tree rules trust timing: two inputs less than about 0.3 s apart (faster than the pacing rule allows) could still get the first one's push.
- While Pinky is connected the phone's accessibility runtime is on and it pushes a tree after every change: up to 1.7 MB (home screen) over the tunnel each time.
- Landscape: touch mapping, element frames and the screen size assume portrait.
- A pasteboard the phone does not let anyone read (a Universal Clipboard item, a password, transient content) cannot be saved; `Type` empties it after pasting. When reading every representation stalls, only each item's first one is put back. Restored contents stay on the phone (no Universal Clipboard) and carry Pinky's source metadata.
- Typing by keys (only without the pasteboard service): ASCII, the first letter capitalized by Latin keyboards, 4–5 s per call with a keyboard switch (each probe walks the tree, slowly while the phone pushes one).
- Wi-Fi: works but slow and lossy (above); `Wireless` stays off.
- Waking a dark screen and `Unlock` are unit-tested only; lock detection for phones without a passcode is not tested.
- iOS below 27 (needs WebDriverAgent), Windows, Linux (cross-compiles, untested).
- `WaitSettled` uses frame sizes (and accessibility notifications when they come); animated widgets keep it from ever settling on such home screens (it times out), and after motion the encoder's slowly falling bitrate can keep it waiting a little after the picture stopped.

## What the owner should try

`Unlock` and waking a dark screen could not be tried on the test phone. With the iPhone connected by USB and nobody else using it:

1. Lock it with the side button and leave it until the screen is dark.
2. `scripts/devlock.sh <udid> /tmp/pinky-iosctl -udid <udid> screen` should print `screen on: false`, and `locked` should print `locked: true`.
3. `wake` should light the screen on the lock screen (Home, as a keyboard's Home key would), and `screen` then print `true`.
4. `unlock`, then type the passcode and Enter: it should print `unlocked` within a few seconds, the phone showing its home screen or last app. Watch the phone: the passcode pad should come up after the swipe and fill one key at a time.
5. Once more with a wrong passcode: it should fail with "device locked" after one attempt (the phone counts one failed attempt) and type nothing more.

If the pad does not come up after the swipe (the phone may want Home instead with a keyboard attached), `unlock` stops with "the passcode pad did not appear" and has typed nothing. If the phone accepts the passcode but `unlock` still fails, lockdown's lock state is slow to change and `unlockWait` (4 s) needs raising.

## Security notes

- While a session runs, the tunnel's forwarder listens on 127.0.0.1 and lets any local process reach the phone's developer services through it (go-ios's own userspace tunnel does the same). It adds nothing an agent with shell access could not already do through usbmuxd, which the threat model covers, but the gateway's isolation advice applies to it too.
- Media streams make the phone show its screen-sharing indicator and block its camera and microphone; `Close` stops the stream, and the phone also stops it by itself 10 s after receiver reports stop (a crashed process).
- The virtual keyboard and any held finger are released on `Close`. A killed process cannot release them; the phone should drop them when the HID connection closes with the tunnel, but that is not tested.
- `Type` holds the person's pasteboard in memory for one call, never logs it, and its writes stay on the phone (`localOnly`). A process killed between the SET and the restore leaves the typed text on the pasteboard, and the saved contents are lost with the process.
- `Unlock` keeps the passcode only for the call and never logs it; it types it only while lockdown says locked and the passcode pad is on screen, so a Face ID unlock on the way cannot send it to an app. `iosctl unlock` reads it from standard input, not the command line.

## Notes for other sessions

- `go.mod` gains go-ios (main), gVisor and purego; expect a merge in `go.mod`/`go.sum`.
- No shared contract changed by this driver; it follows 5e83552 (Toucher points in video pixels).
- For the gateway and the client: `Stream` packets are HEVC Annex B; a `Config` packet (VPS, SPS, PPS) comes before every keyframe a subscriber receives; PTS counts from the stream's start on a 24 kHz clock. A new subscriber waits up to about 100 ms for a requested keyframe. No frames come while the screen is still (from about 7 s after the last change), so the mirror keeps showing the last frame and recordings have gaps. Element bounds and taps are screenshot pixels (1125x2436 here); raw touches are video pixels (1136x2464), which on iPhone are the same numbers inside the screen. The video's right 11 columns and bottom 28 rows are black padding; a mirror can crop the picture to the `Screen`'s width and height.
- `Open` connects `remoteAXService` in the background, which turns on the phone's accessibility runtime for the session (no visible change on the phone), and the pasteboard service. The provider caches its inspections (see Decisions).
- `Type` takes any text and leaves the pasteboard as it was (see Decisions for the cases it cannot). `Waker`: `ScreenOn` is quick (one CoreDevice request); `Wake` presses Home only on a dark screen. `Unlocker` follows the contract: one attempt, `ErrLocked` when the phone stays locked (or the passcode pad never showed, with nothing typed), `ErrUnsupported` for a passcode the keyboard cannot type. `Open` honours `Info.Connection == "wifi"`.
- `WaitSettled` returns no sooner than 500 ms after the call, once the last 250 ms brought frames of one size, or no frames while the stream is alive; a fling in a list takes 2–3 s to stop, and animated widgets never stop.
- During testing `iosctl devices` probed every phone usbmuxd listed, which included the owner's iPad (iPad13,4, over Wi-Fi): one tunnel and one RSD handshake, nothing else.
- The phone was left on its home screen; Settings was only browsed (back navigation, scrolling, searches for "wallpaper" and "clock" that were cancelled, and closed again through the close button). Once a test's cleanup tap landed on a search result and opened Settings > Apps > Clock; nothing there was touched (its Mobile Data switch stayed on), and the test no longer taps after typing. Calculator was left cleared.
- Round three: the phone's pasteboard held a Universal Clipboard item, which the phone does not let anyone read, so it could not be kept; the typing tests set their own pasteboard contents and leave it empty. The keyboard was switched to English (UK) and back to Pinyin by the keys test; it was left on Pinyin. The phone stayed unlocked and awake throughout; nothing was locked, no passcode typed, no setting changed.
- The on-screen keyboard check from OCR needs single letters spread across the screen: a list's alphabetical section index (Settings > Apps) fooled the first version.
