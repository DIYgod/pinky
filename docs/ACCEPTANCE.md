# Acceptance checklist

How to check what was built in rounds one to three (2026-11-06 and 07). Each item says what to do and what should happen; *(checked)* marks what was already checked here, and how. Details are in [notes/integration.md](notes/integration.md) and the other notes under [notes/](notes/).

## Before you start

- [ ] **The Pixel (FA6C20303860)** came back on USB around noon on the 7th. The Android driver's real-phone tests pass on it (reading the screen, screenshots, the Calculator, typing, the video). During a Claude Code run on it, Pinky saw a finger on its screen and paused the agent: if that was you, it worked as designed.
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
- *(checked)* The app carries its own FFmpeg (`Contents/Frameworks`) and the companion APK (`Contents/Resources`), and so does the `.dmg`.

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

- [ ] **Companion app.** The Mi's page shows *Update Pinky Companion* (the Mi has round one's build, signed with another key). Click it with the phone at hand: the phone may ask to allow the install (Play Protect). It installs, replaces the old build and turns its service on for good; Settings › Phones then says *On*.
- [ ] **Background work** (Android 13 and later, on by default; Settings › Phones › Work in the background): during a task the mirror shows the agent's own display, with a switch to the phone's screen; the phone's screen can stay off and locked. *(checked on the Android 16 emulator: Claude Code used the Clock while the phone was off and locked, with screenshots from the display's video, and the task was recorded)*
- [ ] **Unlock from the computer.** Settings › Phones › the Mi › Save PIN…, then lock the phone and start a task: the card offers **Unlock with Touch ID**. A wrong PIN is tried once, never again. *(checked through the gateway on the emulator with a test PIN; Touch ID itself needs you)*
- [ ] **Files.** Ask: *"Put a text file named hello.txt with 'hi' in my Mi's Pinky folder."* The agent uses `phone_files`; the file appears in the phone's `Pinky/` folder (and in `~/Library/Application Support/Pinky/files/<phone>/`).
- [ ] **Wi-Fi pairing.** Add phone › Android › Pair over Wi-Fi: scan the QR code on the phone in Developer options › Wireless debugging › Pair device with QR code, or use *Pair with a code instead*. The phone then shows with *Wi-Fi*. *(checked against fake adb servers only)*

## 5. iPhone

- [ ] Ask: *"On my iPhone, open Settings, search for Wi-Fi and tell me the first result."* Typing pastes the text and puts your clipboard back, so it works with Pinyin active, and with Chinese and emoji. *(checked by the iOS driver's tests on your iPhone)*
- [ ] **Unlock from the computer**: Save PIN… for the iPhone, lock it, then Unlock with Touch ID. Never tried on the phone (it needs your passcode); `docs/notes/ios.md` has the steps with `iosctl unlock`.
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

## Decisions

- **Typing on an iPhone** (yours to decide, decided here): paste through the iPhone's pasteboard and put the clipboard back; typing by keys only as a fallback.
- **The companion app** ships inside Pinky.app and installs when you click Install; Pinky never installs anything by itself.
- **No keeping phones awake**: Pinky wakes a phone before a step and lets its own timeout and lock apply; Android 13+ works on a background display by default so the screen can stay off.
- **The Mac** stays awake while an agent task runs, and only then.
- **Remote agents** are off by default and listen on this computer only unless you change the address.

## Not done, and why

- **Developer ID signing and notarization**: the app is signed ad hoc, which runs on this Mac only. You have a Developer ID certificate: `make app MACOS_SIGN="Developer ID Application: …"` signs with it; notarizing needs your Apple account (`xcrun notarytool`).
- **An Intel or universal build**: only Apple silicon is built.
- **Background displays on Android 10**: apps run there, but nothing on them can be read before Android 11, so Pinky does not offer background work on the Pixel. Android 11 and 12 are not verified and default to the main screen.
- **Things that need a person**: Touch ID and Windows Hello prompts, a real Wi-Fi pairing, unlocking the iPhone, a finger on the Mi during a task.
