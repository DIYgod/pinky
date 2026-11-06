# Acceptance checklist

How to check what was built on the night of 2026-11-06. Each item says what to do and what should happen. Details and measurements are in the notes under [docs/notes](notes/).

## Before you start

- [ ] **The Pixel (FA6C20303860)** dropped off USB at about 01:45 and never came back: macOS does not see it at all. Check its battery and cable, and whether it is stuck while booting.
- [ ] **The Mac's screen** was locked for most of the night, so the app's windows were checked through MyGo's automation socket only. Watch the app with the screen on; the items marked *(screen on)* could not be checked overnight.
- [ ] **Quit other phone tools** that hold the phones (AndroMeld, iPhone-use; both were stopped last night).

## 1. Build and open the app

```sh
cd ~/Code/Projects/pinky
git pull
make app
open build/pinky/darwin-arm64/Pinky.app
```

- [ ] The window shows **Phones** in the sidebar with the Mi 10 Pro and your iPhone (and the Pixel when it is back), each with a status line.
- [ ] Selecting a phone shows its **live screen** in the middle. Scroll or open something on the phone itself: the mirror follows within a fraction of a second.
- [ ] The menu bar has Pinky's icon; closing the window keeps Pinky running there.

## 2. Connect Claude Code

```sh
claude mcp add pinky -- "$HOME/Code/Projects/pinky/build/pinky/darwin-arm64/Pinky.app/Contents/MacOS/Pinky" mcp
mkdir -p ~/.claude/skills/pinky && cp docs/SKILL.md ~/.claude/skills/pinky/SKILL.md
```

Start a new Claude Code session and ask: *"Use my Mi 10 Pro: open the Calculator and work out 6 × 7."*

- [ ] Pinky shows **Needs you: Claude Code wants to connect**. Click **Allow** *(screen on)*.
- [ ] The **Tasks** panel shows the task and each step in plain words; the mirror outlines what the agent taps.
- [ ] Claude reports 42, and the task ends as **Done**.

## 3. Approvals

Ask: *"On my Mi 10 Pro, open Settings and turn on Airplane mode."*

- [ ] When the agent taps the switch, Pinky asks (rule **Settings changes**) with a screenshot of the target. The floating approval panel appears when the main window is not in front *(screen on)*.
- [ ] Click **Deny**. The agent is told no and nothing changes on the phone.
- [ ] With no answer, the approval denies by itself after 60 seconds.

## 4. Take over, hand back, stop

Start any longer task (for example *"Scroll through Settings on my Mi and list every top-level section"*).

- [ ] Click or drag on the mirror: the agent pauses and the phone shows **You're in control** *(screen on)*.
- [ ] Click **Hand back**: the agent continues.
- [ ] Click **Stop** on the task, or press **⌥⌘.** (Stop all): the agent's next call is refused.

## 5. iPhone

Ask: *"On my iPhone, open Settings › General › About and tell me the iOS version."*

- [ ] The agent connects, opens Settings and reads the screen.
- [ ] Known limit: elements without text (icons, Calculator's operators) have no position yet, so the agent may need to tap by coordinates or fail on them. See [notes/ios.md](notes/ios.md).

## 6. History

- [ ] **Activity** lists the tasks with their steps and before/after screenshots; search finds a task by words in it.

## 7. Without a phone

```sh
open build/pinky/darwin-arm64/Pinky.app --args --fake    # quit the real Pinky first
```

- [ ] Two fake phones appear. Ask Claude Code to *"reorder last week's lunch on the fake Pixel"*: it should reach a **Pay $12.80?** approval.

## Decisions waiting for you

- **Typing on an iPhone with Pinyin active.** Letters go through the phone's input method, so with Pinyin on they end up as an uncommitted composition. Two fixes: paste through the clipboard (replaces your clipboard), or switch the keyboard to English for the task. Android already pastes in that case.
- **Installing the companion on the Mi 10 Pro.** A new build makes Google Play Protect ask "Send app for a security check?" on the phone; nobody answered it overnight. Install the current build while you are there (`make companion`, then the install command in [notes/companion.md](notes/companion.md)).
- **How to ship the companion APK**: bundled in Pinky.app (done for local builds), or downloaded from GitHub releases.
- **Keeping Android phones awake while plugged in** (scrcpy's stay-awake changes a setting while Pinky runs); off for now.

## Not built yet

Unlock from the computer (saved PIN with Touch ID), recordings, Streamable HTTP for remote agents, `phone_files`, history export and retention limits, Windows and Linux runs (they compile only), wireless pairing.
