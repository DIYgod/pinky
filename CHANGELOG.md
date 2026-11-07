# Changelog

What changed for people in each release of Pinky, newest first. A version is the release's date in Asia/Shanghai and that day's count: 2026.1107.2 is the second release of 7 November 2026. Notes wait under "Unreleased" until the next release takes them; see [docs/RELEASING.md](docs/RELEASING.md).

## Unreleased

- On iPhones below iOS 27 without the WebDriverAgent helper, Pinky now says up front that taps, swipes and typing need the helper; it can still show and read the screen and open apps.

## 2026.1107.7 (2026-11-07)

- Pairing a phone over Wi-Fi no longer hangs on "Pairing…" when Android's adb tools have stopped seeing the network, as they do after the computer's network changes (for example when an iPhone is plugged in): Pinky now finds the phone itself.
- Phones paired over Wi-Fi come back by themselves when they return to the network, even when adb no longer sees it.
- When pairing over Wi-Fi takes long, Add phone says what is likely wrong, such as the phone not showing up on the network or, on a Mac, Pinky not allowed to use the local network, and offers to pair with a code instead.
- Pairing with a code now also works on networks where Pinky can't find the phone: it asks for the address Wireless debugging shows and connects there.
- On a Mac, Pinky asks for Local Network access to find Android phones over Wi-Fi, and says why.

## 2026.1107.6 (2026-11-07)

- The mirror no longer stays on "Connecting…" when you come back to a phone after a while.
- Unblocking an app in Settings › Safety now works for apps that are blocked by their id, such as Wise.
- An agent's tap on an element keeps working when the iPhone reports that element under another parent or role.
- Waiting for text now also finds text that only shows in the picture, such as in web pages inside apps.
- When a task lists app names that match no installed app, the agent is told which ones.

## 2026.1107.5 (2026-11-07)

- On Windows, Pinky now installs updates by itself, as on macOS: when you're not using it, or when you click Update now, it runs the new installer quietly, starts the new version and shows what's new, and goes back to the version you had if the new one doesn't start. Agents connected through `pinky mcp` stay connected.
- On macOS, Pinky installs an update only after checking that Apple notarized it.
- When an update fails, Settings › Advanced says why, for example that the new version didn't start.
- Homebrew no longer prints a deprecation warning about Pinky's cask with every `brew` command.
- When an agent asks you a question, the answer no longer offers "Allow for this task", which granted nothing there.
- Activity marks the steps that ran without asking because of an Always allow.

## 2026.1107.4 (2026-11-07)

- iPhones on iOS 17.4 to 26 can now be connected. With the WebDriverAgent helper, installed once from Xcode, agents can do everything they do on iOS 27; this is tried on the iOS 18 and 26 Simulators, not yet on such a phone.
- Without the helper, Pinky shows and reads an older iPhone's screen and opens apps; it cannot swipe or type there.
- Pinky says why it cannot drive an iPhone on iOS 17.3 or earlier.

## 2026.1107.3 (2026-11-07)

- Turning Record the screen, Keep activity, Read-only mode or usage statistics on and off quickly no longer leaves Pinky with an earlier choice than the switch shows.

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
