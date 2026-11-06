# Companion protocol

The Android companion app (`android/companion`, package `app.pinky.companion`) serves this protocol from its accessibility service. The Go client is `internal/driver/android/companion`. This page is the reference for both; [companion.md](companion.md) covers the app, the client and test results.

Protocol version: **1**. Additive changes (new commands, new fields, new event kinds) keep the number; incompatible changes bump it. Clients read `protocol` from `ping`.

## Transport

- The service listens on the abstract Unix socket `@pinky_companion`, only while it is bound (from the moment Android connects the service until it is turned off).
- The computer reaches it with `adb forward tcp:0 localabstract:pinky_companion` and connects to `127.0.0.1:<port>`. adb accepts the TCP connection even when nothing listens on the phone and then closes it, so a dead service shows up as EOF on the first read.
- Any app can connect to an abstract socket, so the server checks the peer's credentials and accepts only uid 0 (root), uid 2000 (shell, which is what adbd uses) and the app's own uid. Anyone else gets one line, `{"ok":false,"error":{"code":"forbidden","message":"only adb may connect"}}`, and is disconnected.
- Any number of connections may be open. Each has its own reader thread; requests on one connection run one at a time, in order. Everything that reads the accessibility tree runs on a single worker thread inside the app, so trees from two connections never interleave.

## Framing

- One JSON object per line, UTF-8, terminated by `\n`. Strings may hold any Unicode, including CJK and emoji; the app escapes control characters, U+2028/U+2029 and lone surrogates.
- Request: `{"id": <number or string>, "cmd": "<command>", ...parameters}`.
- Success: `{"id": <same id>, "ok": true, "result": {...}}`.
- Failure: `{"id": <same id>, "ok": false, "error": {"code": "<code>", "message": "<text>"}}`.
- Event (only after `events` on that connection): `{"event": "<kind>", "time": <ms>, ...}`, with no `id`.
- Times are the phone's wall clock in Unix milliseconds. `ping` returns the phone's current time so a client can estimate the clock skew (the Go client does, and converts event times to the host's clock).
- Rectangles are `[left, top, right, bottom]` in screen pixels of the display's current rotation.
- Boolean fields on nodes and windows are sent only when true; absent means false.

## Commands

### `ping`

```json
{"id":1,"cmd":"ping"}
```

| Field | Meaning |
| --- | --- |
| `protocol` | Protocol version (1) |
| `version`, `version_code` | The app's version name and code |
| `sdk`, `release` | Android API level and version string |
| `manufacturer`, `model` | `Build.MANUFACTURER`, `Build.MODEL` |
| `time`, `uptime_ms` | Phone wall clock (Unix ms) and uptime |
| `displays` | Ids of displays that have accessibility windows (always `[0]` before Android 11) |
| `connections` | Open connections, including this one |
| `include_not_important`, `idle_disable_ms` | Current `config` values |

### `tree`

All windows on one display and their node trees.

```json
{"id":2,"cmd":"tree","display":0}
```

| Parameter | Default | Meaning |
| --- | --- | --- |
| `display` | 0 | Display id. Other displays need Android 11+. |
| `ime` | false | Walk the keyboard window's nodes. Off by default: keys are noise for agents. The window is always listed. |
| `bars` | false | Walk the status and navigation bars: system windows that span one screen edge and are at most 12% thick. They are always listed, so clients can tell what covers the app. |
| `invisible` | false | Keep nodes that are not visible to the user (marked `"invisible": true`). By default they are skipped with their subtrees, as `uiautomator dump` does. |
| `max_nodes` | 20000 | Stop after this many nodes and set `truncated`. |
| `timeout_ms` | 10000 | Fail with `timeout` after this long. |

Result:

| Field | Meaning |
| --- | --- |
| `display` | `{id, width, height, density_dpi, rotation}`; width and height in pixels for the current rotation, rotation in degrees |
| `locked` | The keyguard is showing |
| `secure_locked` | The keyguard needs a PIN, pattern or password |
| `windows` | Windows sorted top-most first (by layer) |
| `nodes`, `truncated` | Nodes written, and whether `max_nodes` cut the walk |
| `time` | Phone time when the walk finished |
| `took_ms`, `windows_ms` | Time spent on the request, and on listing windows |

Window:

| Field | Meaning |
| --- | --- |
| `id`, `layer` | Accessibility window id and z-order (higher is on top) |
| `type` | `application`, `input_method`, `system`, `accessibility_overlay`, `split_screen_divider`, `magnification_overlay` or `unknown` |
| `title` | Window title, if any (an activity's label, "Navigation bar", ...) |
| `bounds` | Window rectangle |
| `active`, `focused`, `pip` | Active window, input focus, picture-in-picture |
| `package`, `app` | Package of the root node, and its display name. Names load in the background, so the first tree after an app opens may lack `app`. |
| `root` | The root node, absent when the window was not walked |
| `root_ms`, `walk_ms`, `nodes` | Time to fetch the root (a round trip to the app), time to walk it, and its node count |

Node:

| Field | Meaning |
| --- | --- |
| `class` | View class name, such as `android.widget.Button` |
| `text` | Text. Never sent for password fields, and not sent while a field shows its hint. |
| `desc` | Content description |
| `hint` | Hint text (Android 8+) |
| `id` | Resource id, `package:id/name` |
| `state` | State description, such as "On" or "50%" (Android 11+) |
| `role` | Role description the app set (Compose, `AccessibilityNodeInfoCompat`), such as "Tab"; may be localized |
| `bounds` | Rectangle on the screen, not clipped |
| `clickable`, `long_clickable`, `checkable`, `checked`, `editable`, `scrollable`, `focusable`, `focused`, `selected`, `password`, `heading`, `disabled` | States |
| `node` | A handle for `set_text`: stable while the view lives (it is `AccessibilityNodeInfo.hashCode()`, built from the view and window ids) |
| `children` | Child nodes in order |

By default the tree holds only views that are important for accessibility (see `config`).

### `set_text`

Writes text into an editable node with `ACTION_SET_TEXT`. The text goes straight into the field: any script works, and the clipboard and the keyboard are not involved.

```json
{"id":3,"cmd":"set_text","text":"你好，Pinky","target":{"id":"com.example:id/note"}}
```

| Parameter | Meaning |
| --- | --- |
| `text` | Required. The new text, or the text to append. |
| `target` | Which node; see below. Absent or `{}` means the focused input. |
| `append` | Keep the current text and add to it. Fails with `unsupported` on password fields, whose text cannot be read. |
| `submit` | Focus the node first, then run its IME action (`ACTION_IME_ENTER`, Android 11+). On Android 10 the result has `"submitted": false` and the client should press Enter itself. |
| `timeout_ms` | Default 10000. |

Targets, in order of precedence:

| Target | Picks |
| --- | --- |
| `{"focused": true}` | The node with input focus |
| `{"node": 4242, "window": 41}` | The node with this handle from a `tree` result; `window` narrows the search |
| `{"x": 540, "y": 1296, "id": "..."}` | The smallest editable node under the point, top-most window first; among those, one with the resource id `id` if given |
| `{"id": "com.example:id/note"}` | The first visible editable node with this resource id, top-most window first |

Result: `window` and `node` (the handle of the node written), `text` (the field's text afterwards, read back from the app), `verified` (whether it equals the requested text; an app may change it, for example with a length limit), and `submitted` when `submit` was set. `text` and `verified` are left out for password fields.

### `events`

Subscribes the connection to events. The reply comes first; events follow on the same connection until it closes. A new `events` request replaces the subscription; `"kinds": []` ends it.

```json
{"id":4,"cmd":"events","kinds":["window","toast","notification"]}
```

`kinds` defaults to `window`, `toast` and `notification`. The result is `{"kinds": [...], "time": <ms>}`.

Every event has `event` (the kind), `time` (when it happened on the phone), `package` when known, and `display` (Android 13+).

| Kind | When | Extra fields |
| --- | --- | --- |
| `window` | A new activity, dialog, pane or popup (`TYPE_WINDOW_STATE_CHANGED`) | `class`, `title`, `window` |
| `windows` | A window was added, removed, moved, retitled or focused (`TYPE_WINDOWS_CHANGED`) | `window`, `changes`: any of `added`, `removed`, `title`, `bounds`, `layer`, `active`, `focused`, `accessibility_focused`, `parent`, `children`, `pip` |
| `content` | Content changed, scrolled or text changed. Bursts are folded: at most one event per 100 ms. | `count`: events folded into this one |
| `toast` | A toast appeared, including toasts SystemUI draws for apps targeting Android 11+ | `text`. SystemUI's toasts (Android 12+) also report the app's name; when the event has several texts, the one equal to the app's name is dropped. |
| `notification` | A notification was posted or alerted again | `title`, `text` (falls back to the big text, then the ticker), `category` |
| `dropped` | The connection fell behind and the app dropped events (its queue holds 2048) | `count` |

Notifications can carry personal data. The app forwards what Android reports; filtering (for example of blocked apps) is the gateway's job.

### `wait_idle`

Blocks until no change event (window state, windows, content, scroll, text) arrived for `quiet_ms`, counted from the request at the earliest, or until `timeout_ms` passed. The Go client's `WaitSettled` (the `driver.Settler` interface) uses it with 300 ms.

```json
{"id":5,"cmd":"wait_idle","quiet_ms":300,"timeout_ms":3000}
```

| Parameter | Default | Range |
| --- | --- | --- |
| `quiet_ms` | 300 | 10 to 10000 |
| `timeout_ms` | 3000 | 0 to 30000 |

Result: `settled` (false when the timeout came first), `waited_ms`, and `last_change_ms_ago` when any change was seen since the service started.

### `config`

Changes the service's settings for as long as it stays connected; turning the service off resets them.

| Parameter | Default | Meaning |
| --- | --- | --- |
| `include_not_important` | false | Include views that are not important for accessibility (layout containers), as `uiautomator dump` does. Before Android 13 the app cannot clear its node cache, so the change takes full effect at the next window change. |
| `idle_disable_ms` | 180000 | The service turns itself off (`disableSelf`) after this long without any connection; 0 keeps it on. |

The result holds the current values.

## Error codes

| Code | Meaning |
| --- | --- |
| `bad_request` | Malformed JSON, a missing parameter or an unknown event kind |
| `unknown_command` | No such `cmd` |
| `not_found` | No node matches the target, or the node is gone |
| `not_editable` | The target node is not editable |
| `disabled` | The target node is disabled |
| `unsupported` | The request cannot work here (append to a password field) |
| `action_failed` | The app refused `ACTION_SET_TEXT` |
| `timeout` | The request took longer than `timeout_ms` |
| `forbidden` | The peer is not adb, root or the app itself |
| `internal` | An unexpected exception; the message has its text |

The Go client maps `not_found` to `driver.ErrNotFound` and `unsupported` to `driver.ErrUnsupported`.

## Service lifecycle

- Pinky turns the service on by adding only its own component, `app.pinky.companion/.PinkyAccessibilityService`, to `enabled_accessibility_services`, and off by removing it again; see [companion.md](companion.md#turning-the-service-on-and-off).
- When the service connects it binds the socket (retrying for up to 5 seconds if a previous instance still holds the name); when Android unbinds it, the socket closes and every connection ends.
- After `idle_disable_ms` with no connection the service turns itself off. This is a backstop for a gateway that crashed or lost the phone.
- `uiautomator dump` (and any other UiAutomation client that does not ask to keep services) makes Android unbind every accessibility service for as long as it runs, so the companion restarts with an empty cache around each dump. Do not mix the two.
