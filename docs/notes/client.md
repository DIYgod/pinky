# Desktop client and video

Status as of 2026-11-07, branch `claude/desktop-client-380250`. The client was built against a scripted fake gateway, then run against the real gateway (`internal/gateway`) on `internal/demo`'s drawn phones with scripted agents; see [Against the real gateway](#against-the-real-gateway). Nothing here touched a real phone.

## What is built

| Path | What it is |
| --- | --- |
| `ui/` | The desktop client on MyGo v0.7.4 native UI. Entry point `ui.Run(api gateway.API, opts ui.Options) error`. It uses nothing of the gateway but `gateway.API`. |
| `ui/fakeapi/` | A scripted `gateway.API`: the prototype's scenarios with ffmpeg test streams for the mirrors. It also has the gateway's extra methods (step screenshots, agents, read-only, deleting history). |
| `ui/demogw/` | The real gateway on `internal/demo`'s phones, with their drawn screens streamed through `video.FrameStream`, and scripted agents that call it as an MCP client would. For `pinky-ui-demo -gateway` and the end-to-end tests. |
| `internal/video/` | H.264 and HEVC decoding through FFmpeg 9 (libavcodec 63, libavutil 61, libswscale 10) called with purego, VideoToolbox hardware decoding with software fallback, Annex B helpers, a live test stream from the `ffmpeg` command, and a player that decodes on its own goroutine and keeps the newest picture. |
| `cmd/pinky-ui-demo/` | Runs the client against the fake (`go run ./cmd/pinky-ui-demo`) or the real gateway (`-gateway`), a `bench` subcommand for the video numbers, an `e2e` build tag that links MyGo's automation plugin, and `mygo.json` to package it. |
| `assets/` | The brand files (SVG sources, `Pinky.icns`, `pinky.ico`, Linux icons, menu bar template images) and `assets.go`, which embeds the four the client draws. `BRAND.md` lists them. |

Screens, after prototype v18 (`*.dc.html`):

- **Sidebar.** Logo and name in the header next to the window controls; the phones with a status dot and a plain status line; "All phones" once two or more phones are known; Add phone; Activity, Settings and Stop all (with its shortcut) at the bottom.
- **Phone.** The live mirror in a phone bezel sized to the window, with the agent's overlay (the target outlined, the touch point with two ripples while a step is in progress), the lock (screen dimmed, "Pixel 9 is locked" pill), the "You're in control" banner with Hand back, the gray ring while the person is in control, and the keys below (Back, Home, Recents on Android; Home on iPhone) and ⋯ (Take screenshot works; Record, Rotate and Background display are shown disabled).
- **All phones.** Live tiles converted at tile size, amber edge on phones that need the person, Take over and Stop on hover, the offline tile; a click selects the phone.
- **Right panel.** Needs you (approval cards with Deny, Allow and More options: Allow for this task, Always allow *kind* in *app*, Take over, Deny with a note; unlock cards with "Unlock with Touch ID"; handoff cards, where the agent asked the person to do something, with Done, Take over, Decline and Add a note; first-connection cards with what the agent may do, Deny and Approve), then Tasks: the selected phone's task expanded (status, Take over, Stop, title, agent · phone · time, steps with their marks, a click on a step with a screenshot opens it under the step; in Developer mode a Details switch that shows the tool calls), the other phones' tasks one row each.
- **Approval panel.** A MyGo panel window (`WindowOptions.Panel`) shown with `ShowInactive`, so it never takes the keyboard. It shows while approvals wait and the main window is not in front, at the top right of the display the pointer is on: time-left bar, "1 of 2" paging, "Denies in 42 s", the eyebrow, title and detail, the screenshot cropped around the target with the target outlined, Deny, Allow and More options, the note editor, and a short "Allowed once" / "Denied" confirmation. After the person clicks it, Return allows and Esc denies.
- **Menu bar.** A template icon in the macOS menu bar (the color icon in the Windows and Linux trays) with the number of things waiting as its title; a click opens a flyout (Needs you with Review, the phones, Open Pinky, Stop all ⌥⌘.); right click opens a menu. Linux tray icons report no clicks, so there the menu holds "Show Phones…".
- **Global shortcut.** `CmdOrCtrl+Alt+.` (⌥⌘. on macOS, Ctrl+Alt+. elsewhere) runs Stop all from anywhere.
- **Notifications.** A system notification for each new need while the main window is not in front; a click opens the approval panel or the phone. macOS shows notifications of packaged apps only, so `go run` skips them.
- **Activity.** Finished tasks by day with app letter icon, outcome dot (Done, Stopped, Denied) and approval count; the detail with summary, steps with their screenshots as thumbnails (a click opens the screenshot; the person's steps with "Answered in 14 s") and Watch replay (the steps one at a time with their screenshots, Play, Previous and Next, on the step timeline); Export report (HTML) and Export JSON to the Desktop. Search goes to `History` as the person types.
- **Settings.** Phones, Agents (the gateway's agents with what each may do and Change, and first-connection requests), Safety (the three presets, blocked apps with add and remove, Read-only mode, Unlock from this computer, Hide codes from agents), Privacy and Advanced (Developer mode, backends, the video decoder in use, the rules file).
- **Add phone.** Which phone → Android: "Looking for your phone…", "Found Pixel 8a, but USB debugging is off" with the three steps and Try again, "Found Pixel 8a" and Done; Pair over Wi-Fi; iPhone with the six-digit code, and the older-iPhone choices. With no phone connected the client opens straight into it.
- **Closing the window** hides it; the gateway keeps running in the menu bar. Cmd+W hides too.

## How to run

```sh
go run ./cmd/pinky-ui-demo                       # one phone: Pixel 9 reorders lunch, asks to pay
go run ./cmd/pinky-ui-demo -scenario three       # and an iPhone (HEVC stream) and an offline Galaxy
go run ./cmd/pinky-ui-demo -scenario locked      # the phone is locked; unlock from the card
go run ./cmd/pinky-ui-demo -scenario takeover    # you are in control
go run ./cmd/pinky-ui-demo -scenario empty       # no phones: opens Add phone
go run ./cmd/pinky-ui-demo -theme dark -dev -approval-timeout 10m -speed 2
go run ./cmd/pinky-ui-demo -scenario three -page settings   # or activity, add-phone
go run ./cmd/pinky-ui-demo -scenario handoff     # the agent asks you to sign in to Dinely
go run ./cmd/pinky-ui-demo -scenario agent       # Codex connects for the first time
go run ./cmd/pinky-ui-demo -gateway              # the real gateway on internal/demo's phones
go run ./cmd/pinky-ui-demo -gateway -hidden      # starts in the menu bar; the window opens on the first request
go run ./cmd/pinky-ui-demo -gateway -video=false # demo phones without video, as pinky --fake has them
```

With `-gateway`, Claude Code connects to Pixel 9 (fake) two seconds after start, which the gateway asks the person to allow, then opens Dinely, reorders lunch, adds a note and asks to pay. Touch the mirror to take over; it waits with `phone_handoff` until Hand back. The Demo menu starts more agents (a sign-in handoff on the iPhone, Codex for another first connection) and locks or unplugs the iPhone. History and screenshots go to a temporary folder deleted on quit.

Needs Homebrew's FFmpeg 9 (`brew install ffmpeg`): the `ffmpeg` command makes the test streams and its libraries decode them. The **Demo** menu switches scenarios, restarts the one playing (⇧⌘R) and plugs in a new phone for Add phone. Approvals deny after 60 s, as designed; `-approval-timeout` lengthens that for a slow walk-through.

A walk-through that covers the brief: start with `-scenario three`; watch the Pixel's steps and ripples; Allow (or More options › Deny with a note) on "Pay $12.80?"; click All phones and a tile; Take over on the iPhone, Hand back; hide the window (⌘W) to see the approval panel; click the menu bar icon; Demo › Locked phone and Unlock with Touch ID; Demo › One phone to see the sidebar without "All phones".

Other commands:

```sh
go test ./internal/video ./ui/...                # decoding, the fake, and the views in ui.Tester
go run ./cmd/pinky-ui-demo bench                 # video CPU and latency (below)
go build -tags e2e -o /tmp/pinky-ui-demo ./cmd/pinky-ui-demo && \
  (cd /tmp && MYGO_AUTOMATION=pinky.sock ./pinky-ui-demo)   # drive it over MyGo's automation socket
go tool mygo build cmd/pinky-ui-demo             # Pinky Demo.app and a .dmg in build/pinky-ui-demo
GOOS=windows go build ./... && GOOS=linux go build ./...
```

The automation socket path must be short (macOS limits Unix socket paths to 104 bytes), hence the relative name. The e2e build also has a Debug menu that opens the flyout and the approval panel, which automation cannot reach through the tray.

## Video

`internal/video` loads `libavutil.61`, `libavcodec.63` and `libswscale.10` with purego: from `PINKY_FFMPEG_DIR`, next to the executable (and `Contents/Frameworks` in the app bundle), then Homebrew's and the system's folders. Missing or of another major version, `video.Load` returns an error that names the file, where it looked and how to install FFmpeg 9 (`video.ErrNotFound`); the client calls it at start, logs it, and every mirror shows it.

The struct offsets (AVFrame, AVPacket, AVCodecContext, AVCodec, AVCodecHWConfig) are FFmpeg 9.0's on 64-bit systems, printed with `offsetof` from Homebrew's headers. `checkLayout` verifies them at load against what FFmpeg itself writes (a frame it allocates, a packet it sizes, a context's codec id and thread count), so another layout fails with a clear error instead of corrupting memory. Pixel formats, hardware device types and decoders are looked up by name. `AVERROR(EAGAIN)` follows the platform's errno.

Decoding:

1. A configuration packet (SPS/PPS/VPS) is held and prepended to the next packet, as scrcpy's client does.
2. With `hw_device_ctx` set to a VideoToolbox device (D3D11VA on Windows, VA-API on Linux), FFmpeg's default `get_format` picks the hardware format, and falls back to software by itself when the hardware refuses a stream. Software decoding uses slice threads only, which add no latency, and `low_delay`.
3. A hardware picture is mapped (`av_hwframe_map`, no copy on VideoToolbox) or else copied (`av_hwframe_transfer_data`), then `sws_scale_frame` converts NV12 or YUV420P to RGBA straight into an `image.RGBA` of the size the mirror shows it at, with the stream's color description.
4. `video.Player` decodes on a goroutine of its own and triple-buffers: the picture on screen, the newest one waiting, the one being converted. A player that falls more than 30 packets behind skips to the next key frame. While nobody takes pictures for a second (the window is covered or the screen locked, so it paints nothing), it goes on decoding but converts nothing.
5. The mirror element takes the newest picture in its `Draw` callback, calls `Bitmap.Update` (which reuses the texture for same-size pictures and takes the `*image.RGBA` without a copy) and asks for the next paint with `AnimationFrameRate(60)`. So video frames repaint the window without rebuilding the view, and nothing is converted while the window is hidden.

Pictures are converted at the pixel size they are shown at (the bezel's screen box times the display scale), so a 1080×2400 stream shown 300 points wide on a 2× display is converted and uploaded at about 600×1332, and All phones' tiles at about 316×700. MyGo would otherwise halve a much larger bitmap on the CPU for every frame.

### Measurements

Machine: Apple M4 Pro (14 cores), macOS 27.2, Homebrew FFmpeg 9.0.2 (libavcodec 63.1.102). `go run ./cmd/pinky-ui-demo bench` decodes ffmpeg test streams (testsrc2 or testsrc; libx264 ultrafast or hevc_videotoolbox; 8 Mbit/s, a key frame each second, no B-frames) in real time at 60 fps, 15 s per run after 3 s of warm-up. CPU is the process's user and system time over all its threads, as a share of one core, plus VideoToolbox's decoder service, which decodes in a process of its own. Latency runs from the packet reaching the player to its RGBA picture being ready; the GPU upload and the display's refresh come on top.

| Stream → picture | Decoder | CPU, process + VT service | Packet → RGBA, median / p95 | Decode + convert, means |
| --- | --- | --- | --- | --- |
| H.264 1920×1080 → 1920×1080 | VideoToolbox | 6% + 3% | 2.2 ms / 3.5 ms | 2.3 ms + 0.36 ms |
| H.264 1920×1080 → 1920×1080 | software | 15% | 0.74 ms / 1.0 ms | 0.44 ms + 0.32 ms |
| H.264 1080×2400 → 1080×2400 | VideoToolbox | 29% + 3% | 4.3 ms / 4.9 ms | 2.2 ms + 2.3 ms |
| H.264 1080×2400 → 600×1332 (a mirror at 2×) | VideoToolbox | 15% + 3% | 3.2 ms / 4.0 ms | 2.3 ms + 1.1 ms |
| H.264 1080×2400 → 600×1332 | software | 22% | 1.3 ms / 2.0 ms | 0.40 ms + 1.1 ms |
| H.264 1080×2400 → 1080×2400 | software | 23% | 1.4 ms / 2.0 ms | 0.37 ms + 1.1 ms |
| HEVC 1206×2622 → 600×1304 | VideoToolbox | 16% + 3% | 3.5 ms / 3.9 ms | 2.4 ms + 1.2 ms |
| HEVC 1920×1080 → 1920×1080 | VideoToolbox | 6% + 3% | 2.1 ms / 3.5 ms | 2.1 ms + 0.29 ms |

About 4 of 900 pictures were replaced before they were taken, all at the start, when ffmpeg sends its first half second at once.

What the numbers say:

- **1080p60 costs about 9% of one core with VideoToolbox, and a picture is ready 2.2 ms after its packet.** FFmpeg waits for VideoToolbox frame by frame, which is most of that.
- **Converting to RGBA is most of the CPU.** VideoToolbox hands over NV12, which libswscale converts at about half the speed of YUV420P from the software decoder (it seems to miss the fast path), so at a phone's full size software decoding is both cheaper (23% against 32%) and quicker (1.4 ms against 4.3 ms) on this machine. At the size a mirror shows, where scaling dominates, both cost about the same. Hardware decoding stays the default: phones' real screens are harder to decode than test patterns, and smaller machines lean on the decoder more.
- **Two conversion threads** halve the conversion time for no more CPU: at mirror size, one thread took 15% and 2.1 ms a picture, two 14% and 1.0 ms, fourteen (libswscale's automatic choice) 20% and 0.4 ms. The decoder uses two (`DecoderOptions.ConvertThreads`).
- **Converting at the shown size matters:** a 1080×2400 stream shown at 600×1332 costs half the CPU of converting it whole.
- **Not measured:** the upload in `Bitmap.Update`, painting, and the whole client's CPU while it mirrors. The screen was locked for most of this overnight session, and macOS draws no windows then, so the client paints nothing. Earlier interactive runs played smoothly; Activity Monitor next to `go run ./cmd/pinky-ui-demo` shows the client's share.

Ways to cut the CPU further, in the order I would try them: ask VideoToolbox for planar YUV or BGRA output through `hw_frames_ctx` (needs a `get_format` callback through purego), which removes the slow NV12 path or the YUV conversion; MyGo's NV12 or I420 bitmap updates converted in a shader (the design's first upstream ask); and zero-copy CVPixelBuffer to Metal (#973).

## How the client reads gateway.API

`gateway.API` leaves some things to convention. The client assumes these; the fake follows them, and the gateway should too (or tell me what to change):

- **Device coordinates.** A mirror opens `Stream` with `MaxSize: 0` and takes the decoded picture's size as the device's coordinate space: touches are sent in those pixels (as `driver.Toucher` defines them since 5e83552; `Key` and `Text` carry no coordinates) and steps' `Target` and `Point` are drawn in them. On an iPhone screen pixels and picture pixels differ by about 1%, so the overlay is clipped to the screen and an outline at the edge cannot spill onto the bezel. That matches the design, where screenshots are decoded from the stream. Tiles open the stream at full size too and scale it down while converting, so their overlays stay right without a `DeviceView` screen size.
- **Step marks.** Status `approval_pending` is "waiting for you" (amber dot); status `user`, or no tool and text starting "You ", is the person's step (person icon); status `""` or `running` on the last step of a running task is in progress (green pulse, and the mirror draws its touch); everything else is done (check). A step's text may hold a second line after a newline, shown smaller ("Your note: …").
- **Outcomes.** A done task with a "You denied" step (or status `approval_denied`) ended "after your answer", shown as Denied in Activity unless something was allowed after it.
- **Summaries and app icons.** Activity shows the `summary` the agent gave `phone_release` (any step's `Args` with a `summary` field), and takes the app for the letter icon from the first `phone_launch` step's `app` argument, or an "Opened X" step.
- **Approvals.** `Decide` gets `allow_once`, `allow_task`, `allow_always`, `deny` (with the note) or `take_over`. The approval card names the rule in "Always allow *payments* in *Dinely*" from `NeedView.Rule` and `App`. `NeedView.Screenshot` must be in the same pixels as `Target`.
- **Input.** Pointer down, move (throttled to 125 Hz) and up go to `Touch`; right click is Back and middle click is Home; the wheel and the touchpad become a finger dragging the content (down at the pointer, moves against the scroll, up 140 ms after the last event, lifted and put back at the edges); Enter, Tab, Backspace, Delete, Escape, the arrows and Page Up/Down go to `Key`; typed and input-method text goes to `Text`; ⌘V pastes the computer's clipboard as text. The gateway is expected to pause the agent on any of them (the API's comment says so); the client shows the banner when the device reports `in_control`, or while a handoff is open on it. Calls run off the main thread, one queue per device, so they keep their order.

## Gaps in gateway.API (for the main session)

None of these block the demo; each is a small additive change when the gateway is ready for it. I did not change `api.go`. The gateway's methods beyond `gateway.API` (`MediaPath`, `Agents`, `SetAgentScope`, `SetReadOnly`, `WipeHistory`) are used through optional interfaces, see below.

- **Settings.** No methods to read or change the safety preset, blocked apps, unlock, code hiding, retention or recording. The client keeps them (`ui.Options.Settings`) and reports changes through `ui.Options.OnSettings`; `cmd/pinky` persists them.
- **Pairing.** No way to start Android wireless pairing (the QR code) or iPhone pairing with a code. Add phone watches `Devices` for a new phone, so USB works; the Wi-Fi QR shows a placeholder, and Pair waits for the gateway to report the iPhone.
- **Unlock.** `Unlock(deviceID)` is called after the person clicks "Unlock with Touch ID"; the client does not run Touch ID itself. If presence should be proven in the client, it needs LocalAuthentication through purego.
- **Rules file path.** `ui.Options.RulesFile` opens it from Settings › Advanced.
- **Phone controls.** Record screen, Rotate and Background display have no API; they show disabled.
- **One task's history.** Activity's per-task Delete stays disabled: the gateway can only wipe all of it.
- **Recordings.** Watch replay shows step screenshots; there are no screen recordings yet.

## Against the real gateway

The second pass (2026-11-07) ran the client against `internal/gateway` itself and closed the gaps it showed. `go run ./cmd/pinky-ui-demo -gateway` is the way to see it; `go test ./ui -run TestGateway` clicks through it.

**What changed**

- **Handoffs** (`NeedHandoff`): the agent called `phone_handoff` and waits. The card's title is the agent's reason. Done → `Decide(id, AllowOnce, note)` (hand back with the note), Take over → `Decide(id, TakeOver, "")`, Decline → `Decide(id, Deny, note)`; Add a note opens the note field for Done and Decline. The floating panel and the flyout show handoffs too; the panel has no Return and Esc for them, since their buttons are not Allow and Deny.
- **First connections** (approval with rule `New agent`): a card with "Let *agent*", a select of what it may do (All apps except blocked ones, Only the apps I choose with an apps field, Browse and read, never commit, Watch only), Deny and Approve. It shows in Needs you, in the floating panel and at the top of Settings › Agents, with an amber dot on the Agents tab.
- **The gateway's extra methods**, found with type assertions on the API value (`ui/extras.go`), so `gateway.API` is unchanged and the client still runs on anything that only implements it: `MediaPath` for step screenshots (task cards, Activity thumbnails and previews, replay); `Agents` and `SetAgentScope` for Settings › Agents (reloaded every 5 s while shown, and at once when a first-connection request opens or closes); `SetReadOnly("", on)` when the Read-only switch changes; `WipeHistory` behind Settings › Privacy › Delete…, with a confirmation.
- **In control without a task**: the banner reads "You're in control. Hand back to let agents use this phone again." and Hand back calls `HandBack`; the All phones tile shows Hand back on hover too.
- **`ui.Options.Hidden`**: on macOS the app starts as an accessory (menu bar icon, no Dock icon, no window); the first Needs you item opens the main window without taking the keyboard (`ShowInactive`) and makes the app regular. Opening it from the menu bar does the same.
- **App Nap**: `ui.Run` holds an `NSProcessInfo` activity for the app's lifetime (`ui/appnap_darwin.go`, purego's objc; a no-op elsewhere).

**Decisions**

1. **App Nap flags.** The brief asked for `NSActivityUserInitiated | NSActivityLatencyCritical`. `NSActivityUserInitiated` includes `NSActivityIdleSystemSleepDisabled`, so holding it for the app's lifetime would keep the Mac from ever idle-sleeping while Pinky runs, and Pinky lives in the menu bar all day. I used `NSActivityUserInitiatedAllowingIdleSystemSleep | NSActivityLatencyCritical`: App Nap, timer coalescing and background throttling stay off, which is what slowed the streams, and the Mac may still sleep when idle (an agent on the same Mac sleeps with it). It is one constant in `ui/appnap_darwin.go` if the gateway should keep the Mac awake instead; I would do that only while a task runs.
2. **The scope chosen for a new agent.** The gateway's `approveAgent` calls `setAgentApproved(agent, DefaultScope)` when the agent's waiting call picks up the answer, which would replace a narrower scope chosen in the card. The client calls `SetAgentScope(name, scope)` (which also approves) before `Decide(id, AllowOnce, "")`, so a call that comes back later finds the agent allowed with that scope, and then watches `Agents` for three minutes (every 100 ms for the first 10 s) and sets the scope again if the default replaced it. That leaves a window of up to 100 ms with the default scope when the call was still waiting. The clean fix is in the gateway: keep the scope when the agent is already approved (`TestGatewayAgentScope` covers the client side).
3. **The request's detail.** The gateway's "Allowing gives it everything access." would contradict a narrower choice in the select, so with the select shown the client drops that sentence and adds the time ("Asked at 03:51.").
4. **A handoff on a locked phone** is one card: the handoff's reason, "Unlock with Touch ID" (which ends the handoff, as decision 11 says), and Decline and Done for after an unlock on the phone. The separate unlock card for that phone is hidden, so two amber cards never ask for one thing.
5. **A handoff outranks control.** The gateway reports `needs_you` for a phone with an open handoff even while the person is in control (the agent waits with `phone_handoff` after it got `user_took_over`). So the phone screen shows the banner whenever a handoff is open: "You're in control. *Agent* is paused." when the task is paused, else "Your turn. *Agent* waits for you: *reason*". Hand back answers the handoff through `HandBack`. When the person already holds the phone, the card says "You're in control. Click Done to hand the phone back" and offers Decline instead of Take over.
6. **Step wording** now reads the gateway's conventions as well as the fake's: "Waiting for you: Tapped “Pay”" waits as "Waiting for you: tap “Pay”", then reads "You denied: tap “Pay”" (`approval_denied`), "Tap “Pay”" with "Not done: you took over" (`user_took_over`), "… stopped", "… the agent did not come back for it" (`expired`); handoffs read "Asked you to take over: …", then "You handed it back" or "You declined" with "Asked: “…”"; `ask` steps read "You agreed" or "You said no"; failures name their code. `device_locked` no longer marks a step as waiting: the gateway's locked phone is an unlock need, not a step. Between the person's answer and the end of the action, the gateway keeps the step `approval_pending` while the task is already working again; the client words that as "You answered: tap “Pay”" instead of showing an amber step with no card.
7. **Step screenshots** load with `MediaPath`, `os.ReadFile` and `image.Decode` off the main thread, at most 400 kept. The target is outlined on a screenshot only when the phone's live picture size is known (the gateway scales screenshots down by a whole factor), and never on 54-point thumbnails, where it said nothing.
8. **Read-only** calls `SetReadOnly("", on)` from the switch as well as reporting the change through `OnSettings`. If `cmd/pinky` also applies it, the two calls agree.
9. **Demo phones get video** through `video.FrameStream`, which encodes `Screenshot()` twelve times a second with the `ffmpeg` command. `ui/demogw` wraps `internal/demo`'s provider for that, so the gateway still sees the demo's `Toucher` and `PersonWatcher`.

10. **The mirror's states.** The first picture is taken while the mirror paints, and painting does not build the view again, so the "Connecting…" overlay from the build before it stayed over the live picture until something else changed (an idle iPhone sends no frames while its screen is still). Taking the first picture now asks for a build. A stream that fails shows why ("This phone has no live picture." for `ErrUnsupported`) and is opened again after 3 s (2 s after it ends) on a timer, not on the next build, which an idle window or a locked screen never makes. Updates that goroutines post before the main window exists are kept and run once it does, instead of being dropped.

**For `cmd/pinky` (main session)**

- Pass `ui.Options{Hidden: true}` when `pinky mcp` starts the app.
- `pinky --fake` app mode: `internal/demo.Device.Stream` returns `ErrUnsupported`, so the mirror shows its error. `demogw`'s wrapper (`streamingProvider` in `ui/demogw/demogw.go`) can move into `internal/demo`, or `Device.Stream` can return `video.FrameStream(ctx, d.w, d.h, 12, func() image.Image { img, _ := d.Screenshot(context.Background()); return img })`.
- With `TrustNewAgents: true` in `--fake` mode the first-connection card never shows; leave it false in app mode if people should see it.
- Gateway wording seen on the way: the payment approval's title is "Pay: “Place order · $12.80”?" where the prototype says "Pay $12.80?", and the first-connection detail says "everything access".

## Brand colors, and keeping amber and red apart

The prototype's colors stay, quiet as they were: neutral grays, indigo for primary buttons, links, switches and the agent's overlay, amber for what needs the person, red for Stop, green for working. The brand appears only where the eye looks for identity:

- **Logo:** the 32-point icon (`pinky-32.svg`, lemon field, phone face, watermelon ring) in the sidebar header, next to the name, right of the window controls. Lemon appears nowhere else.
- **Selection:** the selected row of the sidebar, Settings' sections and Activity keeps the prototype's gray fill and gains a 3-point watermelon bar at its start.
- **Focus:** the keyboard focus ring is watermelon (`#EE4D6C` light, `#FF8A9B` dark; 3.6:1 and 7.6:1 against the background).

The prototype's amber and red sat too close to the brand. In OKLCH, dark mode's amber `#F2BB59` (L 0.82, hue 80°) was almost the logo's lemon `#FFBE1A` (L 0.84, hue 83°), and dark mode's red `#FF8A80` (hue 26°) had the same lightness as the watermelon highlight `#FF8A9B` (hue 12°). So I moved the semantic colors away from the brand's hues, and kept each color to its own shapes:

| Role | Light (was) | Dark (was) | OKLCH hue |
| --- | --- | --- | --- |
| Needs you: dot | `#DF8601` (`#E09100`) | `#F7A74E` (`#F2BB59`) | 65° (lemon is 83–96°) |
| Needs you: text | `#975800` (kept) | `#F7A74E` (`#F2BB59`) | 64–66° |
| Needs you: card, line, tile edge | `#FFF6EB`, `#F6D7B1`, `#E79733` | `#342210`, `#674520`, `#B6762A` | 66–72° (were 83–85°) |
| Stop: text, border, hover | `#C32C18`, `#F0C6BD`, `#FEF0ED` | `#F87A60`, `#723B31`, `#3A1E19` | 31–33° (watermelon is 11–13°) |
| Brand: selection bar, focus ring | `#EE4D6C` | `#FF8A9B` | 13° / 12° |

The trade-off: amber now leans orange and red leans vermilion, so amber and red are closer to each other than in the prototype (hue 65° against 31°, but lightness 0.70 against 0.54 in light mode), still clearly apart, while neither can be mistaken for the logo or the selection mark. Shapes back the hues up: amber is dots, card tints and the tile edge; red is only the text and border of Stop; watermelon is only a thin bar and the focus ring; lemon is only inside the logo. No brand color is used as a fill, so selected rows never look like Needs you cards.

## Differences from the prototype

- The phone in the middle grows with the window (the prototype draws it 300 × 632); its bezel radius scales with it.
- The mirror shows ffmpeg test patterns (testsrc2 for the Pixel, testsrc in HEVC for the iPhone), not the prototype's drawn Dinely, Chatter and Tally screens. Targets and touch points are where those apps' buttons would be.
- "Replay this demo" links are gone (the client is not a demo); the Demo menu restarts scenarios.
- More phone controls: Record screen, Rotate and Background display are disabled (no API).
- Add phone's Wi-Fi QR is a placeholder, since the gateway cannot start wireless pairing yet; the prototype's QR encoded nothing either. The older-iPhone choices say this build cannot set them up yet.
- Activity's step thumbnails are the steps' screenshots where the gateway kept one, and the prototype's sketch otherwise; Watch replay is a slideshow of those screenshots, not a video.
- Settings › Agents shows first-connection requests as the prototype does, and the same request is also a card in Needs you, so it can be answered from the phone screen and the floating panel.
- The tray shows the number of waiting needs as its title on macOS.
- The approval panel is 360 × 330 rather than 360 × 320, to fit the two-line detail under the crop.

## Known issues

- Unpackaged builds (`go run`) show no notifications; macOS allows them for packaged apps only. The packaged demo (`go tool mygo build cmd/pinky-ui-demo`) builds, signs ad hoc and starts its streams, but its notifications were not tried: the screen was locked by then, so nobody could answer macOS's permission question.
- The approval panel shows when approvals wait and the main window is not focused. While Pinky itself is the active app with only the panel and the flyout open, macOS may make the panel the key window when the main window hides; it never takes the keyboard from another app.
- `Painter.AnimationFrameRate(60)` repaints the window at 60 fps while a mirror plays, even when the picture is still. A later step is to repaint only when a new picture arrives.
- While the screen is locked MyGo draws nothing, so a window builds its view only when something asks it to (the automation socket's input, a capture). The client reads the gateway's views before its window shows, so the first frame already has the phones.
- Wheel scrolling maps a notch to 2.5× its DIPs of finger travel; touchpads map one to one. Tune on real phones.
- The panel's Return and Esc work only after the person clicked it, by design.
- Windows and Linux builds compile but were not run; D3D11VA and VA-API decoding are untested.
- FFmpeg 9 only; other major versions are refused rather than supported with other offsets.
- Approving a new agent with a narrower scope leaves up to 100 ms with the gateway's default scope (decision 2 above) until the gateway keeps the chosen scope itself.
- With `-hidden`, the first request opens the main window and, because that window is not focused, the floating panel as well.
- `video.FrameStream` costs a full-size RGBA copy and an x264 encode per frame; it is for demo phones only.

## Verification

Each screen was driven over MyGo's automation socket (`e2e` build) and captured in light and dark; the captures are in `docs/notes/screenshots/` (test patterns and the fake's data only, no real screen). The flows were clicked through with the screen on: approving, denying, taking over by touching the mirror, handing back, unlocking, three phones and All phones, the flyout, Add phone with a phone plugged in. The screen locked partway through the night, so the final set was captured with it locked: each capture is preceded by a nudge and a throwaway capture so the view builds with the current state, the approval panel was drawn by `ui.Tester` (`PINKY_SCREENSHOTS=dir go test ./ui -run TestPanelScreenshots`), and Add phone is shown in the first-run state (`-scenario empty`).

| Screen | Light | Dark |
| --- | --- | --- |
| Phone, one phone, payment waiting | `phone-one-light.png` | `phone-one-dark.png` |
| Phone, three phones | `phone-three-light.png` | `phone-three-dark.png` |
| All phones | `all-phones-light.png` | `all-phones-dark.png` |
| Phone locked, unlock card | `locked-light.png` | `locked-dark.png` |
| You're in control | `takeover-light.png` | `takeover-dark.png` |
| Approval panel, 1 of 2 | `approval-panel-light.png` | `approval-panel-dark.png` |
| Menu bar flyout | `flyout-light.png` | `flyout-dark.png` |
| Activity | `activity-light.png` | `activity-dark.png` |
| Settings › Safety | `settings-light.png` | `settings-dark.png` |
| Add phone, first run | `add-phone-light.png` | `add-phone-dark.png` |

Against the real gateway, `ui/gateway_e2e_test.go` runs the views in `ui.Tester` on `internal/gateway` with the demo phones and scripted agents: the first-agent approval clicked in Needs you, the payment approval with its screenshot and target, a takeover by touching the mirror (pointer events through `mirrorInput`, so `Touch` reaches the demo phone), the banner and Hand back, the payment allowed, the finished task in Activity with step screenshots from `MediaPath` and Watch replay; a narrower scope for a new agent that survives the agent picking up the answer, and Settings › Agents showing it; a handoff answered with Done and a note; a handoff on a locked phone as one card; Stop and Stop all; and a phone kept after the agent left, handed back. `go test -race ./ui/...` passes.

The same flow was clicked through in the real app over the automation socket with the screen locked (`-gateway -hidden`, e2e build): no window at start, the window opening by itself on the first-agent request, Approve, the live demo screen with the payment's target outlined on the video, a click on the mirror taking over, Hand back, Allow, and the task done. These were captured with `ui.Tester` against the real gateway (`PINKY_SCREENSHOTS=dir go test ./ui -run TestGatewayScreenshots`), the mirrors showing still screenshots of the demo phones:

| Screen | Light | Dark |
| --- | --- | --- |
| First connection, in Needs you | `gateway-new-agent-light.png` | `gateway-new-agent-dark.png` |
| Payment approval from the gateway | `gateway-payment-light.png` | `gateway-payment-dark.png` |
| Handoff, with the "Your turn" banner | `handoff-light.png` | `handoff-dark.png` |
| Handoff in the floating panel | `handoff-panel-light.png` | `handoff-panel-dark.png` |
| Settings › Agents, changing a scope | `settings-agents-light.png` | `settings-agents-dark.png` |
| Activity with step screenshots | `activity-shots-light.png` | `activity-shots-dark.png` |

The `ui` tests run the views in `ui.Tester` against the fake: approving, denying with a note, taking over and handing back, the lock card, one and three phones, All phones, the offline phone, Activity, Settings, Developer mode's details, Add phone with a new phone plugged in, the approval panel's paging and Esc, the flyout's Stop all, and mirror input (touch mapping, Back, Enter, Chinese text, wheel to swipe).
