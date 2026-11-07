# Acceptance checklist

How to check what was built in rounds one to three (2026-11-06 and 07). Each item says what to do and what should happen; *(checked)* marks what was already checked here, and how. Details are in [notes/integration.md](notes/integration.md) and the other notes under [notes/](notes/).

## Before you start

- [x] **The Pixel (FA6C20303860)** came back on USB around noon on the 7th. The Android driver's real-phone tests pass on it (reading the screen, screenshots, the Calculator, typing, the video). During a Claude Code run on it, Pinky saw your finger on its screen and paused the agent, as designed.
- [ ] **The Mi 10 Pro keeps its screen on while charging**: Developer options › Stay awake is on (`stay_on_while_plugged_in=15`). Pinky did not set it; one of the other tools on the phone may have. Turn it off if you share the burn-in worry: Pinky wakes the phone when a step needs it and, on Android 13 and later, works on a background display while the phone stays off and locked.
- [ ] **Quit other phone tools** that hold the phones (AndroMeld, iPhone-use).
- [ ] **Unlock the Mac and keep the screen on**: a locked Mac does not draw windows.

## 1. Build and open the app

```sh
cd ~/Code/Projects/pinky
git pull
make companion && make app      # builds FFmpeg the first time (about a minute)
open build/pinky/darwin-arm64/Pinky.app
```

- [ ] The sidebar lists your phones with a status line; selecting one shows its live screen.
- [ ] Pinky's icon is in the menu bar; closing the window keeps Pinky running there. `Pinky.app/Contents/MacOS/Pinky quit` (or the menu) quits it.
- *(checked)* The app carries its own FFmpeg (`Contents/Frameworks`) and the companion APK (`Contents/Resources`), and so does the `.dmg`. `make release` signs it with the Developer ID and notarizes it, so the `.dmg` opens on other Macs.

## 2. Connect Claude Code

```sh
claude mcp add pinky -- "$HOME/Code/Projects/pinky/build/pinky/darwin-arm64/Pinky.app/Contents/MacOS/Pinky" mcp
mkdir -p ~/.claude/skills/pinky && cp docs/SKILL.md ~/.claude/skills/pinky/SKILL.md
```

Ask a new Claude Code session: *"Use my Mi 10 Pro: open the Calculator and work out 6 × 7."*

- [ ] Pinky shows a **New agent** card with a choice of what Claude Code may do; **Approve** lets it connect. *(checked with the screen on, on an emulator; with no phone added it now shows in the floating panel instead of nowhere)*
- [ ] The Tasks panel shows each step in plain words; the mirror outlines what the agent taps.
- [ ] Claude reports 42 and the task ends as **Done** (37 s in round two).

## 3. Approvals, take over, stop *(checked with the screen on, on an emulator)*

Ask: *"On my Mi 10 Pro, open Settings and turn on Airplane mode."*

- [ ] Pinky asks before the switch (rule **Settings changes**) with a screenshot of it; **Deny** tells the agent no. With the window in the back, the floating panel at the top right asks instead, with a countdown.
- [ ] Clicking or dragging on the mirror takes over: **You're in control**; the agent is paused. **Hand back** lets it go on; **Stop** or **⌥⌘.** ends its task.
- [ ] Touch the phone itself during a task: the agent pauses too (the driver watches the touchscreen; Pinky's own input never counts).

## 4. Android: companion, background work, unlock, files, Wi-Fi

- [x] **Companion app.** Updated on the Mi on the 7th with `Pinky companion install` (build 130, replacing round one's build signed with another key); its service is on, next to Mika's. *(checked)* Claude Code then worked out 8 × 9 in the Mi's Calculator on a background display in 38 s ($0.21, about 1.4 s a tap), and the Calculator task you had open went back to the main screen at the end.
- [ ] **Background work** (Android 13 and later, on by default; Settings › Phones › Work in the background): during a task the mirror shows the agent's own display, with a switch to the phone's screen; the phone's screen can stay off and locked. *(checked on the Android 16 emulator: Claude Code used the Clock while the phone was off and locked, with screenshots from the display's video, and the task was recorded)*
- [x] **Unlock from the computer** *(checked on the 7th with the Mi 10 Pro, over Wi-Fi)*: a locked Mi's page offers Unlock with Touch ID once its PIN is saved. Phones with an under-display fingerprint sensor first show a fingerprint screen; Pinky taps past it to the PIN pad. A PIN the phone refuses is not tried again until you unlock the phone yourself or change the PIN.
- [ ] **Files.** Ask: *"Put a text file named hello.txt with 'hi' in my Mi's Pinky folder."* The agent uses `phone_files`; the file appears in the phone's `Pinky/` folder (and in `~/Library/Application Support/Pinky/files/<phone>/`).
- [x] **Wi-Fi pairing** *(checked on the 7th with the Mi 10 Pro)*: Add phone › Android › Or pair over Wi-Fi, then on the phone Developer options › Wireless debugging › Pair device with QR code. A phone on both USB and Wi-Fi shows once, by USB while the cable is in, and keeps its id (and saved PIN, settings, history) on Wi-Fi. If pairing hangs, adb may have stopped seeing the network after running for days: quit other Android tools and run `adb kill-server` (the pairing step says so after 30 s).

## 5. iPhone

- [x] Ask: *"On my iPhone, open Settings, search for Wi-Fi and tell me the first result."* Typing pastes the text and puts your clipboard back, so it works with Pinyin active, and with Chinese and emoji. *(checked on the 7th: Claude Code searched with Pinyin active, read "Wi-Fi" as the first result, went home and released the phone, in 45 s for $0.25)*
- [x] **Unlock from the computer** *(checked on the 7th)*: a locked iPhone's page offers to save its passcode (the card appears as soon as the phone locks), then Unlock with Touch ID. With the iPhone locked, Claude Code connected, asked for help with `phone_handoff`, you clicked Unlock with Touch ID, the iPhone unlocked in 4 s, and Claude went on to work out 12 × 12 = 144 (76 s, $0.27).
- Known limits: iPhones over Wi-Fi stay off (3–5 s screenshots, lost video); `phone_files` is Android only.

## 6. History, recordings, export

- [ ] **Activity** lists tasks with their steps; **Watch recording** plays a task's screen with a mark per step to jump to. Nothing of a blocked app, a lock screen or your own use is recorded.
- [ ] **⋯ › Export…** and **Export all** write an HTML report and JSON into a folder on the Desktop.
- [ ] Settings › Privacy: *Keep activity for* (30 days by default) deletes older tasks, screenshots and recordings; *Record the screen* turns recording off.

## 7. Remote agents *(checked with fake phones)*

- [ ] Settings › Agents › Remote agents: turn on *Let agents connect over HTTP*, copy the `claude mcp add --transport http …` command and run it. The agent connects to `http://127.0.0.1:7465/mcp` with the agent token; it asks for a first approval like any agent, named "… (remote)".

## 8. Windows and Linux *(checked in CI)*

- GitHub Actions builds, vets and tests on Linux and Windows and builds on macOS, and runs a smoke test on each (the fake gateway, `pinky devices`, an MCP task, `pinky quit`); on Linux it also opens the window under Xvfb. PIN storage uses the Credential Manager with Windows Hello on Windows and the Secret Service on Linux, neither tried by a person.

## 9. Without a phone

```sh
open build/pinky/darwin-arm64/Pinky.app --args --fake    # quit the real Pinky first
```

- [ ] Two fake phones with drawn screens. Ask Claude Code to *"reorder last week's lunch (the chicken rice bowl) on the fake Pixel"*: it reaches a **Pay** approval.

## 10. Updates

- [x] **Pinky updates itself** *(checked on the 7th against a local feed, with two builds signed with the Developer ID in a temporary folder: `TestUpdateEndToEnd`)*: the older Pinky, left alone in the menu bar, found the newer one within a minute, downloaded and checked it, quit, and the newer one started in its place, removed the old bundle and showed **Pinky was updated** with What's new.
- [ ] **With the real feed**, once the release pipeline publishes `latest.json`: in an installed Pinky.app the sidebar shows **Pinky … is available**; **What's new** shows the notes; **Update now** restarts into the new version; **What's new** after the restart lists the changes since the version you had. Settings › Advanced › **Check now** says *Up to date* afterwards.
- [ ] **Not while you work**: during an agent task, an approval or with the window in use, nothing restarts; **Update now** then asks first.
- [x] **Pinky Companion** *(checked on the Android 10 and 16 emulators)*: a phone with an older companion is updated while it is free, its accessibility service stays as it was (Android keeps it on across the update), and a toast and What's new say so. On phones where Play Protect checks USB installs it waits for your click instead, since the phone would ask.

## Decisions

- **Typing on an iPhone** (yours to decide, decided here): paste through the iPhone's pasteboard and put the clipboard back; typing by keys only as a fallback.
- **The companion app** ships inside Pinky.app and installs when you click Install. With Update automatically on, Pinky updates an installed companion by itself while the phone is free (only Pinky's own package, and not where the phone would ask); it never installs anything else.
- **Updates** are automatic by default (Settings › Advanced), only while nothing is going on, and the old version comes back if the new one does not start.
- **No keeping phones awake**: Pinky wakes a phone before a step and lets its own timeout and lock apply; Android 13+ works on a background display by default so the screen can stay off.
- **The Mac** stays awake while an agent task runs, and only then.
- **Remote agents** are off by default and listen on this computer only unless you change the address.

## Not done, and why

- ~~Notarization~~ done on the 7th: `make release` signed Pinky.app with the NATURAL SELECTION LABS Developer ID (hardened runtime, timestamp; FFmpeg loads from the app) and Apple notarized the `.dmg` (accepted, stapled; Gatekeeper: "Notarized Developer ID"). Run it again for each release; the `pinky-notary` profile is in your keychain.
- **An Intel or universal build**: only Apple silicon is built.
- **Background displays on Android 10**: apps run there, but nothing on them can be read before Android 11, so Pinky does not offer background work on the Pixel. Android 11 and 12 are not verified and default to the main screen.
- **Things that need a person**: Touch ID and Windows Hello prompts, a real Wi-Fi pairing, unlocking the iPhone, a finger on the Mi during a task.
