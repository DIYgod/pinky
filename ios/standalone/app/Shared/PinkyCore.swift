import Foundation
import UserNotifications

/// Bundle ids and Darwin notification names the app and its extensions share.
enum Pinky {
    static let app = "cc.diygod.pinky.standalone"
    static let tunnel = "cc.diygod.pinky.standalone.tunnel"
    static let activity = "cc.diygod.pinky.standalone.activity"
    /// Every process of Pinky's, the lite build's too: the engine refuses
    /// input while one is in front.
    static let selfIDs = [app, tunnel, activity, app + ".lite", app + ".lite.activity"]

    static let stopNote = "cc.diygod.pinky.standalone.stop"
    static let uiOnNote = "cc.diygod.pinky.standalone.ui.on"
    static let uiOffNote = "cc.diygod.pinky.standalone.ui.off"
}

/// Darwin notifications: instant, payload-free signals between the app and
/// whichever process hosts the engine.
enum DarwinNote {
    static func post(_ name: String) {
        CFNotificationCenterPostNotification(
            CFNotificationCenterGetDarwinNotifyCenter(), CFNotificationName(name as CFString), nil, nil, true)
    }

    static func observe(_ name: String, _ callback: CFNotificationCallback) {
        CFNotificationCenterAddObserver(
            CFNotificationCenterGetDarwinNotifyCenter(), nil, callback, name as CFString, nil, .deliverImmediately)
    }
}

#if !WIDGET_EXTENSION
/// The Go core (libpinkycore.a, built by ../build.sh).
enum PinkyCore {
    private static var started = false

    private static func withC(_ s: String, _ f: (UnsafeMutablePointer<CChar>) -> UnsafeMutablePointer<CChar>?) -> String {
        let p = strdup(s)!
        defer { free(p) }
        guard let r = f(p) else { return "{}" }
        defer { PinkyFree(r) }
        return String(cString: r)
    }

    /// Starts the engine in this process and connects it to the phone.
    @discardableResult
    static func start(host: String, addr: String) -> [String: Any] {
        guard let pairing = PairingFile.load() else {
            return ["ok": false, "error": "this build carries no pairing (see ios/standalone/README.md)"]
        }
        var config: [String: Any] = [
            "pairing": pairing.pairing, "udid": pairing.udid, "addr": addr,
            "self": Pinky.selfIDs, "host": host, "heartbeat_s": 10,
        ]
        if host == "extension" {
            // Network extensions get about 50 MB.
            config["memory_mb"] = 30
        }
        let json = String(data: try! JSONSerialization.data(withJSONObject: config), encoding: .utf8)!
        let out = decode(withC(json) { PinkyStart($0) })
        if out["ok"] as? Bool == true, !started {
            started = true
            DarwinNote.observe(Pinky.stopNote) { _, _, _, _, _ in PinkyStop() }
            DarwinNote.observe(Pinky.uiOnNote) { _, _, _, _, _ in PinkyUIActive(1) }
            DarwinNote.observe(Pinky.uiOffNote) { _, _, _, _, _ in PinkyUIActive(0) }
        }
        return out
    }

    static var isStarted: Bool { started }

    static func call(data: Data) -> Data {
        let s = String(data: data, encoding: .utf8) ?? "{}"
        return withC(s) { PinkyCall($0) }.data(using: .utf8)!
    }

    static func call(_ req: [String: Any]) -> [String: Any] {
        decode(String(data: call(data: try! JSONSerialization.data(withJSONObject: req)), encoding: .utf8)!)
    }

    static func decode(_ s: String) -> [String: Any] {
        (try? JSONSerialization.jsonObject(with: Data(s.utf8))) as? [String: Any] ?? ["ok": false, "error": "bad reply"]
    }
}

/// The pairing the build carries: Pairing.json, {"udid": ..., "pairing": base64 plist}.
struct PairingFile {
    let udid: String
    let pairing: String

    static func load() -> PairingFile? {
        guard let url = bundleFile("Pairing.json"),
              let data = try? Data(contentsOf: url),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: String],
              let udid = obj["udid"], let pairing = obj["pairing"]
        else { return nil }
        return PairingFile(udid: udid, pairing: pairing)
    }

    /// A file of the app's bundle, also from inside an extension
    /// (App.app/PlugIns/X.appex).
    static func bundleFile(_ name: String) -> URL? {
        if let u = Bundle.main.url(forResource: name, withExtension: nil) { return u }
        let app = Bundle.main.bundleURL.deletingLastPathComponent().deletingLastPathComponent()
        let u = app.appendingPathComponent(name)
        return FileManager.default.fileExists(atPath: u.path) ? u : nil
    }
}

/// Polls the engine in the process that hosts it and turns its events into
/// local notifications: approvals to answer in the app, tasks that ended.
/// The notifications carry no actions, so nothing can be approved from a
/// banner; the person opens Pinky, where the engine refuses input.
final class EventWatcher {
    private var after = 0
    private var timer: DispatchSourceTimer?

    func start() {
        let t = DispatchSource.makeTimerSource(queue: .global(qos: .utility))
        t.schedule(deadline: .now() + 1, repeating: 1)
        t.setEventHandler { [weak self] in self?.poll() }
        t.resume()
        timer = t
    }

    private func poll() {
        let st = PinkyCore.call(["op": "status", "after": after])
        guard let r = st["result"] as? [String: Any], let events = r["events"] as? [[String: Any]] else { return }
        for ev in events {
            after = max(after, ev["seq"] as? Int ?? 0)
            let c = UNMutableNotificationContent()
            switch ev["kind"] as? String {
            case "approval":
                c.title = "Pinky asks"
                c.body = (ev["text"] as? String ?? "") + "\nOpen Pinky to answer."
                c.interruptionLevel = .timeSensitive
            case "task_ended":
                c.title = ev["end"] as? String == "done" ? "Pinky finished" : "Pinky stopped (\(ev["end"] as? String ?? ""))"
                c.body = ev["summary"] as? String ?? ""
            default:
                continue
            }
            c.sound = .default
            let id = "pinky-\(ev["seq"] as? Int ?? 0)"
            UNUserNotificationCenter.current().add(UNNotificationRequest(identifier: id, content: c, trigger: nil))
        }
    }
}
#endif
