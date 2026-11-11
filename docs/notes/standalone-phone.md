# Pinky on the phone itself

Researched on 2026-11-11. The question: can Pinky run entirely on a phone, with no computer, and operate that same phone, with the person watching, approving, taking over and stopping it from the phone? This is a feasibility study, not a build. Nothing here ships; one throwaway prototype ran on an emulator and was deleted. A second Android prototype, kept in the repository, then ran end to end: see [Prototype results (Android)](#prototype-results-android).

Sources are cited inline. **[P]** marks a primary source (the vendor's documentation, AOSP or Apple's own text, a project's own repository), **[S]** a secondary one (press, forums, other people's write-ups). "Measured here" means it ran in this study.

## Verdict

| | Android | iOS |
| --- | --- | --- |
| Verdict | **Feasible with caveats** | **Not feasible** as an App Store app; **feasible with heavy caveats** only outside the App Store (see below) |
| Seeing | Accessibility tree (the companion already reads it) plus pixels | A screen broadcast the person starts (ReplayKit) shows pixels; no app can read another app's elements |
| Acting | Accessibility gestures, global actions and `ACTION_SET_TEXT`; more with a shell (Shizuku or Wireless debugging) | No public way to touch another app. Only Apple's own developer services, reached through a loopback VPN and a pairing file, or a hardware mouse |
| Staying alive | The accessibility service runs at about visible-app priority; force stop and vendor cleaners turn it off | iOS suspends an app that leaves the screen; only an XCTest runner (untested on the phone for long sessions) or keep-alive tricks keep a driver running |
| Distribution | Not Google Play (its accessibility policy bans autonomous agents since 2026-01-28); sideload or another store, where Play Protect's fraud protection and developer verification add friction | Not the App Store, TestFlight or the EU marketplaces (2.5.1, private APIs); sideload only, re-signed every 7 days or every year |
| Pinky's Go core | Runs as it is (measured here on Android 17) | Links as a static library (measured here); not run on a device |

## What ran here

A throwaway Go program (not committed) put Pinky's gateway on the phone: `gateway.New` with the default policy, a data folder, SQLite history, and `mcp.Server.HTTP` serving Streamable HTTP on `127.0.0.1:7777` with a bearer token. The phone was the `fake` driver showing a food app with a "Pay $12.80" button; a goroutine answered every approval with Allow once, standing in for the person, and a second port called `StopAll`. An MCP client on the computer reached it through `adb forward`.

Measured on the Android 17 emulator (API 37, arm64), 2026-11-11:

- It built for `GOOS=android GOARCH=arm64` with cgo and the NDK's `aarch64-linux-android29-clang` (NDK 27.1). Without cgo it does not build: purego, pulled in by `internal/ocr` and `internal/video`, needs `runtime/cgo` on Android (`undefined: cgo.Dlopen` in `dlfcn_android.go`). The stripped binary is 21.5 MB; as a shared library (`-buildmode=c-shared`, what `gomobile bind` produces) 30.4 MB unstripped.
- `phone_connect` waited for the "New agent" approval ("Let Probe Agent (remote) use your phones?"), as on a computer.
- `phone_tap` on "Pay $12.80" waited for the Payments rule ("Pay: “Pay $12.80”?"); the tap reached the phone only after Allow once, and the result said "The person allowed this once."
- After `StopAll`, the next call returned `stopped` ("The person pressed Stop and the lease is gone").
- The history database, policy files and the MCP server needed no change.

Also measured: the same program links for iOS as a static library (`GOOS=ios GOARCH=arm64 CGO_ENABLED=1 -buildmode=c-archive`, Xcode 27.2's iPhoneOS SDK, minimum iOS 17), 25.7 MB; and `internal/driver/ios` (go-ios and the gVisor tunnel) and `internal/driver/android` (the adb client, scrcpy and the companion client) both compile for `ios/arm64` and `android/arm64`. Nothing ran on an iPhone. No real phone was touched in this study.

So the safety model (rules, approvals, Stop all, history) is portable code today. What does not exist on a phone is everything around it: a driver that acts on the same phone without adb or usbmuxd, a UI for approvals that the agent cannot press, and a process that stays alive.

## Android

Android lets an app act on other apps through an accessibility service, and the companion already has one. That is the base tier. A shell tier on top, from the phone's own Wireless debugging or from Shizuku, gives back the rest of what Pinky does from a computer today.

### The accessibility tier: an app alone

An accessibility service the person turns on in Settings can read and act on every app. No computer, adb or root is needed. AOSP references below are to `android17-release` ([frameworks/base](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android17-release/) [P]).

**Seeing**

- **The element tree.** The companion already reads it fast, from the app's own process: 1 to 60 ms on the phone in its measurements, against seconds for `uiautomator dump` ([companion.md](companion.md)). On a computer, Pinky reads it today over adb; on the phone, the same app reads it directly.
- **Screenshots** need `android:canTakeScreenshot` and `takeScreenshot` (Android 11, API 30).
  - The system refuses a second request within 333 ms (`ACCESSIBILITY_TAKE_SCREENSHOT_REQUEST_INTERVAL_TIMES_MS` in `AccessibilityService.java`), so about three a second. That is enough for an agent, which looks after each step, but not for a live view.
  - Secure windows (`FLAG_SECURE`: banking apps, password screens, some video apps) come out redacted. Only a system app that is also an accessibility tool gets them (`canCaptureSecureLayers` in `AbstractAccessibilityServiceConnection.java`). On a computer, scrcpy shows them black too.
- **A live view** needs MediaProjection.
  - Since Android 14, every capture session needs the person's consent in a system dialog ([Android 14 changes](https://developer.android.com/about/versions/14/behavior-changes-14#media-projection-consent) [P]).
  - Since Android 14 QPR2, the person may share a single app instead of the screen ([app screen sharing](https://developer.android.com/about/versions/14/features/app-screen-sharing) [P]).
  - Since Android 15 QPR1 a projection shows a chip in the status bar and stops when the screen locks, and apps can tell they are being recorded ([Android 15 features](https://developer.android.com/about/versions/15/features) [P]).
  - The person, who is holding the phone, does not need a live view of it. Pinky would need one only to record the session, and it can skip that.

**Acting**

- **Taps, swipes, long presses, pinches and drags.**
  - These need `android:canPerformGestures` and `dispatchGesture` (Android 7).
  - A gesture holds up to 20 strokes and 60 seconds (`GestureDescription.java`). Strokes continue across calls (Android 8), so a drag can follow the agent's steps.
  - `setDisplayId` (Android 11) targets another display.
  - The companion turns gestures off today (`canPerformGestures="false"`) because adb does the touching. The phone app turns them on.
- **Text.**
  - `ACTION_SET_TEXT` fills a field, and the companion already falls back to the field's input connection and a paste when an app refuses it. The fallback is the accessibility input method, Android 13 and `flagInputMethodEditor`, which needs no keyboard switch ([companion.md](companion.md)).
  - The same input connection sends keys and editor actions (`InputMethod.java`: `commitText`, `sendKeyEvent`, `performEditorAction`), so Enter, Send and Search work.
  - Keys outside a text field cannot be sent, except as global actions.
- **Keys and system actions** are global actions (`AccessibilityService.java`):
  - Back, Home, Recents, Notifications, Quick Settings, Power dialog, Lock screen and Take screenshot;
  - Menu, D-pad and Play/pause;
  - dismissing the notification shade.
  - There is no volume key, no Power key to wake the phone, and no unlock. Pinky never types a passcode anyway.
- **Launching apps.**
  - The service may start activities from the background, because the system binds it with `BIND_ALLOW_BACKGROUND_ACTIVITY_STARTS` (below).
  - Pinky launches an app with its launch intent or a deep link.
  - Off Play, `QUERY_ALL_PACKAGES` lists the installed apps.

**What this tier cannot do**

- **No hidden second screen.**
  - An app can create a virtual display, but other apps' activities may only launch there if they allow embedding, which almost none do. The check is `isCallerAllowedToLaunchOnDisplay` in `ActivityTaskSupervisor.java`: on an untrusted display the activity must set `allowEmbedded`, and the caller must hold `ACTIVITY_EMBEDDING` or already show something there.
  - A trusted display needs `ADD_TRUSTED_DISPLAY`, which the shell has and apps do not.
  - So the agent works on the screen the person holds. Settings › Phones › Work in the background, which keeps the person's screen theirs, needs the shell tier.
- **It cannot watch the touchscreen directly.** An accessibility service sees events, not touches; asking for raw touches (`setMotionEventSources`, Android 14) takes them away from the apps. Take over needs a workaround (see [The safety model on the same phone](#the-safety-model-on-the-same-phone), rule 6) or `getevent` with the shell.
- **Secure and sensitive content stays out of reach.**
  - Redacted screenshots, as above.
  - Views marked `accessibilityDataSensitive` (Android 14) are left out of the tree, and non-tool services cannot act on them ([View](https://developer.android.com/reference/android/view/View#setAccessibilityDataSensitive(int)) [P]).
  - Payment and password screens that use these mean the agent hands over to the person, as it should anyway.
- **The lock screen.** It cannot unlock the phone. The person unlocks it, and Pinky works until the screen goes off; a wake lock keeps it on while a task runs, at a battery cost.

### The shell tier: adb from the phone itself

An app can give itself the shell user's rights (uid 2000) without a computer. Pinky's Android driver uses exactly those rights over adb today.

**How the app gets the shell**

- **Pairing with its own phone.**
  - In Shizuku, the person taps "Pair device with pairing code" in Wireless debugging and types the code into Shizuku's notification ([Shizuku setup](https://shizuku.rikka.app/guide/setup/) [P]).
  - LADB bundles an adb server for the same purpose ([LADB](https://github.com/tytydraco/LADB) [P]).
  - After pairing, the app finds the `_adb-tls-connect` port over mDNS and connects to `127.0.0.1` ([Shizuku BootCompleteReceiver.kt](https://github.com/RikkaApps/Shizuku/blob/b844bc491f1790c72328e1a8e5b2349f8978f0ea/manager/src/main/java/moe/shizuku/manager/receiver/BootCompleteReceiver.kt) [P]).
- **Key Mapper 4.0 (January 2026) does this without Shizuku** ([CHANGELOG](https://github.com/keymapperorg/KeyMapper/blob/develop/CHANGELOG.md) [P]). It is the closest template for Pinky:
  - its "Expert Mode" pairs through Wireless debugging and starts its own "system bridge" process as shell;
  - it taps the pairing button itself;
  - it turns Wireless debugging off once the bridge runs.
- **Pinky's own pieces fit, mostly.** Pinky's adb client already speaks `host:pair` and `host:connect`, but to an adb server ([android.md](android.md)), and a phone has none. Pinky would bundle one, as LADB does, or speak the pairing and TLS protocol to adbd itself, as Shizuku's manager does. The scrcpy server is a jar that any shell process can start with `app_process` ([scrcpy develop.md](https://github.com/Genymobile/scrcpy/blob/master/doc/develop.md) [P]). No one was found running it against the same phone.

**What the shell gives**

All of these are in AOSP 17's Shell manifest (`packages/Shell/AndroidManifest.xml` [P]):

- input injection (`INJECT_EVENTS`);
- screen capture (`READ_FRAME_BUFFER`, `CAPTURE_VIDEO_OUTPUT`), but not of secure windows;
- virtual displays for other apps (`ADD_TRUSTED_DISPLAY` since Android 15), which is Pinky's hidden second screen;
- `am start --display`, `uiautomator dump`, `pm grant` and `appops set`;
- `settings put secure`, which can turn the companion's service on and off as Pinky does now;
- `getevent`, for Take over.

In AOSP 17, `appops set <package> PROJECT_MEDIA allow` also makes screen capture start without the consent dialog ([MediaProjectionManagerService.java and MediaProjectionPermissionActivity.java, android17-release](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android17-release/) [P]). Vendor builds were not checked.

**Its limits**

- **Wi-Fi on a trusted network is required.**
  - `AdbDebuggingManager` turns ADB over Wi-Fi off unless the phone is on a Wi-Fi network in its trusted list ([AdbDebuggingManager.java](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android17-release/services/core/java/com/android/server/adb/AdbDebuggingManager.java) [P]).
  - Below Android 17, any network change turns it off, which kills a shell process started through it ([Automate forum](https://groups.google.com/g/automate-user/c/5AoEtmF9yVk) [S]).
  - Android 17's "ADB Wi-Fi 2.0" turns it back on when the phone rejoins a trusted network ([Android Developers blog](https://developer.android.com/blog/posts/introducing-fast-and-reliable-wireless-debugging-with-android-debug-bridge-adb-wi-fi-2-0) [P]).
  - A shell process that is already running does not need the network, but restarting it does.
- **After a reboot it must be started again.**
  - Shizuku 13.6 (May 2025) starts itself after a reboot without root on Android 13 and later, while the phone is on a trusted Wi-Fi network ([release](https://github.com/RikkaApps/Shizuku/releases/tag/v13.6.0) [P]).
  - It does this with `WRITE_SECURE_SETTINGS`, which the shell grants it once. With that permission it writes `adb_wifi_enabled=1` at boot and reconnects (BootCompleteReceiver.kt above).
  - Pinky could do the same. Whether stock Android 17 turns Wireless debugging back on by itself after a reboot was not verified.
- **Vendors add friction.**
  - Xiaomi asks for "USB debugging (Security settings)".
  - Samsung's Auto Blocker can block Wireless debugging.
  - On Android 17 QPR1, turning on USB debugging while Wireless debugging is on kills Key Mapper's bridge (Key Mapper 4.6.1 above). Pinky's shell process would likely die the same way when adbd restarts.
- **`ACCESS_LOCAL_NETWORK`.** Apps that target API 37 need it, a dangerous (runtime) permission, to find the pairing service ([local network permission](https://developer.android.com/privacy-and-security/local-network-permission) [P]).

**On-phone agents already use this tier**

- AutoGLM-For-Android ("无需电脑", no computer needed) and Roubao drive the phone through Shizuku ([AutoGLM-For-Android](https://github.com/Luokavin/AutoGLM-For-Android), [Roubao](https://github.com/Turbo1123/roubao) [P]).
- Operit uses accessibility, Shizuku or root, with virtual displays since v1.7.0 ([Operit](https://github.com/AAswordman/Operit) [P]).
- Panda (blurr) uses accessibility gestures alone ([blurr](https://github.com/Ayush0Chaudhary/blurr) [P]).
- Tasker added full Shizuku integration in 6.6.18 ([changes](https://tasker.joaoapps.com/changes/changes6.6.html) [P]).
- Mobilerun (formerly DroidRun), Open-AutoGLM, GELab-Zero and minitap's mobile-use all need a computer running adb ([mobilerun](https://github.com/droidrun/mobilerun), [Open-AutoGLM](https://github.com/zai-org/Open-AutoGLM), [GELab-Zero](https://github.com/stepfun-ai/gelab-zero), [mobile-use](https://github.com/minitap-ai/mobile-use) [P]).

**Platform agents are not open to Pinky**

- Android's Computer Control API runs a session on a secure background display with a system consent dialog. It is only for assistants an OEM preloads with the privileged `ACCESS_COMPUTER_CONTROL` permission ([Computer Control](https://developer.android.com/ai/computer-control) [P]).
- Gemini's task automation on the Pixel 10 and Galaxy S26 is Google's own ([Google](https://blog.google/innovation-and-ai/products/gemini-app/android-multi-step-tasks/) [P]).

### Staying alive

- **The accessibility service is the host to use.**
  - `system_server` binds the service with `BIND_FOREGROUND_SERVICE_WHILE_AWAKE` and `BIND_ALLOW_BACKGROUND_ACTIVITY_STARTS` ([AccessibilityServiceConnection.java](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/main/services/accessibility/java/com/android/server/accessibility/AccessibilityServiceConnection.java) [P]).
  - `OomAdjuster` then keeps its process at about the priority of a visible app.
  - It is not a foreground service, so the foreground-service time limits do not apply: dataSync's 6 hours in 24 since Android 15, for example ([Android 15 changes](https://developer.android.com/about/versions/15/behavior-changes-15) [P]).
  - It may start activities from the background ([background starts](https://developer.android.com/guide/components/activities/background-starts) [P]).
  - A foreground service of type `specialUse` can be added only for the notification.
- **A crash is survivable; a force stop is not.**
  - After a crash, the system restarts the service.
  - Repeated crashes mark it as broken, and the person has to turn it off and on.
  - A force stop removes it from `ENABLED_ACCESSIBILITY_SERVICES`. The person has to turn it on again in Settings (`onPackagesForceStoppedLocked`, AOSP [P]). That happens with Settings › Force stop, `am force-stop`, and every vendor cleaner that force-stops apps.
- **Vendor cleaners do exactly that.**
  - HyperOS force-stops apps during its lock-screen cleanup and Ultra Battery Saver, which strips the accessibility grant ([hyperos-accessibility-fix](https://github.com/chickendrop89/hyperos-accessibility-fix) [S]).
  - On One UI 7 and 8, Samsung users report accessibility services turning themselves off ([Samsung community](https://eu.community.samsung.com/t5/galaxy-s23-series/norton-password-manager-accessibility-switches-off-automatically/td-p/13375251) [S]).
  - OPPO kills services, accessibility services included, at screen-off ([dontkillmyapp](https://dontkillmyapp.com/oppo) [S]).
  - Huawei's PowerGenie kills apps ([dontkillmyapp](https://dontkillmyapp.com/huawei) [S]).
  - Pinky's checkup already tells people what to change on Huawei, OPPO, vivo and Xiaomi when the companion's service keeps turning off (`checkCompanionOff*` in `cmd/pinky/locales/en.json`). The phone app would show the same steps, notice when its service went away, and walk the person back.
- **A reboot keeps the service.** An enabled accessibility service stays enabled, and the system binds it again after boot. A shell process, as above, has to be started again.
- **Android 17 caps memory by the phone's RAM** ([Android 17 changes](https://developer.android.com/about/versions/17/behavior-changes-all) [P]). This matters for an on-device model, not for the Go core's few tens of MB.

### Distribution and policy

- **Not Google Play.**
  - Since 28 January 2026, Play's accessibility policy says that using the Accessibility API "to autonomously initiate, plan, and execute actions or decisions is strictly prohibited" ([Play policy](https://support.google.com/googleplay/android-developer/answer/10964491) [P], [Play newsletter, January 2026](https://developer.android.com/newsletter/play-monthly/2026/content/january) [P]).
  - `isAccessibilityTool` is reserved for apps built for people with disabilities.
  - An on-phone Pinky would also be offered through the shell tier, but the accessibility service is its base, so Play is out.
- **Sideloading.** Pinky already ships the companion APK outside Play, and the app would ship the same way: its own site, GitHub releases, perhaps F-Droid or another store. These sit in the way:
  - **Restricted settings.** On Android 13 and later, an app installed from a file must be allowed in App info before its accessibility service can be turned on ([Google](https://support.google.com/android/answer/12623953) [P]). Android 15's Enhanced Confirmation Mode extends this to apps from any installer that is not preinstalled or allowed, and behind a flag it refuses to turn on such a service during a call from an unknown number ([EnhancedConfirmationService.java](https://android.googlesource.com/platform/packages/modules/Permission/+/refs/heads/android17-release/service/java/com/android/ecm/EnhancedConfirmationService.java) [P]).
  - **Privacy reminders.** For a service that is not an accessibility tool, "the system will show a notification after a duration to inform the user about the privacy implications" ([AccessibilityServiceInfo](https://developer.android.com/reference/android/accessibilityservice/AccessibilityServiceInfo) [P]). Pinky's service would get one.
  - **Developer verification.** From 30 September 2026 in Brazil, Indonesia, Singapore and Thailand, and worldwide in 2027, certified phones install only apps whose developer is verified and whose package is registered. Unregistered apps need adb or an "advanced flow" with a restart and a 24-hour wait ([developer verification](https://developer.android.com/developer-verification/guides) [P], [9to5Google](https://9to5google.com/2026/08/18/google-gradually-rolling-out-androids-advanced-sideloading-ahead-of-developer-verification/) [S]). Pinky can register through the Android Developer Console. Installs through adb from a computer, today's path, stay exempt.
  - **Play Protect's enhanced fraud protection.** In a growing list of countries, Play Protect blocks the install of an app from a browser, a messaging app or a file manager when it requests SMS, notification-listener or accessibility permissions. That is exactly how a phone-only Pinky would arrive. It began in Singapore in 2024, then went to Thailand, Brazil and India ([Google India](https://blog.google/intl/en-in/products/launching-enhanced-fraud-protection-pilot-in-india/) [P], [Play Protect developer guidance](https://developers.google.com/android/play-protect/warning-dev-guidance) [P]), and since April 2026 it covers every Android user in Malaysia ([Google Malaysia](https://blog.google/intl/en-my/company-news/technology/google-launches-enhanced-protection-in-google/) [P]). It covers installs from those three kinds of app, so installs through adb or an app store are outside it. The study did not find whether the person can install anyway, other than by turning Play Protect off.
  - **Advanced Protection.** On Android 17, with this opt-in mode on, accessibility services that are not accessibility tools are blocked and their grants revoked ([Google](https://support.google.com/android/answer/16339980) [P]). Pinky could not run at all on such a phone.
- **Banking apps.**
  - Some banking apps refuse to sign in while an app from outside an app store has an accessibility service turned on. Banks in Malaysia have done this since 2024 and in Singapore since 2023 ([companion.md](companion.md)).
  - On a computer, Pinky keeps the service on only while an agent works. On the phone, turning it off is easy (`disableSelf()`), but turning it back on takes the shell tier (`WRITE_SECURE_SETTINGS`). Without it, either the service stays on and those banks stop working, or the person turns it on in Settings at the start of every task.

### Setup, once

1. Install the APK, allowing the browser to install apps. Where Play Protect's enhanced fraud protection runs, the browser route is blocked (see above).
2. Allow restricted settings in App info.
3. Turn on Pinky's accessibility service.
4. Battery settings, on some vendors: no restrictions, autostart, locked in recents.
5. A model key or account.
6. For the shell tier: turn on Developer options and Wireless debugging, on a trusted Wi-Fi network, and pair once.

Everything except the shell process survives a reboot. A force stop undoes step 3.

## Prototype results (Android)

Unlike the throwaway program in [What ran here](#what-ran-here), this prototype is kept: `android/standalone`, `internal/standalone` and `internal/driver/android/local`. It follows [the recommended architecture](#android-one-app-the-companion-grown-up) and ran end to end on 2026-11-11: on the Android 17 emulator (API 37, arm64) and on a Xiaomi Mi 10 Pro with LineageOS (Android 17). It does not ship.

### What was built

- **An app of its own, Pinky Standalone (`app.pinky.standalone`).** Its Gradle project compiles the companion's sources with its own manifest, service and screens, so the shipped companion stays as it is. The companion only gained hooks a subclass can use: an open service class, `extraCommand`, `extraFeatures` and `defaultIdleDisableMs`. A product would grow the companion itself instead.
- **The accessibility service** (`StandaloneService`) extends the companion's. It serves the same protocol on `@pinky_standalone` and adds the verbs that act:
  - `gesture`: up to 20 strokes through `dispatchGesture`, for taps, long presses, swipes and drags;
  - `global`: Back, Home, Recents and the shade;
  - `screenshot`: `takeScreenshot`, sent as a JPEG;
  - `launch` and `apps`: starting apps by package and listing them.

  It declares `canPerformGestures` and `canTakeScreenshot` and is still not an accessibility tool.
- **The Go core**, `libpinkycore.so` (`internal/standalone/libcore`), is built with `-buildmode=c-shared` for `android/arm64` with the NDK's clang (`scripts/standalone.sh`). It is loaded over JNI inside the service's process and holds:
  - the gateway, with the default rules and history in SQLite in the app's files folder;
  - the MCP server;
  - Ask Pinky's agent loop (`internal/pilot`), unchanged.
- **The local driver** (`internal/driver/android/local`) is a `driver.Device` over `companion.NewLocal`, which dials the abstract socket directly instead of through an adb forward. Taps, swipes, long presses and drags are gestures; keys are global actions; typing is `set_text`; apps start by package.
- **A control socket for Pinky's own screens**, `@pinky_standalone_core` (`internal/standalone/control.go`). It speaks line JSON: state, subscribe, start, decide, preset, stop and dismiss. Only the app's own uid may connect (`SO_PEERCRED`). The agent has only the MCP tools, so it cannot reach it.
- **The UI, in Kotlin:**
  - The task screen (`TaskActivity`, in a process of its own) holds the task, the provider, model and base URL, and the key. The key is never saved. The screen also has "Ask before", Start, Stop, New task and the steps as they happen.
  - The Working pill with Stop, and the card for an approval or a handoff (Deny, Allow for task, Allow once). The service draws both as accessibility overlays above every app, so they need no overlay permission.
- **Tools for testing, not in the APK:**
  - `corectl` drives the control socket from adb as the app's uid (`run-as`), and can start a task with any provider, model and base URL;
  - `localbench` times the service's verbs;
  - `fakemodel` stands in for the model. It speaks the Anthropic Messages API with scripted runs: `settings`, `loop` and `attack` in Settings, and `calc` and `calcloop` in Calculator, where a missed tap on a real phone changes nothing.

Where it differs from the recommended architecture, and why:

- **A separate package**, so the shipped companion and its users stay untouched.
- **The approval is a card over the current app, not a screen of Pinky's.** A card needs no activity start and stays over every app. The person sees what the agent wants to do next to where it will happen.
- **No `FLAG_SECURE` on the card yet.** The agent's screenshots show the card, but the agent cannot press it (below).
- **The key is typed for each task** and held in memory, not in the Keystore.

### Milestone 1: the service acts alone

`localbench` ran as the app's uid, over the service's socket only (no adb input), 10 runs of each verb: on the emulator, and on the Mi 10 Pro in Settings. Times are in ms; where a note has two numbers, the emulator's comes first. "Settled" means the screen stopped changing.

| Verb | Emulator p50 | Emulator p90 | Mi p50 | Mi p90 | Notes |
| --- | --- | --- | --- | --- | --- |
| Connect and ping | 16.4 | | 12.4 | | |
| Ping | 0.5 | 7.2 | 1.6 | 8.4 | |
| List apps | 146 | | 85.4 | | 59 launchable apps on the Mi |
| Launch Settings | 6.5 | | 31.4 | | Settled after 307 / 1,173; Settings' own screen read after 1,432 / 1,334 |
| Read the tree | 4.3 | 6.5 | 24.1 | 30.3 | 1.5–79.6 across the emulator's runs |
| Screenshot, JPEG q80 | 380 | 484 | 475 | 504 | Capture 349 / 371, encode 25 / 86, 137 / 121 KB; decoded in Go in 13.8 / 66.6 |
| Screenshot, PNG | 437 | | 613 | | 207 / 198 KB |
| Swipe of 300 ms | 307.8 | | 328.4 | 336.7 | 7.8 / 28.4 over the gesture; settled after 820 / 813 |
| Tap (50 ms) | 109 | 175 | 55.8 | 59.6 | 59 / 5.8 over the gesture; settled after 1,034 / 866 |
| Type (`set_text`) | 198 | 477 | 18.8 | 33.0 | |
| Long press of 800 ms | 872 | | 821.8 | 823.8 | 72 / 21.8 over the gesture |
| Back | | | 4.5 | 5.8 | Settled after 562 |
| Home | | | 2.3 | | Settled after 303 |

Every verb answers well within a model's turn, on the phone as on the emulator. Screenshots cost the most, and the system allows about three a second anyway (see [Seeing](#the-accessibility-tier-an-app-alone)).

### Milestone 2: the core drives through the local driver

With the core inside the service, MCP calls went through the gateway to the local driver and the phone:

- Launch, tap, swipe and type all settled.
- With "Ask before" on commits, Settings changes waited for approval. Answered over the control socket, Allow once acted once, and Deny returned `approval_denied` with nothing done.
- Launching Pinky Standalone returned `blocked_by_policy`. The prototype's package is one of Pinky's own apps (`policy.StandaloneApp`): its screen is blocked outright, and no task can lift that.
- Stop over the control socket ended the task in about 0.1 s.

`internal/standalone/standalone_test.go` runs the same flows on fake phones.

### Milestone 3: end to end on the phone

These were run on the emulator with `fakemodel` on the computer in place of a cloud model, so no API key was needed. A finger was stood in for by `adb shell input tap`. A task was typed on the task screen and started there; the screen moved to the back and the pill showed.

| Check | Result |
| --- | --- |
| The model's calls go through the gateway | Each became a step under the usual rules, with history |
| An approval shows on the card | The card showed over Settings with the step's title. Allow once settled the step (dark theme turned on); Deny returned `approval_denied` with nothing done |
| The agent waits while the card shows | `internal/pilot` holds the call until the person answers, so the model gets no turn meanwhile (safety rule 2) |
| The agent opens Pinky | `blocked_by_policy` |
| The agent taps and swipes on the pill's Stop | Refused and reported as not done; the task kept running |
| A gesture on the card's Allow once, sent to the service directly (not through the gateway) | Refused by the service (`own_ui`); the approval stayed pending and nothing changed |
| The same, in a test build without the service's refusal: taps on Allow once, Deny and the pill's Stop | `dispatchGesture` ran, and none of the three buttons reacted: the approval stayed pending and the task kept running. `input tap` at the same points worked |
| The person taps Stop on the pill | The task stopped in under a second |
| The agent reads the screen while the card shows | Settings' elements as without the card; the card's and pill's areas count as covered, and their content is absent |
| The agent reads the screen while the task screen is in front | One element, `list ""`; every action there is refused |

So the agent's input meets three walls, from the top:

1. **The gateway** blocks Pinky's own screen by package, as it does for the companion.
2. **The service** refuses any gesture with a point on the pill or the card, or on a window of Pinky's own (`OwnUI`), before `dispatchGesture` runs. It refuses Pinky's windows on other displays too. The driver reports `own_ui` as `driver.ErrUnsupported`, so the agent reads "Nothing was done", not `uncertain`.
3. **Android:** the pill, the card and the task screen are `accessibilityDataSensitive` and `filterTouchesWhenObscured`. Android drops the touches our service injects on them, even when the service's own check is gone.

Found on the way, and worth keeping in the product:

- **`accessibilityDataSensitive` hides Pinky's views from Pinky's own service too.** The card's and pill's windows come back with an empty package and no root. Only the window title is left, so the prototype sets the title to its package and the driver knows its own overlays by that. Only accessibility services can draw accessibility overlays. Another service could copy the title, but all it would gain is that its overlay stops hiding the app below from the agent.
- **Pinky's own card made every approved step stale.** The card occluded the app's elements under it, so the screen read after the person allowed a step differed from the one the step was asked on. The gateway then refused the step as `stale`. Now the driver leaves Pinky's overlays out of the screen the agent reads and adds their areas to the covered ones (`agentScreen` in `internal/driver/android/local`).
- **Android itself ignores the service's taps on sensitive views**, as rule 3 of [the safety model](#the-safety-model-on-the-same-phone) expects. With `OwnUI` taken out of a test build, `dispatchGesture` taps on the card and the pill reached nothing. The card does not check `FLAG_IS_ACCESSIBILITY_EVENT` itself and nothing covered it, so the drop is most likely the sensitive views' own filter. So far this is the stock Android 17 emulator only; vendor versions still need checking.

### On a real phone

On 2026-11-11 the prototype ran on a Xiaomi Mi 10 Pro with LineageOS (Android 17), with the owner's OK. It was installed by adb and its own accessibility service was turned on; no other setting changed. The agent and the tests touched only Calculator, the home screen and Settings, where they scrolled and typed into the search field. Afterwards the service was turned off again and the app uninstalled.

**A cloud model.** DeepSeek answered through an OpenAI-compatible gateway on the home network (provider `openai`, with a base URL). The task "Open Calculator and work out 12 × 34" was started over the control socket (`corectl start -provider openai`), because typing on the task screen through adb fights the phone's Chinese keyboard; the task screen's own fields were tested on the emulator. "Ask before" was set to every action, so each step waited for a card.

- The first thing the model met was Android's warning that the library was not aligned to 16 KB pages (below). It asked before closing it: "Dismiss the system compatibility warning dialog".
- It opened Calculator and sent all seven key taps in one `phone_batch`. Each tap settled in 418–970 ms.
- It finished with the right answer, 408, after 7 steps and 77K tokens, in 93 s. About 45 s of that was the cards waiting for an answer. The model's turns took 1.9–3.5 s.
- Two of its steps came back `stale`, from the card bug below.

**The checks of Milestone 3** ran again, with `fakemodel` on the computer (reached through `adb reverse`) and `input tap` for the finger:

| Check | Result |
| --- | --- |
| The person answers a card | Allow once, Deny and Allow for task each reached the core within 384–815 ms of the tap, counting the adb command and polling every 250 ms |
| The agent opens Pinky | `blocked_by_policy` |
| The agent taps and swipes on the pill's Stop | Both refused as not done, in 1–2 ms; the task went on and finished |
| The person taps Stop on the pill | The task stopped 661 ms after the tap |

The real phone found three problems, all fixed on the branch:

- **A step whose target lay under the card came back `stale` after Allow once.** Before an approved step acts, the gateway reads the screen again. The card was still up then, so a target under its top edge, such as Calculator's 6 and =, counted as covered, and the gateway refused the step. On the emulator no Settings row it tapped sat under the card. Now the card goes away first and sends the answer once its window is gone, at most 500 ms later. In the same 7 × 6 task with every step allowed once, 6 and = were `stale` before the fix and every step settled after it.
- **Android 17 showed a dialog warning that `libpinkycore.so` was not aligned to 16 KB pages.** `scripts/standalone.sh` now links with `max-page-size=16384`, which aligns its segments to 16 KB, and the warning is gone. The library's size did not change.
- **The phone locks about 2 minutes after the last touch, and a locked phone ends the task.** A person reading a card is not touching the screen either. The pill now keeps the screen on while a task runs (`keepScreenOn`). It was in the build of the last task, which was too short to show it.

### Size, memory and battery

- **`libpinkycore.so`:** 21.1 MB, stripped.
- **The debug APK:** 23.9 MB, of which 2.7 MB is not Go.
- **Memory over a 10-minute task.** The `loop` script browsed Settings for 10 minutes: 98 model turns, 97 steps, every one settled, with `fakemodel` answering after 5 s as a model would. `dumpsys meminfo` every 30 s:
  - The service's process, which holds the Go core: 74 MB PSS idle, then 79–80 MB through the task, flat with no growth. RSS was about 212 MB, most of it shared code. Of the PSS, about 27 MB is the Go heap (counted as "Unknown"), 20 MB the library mapped from the APK, 11 MB native heap and 9 MB Java heap.
  - The task screen's process: 23 MB PSS. Android froze it while it was in the back, as it does with any cached process; the pill and the card are in the service's process and kept working.
- **CPU:** 6.6 s of the service's process over the 10 minutes, about 1.1% of one core. Steps took 515 ms for a key, 590 ms for a tap, 750 ms for a swipe and 1.1 s for a launch on average, settling included.
- **The same on the Mi 10 Pro.** The `calcloop` script worked sums in Calculator, tapping keys and reading the screen in turn, with `fakemodel` answering after 5 s. The task stopped at its limit of 100 steps after 567 s.
  - The service's process: 82–85 MB PSS through the task, and 84 MB before it. Three of the 16 readings were 139–141 MB; the two with a reading after them were back to 82–83 MB 30 s later, and the third came as the task ended. RSS stayed at 214–217 MB.
  - The task screen's process: 33 MB PSS.
  - CPU: 28.6 s of the service's process, about 5% of one core. That is more than on the emulator, whose cores are faster.
- **One model reply came 35 s late on the emulator** (96 s in an earlier run). Both times Settings had just opened Network & internet, which makes the emulator's simulated Wi-Fi scan. On the Mi no reply was late: over 100 turns the longest gap between two was 7 s, of which `fakemodel` took 5. So it was the emulator's network.
- **Battery:** not measured. The emulator's battery is simulated, and the Mi runs on its charger, which holds it at 54% without charging. Its battery warmed from 30.9 °C to 32.0 °C over the 10 minutes.

### Not verified yet

- **Other phones.** Only the Mi 10 Pro on LineageOS, which is close to stock Android, has run it. Vendor systems such as HyperOS may need extra permissions for overlays or to keep the service alive, and Android's drop of injected touches on sensitive views was only tried on the emulator. The Pixel (Android 10) is next, with the owner's OK.
- **Android 10 to 13.**
  - `takeScreenshot` needs Android 11, so Android 10 has no screenshots.
  - `accessibilityDataSensitive` needs Android 14. Before it, Pinky's own controls are visible to the service and take its injected touches, so only the gateway and `OwnUI` keep the agent off them.
- **Claude through the Anthropic API** with the person's own key. One cloud model ran (DeepSeek, OpenAI-compatible); `fakemodel` speaks the Anthropic API.
- **Take over.** Not built: the person's touches are not told apart from the agent's.
- **Battery and heat** over a long task, on a phone running on its battery.
- **Keeping the screen on** through a card left unanswered for more than 2 minutes.

### The shell tier

Not tried, because it needs a setting changed on the phone: Developer options and Wireless debugging. For this prototype it would take:

1. Pairing once with the phone's own adbd. Pinky's adb client speaks `host:pair` to an adb server, and the phone has none, so the app would need its own pairing and TLS to adbd, as Shizuku and Key Mapper do.
2. Starting a shell process with `app_process` that serves `internal/driver/android`'s shell features: `getevent` for Take over, `input`, and the hidden second screen.
3. Having the core use that process for those features and the local driver for the rest.

[Effort](#effort) puts this at 3–4 more weeks.

## iOS

There are three ways to give an iPhone app hands without a computer, from most to least acceptable to Apple: an App Store app on public APIs, which can watch but not act; Apple's developer services reached from the phone itself, which act but rule out every store; and a hardware mouse plugged into the phone.

### An App Store app watches but cannot act

What public APIs give an app on iOS 26 and 27:

- **Pixels, if the person starts a broadcast.** A broadcast upload extension receives every frame of the whole screen, other apps included, once the person taps Start Broadcast in the system picker ([RPBroadcastSampleHandler](https://developer.apple.com/documentation/replaykit/rpbroadcastsamplehandler), [RPSystemBroadcastPickerView](https://developer.apple.com/documentation/replaykit/rpsystembroadcastpickerview) [P]). It runs as its own process, keeps running while other apps are in front, and has about 50 MB of memory ([forum thread](https://developer.apple.com/forums/thread/651367) [S]). FairPlay video stays black, apps can hide their content while [`isCaptured`](https://developer.apple.com/documentation/uikit/uiscreen/iscaptured) is true [P], and the passcode screen and Apple Pay are not mirrored ([esp32-iphone-agent](https://github.com/onehumanbeing/esp32-iphone-agent) [S]). ChatGPT and Gemini Live already ship this, to watch only ([Neowin](https://www.neowin.net/news/chatgpt-mobile-apps-now-support-video-and-screensharing-in-advanced-voice-mode/) [S]).
- **No elements.** No API reads another app's accessibility tree. On iOS 27 apps tag their own views for Siri's on-screen awareness, and only Siri and Shortcuts read those tags ([WWDC26 session 240](https://developer.apple.com/videos/play/wwdc2026/240/) [P]). Shortcuts' "Get What's On Screen" returns more structure on iOS 27 ([Apple](https://support.apple.com/en-al/149045) [P]), but it is an action in a shortcut, not an API.
- **No hands.** No public API taps, types or scrolls in another app. What is left is coarse:
  - deep links;
  - shortcuts the person built, run through `shortcuts://run-shortcut`, which brings Shortcuts to the front ([Apple](https://support.apple.com/guide/shortcuts/run-a-shortcut-from-a-url-apd624386f42/ios) [P]);
  - other apps' App Intents, which only Shortcuts and Siri can call;
  - `RunSystemShortcutIntent`, new on iOS 27, which works only in widgets and controls ([Apple](https://developer.apple.com/documentation/appintents/runsystemshortcutintent) [P]).

  Siri AI shipped with iOS 27 on 14 September 2026 as a beta (English first, iPhone 15 Pro or later, not in the EU or China) and acts in other apps only through their App Intents ([Apple Newsroom](https://www.apple.com/newsroom/2026/09/siri-ai-a-profoundly-more-capable-and-personal-assistant-is-here/) [P]). Actions inside third-party apps are rumored for iOS 27.5 or 27.6 ([CellCog, citing Gurman](https://cellcog.ai/blog/apple-siri-ai-agent/) [S]). Playing synthesized speech to Voice Control is an untested hack that would fail 2.5.1's "intended purposes".
- **Thinking, reporting and approving all work.**
  - The Foundation Models framework: on iOS 27 an on-device model with 8,192 tokens of context and image input, and a Private Cloud Compute model with 32K behind an entitlement ([WWDC26 session 241](https://developer.apple.com/videos/play/wwdc2026/241/) [P]). It is rate-limited in the background ([forum thread](https://developer.apple.com/forums/thread/793787) [S]).
  - Or a cloud model, with the explicit consent guideline 5.1.2(i) asks for before personal data goes to a third-party AI.
  - Live Activity and Dynamic Island buttons through [`LiveActivityIntent`](https://developer.apple.com/documentation/appintents/liveactivityintent) [P], and actionable notifications.
  - [`BGContinuedProcessingTask`](https://developer.apple.com/documentation/backgroundtasks/bgcontinuedprocessingtask) (iOS 26) for a task the person starts in the foreground [P].

So an App Store Pinky could watch the screen and say what to tap, but could not tap it. That is a different product.

### Apple's developer services, from the phone itself

Pinky on a Mac reaches an iPhone's developer services through a CoreDevice tunnel ([ios.md](ios.md)). An app on the phone can reach the same services on its own phone through a loopback VPN.

**The route**

- LocalDevVPN is a packet-tunnel app on the App Store. It routes one address, 10.7.0.1, and swaps the source and destination of every packet, so traffic sent there comes back into the phone's own network stack ([PacketTunnelProvider.swift](https://github.com/jkcoxson/LocalDevVPN/blob/main/TunnelProv/PacketTunnelProvider.swift#L59-L91) [P]).
- An app holding a RemotePairing ("RPPairing") file then does pair-verify, the TLS-PSK tunnel and RSD in userspace against its own phone, as Pinky does from a Mac ([StikDebug](https://github.com/StikDebug/StikDebug/blob/main/StikDebug/Device/JITEnableContext.swift) [P], [SideStore's minimuxer](https://github.com/SideStore/minimuxer/blob/develop/DeviceGateway/idevice/IdeviceGateway.swift#L263-L302) [P]).
- StikDebug needs no special entitlement for this ([entitlements](https://github.com/StikDebug/StikDebug/blob/main/StikDebug/StikDebug.entitlements) [P]).

**Proven on the phone itself**

- SideStore and StikDebug use installation_proxy, AFC, debugproxy, DVT process control and syslog this way.
- They also mount the Developer Disk Image themselves ([DeveloperDiskImageService.swift](https://github.com/StikDebug/StikDebug/blob/main/StikDebug/Services/DeveloperDiskImageService.swift) [P]):
  - below iOS 26.4, the personalized image;
  - from 26.4 on, the cryptex, which stays mounted until an update or a reset ([StikDebug 3.1.11](https://github.com/StikDebug/StikDebug/releases/tag/3.1.11) [P]).
- The first mount needs the internet, because Apple's signing server issues a ticket for each phone ([iCTRL](https://github.com/OpenMinis/iCTRL) [P]).

**Driving other apps through XCTest: shown in open code**

- iCTRL (Apache-2.0) is an XCUITest runner the person starts from its Home Screen icon. It mounts the DDI over LocalDevVPN ([DDIMounter.swift](https://github.com/OpenMinis/iCTRL/blob/main/iCTRLApp/DDI/DDIMounter.swift) [P]).
- It serves agents taps, swipes, typing, an accessibility page source, screenshots, an H.264 stream and app launches ([iCTRL](https://github.com/OpenMinis/iCTRL) [P]).
- This is Pinky's iOS 17.4–26 path (WebDriverAgent through testmanagerd), moved onto the phone.
- iCTRL calls its use of private XCTest and `LSApplicationWorkspace` APIs "not App Store safe", and its first run asks for the passcode to enable UI automation.

**Driving other apps through iOS 27's services: claimed, narrowly**

- Actell (GPL-3.0, October 2026) reports working over LocalDevVPN ([ja1chen/actell](https://github.com/ja1chen/actell/blob/main/coredevice/docs/LOCALDEVVPN.md) [P]):
  - screenshots through `displayservice`;
  - real taps through the HID service;
  - an element tree through `remoteAXService`.
- That is Pinky's iOS 27 path. The evidence has three limits:
  - It was tested in the foreground only.
  - Its tree reader is tied to iOS 27.0 build 24A437.
  - Its authors say first pairing and DDI preparation still need a computer.
- The DDI's HID daemon drops synthetic events "unless a displayservice media stream is active" ([idevice hid.rs](https://github.com/jkcoxson/idevice/blob/master/idevice/src/services/core_device/hid.rs) [P]). So the app must keep streaming its own screen while it taps.

**Pairing without a computer only works from iOS 27**

- In Settings › Privacy & Security › Developer Mode, the phone can pair with an app on the same network that advertises itself as a pairable host ([idevice responder.rs](https://github.com/jkcoxson/idevice/blob/master/idevice/src/remote_pairing/responder.rs) [P]).
- StikDebug 3.2.0 does this on the phone itself ([release](https://github.com/StikDebug/StikDebug/releases/tag/3.2.0) [P]).
- Below iOS 27, pairing needs a computer once ([idevice_pair](https://github.com/jkcoxson/idevice_pair) [P]).
- A pairing stops working when the passcode changes ([StikDebug PAIRING.md](https://github.com/StikDebug/StikDebug/blob/main/PAIRING.md) [P]), and Apple says to pair again after an iOS upgrade.
- Developer Mode appears in Settings only once pairing has started ([Apple](https://developer.apple.com/documentation/xcode/enabling-developer-mode-on-a-device) [P]).

**Apple breaks it from time to time**

- iOS 26.4 stopped accepting lockdown-style pairing files over the loopback, and the tools moved to RPPairing ([SideStore 0.6.3](https://github.com/SideStore/SideStore/releases/tag/0.6.3) [P], [issue 1698](https://github.com/SideStore/SideStore/issues/1698) [P]).
- The loopback works only on Wi-Fi ([iCTRL](https://github.com/OpenMinis/iCTRL) [P]).

What stops it from being a product:

- **Staying alive.** iOS suspends an app soon after it leaves the screen.
  - StikDebug keeps itself alive while it pairs by playing background audio and using location ([OnDevicePairingService.swift](https://github.com/StikDebug/StikDebug/blob/main/StikDebug/Services/OnDevicePairingService.swift) [P]). Guideline 2.5.4 forbids that in store apps, and iOS can still end it.
  - Pinky's work happens while other apps are in front, so the driver would have to live in a process iOS keeps running.
  - One candidate is an XCTest runner, as on a Mac (iCTRL's approach, not checked here for long sessions).
  - The other is the broadcast extension, which has 50 MB and exists only while the person broadcasts.
- **Distribution.**
  - Guidelines 2.5.1 (public APIs only) and 2.5.2 rule it out of the App Store. Both also apply to notarization for the EU's alternative marketplaces, so AltStore PAL is out too ([guidelines, updated 16 October 2026](https://developer.apple.com/app-store/review/guidelines/) [P]).
  - StikDebug reached the App Store in 2025. It was removed when its developer's account was terminated, around December 2025 ([Ubergizmo](https://www.ubergizmo.com/2025/12/stikdebug-pulled-appstore/) [S]).
  - TestFlight goes through Beta App Review, and its builds expire after 90 days ([Apple](https://developer.apple.com/help/app-store-connect/test-a-beta-version/testflight-overview/) [P]).
  - The enterprise program is for a company's own employees ([Apple](https://developer.apple.com/programs/enterprise/) [P]).
  - That leaves sideloading. A free Apple account signs for 7 days and 3 apps, a paid one for a year ([iCTRL](https://github.com/OpenMinis/iCTRL) [P], [SideGuide](https://sideguide.pages.dev/) [S]). The first install needs a computer (SideStore through iloader, [0.6.3](https://github.com/SideStore/SideStore/releases/tag/0.6.3) [P]).
- **Setup.** A computer once for the first sideload, and below iOS 27 once more for pairing. Then Developer Mode on, the passcode typed once to enable UI automation, Wi-Fi, and LocalDevVPN running.

### A hardware mouse

- **How it works.** The ESP32 iPhone Agent, now "Handsy AI", plugs an ESP32-S3 board into the iPhone's USB-C port as a mouse and keyboard. With AssistiveTouch on, its pointer can tap anything ([esp32-iphone-agent](https://github.com/onehumanbeing/esp32-iphone-agent) [P], [Hackaday, 14 October 2026](https://hackaday.com/2026/10/14/ai-controls-iphone-via-esp32/) [S]).
  - The phone mirrors its screen to the board over AirPlay.
  - The agent app on the same iPhone talks to the board over Bluetooth and Wi-Fi.
  - A stop button sits in the Dynamic Island.
  - Mirroring stops when the phone locks.
- **Status.** The app is on TestFlight, and its App Store listing is "in review"; whether Apple accepts it is open.
- **What it means for Pinky.** It needs no computer once the board is flashed, and it uses only public APIs. It is the one route that might pass review. But it is hardware that Pinky would have to sell or explain, and it gives no element tree: everything is pixels.

### Ruled out

- **Jailbreak.** Dopamine covers iOS 15.0–17.3.1. On A12–A13 only, it also covers up to 18.7.1 and 26.0–26.0.1 ([opa334/Dopamine](https://github.com/opa334/Dopamine) [P]). Nothing public covers 26.1 or later.
- **TrollStore** works up to iOS 17.0, and "17.0.1+ will NEVER be supported" ([opa334/TrollStore](https://github.com/opa334/TrollStore) [P]).
- **MDM** is for organizations (guideline 5.5) and has no command that injects input or reads other apps. Its only screen command, Request Mirroring, asks for AirPlay ([Apple](https://developer.apple.com/documentation/devicemanagement/commands-and-queries) [P]).
- App Review would reject an app built on any of the three. A jailbreak tweak or a TrollStore install never goes through it at all.

## The agent loop

On a computer, the agent is someone else's program (Claude Code, Codex, a script), which talks to Pinky over MCP. Without a computer, the loop has to run somewhere else. There are three choices.

1. **An outside agent over MCP, as today.**
   - Pinky's MCP server runs on the phone unchanged (measured above).
   - Only an agent on the same phone, such as a command-line agent in Termux on Android, could reach it on 127.0.0.1.
   - Cloud agents connect to remote MCP servers from their own servers. The Claude API's MCP connector, for one, makes the connection from Anthropic's side ([MCP connector](https://platform.claude.com/docs/en/agents-and-tools/mcp-connector) [P]).
   - To reach a phone, such an agent needs a public address. Pinky would have to run a relay with accounts and authentication: a new service, and a way in from the internet that today's Pinky does not have.
2. **Pinky runs the loop itself and calls a cloud model.**
   - The app sends the model the task, the screen and Pinky's own tool definitions (`phone_observe`, `phone_tap` and the rest). For the screen, the text tree comes first and a screenshot only when needed.
   - Each tool call the model returns goes through the gateway exactly as an MCP call does today, under the same rules, approvals, history and Stop all.
   - The phone needs an API key, either the person's own (kept in the Android Keystore or the iOS Keychain) or one tied to a Pinky account.
   - With Claude, the default would be `claude-opus-5-5`, with `claude-sonnet-5-5` as the cheaper choice. The calls would go through the Go SDK (`github.com/anthropics/anthropic-sdk-go/v2`) inside the Go core.
   - Both stores require clear consent before personal data goes to a third-party AI: Apple in 5.1.2(i), Google in Play's User Data policy.
3. **An on-device model.** None is ready to run the loop:
   - Gemini Nano, through ML Kit's GenAI APIs, returns `BACKGROUND_USE_BLOCKED` unless the calling app is in front ([ML Kit GenAI](https://developers.google.com/ml-kit/genai) [P]). Pinky works while other apps are in front.
   - Gemma 4 on LiteRT-LM decodes about 52 tokens a second on a Galaxy S26 Ultra's GPU for E2B, and about 22 for E4B ([LiteRT-LM](https://developers.google.com/edge/litert-lm/models/gemma-4) [P]). No one has published how well it does as a GUI agent.
   - Small GUI-agent models do worse than cloud models:
     - MAI-UI scores 49.1% on AndroidWorld at 2B and 70.7% at 8B ([arXiv 2512.22047](https://arxiv.org/abs/2512.22047) [P]).
     - On the harder MobileWorld benchmark, small models score about 5–11% ([arXiv 2512.19432](https://arxiv.org/abs/2512.19432) [P]).
     - A 2–4 GB model held in memory also runs into Android 17's new memory limits based on RAM ([behavior changes](https://developer.android.com/about/versions/17/behavior-changes-all) [P]).
   - Apple's on-device model has 8,192 tokens of context on iOS 27, and it is rate-limited in the background. One screen's tree can fill that context.

   An on-device model could still help at the edges, for example to describe a screenshot without sending it off the phone.

**Recommended:** choice 2, with the model and key the person picks. Choice 1 can come later for people who bring their own agent, through a relay Pinky runs or a tunnel they run themselves.

## The safety model on the same phone

Most of the safety model carries over as it is, because it is code in the gateway, and the gateway runs on the phone (measured above):

- rules and approvals bound to their arguments, single use, re-checked before acting (`stale`);
- blocked apps hidden;
- the agent's scope, the task's app list and read-only mode;
- passwords and one-time codes refused in `phone_type`;
- history.

One assumption breaks. [DESIGN.md](../DESIGN.md)'s threat model says "Consent happens only in the client UI", and on a computer the agent cannot reach that UI. On one phone, the client UI is on the screen the agent drives. A confused or prompt-injected agent could:

- tap Allow on Pinky's own card, or on the notification or Live Activity that carries it;
- open Pinky and add an Always allow, or turn off read-only mode;
- open Settings and change Pinky's permissions, Shizuku or Wireless debugging.

Turning Pinky off only stops the agent, so it is not the danger. The danger is the agent approving its own steps.

**The fix rests on one fact: on the phone, every input the agent makes goes through Pinky's own driver.** The agent has no hands of its own. So the gateway can enforce these rules itself, and the operating system's checks below are a second layer for races and bugs.

1. **The agent cannot act on Pinky.**
   - The gateway refuses any action that lands on Pinky's own windows, notification, tile, Live Activity or app.
   - It also refuses actions on the Settings pages that manage Pinky: its App info, Accessibility, Shizuku, Developer options and Wireless debugging.
   - Today `phone_app` already never touches Pinky's own apps. This extends that rule to every action.
   - A tap at coordinates is checked against the windows on screen when it is sent. On Android the accessibility window list gives each window's package.
2. **While Pinky asks, the agent waits.** The gateway knows when its card is on screen and refuses all of the agent's input until the person answers. On a computer the agent can keep working while a card waits; on the phone the card covers the same screen.
3. **Allow only goes through screens where Pinky can protect it.** A notification button is drawn by the system, so Pinky cannot check who pressed it. On a phone, a notification or Live Activity therefore only opens Pinky's own approval screen. Allow sits on that screen, never in the notification itself. That screen (Android) has three protections:
   - **Hidden from the agent's tree.** Views marked `accessibilityDataSensitive="yes"` (API 34) are left out of the tree given to services that are not accessibility tools, and those services cannot act on them ([View.java](https://cs.android.com/android/platform/superproject/main/+/main:frameworks/base/core/java/android/view/View.java) [P]). Pinky's service must therefore not declare itself a tool (`isAccessibilityTool`), which is meant for disability apps anyway.
   - **Injected touches ignored.** The tree rule does not stop a gesture at coordinates, but the injected event carries a mark:
     - Android sets `FLAG_IS_ACCESSIBILITY_EVENT` (0x800) on every event an accessibility service injects, "to allow apps to distinguish them from events that originate from actual hardware" ([InputDispatcher.cpp](https://cs.android.com/android/platform/superproject/main/+/main:frameworks/native/services/inputflinger/dispatcher/InputDispatcher.cpp) [P]). The constant is hidden, but `MotionEvent.getFlags()` is public, so Allow can ignore such touches.
     - Input injected through a shell (Shizuku, Wireless debugging, `input`) gets the virtual device id −1 (same file), which a real finger never has.
   - **Out of sight and protected from overlays.** `FLAG_SECURE` keeps the screen out of screenshots and screen capture, and `filterTouchesWhenObscured` blocks taps through overlays.

   On iOS no such mark is known for HID taps from the developer services, so rules 1 and 2 carry the whole load there.
4. **Biometrics for what matters most.** Allowing a payment, or any Always allow, can ask for a fingerprint or face through `BiometricPrompt` (Android) or LocalAuthentication (iOS). DESIGN.md already asks for Touch ID from clients outside the in-process UI. An agent cannot make a fingerprint. Face unlock, though, can pass just because the person is looking at the phone: it proves presence, not intent. So it must come after a tap the agent cannot make (rule 3), never instead of one.
5. **Stop all from where the agent cannot reach.**
   - **Android:**
     - A volume-key pattern, which the accessibility service sees before apps do ([`onKeyEvent`](https://developer.android.com/reference/android/accessibilityservice/AccessibilityService#onKeyEvent(android.view.KeyEvent)) with `canRequestFilterKeyEvents` [P]).
     - A Quick Settings tile, and Stop on Pinky's notification. A foreground service must show that notification anyway.
   - **iOS:**
     - Stop on the Live Activity.
     - A control the person can put in Control Center, on the Lock Screen or on the Action button.
   - The agent pressing Stop only stops itself, so these need no protection from it.
   - Locking the phone pauses the task (`device_locked`), as today.
6. **Take over needs a new signal.**
   - Today the Android driver reads the person's touches with `getevent` through adb's shell. Input that Pinky injects never reaches `/dev/input` ([person.go](../../internal/driver/android/person.go)).
   - With the shell tier (Shizuku or Wireless debugging) the same code works on the phone.
   - Without a shell, an accessibility service cannot watch the touchscreen without taking the touches from the apps. One workaround: a tiny accessibility overlay with `FLAG_WATCH_OUTSIDE_TOUCH` gets `ACTION_OUTSIDE` for every touch that starts anywhere else ([WindowManager.java](https://android.googlesource.com/platform/frameworks/base/+/refs/heads/android17-release/core/java/android/view/WindowManager.java) [P]). Touches that come while Pinky's own gesture runs, or that carry the 0x800 flag, are the agent's; the rest are the person's. Not tried. Failing that, the gateway would infer the person from screen changes the agent did not cause, which is weaker.
   - On iOS nothing is known.
7. **Passcodes stay with the person.**
   - Password and one-time-code fields are refused as today, and a locked phone waits for the person.
   - Pinky on a computer can unlock with a saved PIN after Touch ID (DESIGN.md, "Unlock from the computer"). On the phone the person is holding it, so that feature has no place there and Pinky should not store the passcode at all.
8. **Watching is easier.** The person sees the same screen the agent drives. Android can draw a frame around the screen while the agent acts: an accessibility overlay sits above the notification shade and the lock screen, needs no overlay permission, and stays out of the agent's window screenshots (`takeScreenshotOfWindow`, Android 14). iOS has the Live Activity. What is lost is the second screen: the person cannot keep using the phone for something else while the agent works, unless Android's hidden second screen is available (the shell tier).

## Recommended architecture

### Android: one app, the companion grown up

```
┌──────────────── Pinky for Android (app.pinky.companion) ────────────────┐
│ Kotlin UI: task view, approval screen (sensitive views, FLAG_SECURE),   │
│ settings, history, onboarding, Quick Settings tile, notification        │
│                                                                         │
│ Accessibility service process                                           │
│   ├─ Go core (c-shared library, loaded over JNI)                        │
│   │    gateway · policy · approvals · history (SQLite) · agent loop      │
│   │    driver "android/local" ──► @pinky_companion (same uid, allowed)  │
│   │    MCP on 127.0.0.1, off by default                                 │
│   └─ Kotlin service: tree, events, set_text (today)                     │
│        + dispatchGesture, global actions, takeScreenshot, app launches  │
│                                                                         │
│ Optional shell tier: Shizuku or Wireless debugging                      │
│   getevent (take over), input injection, hidden second screen,          │
│   WRITE_SECURE_SETTINGS (service on only while an agent works)          │
└─────────────────────────────────────────────────────────────────────────┘
                 │ HTTPS: model API (the person's key)
```

1. **One process for the loop.** The Go core loads as a shared library inside the accessibility service's process, which Android keeps at about visible-app priority (see [Staying alive](#staying-alive)). The UI talks to the core through JNI. No second app, no foreground-service time limits.
2. **The driver reuses the companion's socket.** A new `driver.Device`, `android/local`, dials `@pinky_companion` directly; the server accepts its own uid ([CompanionServer.kt](../../android/companion/app/src/main/kotlin/app/pinky/companion/CompanionServer.kt)). The protocol and the Go client in `internal/driver/android/companion` stay; only the adb forward goes. The companion gains the verbs it lacks today: gestures (`canPerformGestures` is `false` now, on purpose), global actions (Back, Home, Recents, notifications, lock), `takeScreenshot` (API 30) and starting apps.
3. **The agent loop is new code in the Go core** (see The agent loop): Pinky's tool definitions offered to a cloud model, each call through the gateway.
4. **The approval UI is native Kotlin.** MyGo is a desktop UI, so every screen on the phone is new. The client API the desktop UI uses (subscribe, needs, decide) is what the Kotlin UI calls through JNI.
5. **A shell tier for people who want more.** With Shizuku or Wireless debugging, the existing Android driver's shell features become available on the phone: `getevent` for Take over, the hidden second screen for background work, and granting the app `WRITE_SECURE_SETTINGS` once, so it can turn its own service on only while an agent works, as Pinky does from the computer today.
6. **Distributed outside Google Play.** From Pinky's own site and GitHub releases, as the companion APK is today, and possibly other stores. See [Distribution and policy](#distribution-and-policy) and [Setup, once](#setup-once) for what this costs.

### iOS: do not build it now

- An App Store app can only watch and advise. That is not Pinky.
- The developer-services route works in open code, but it is sideload-only, and Apple has broken its foundation before (iOS 26.4). Keeping it alive in the background is unproven. Pinky's own `internal/driver/ios` is the right base if it is ever built: it already speaks the same services, and it compiles for `ios/arm64`. It would dial the loopback address through LocalDevVPN instead of making its own tunnel. That would be an enthusiast build, not a product.
- Watch two things: whether Apple accepts the HID-dongle app in App Review, and whether Siri gains third-party in-app actions (rumored for iOS 27.5 or 27.6). Either could change this verdict.
- Meanwhile, the iPhone can be the remote control for a Pinky that runs on a computer: approvals, Stop all and the task view on the phone. Nothing in this study blocks that, and it needs no private API.

## Effort

Rough sizes for the Android app above, in weeks of focused work by someone who knows Pinky's code. They assume the safety model and tool behaviour stay as they are.

| Piece | Weeks | Reuses |
| --- | --- | --- |
| Go core as a library in the companion: cgo build with the NDK, JNI bridge, data folder, logs | 1 | The gateway, policy, history and MCP packages as they are (about 25,000 lines without tests; measured to build and run above) |
| `android/local` driver: gestures, global actions, screenshots, app launches, settle, locked screen | 2–3 | The companion client and protocol, tree conversion, `driver.Device` and its optional interfaces |
| Agent loop: model client, tool definitions, prompts, screenshots, cost limits | 1–2 | The MCP tool schemas and results the gateway already writes for agents |
| Phone UI in Kotlin: task view, approval screen, Stop all, settings for rules and agents, history | 3–5 | None; MyGo is desktop only. The client API and its strings (`cmd/pinky/locales`) |
| Same-phone safety: Pinky's own surfaces off limits, the agent waits while a card shows, protected approval screen, biometrics, volume-key Stop | 1–2 | Rules, approvals and Stop all in the gateway |
| Staying alive and setup: restricted settings, vendor battery settings, noticing a force stop and guiding the person back | 1–2 | Pinky's onboarding texts |
| **Accessibility tier, total** | **about 9–15** | |
| Shell tier: Shizuku or Wireless debugging on the phone, `getevent`, the hidden second screen, `WRITE_SECURE_SETTINGS` | 3–4 more | Most of `internal/driver/android` (shell, input, background displays, Take over) |

iOS: no estimate. An enthusiast build on the developer-services route would reuse `internal/driver/ios`, but it needs a prototype on a real iPhone first, to see whether a driver can stay alive while other apps are in front.

## Open risks

Ordered by how much each could stop the Android app; iOS last.

1. **Google closes the door further.** Every route above depends on Google's goodwill.
   - Play already bans autonomous agents built on accessibility.
   - Play Protect blocks sideloaded apps that ask for accessibility in more countries each year.
   - Advanced Protection turns non-tool services off.
   - Developer verification goes worldwide in 2027.
   - Each step so far has been aimed at fraud, but Pinky looks like what they target: an app from a website that reads the screen and taps for someone. The next step could cover all non-tool accessibility services on certified phones, or the shell tier (Android 17 QPR1 already breaks Key Mapper's bridge when USB debugging turns on).
   - Mitigation: register with developer verification early, offer the app through a store as well as the website, and keep the computer path as the one that always works.
2. **The approval protection is untested.** Rule 3 of the safety model rests on reading from AOSP that injected events carry `FLAG_IS_ACCESSIBILITY_EVENT` or device id −1 and that sensitive views are hidden from Pinky's own service. A prototype must check, on stock Android and on HyperOS, One UI and ColorOS:
   - that a `dispatchGesture` tap on the approval screen arrives with the flag and is ignored;
   - that the shell's `input tap` arrives with device id −1;
   - that the approval screen is missing from the companion's tree;
   - that a real finger still works.
   If any fails, rules 1 and 2 in the gateway carry the whole load. The Android prototype checked the first and third on the stock Android 17 emulator, and that adb's `input tap` still works ([Prototype results](#prototype-results-android)). The shell's device id, a real finger and the vendors' versions are still open.
3. **Banking and payment apps.** Some refuse to run while a non-store accessibility service is on, and their screens are secure or sensitive, so Pinky sees black or nothing. Payments, often the reason to want an agent, would mostly be the person's to do. Without the shell tier the service cannot turn itself on per task, so the conflict is constant; with it, some banking apps are reported to refuse to run while Developer options are on (not checked here). Other apps may fight agents too: when ByteDance's Doubao assistant shipped on a ZTE nubia phone, Chinese apps blocked it ([Sixth Tone](https://www.sixthtone.com/news/1019137) [S]).
4. **Vendors kill the service.** A force stop by a vendor cleaner turns the service off until the person turns it on again. The app must notice and guide, and some phones will still be unreliable.
5. **Take over without the shell is unproven.** Without `getevent`, Pinky has an outside-touch overlay or guesses from changes the agent did not cause. A miss lets the agent act against the person's hand; a false alarm stops it at random. This needs a prototype too, or Take over needs the shell tier.
6. **One screen for two.** Without the shell tier the agent works on the screen the person holds: they cannot use the phone meanwhile, and their touch is Take over. Wireless debugging needs trusted Wi-Fi, so on mobile data the hidden second screen is gone.
7. **The cloud model.**
   - Every step sends the screen's text, and sometimes a picture, to the model provider. On a computer that is the person's own agent's business; here it is Pinky's, so Pinky needs clear consent, a privacy policy and a way to keep apps out of what is sent.
   - Cost: a long task with screenshots could cost a few dollars (an estimate, not measured). Pinky needs limits per task and per day.
   - Key handling: the person's own key in the Keystore, or an account Pinky runs, which is a service Pinky does not have today.
8. **Prompt injection is closer.** On a computer the agent's screen is the phone; here the phone is also where the agent's controls live. Rules 1 and 2 must hold for every input path, the shell tier included, and any new verb added to the driver must pass the same checks.
9. **On-device models are not ready.** If people expect Pinky to work offline or privately, the answer for now is no.
10. **Battery and heat.** A model call per step is cheap for the phone, but the screen stays on and the service works hard. Not measured.
11. **iOS.**
    - The developer-services route depends on Apple not closing the loopback VPN and pairing files (it broke them once, in iOS 26.4).
    - Keeping a driver alive while other apps are in front is unproven.
    - App Review rejects it (2.5.1, 2.5.2), sideloading needs re-signing every 7 days or every year, and StikDebug's developer account was terminated.
    - Only the HID dongle has a chance in App Review, and its outcome is unknown.
