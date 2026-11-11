# Pinky's iPhone app

Started on 2026-11-11. Pinky's iPhone app, named **Pinky**, runs agent tasks on the iPhone itself, with no computer at run time, and is to replace Pinky Helper, the WebDriverAgent runner the desktop app installs on iOS 17.4–26. It grew out of the iOS prototype in [standalone-phone.md](standalone-phone.md#prototype-results-ios). The code is in [ios](../../ios/README.md), which also says how to build and run it. It is sideloaded only: it uses Apple's developer services, which App Review never allows.

This file holds what the build found, the decisions, what is left, and what the owner has to clean up on the test phone.

## Milestones

| | What | State |
| --- | --- | --- |
| M1 | The investigations below; the prototype renamed Pinky, with the real gateway (rules, approvals, history) on the phone; a task end to end on a real iPhone | Built. The end-to-end run and the helper probe are below |
| M2 | Onboarding (on-phone pairing on iOS 27), the app in three languages, desktop installing the app in place of Pinky Helper and the rename everywhere | Not started |
| M3 | iOS 17.4–26 on Simulators, battery over long runs, polish | Not started |

## What M1 built

- **The gateway on the phone.** [onphone](../../ios/onphone) wraps `internal/standalone`'s core, the one Android's app uses: `gateway.New` with Pinky's rules and approvals, SQLite history, and the agent loop of `internal/pilot`, over one device, the iPhone itself through the loopback. Nothing in `internal/standalone` changed.
- **The guard** sits between the gateway and the driver. It refuses input while Pinky's own app is in front (so an agent can never tap Allow or Stop), hides Pinky from the app list, refuses to launch it, and brings the task's app back after the person answers in Pinky. Pinky's app ids are its own and any id under it (`<id>.lite`, the extensions); `policy.IsStandalone` also matches them, so desktop agents can never reach the app either.
- **One JSON call** for the app's screens (`state`, `wait`, `start`, `decide`, `preset`, `stop`, `dismiss`, `ui`, `history`, `info`). `wait` is a long poll on the state's revision, so the app wakes only when something changed, not on a timer.
- **The app**: the task and its last steps, approvals (Allow needs Face ID or the passcode) and handoffs, Stop, history with search, and settings for the model, its key and when Pinky asks first. The Live Activity shows the task, "Pinky waits for you" in orange while an approval waits, and Stop.
- **The model**: bring your own key. Claude through the Anthropic API is the default; any OpenAI-compatible server with a base URL is the other choice. The key is kept per provider in the Keychain (`AfterFirstUnlockThisDeviceOnly`), never shown again, and stays in the core's memory only for the length of a task. Tests use `internal/standalone/cmd/fakemodel`.
- **The bundle id is a build setting** (`PINKY_BUNDLE_ID`); every other name derives from it, so anyone can build Pinky under their own team.
- **Two builds.** *Full*: Pinky's own packet tunnel, the core in the extension (paid developer account). *Lite* (`NO_TUNNEL`): no extension, LocalDevVPN does the loopback and the core runs in the app, which silent audio keeps alive during a task; a free personal team can sign it.

### Prototype follow-ups

| Follow-up | Done? |
| --- | --- |
| The Live Activity that stays behind when a task ends while the app is suspended | **Fixed in two ways.** Silent audio keeps the app running while a task is busy and Pinky is not in front, so it sees the end and ends the activity (showing how it ended for 8 s). On every launch and foreground, an activity left over with no task running is ended. Stop on the activity ends it from the widget's process too |
| Fewer CPU wakeups | **Done for the app**: the long-poll `wait` replaced the 1 s poll. The core's heartbeat is off unless asked for |
| The display stream only around taps | **Partly.** The driver already stops the stream and its HID session after 30 s without input or a viewer (`idle.go`); no one watches on the phone, so the stream lives only around input. Starting it later than the first tap was not tried |
| `.shim.remote` services over the tunnel | Not done (M2) |
| A screenshot fallback when `remoteAXService` hangs | Not done (M2). The driver's OCR fallback exists (`observe.go`) but needs Vision, which is macOS only; on the phone a hang still fails the read |

## End-to-end run

<!-- filled in after the run -->
On the iPhone 13 mini (iOS 27.0.1), with the fake model on the Mac (`fakemodel -script calc`) and the task "Work out 7 × 6 in Calculator" started from the debug launch arguments.

- **What worked.** The app installed and launched; after the owner allowed the VPN configuration, the tunnel came up, the core in the extension pair-verified through the loopback and connected (remotepairingd's tunnel listener, on a port of its own each time), the app read its state by the long-poll `wait`, the debug task started once the phone was ready, and the Live Activity appeared ("Working · 0 steps").
- **What failed.** About a second after the task started, iOS killed the extension: `memorystatus: killing_specific_process … [PinkyTunnel] (per-process-limit …) 51233KB`. The VPN dropped ("Plugin failed"), the fake model never got a request, and the app was left showing Turn on. Twice in a row; no crash report, since a memory kill writes none (the kernel's line in the device's syslog is the only trace).
- **Why, most likely.** The tunnel connection had brought in 9.3 MB in its 5.8 s, far more than the display stream sends. Over the tunnel the driver lists apps through the installation proxy's shim with no `ReturnAttributes`, so the phone sends every key of every app, entitlements included, and Go decodes all of it into maps. The fix asks only for the eight keys the app list reads (`shim.go`, `appAttributes`). Not yet rerun on the phone.
- **Logging.** The device's syslog relay showed none of the core's or the app's own `os_log` lines, only other processes' lines about Pinky (runningboardd, neagent, the kernel). The extension's memory and exit are visible that way; the core's own lines are not.

## Pinky Helper inside the app

<!-- filled in after the probe -->
Pending.

## Signing

What the research found. [P] marks a primary source: Apple, or the project's own code or docs.

- **A free personal team cannot sign a packet tunnel**, by any route. Apple's capability table does not tick Network Extensions (or Personal VPN, or Push) for free accounts, Xcode refuses it ("Personal development teams … do not support the Network Extensions capability"), and Apple DTS says there is no workaround on iOS ([capabilities](https://developer.apple.com/help/account/reference/supported-capabilities-ios) [P], [forum 825136](https://developer.apple.com/forums/thread/825136) [P]).
- **It can sign the rest Pinky needs**: App Groups, Keychain sharing, Background modes (audio), and a Live Activity updated by the app (no capability is involved; updating it by push would need Push).
- **Limits** ([compare memberships](https://developer.apple.com/support/compare-memberships) [P]): 10 App IDs per 7 days, 3 devices, 3 apps per device, profiles that expire after 7 days. Every extension is an App ID of its own: the full build costs 3, the lite build 2. Keep the bundle id fixed, or every rebuild spends new App IDs.
- **After 7 days** the app and its extensions stop launching until they are rebuilt and reinstalled. The app should read the expiry from `embedded.mobileprovision` and warn ahead (M2).

**The fallback for free accounts is the lite build**: LocalDevVPN from the App Store provides the loopback (its address set to a /30, the phone 10.7.0.2 and the peer 10.7.0.1; its 1.3.0 default, 10.7.1.1/32, got no answer in the prototype), and the core runs in Pinky's app, kept alive by silent audio during a task, as StikDebug does. The prototype measured that for 15 minutes (17–39 MB, +160 mA against +90 mA in the extension); hours, a locked phone and memory pressure are untested. The other fallbacks were an XCUITest runner like iCTRL (heavier: the developer disk image and a passcode prompt, private XCTest API) and requiring a paid account; the lite build keeps both builds from one project.

### SideStore and AltStore

- Both re-sign from the provisioning profile and keep only the entitlements the profile has (AltSign `ALTSigner.mm`, SideStore's SideSign `CodeSignerAPI.swift` [P]). A free team's profile never has Network Extensions, so the full build would install with a tunnel that cannot run. **Only the lite build works through them.**
- AltStore appends the team id to App Group ids; Pinky must read its group from its entitlements at run time if it ever uses one (it does not today: the app and the extension talk through app messages and the Keychain).
- SideStore refreshes apps on the phone before the 7 days run out, through LocalDevVPN and a pairing file; it takes one of the 3 app slots itself. A computer is needed once, to install SideStore and its pairing file. On iOS 26.4+ that file must be a RemotePairing one.
- Pinky would need nothing special for them beyond the lite build, an `.ipa` and a bundle id the person's team can register. Not tried.

## Setup on the phone

- **Pairing on iOS 27, with no computer.** iOS 27 lets the phone pair with a host on the network from Settings › Privacy & Security › Developer Mode › "Pair with …": the app advertises `_remotepairing-pairable-host._tcp` (with a Mac model, "Mac17,7", since iOS treats the host as a computer), the phone connects and runs pair-setup with the roles flipped, and the app shows a 6-digit PIN (StikDebug posts it as a notification, since the person is in Settings) that the person types on the phone. The result is the same RemotePairing identity `rppair pair` makes ([idevice responder.rs](https://github.com/jkcoxson/idevice/blob/master/idevice/src/remote_pairing/responder.rs) [P], StikDebug 3.2.0 [P]). It needs Local Network permission, Wi-Fi, the app alive while the person is in Settings (background audio), and Developer Mode on, whose switch appears only once something started pairing or a Mac paired with the phone. M2.
- **The developer disk image.** Pinky needs it on the phone too: taps go through the DDI's HID daemon. The test phone's came from a Mac. From 26.4 Xcode installs a Cryptex1 DDI through `cryptexd` with persistence 2, which survives reboots until an update or reset; from 17.0 to 26.3 it is a personalized image (a TSS ticket from Apple, so internet once) that every reboot unmounts. Both can be done from the phone over the loopback (StikDebug, iCTRL); the files come from Xcode or the community mirror (about 100 MB).
- **iOS 17.4–26 needs a computer once**, to make the RemotePairing file; on-device pairing is iOS 27 only. It must be a RemotePairing file, not a lockdown pair record: from 26.4 the phone refuses lockdown over the loopback (seen by StikDebug, SideStore and iCTRL). Pinky's desktop can make it (`rppair pair`). On those versions Pinky's iPhone app also needs the DDI mounted for the accessibility and display services it uses, which below 26.4 means after every reboot. Not tried: M3.

## Not verified

- Anything on iOS 17.4–26 (M3).
- The lite build on a phone, and with a free personal team.
- Hours-long runs, battery, a locked phone during a task, memory pressure in the extension.
- On-phone pairing and the DDI mount from the phone (designed from StikDebug and idevice; not built).
- SideStore or AltStore installs.
- A real model on the phone (the run used the fake model).

## Needs the owner

- Unlocking the phone and answering the first-launch prompts (notifications, Local Network, adding the VPN configuration) for each new build or bundle id.
- A real-model run: the owner types the key into Pinky's Settings on the phone.

## Cleanup list for the owner

- Pinky (`cc.diygod.pinky.standalone`) on the iPhone 13 mini, and its VPN configuration ("Pinky", in Settings › VPN). Deleting the app removes its Keychain items (the pairing and any key) with it.
- Two RemotePairing host identities on the iPhone 13 mini: the prototype's and EF52BBBF-C94D-495A-9BE5-09F369830DE7 from this build. iOS shows no list of them; Settings › General › Transfer or Reset iPhone › Reset › Reset Location & Privacy forgets trusted computers, and whether it forgets these too was not checked. A passcode change makes them useless. The Mac copy is `~/.pinky-standalone/rp_pairing.plist`; delete it when the app goes.
- The App IDs `cc.diygod.pinky.standalone`, `.tunnel` and `.activity` and their development profiles in the team (valid to 2027-11-11); keep them while the app is in use.
- Desktop Pinky may show the iPhone as paused after these tests; Resume it from the phone's menu.
