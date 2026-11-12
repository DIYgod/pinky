# Pinky for Android

Pinky has one Android app, named **Pinky**. It does two jobs:

- **Desktop mode.** Pinky on a computer drives the phone over adb, and the app's accessibility service reads the screen for it. This is what Pinky Companion did, and it works as before.
- **On-phone mode.** Pinky runs agent tasks on the phone by itself, with no computer: the person types a task, watches, approves, takes over and stops from the phone.

It grew out of the Android prototype in [standalone-phone.md](standalone-phone.md#prototype-results-android), which follows the architecture recommended there ([Android: one app, the companion grown up](standalone-phone.md#android-one-app-the-companion-grown-up)). This note records the decisions and the state of the work, milestone by milestone.

## Names

| What | Name | Why |
| --- | --- | --- |
| Package | `app.pinky.companion` (release), `app.pinky.companion.dev` (debug) | Kept, so installs update in place and desktop Pinky's install, update and checkup keep working. A package name cannot change without a new app. |
| App and service label | Pinky, Pinky (dev) | In every language. The person sees it under Accessibility, in the launcher and in Android's dialogs. |
| Name in desktop Pinky | Pinky for Android (「Android 版 Pinky」, 「Android 版 Pinky」) | Desktop Pinky's cards, checkup and settings speak of the app on the phone; "Install Pinky" there would read as Pinky installing itself. Steps that name the app in the phone's own settings say Pinky, as the phone does. |
| APK file | `pinky-android.apk` | In the desktop bundle and as a release asset. Was `pinky-companion.apk`. |
| Go package, sockets, environment variables, `assets/pinky-companion.json`, remote temp files | unchanged | Not seen by people. Renaming them would only break running installs and scripts. |

Desktop Pinky recognises the service under both labels (`companion.App.OldLabel`): the phone may still have the old build, labelled Pinky Companion, until desktop Pinky updates it.

The prototype's own app, `app.pinky.standalone` (`android/standalone`), is gone. Its sources live in `android/companion` now, its control and core socket names follow the companion's (`@pinky_core`, `@pinky_core_dev`), and `scripts/standalone.sh` became `scripts/androidcore.sh`.

## Milestone 1: one app, desktop mode unchanged

### What is in which build

`res/values/bools.xml` holds `on_phone_ui`: false in release builds, true in debug builds. It decides everything on-phone mode adds:

| | Release (ships) | Debug |
| --- | --- | --- |
| Launcher entries | Pinky's debug page only | the debug page and Pinky tasks (`TaskActivity`) |
| Service capabilities | retrieve windows and perform gestures | also `canTakeScreenshot` |
| `gesture` and `global` | served: Pinky on a computer taps through them on Xiaomi phones' background displays (GitHub issue #48) | served |
| `screenshot`, `launch`, `apps` and `on_phone` | `unknown_command` / `unsupported` | served |
| `INTERNET` permission | no | yes (`src/debug/AndroidManifest.xml`) |
| Go core (`libpinkycore.so`) | no | yes, built by `scripts/androidcore.sh` |
| Size | 142 KB | 24 MB |

So on-phone mode stays hidden in releases until Milestone 2 makes it complete. A release build asks Android for what Pinky Companion asked for plus performing gestures, which desktop mode needs on Xiaomi's MIUI and HyperOS: they drop the taps adb sends to a background display, so Pinky on the computer taps, swipes and presses back there through the service instead ([android.md](android.md), GitHub issue #48). Android's consent dialog for the service then also says it can perform gestures, and the service's description says why.

### On-phone mode

On-phone mode is a flag the service keeps in its own preferences (`mode`, `on_phone`):

- Only the app itself turns it on or off, with the `on_phone` command over the companion socket. A connection from another uid (adb, desktop Pinky) gets `forbidden`. Opening the task screen turns it on: the screen finds the core down and asks the service to start it.
- While it is on, the service keeps itself on (the idle watchdog's limit is 0), runs the Go core and draws Pinky's pill and approval card (`Overlays`).
- It survives a restart of the process or the phone. Turning the service off, in Android's settings or by desktop Pinky, turns it off too, as it clears the service's config.
- `ping` reports it (`on_phone`), so desktop Pinky can tell a phone that runs its own tasks.
- The idle watchdog counts only connections from other uids. The core's own connection and Pinky's screens never keep the service on in desktop mode; in on-phone mode it stays on anyway.

The Go core is loaded once per process and cannot be unloaded. Turning on-phone mode off puts the overlays away; the core stays idle until the process ends.

### Pinky's own screens

`policy.IsPinkyScreen` replaces `policy.StandaloneApp` and `IsStandalone`. It covers `app.pinky.companion` and `app.pinky.companion.dev` (and their clones), and the gateway blocks every action on them for every agent, from a computer or on the phone, whatever the task allows. Before, the gateway blocked only the prototype's package, so this is a change for desktop agents too: they can no longer open Pinky Companion's debug page. Nothing an agent does needs it.

The other two walls stay as the prototype built them: the service refuses gestures on Pinky's windows and overlays (`OwnUI`), and Pinky's views are `accessibilityDataSensitive` and `filterTouchesWhenObscured`.

### Checked

On 2026-11-11, with the debug build unless noted:

| Where | What | Result |
| --- | --- | --- |
| `make check` | build, vet, tests | pass |
| Android 17 emulator | `TestDeviceCompanion` (desktop mode), debug build | pass |
| Android 17 emulator | the same with a release build signed with a throwaway key | pass; one launcher entry; `TaskActivity` disabled |
| Android 17 emulator | a task from the task screen with `fakemodel -pinky "Pinky (dev)"`, rules on commits | Settings opened; two "Settings changes" waited on the card and were allowed; opening Pinky returned `blocked_by_policy`; done |
| Android 17 emulator | on-phone mode kept the service on for over 10 minutes without a computer | yes |
| Mi 10 Pro, LineageOS (Android 17) | `TestDeviceCompanion`; the driver's `TestDeviceObserveAndScreenshot`, `TestDeviceCalculator`, `TestDeviceStreamAndSettle` and `TestDeviceCheckup` with `PINKY_COMPANION_APK` set to the debug build | pass; every checkup item passed |
| Pixel, Android 10 | `TestDeviceCompanion` | pass on the second run; the first failed once on Settings' slow cold start right after the install |

The release build signed with Pinky's real key was not installed on a phone: only the release workflow has that key. Its contents match the throwaway-signed build.

## Two modes on one phone

Never two agents at once, and the person always sees who is acting. Milestone 1 needs nothing for this: on-phone mode exists only in debug builds. Milestone 2 adds it before on-phone mode ships:

- **A claim per connection.** A new companion command, `claim` (`{"cmd":"claim","by":"computer"|"phone","task":"…"}`), gives the phone to one client and answers `busy` with the holder's `by` and `task` to anyone else. A claim ends with its connection, so a crashed or unplugged client frees the phone by itself.
- **Desktop Pinky** claims when an agent starts working on the phone, on the connection that already holds its event subscription. If the phone is busy, the task does not start, and Pinky shows the phone as busy: "Pinky on the phone is running a task". An older app without `claim` answers `unknown_command`, and desktop Pinky goes on as today.
- **Pinky on the phone** claims when a task starts. If a computer holds the phone, the task screen says so and Start waits; the core refuses `start` too. An older desktop Pinky that does not claim still holds an event subscription while its agent works; the app counts that as a computer's claim.
- **Desktop Pinky leaves the service on** when `ping` says `on_phone`: the person turned it on for Pinky on the phone, and turning it off would also turn on-phone mode off. `Enable` already leaves a service that is on alone, but two backstops in `internal/driver/android/companion.go` force it off: `quietLocked`, for a service an older Pinky left on for good, and `disableLocked` when the service was on before the agent started. Today they spare it only because the core's connection counts as another client (`companionPeers`); Milestone 2 makes both check `on_phone`. Desktop Pinky still turns the service off when it turned it on itself and on-phone mode is off.
- **Who is acting.** On-phone mode's pill shows while either side works: "Pinky on your computer is working" with Stop for a computer's agent (Stop releases the claim and the computer's agent gets `stopped`), and the task's own line for a task on the phone. In desktop mode alone nothing changes on the phone: Pinky on the computer shows the person what happens.

## Milestone 2 so far

On-phone mode is still in debug builds only (`on_phone_ui`), and everything below is in them.

### Pinky's screens on the phone

Both run in the `:ui` process, are closed to agents as the debug page is (`policy.IsPinkyScreen`, `OwnUI`, sensitive content), and speak English, Simplified Chinese and Japanese (`res/values*/phone_strings.xml`).

- **The task screen** (`TaskActivity`) takes the person through setup first: turning the service on, with the hint for Android 13+'s restricted settings and a link to App info, then a key and a model. If the core cannot start, it says why (`core_error` in the service's reply to `on_phone`). Then it takes a task, shows it while it runs (its steps, cost and time, what it waits for, Stop) and how it ended, and lists its steps under Activity. Start's errors are named by the core's codes (`no_device`, `no_key`, `no_model`, `no_task`, `busy`). When the feed offers an update, a card offers it (Download, Later).
- **Settings** (`SettingsActivity`), as desktop Pinky has them in Settings › Ask Pinky and Approvals: the service (Anthropic by default, or OpenAI-compatible), its key, model and address, each kept per service (`TaskSettings`); a task's limits on steps, time and cost (tokens for a model without prices); when Pinky asks (the core's `policy.Preset`, so it changes only while the core runs); the service and App info in Android's settings; the version and Check for updates.
- **Keys** (`Secrets`) are encrypted with an AES key that lives in the Android Keystore and never leaves it. Only the ciphertext is in the app's files, which are not backed up, and nothing logs a key or its length.
- **The look** (`Look`) is desktop Pinky's palette and type, light and dark (`res/values/colors.xml`, `res/values-night`), built in code.
- **Words** (`Words`) puts amounts, times, a task's end and its steps as desktop Pinky words them (`ui/format.go`, `ui/extras.go`): "You denied: open Settings" with the person's mark, "Waiting for you: tap “Pay”", a step stopped while it waited as "Not done: stopped". The gateway's own words (steps, card titles, rule names) stay English: desktop Pinky translates them (`ui/gwtext.go`), the phone does not yet.
- **The pill** reads the task's last step as Activity does, so an answered approval reads "You answered: …", not "Waiting for you". Its line gives way to its button: a long line no longer pushes Stop or Close onto a second line.
- **Updates** (`UpdateCheck`): the task screen checks the feed at most every 12 hours and offers a higher `version_code`; Download opens the APK's address in the browser. The check is off in debug builds and in builds without a version, and the feed does not publish the Android asset yet (below).

### Checked

On 2026-11-12, on the Android 17 emulator with the debug build and `fakemodel -script loop`:

| What | Result |
| --- | --- |
| `make check`, `go vet` for Windows and Linux, `CGO_ENABLED=0 go build`, `lintVitalRelease` | pass |
| Settings: an address without `http://` or `https://` | the error shows under the field and the dialog stays; typing clears it |
| Settings: Remove the API key, and an empty address | "API key removed", and the row offers Add; the address reads Default |
| A task with rules on commits | 27 steps, done; the pill read "Working · " and the last step, then "Done. …" with Close on the same line; Activity marked each step ✓ |
| Rules on every action: Deny on the card | "You denied: open Settings", with the person's mark |
| Allow on the card | the pill read "Working · You answered: press back"; the step settled as "Pressed back" ✓ |
| Stop on the pill while a step waited | the step read "Press back" and "Not done: stopped", marked –; the pill read "Stopped" with Close |
| Stop on the task screen; tapping the pill | stops the task; opens Pinky |
| Simplified Chinese and Japanese (`cmd locale set-app-locales`) | the task screen, Settings, the card, the pill and the steps' wording; the gateway's words stay English |

Not checked: the new screens on the Pixel and the Mi 10 Pro, the dark theme (switching it is a phone setting), an update from the feed, and a real model.

## Next

### Milestone 2: tasks on the phone, complete

- **The core in release builds.** On-phone mode needs `INTERNET`, gestures, screenshots and the core in the release build, and `on_phone_ui` turns true. The core is arm64 only. Android refuses to install an APK whose native code does not match the phone (`INSTALL_FAILED_NO_MATCHING_ABIS`) only when the APK has no code for the phone's ABI at all, so an arm64-only APK does not install on 32-bit-only phones (some Android Go phones) or x86 emulators. Two APKs then: `pinky-android.apk` with the core, and a small one without it for phones that cannot run it, which desktop Pinky picks by `ro.product.cpu.abilist`. To decide by measuring: the library is stored uncompressed today (21.1 MB; Android maps it straight from the APK), and compressed it is 7.8 MB (`gzip -9`). `useLegacyPackaging = true` would make the download about 13 MB smaller, but Android then extracts the library on install, so it takes space twice on the phone.
- **Setup screens in Pinky's words and look:** the task screen and Settings are in (above). Still to come: a history of past tasks, and the rest of onboarding: vendor battery settings and staying alive.
- **Updates without a computer:** the app checks Pinky's release feed, `latest.json` ([updates.md](updates.md#the-feed)), and offers the new APK; desktop Pinky keeps updating it as today where there is a computer. The feed gains an asset `android-arm64` for `pinky-android.apk` with the usual `url`, `sha256` and `size`, plus `version_code`: the APK changes only when `android/companion` does, so the app offers an update only for a higher `version_code`. Desktop Pinky ignores the new key, as it reads only its own `GOOS-GOARCH`.
- **Distribution:** `pinky-android.apk` as a release asset of its own and a download link in the README.
- **Two modes on one phone,** as above.
- **Tests:** the prototype's `fakemodel` stands in for the model. A run with a real model needs the owner, who types the key on the phone.

### Milestone 3: take over and polish

- The person's touch pauses the agent (Take over), and `FLAG_SECURE` on the approval card and Pinky's screens.
- Android 10 (the Pixel): no `takeScreenshot` (API 30) and no `accessibilityDataSensitive` (API 34). On-phone mode should say what it cannot do there instead of failing.
- Battery over a long task, on a real phone.
- A shell tier (Shizuku or Wireless debugging) is not in the first version. The local driver keeps room for it: actions go through the service's commands, and a shell could serve the same ones.

## Building and testing

- `make check` builds and tests the Go side, the companion included.
- `android/companion/gradlew -p android/companion assembleRelease` builds the release APK (signed when `PINKY_COMPANION_KEYSTORE` is set).
- `scripts/androidcore.sh` builds the core for arm64 with the NDK and then the debug APK; `scripts/androidcore.sh core` builds only the core.
- `cmd/corectl` drives the core's control socket from adb as the app's uid (`run-as app.pinky.companion.dev`); `cmd/fakemodel` stands in for the model (`-pinky "Pinky (dev)"` for the debug build's label); `cmd/localbench -dev` times the debug build's commands.
- Device tests: `scripts/devlock.sh <id> env PINKY_DEVICE=<serial> go test -tags device …`, following the phone rules in [WORKING.md](../WORKING.md).
- `adb shell cmd locale set-app-locales app.pinky.companion.dev --locales zh-Hans` (or `ja`) shows Pinky's screens in another language without changing the phone's; the same command without `--locales` goes back.
