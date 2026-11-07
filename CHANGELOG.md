# Changelog

What changed for people in each release of Pinky, newest first. A version is the release's date in Asia/Shanghai and that day's count: 2026.1107.2 is the second release of 7 November 2026. Notes wait under "Unreleased" until the next release takes them; see [docs/RELEASING.md](docs/RELEASING.md).

## 2026.1107.2 (2026-11-07)

- "Always allow" on an approval now lasts: in that app, Pinky stops asking about steps like that one, in later tasks and after a restart. Settings › Safety › Always allowed lists them, and any of them can be made to ask again. It never lifts a block, read-only mode or what an agent may do, and "Ask before every action" still asks about everything.
- "Always allow" is offered only where Pinky can keep it: not for an agent's own questions, copying files, or opening apps and links.
- Settings › Safety has a rules editor: add, change, reorder and remove your own rules (apps, categories, actions, button text; Ask, Block or Allow), with Pinky's built-in rules shown alongside. Removing or loosening a rule that blocks asks you first.
- Buttons that show only an icon, such as a paper plane that sends or a trash can that deletes, now ask before they commit, like buttons with words.

## 2026.1107.1 (2026-11-07)

- The first release of Pinky: AI agents such as Claude Code, Codex and Cursor operate your iPhone or Android phone over MCP while you watch on your computer, approve risky steps, take over and look back at what happened.
- Android phones over USB or Wi-Fi. The companion app, which Pinky offers to install, reads the screen in about 70 ms and types any language; on Android 13 and later agents work on a background display while the phone stays locked.
- iPhones on iOS 27 or later over USB, with nothing to install on the phone.
- Unlock a locked phone from the computer with Touch ID (Windows Hello on Windows); agents never see the PIN.
- History of every task, with recordings and export.
- Pinky for macOS 12 or later (Apple silicon and Intel), Windows and Linux; on macOS, `brew install --cask diygod/tap/pinky` also installs the `pinky` command.
- Pinky keeps itself up to date: it tells you when a new version is out and what changed, installs it by itself when you're not using it (Settings › Advanced › Update automatically), and shows what's new afterwards.
- Pinky Companion on your Android phones is updated by itself while the phone is free, and its service stays on.
- Pinky shares anonymous usage statistics and crash reports to help find what to fix: counts such as tasks run and tools used, never your screens, what you type, app names, phone names or PINs. Turn it off in Settings › Privacy or with `DO_NOT_TRACK=1`; Settings › Privacy › What's shared shows exactly what goes out.
- Help › Send Feedback… opens an issue on GitHub with your message (and diagnostics, if you choose to attach them) filled in; you review it there and submit it yourself.
- Pinky speaks Simplified Chinese. It follows your Mac's language, and Settings › Advanced › Language switches between System, English and Simplified Chinese right away, without a restart.
- In Chinese, agents' steps, approval requests, phone status lines and update messages read in Chinese too. What agents and apps write stays as written.
