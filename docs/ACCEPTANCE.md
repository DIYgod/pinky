# Acceptance checklist

How to check what was built on the night of 2026-11-06. Each item says what to do and what should happen. Details and measurements are in [notes/integration.md](notes/integration.md) and the other notes under [notes/](notes/).

## Before you start

- [ ] **The Pixel (FA6C20303860)** dropped off USB at about 01:45 and never came back: macOS does not see it at all. Check its battery and cable, and whether it is stuck while booting.
- [ ] **Unlock the Mac and keep the screen on.** It was locked most of the night, and macOS does not draw windows then, so the window was checked through MyGo's automation socket and `ui.Tester` only. Items marked *(screen on)* are the ones that could not be watched.
- [ ] **Quit other phone tools** that hold the phones (AndroMeld, iPhone-use; both were stopped last night).

## 1. Build and open the app

```sh
cd ~/Code/Projects/pinky
git pull
make companion && make app      # make app alone if you skip the Android companion
open build/pinky/darwin-arm64/Pinky.app
```

- [ ] The sidebar lists the Mi 10 Pro and your iPhone (and the Pixel once it is back), each with a status line.
- [ ] Selecting a phone shows its **live screen**. Scroll on the phone itself: the mirror follows.
- [ ] Pinky's icon is in the menu bar; closing the window keeps Pinky running there.

## 2. Connect Claude Code

```sh
claude mcp add pinky -- "$HOME/Code/Projects/pinky/build/pinky/darwin-arm64/Pinky.app/Contents/MacOS/Pinky" mcp
mkdir -p ~/.claude/skills/pinky && cp docs/SKILL.md ~/.claude/skills/pinky/SKILL.md
```

In a new Claude Code session, ask: *"Use my Mi 10 Pro: open the Calculator and work out 6 × 7."*

- [ ] Pinky shows a **New agent** card for Claude Code with a choice of what it may do. Click **Approve** *(screen on)*.
- [ ] The **Tasks** panel shows the task and each step in plain words; the mirror outlines what the agent taps.
- [ ] Claude reports 42 and the task ends as **Done**. (Overnight, `claude -p` did this in 37 s.)
- [ ] If Pinky is not running when Claude starts, `pinky mcp` starts it in the menu bar, and the window opens with the New agent card.

## 3. Approvals

Ask: *"On my Mi 10 Pro, open Settings and turn on Airplane mode."*

- [ ] When the agent taps the switch, Pinky asks (rule **Settings changes**) with a screenshot of the target. With the main window in the back, the floating approval panel appears instead *(screen on)*.
- [ ] Click **Deny**: the agent is told no and nothing changes. Unanswered, an approval denies by itself after 60 s.
- [ ] **Settings › Safety**: change "When should agents ask you?" to *every action* and run any task: each step asks. Set it back. The choice is saved in `rules.yaml`.

## 4. Take over, hand back, stop

Start a longer task (*"Scroll through Settings on my Mi and list every top-level section"*).

- [ ] Click or drag on the mirror: the agent pauses and the phone shows **You're in control** *(screen on)*.
- [ ] Touch the Mi's screen with your finger: the same happens (the driver watches for touches on the phone; untested with a real finger).
- [ ] Click **Hand back**: the agent continues.
- [ ] Click **Stop**, or press **⌥⌘.** (Stop all): the agent's next call is refused.

## 5. iPhone

Ask: *"On my iPhone, open the Calculator and work out 12 × 12."* (Overnight `claude -p` got 144 in 43 s.)

- [ ] The agent connects, opens Calculator, taps the keys by their labels (including × and =) and reads the result.
- Known limits: the first read after an app opens can take 2–3 s; elements scrolled off screen are not listed; home screen widgets are left out; Calculator's display reads as `StandardInputView;value:144`.

## 6. History and Settings

- [ ] **Activity** lists the tasks with their steps and before/after screenshots; search finds a task by its words.
- [ ] **Settings › Agents** lists Claude Code with what it may do.
- [ ] **Settings › Safety › Blocked apps** lists the banking and password apps on your phones that the rules block. Add an app by name; the agent then gets "blocked app" for it.

## 7. Without a phone

```sh
open build/pinky/darwin-arm64/Pinky.app --args --fake    # quit the real Pinky first
```

- [ ] Two fake phones appear, each with a drawn screen in the mirror. Ask Claude Code to *"reorder last week's lunch (the chicken rice bowl) on the fake Pixel"*: it reaches a **Pay** approval. (Overnight Claude found the order, reordered it and stopped at the payment when nobody answered.)

## Decisions waiting for you

- **Typing on an iPhone with Pinyin active.** Letters go through the phone's input method, so with Pinyin on they end up as an uncommitted composition. Two fixes: paste through the clipboard (replaces your clipboard), or switch the keyboard to English for the task. Android avoids it: it types through the companion, or pastes.
- **Installing the companion on the Mi 10 Pro.** The Mi runs the 01:43 build, which works. A newer build makes Google Play Protect ask "Send app for a security check?" on the phone, which nobody could answer overnight. While you are at the phone: `build/pinky/darwin-arm64/Pinky.app/Contents/MacOS/Pinky companion install "Mi 10 Pro"`.
- **How to ship the companion APK**: bundled in Pinky.app (done by `make app`), or downloaded from GitHub releases.
- **Keeping Android phones awake while plugged in** (scrcpy's stay-awake changes a setting while Pinky runs): off for now.
- **App Nap vs sleep**: Pinky keeps App Nap off but lets the Mac idle-sleep (`ui/appnap_darwin.go`). Keeping the Mac awake only while a task runs is a one-line change.

## Not built yet

Unlock from the computer (saved PIN with Touch ID), screen recordings, Streamable HTTP for remote agents, `phone_files`, history export and retention limits, Wi-Fi pairing (Add phone works over USB; the QR code is a placeholder), Background display on Android (the driver has it, the app does not use it), Windows and Linux runs (they compile only).
