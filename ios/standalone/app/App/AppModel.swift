import ActivityKit
import LocalAuthentication
import os
import SwiftUI
#if !NO_TUNNEL
import NetworkExtension
#endif

/// Where the Go engine runs: in the packet-tunnel extension, which iOS keeps
/// running in the background, or in this app.
enum EngineHost: String, CaseIterable, Identifiable {
    case tunnel, app
    var id: String { rawValue }
    var label: String { self == .tunnel ? "VPN extension" : "This app" }
}

struct Approval: Equatable {
    var id: String
    var text: String
}

@MainActor
final class AppModel: ObservableObject {
    #if NO_TUNNEL
    let host = EngineHost.app
    #else
    @Published var host: EngineHost = EngineHost(rawValue: UserDefaults.standard.string(forKey: "host") ?? "") ?? .tunnel {
        didSet { UserDefaults.standard.set(host.rawValue, forKey: "host") }
    }
    #endif
    /// remotepairingd through the reflector. Without a port the app finds
    /// the one remotepairingd advertises on the loopback interface.
    @Published var addr = UserDefaults.standard.string(forKey: "addr") ?? "10.7.0.1" {
        didSet { UserDefaults.standard.set(addr, forKey: "addr") }
    }
    @Published var connection = "Off"
    @Published var connected = false
    @Published var state = ""
    @Published var heartbeat = ""
    @Published var pending: Approval?
    @Published var task: [String: Any] = [:]
    @Published var soak: [String: Any] = [:]
    @Published var log: [String] = []
    @Published var probe = ""
    @Published var backgroundAudio = false {
        didSet { audio.set(backgroundAudio) }
    }

    private let audio = SilentAudio()
    private var poller: Timer?
    private var after = 0
    /// A status asked for before this time may not end the Live Activity:
    /// until the engine has taken the task or soak the activity is for, its
    /// status still shows none running.
    private var keepActivityUntil = Date.distantPast
    private var watcher: EventWatcher?
    private var active = true
    private var engineAddr: String?
    private let syslog = Logger(subsystem: Pinky.app, category: "app")

    init() {
        #if !NO_TUNNEL
        NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in self?.vpnChanged() }
        }
        #endif
        Task {
            #if !NO_TUNNEL
            await loadManager()
            #endif
            #if DEBUG
            await testRun()
            #endif
        }
        startPolling()
    }

    // MARK: Connection

    #if !NO_TUNNEL
    private var manager: NETunnelProviderManager?

    private func loadManager() async {
        manager = try? await NETunnelProviderManager.loadAllFromPreferences().first
        vpnChanged()
    }

    /// Turns the loopback on: saves the VPN configuration (iOS asks the
    /// person to allow it the first time) and starts the tunnel, with the
    /// engine in it or not.
    func connect() async {
        do {
            let m = manager ?? NETunnelProviderManager()
            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = Pinky.tunnel
            proto.serverAddress = "this iPhone"
            m.protocolConfiguration = proto
            m.localizedDescription = "Pinky loopback"
            m.isEnabled = true
            try await m.saveToPreferences()
            try await m.loadFromPreferences()
            manager = m
            let a = await engineAddress()
            var opts: [String: NSObject] = ["engine": NSNumber(value: host == .tunnel), "addr": a as NSString]
            #if DEBUG
            // Test runs may try another tunnel address: -iface 10.7.1.1 -mask 255.255.255.0
            for k in ["iface", "mask"] {
                if let v = UserDefaults.standard.string(forKey: k) { opts[k] = v as NSString }
            }
            #endif
            try m.connection.startVPNTunnel(options: opts)
        } catch {
            note("VPN: \(error.localizedDescription)")
        }
    }

    func disconnect() {
        manager?.connection.stopVPNTunnel()
    }

    private func vpnChanged() {
        let s = manager?.connection.status ?? .invalid
        connection = switch s {
        case .connected: "On"
        case .connecting: "Connecting…"
        case .disconnecting: "Disconnecting…"
        case .reasserting: "Reconnecting…"
        default: "Off"
        }
        connected = s == .connected
        if connected && host == .app { startInApp() }
    }
    #else
    /// The lite build has no tunnel of its own: LocalDevVPN reflects the
    /// packets, and the engine runs here.
    func connect() async {
        startInApp()
        connected = PinkyCore.isStarted
        connection = connected ? "Engine started (needs LocalDevVPN on)" : "Off"
    }

    func disconnect() {}
    #endif

    /// The address the engine dials: addr as set or, with no port in it, at
    /// the port remotepairingd advertises on the loopback interface.
    private func engineAddress() async -> String {
        if addr.contains(":") {
            engineAddr = addr
            return addr
        }
        let found = await PairingPortFinder.find()
        found.notes.forEach { note("bonjour: \($0)") }
        let a = "\(addr):\(found.port ?? 49152)"
        note("remotepairingd at \(a)\(found.port == nil ? " (fallback)" : "")")
        engineAddr = a
        return a
    }

    private var finding = false

    private func startInApp() {
        if PinkyCore.isStarted { return }
        guard let a = engineAddr else {
            guard !finding else { return }
            finding = true
            Task {
                _ = await engineAddress()
                finding = false
                startInApp()
            }
            return
        }
        let out = PinkyCore.start(host: "app", addr: a)
        if out["ok"] as? Bool != true {
            note("engine: \(out["error"] as? String ?? "?")")
            return
        }
        PinkyUIActive(active ? 1 : 0)
        let w = EventWatcher()
        w.start()
        watcher = w
    }

    /// One request to the engine, wherever it runs.
    func call(_ req: [String: Any]) async -> [String: Any] {
        if host == .app {
            guard PinkyCore.isStarted else { return ["ok": false, "error": "the engine is not running"] }
            return await Task.detached { PinkyCore.call(req) }.value
        }
        #if NO_TUNNEL
        return ["ok": false, "error": "no tunnel in this build"]
        #else
        guard connected, let session = manager?.connection as? NETunnelProviderSession else {
            return ["ok": false, "error": "the loopback is off"]
        }
        let data = try! JSONSerialization.data(withJSONObject: req)
        return await withCheckedContinuation { cont in
            do {
                try session.sendProviderMessage(data) { resp in
                    cont.resume(returning: PinkyCore.decode(resp.flatMap { String(data: $0, encoding: .utf8) } ?? ""))
                }
            } catch {
                cont.resume(returning: ["ok": false, "error": error.localizedDescription])
            }
        }
        #endif
    }

    // MARK: Foreground and background

    /// Tells the engine whether Pinky's own screen is in front: it refuses
    /// input while it is, so an agent cannot tap Approve or Stop.
    func phaseChanged(_ p: ScenePhase) {
        active = p != .background
        DarwinNote.post(active ? Pinky.uiOnNote : Pinky.uiOffNote)
        if host == .app && PinkyCore.isStarted { PinkyUIActive(active ? 1 : 0) }
        if p == .active { startPolling() } else if p == .background { stopPolling() }
    }

    private func startPolling() {
        guard poller == nil else { return }
        poller = Timer.scheduledTimer(withTimeInterval: 1, repeats: true) { [weak self] _ in
            Task { @MainActor in await self?.refresh() }
        }
    }

    private func stopPolling() {
        poller?.invalidate()
        poller = nil
    }

    private func refresh() async {
        guard connected else { return }
        let asked = Date.now
        let st = await call(["op": "status", "after": after])
        guard let r = st["result"] as? [String: Any] else {
            state = st["error"] as? String ?? state
            return
        }
        // The engine, wherever it runs, learns that Pinky is in front.
        if active, r["ui_active"] as? Bool == false { _ = await call(["op": "ui_active", "on": true]) }
        state = r["state"] as? String ?? ""
        heartbeat = r["heartbeat"] as? String ?? ""
        if let p = r["pending"] as? [String: Any], let id = p["id"] as? String {
            pending = Approval(id: id, text: p["text"] as? String ?? "")
        } else {
            pending = nil
        }
        task = r["task"] as? [String: Any] ?? [:]
        soak = r["soak"] as? [String: Any] ?? [:]
        for ev in r["events"] as? [[String: Any]] ?? [] {
            after = max(after, ev["seq"] as? Int ?? 0)
            let kind = ev["kind"] as? String ?? ""
            note([kind, ev["text"] as? String, ev["end"] as? String, ev["summary"] as? String]
                .compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · "))
        }
        let running = (task["task"] != nil && task["end"] == nil) || (!soak.isEmpty && soak["done"] as? Bool != true)
        if !running && asked > keepActivityUntil { endActivities() }
    }

    func note(_ s: String) {
        let t = Date.now.formatted(date: .omitted, time: .standard)
        log.insert("\(t) \(s)", at: 0)
        syslog.log("pinky app: \(s, privacy: .public)")
        if log.count > 100 { log.removeLast() }
    }

    // MARK: Tasks

    func startTask(_ text: String, key: String, model: String, baseURL: String) async {
        // iOS starts a Live Activity only for the app in front, and the
        // engine takes Pinky off the screen before its first step.
        startActivity(text)
        let r = await call(["op": "task_start", "task": text, "api_key": key, "model": model, "base_url": baseURL])
        if r["ok"] as? Bool != true {
            endActivities()
            note("task: \(r["error"] as? String ?? "?")")
        }
        keepActivityUntil = .now
    }

    func stop() async {
        DarwinNote.post(Pinky.stopNote)
        _ = await call(["op": "stop"])
        endActivities()
    }

    /// Approve asks for Face ID or the passcode first: an injected touch
    /// cannot pass that, on top of the engine refusing input while Pinky is
    /// in front.
    func approve(_ a: Approval) {
        let ctx = LAContext()
        ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: "Approve: \(a.text)") { ok, err in
            Task { @MainActor in
                if ok {
                    await self.answer(a, yes: true)
                } else {
                    self.note("approval not confirmed: \(err?.localizedDescription ?? "?")")
                }
            }
        }
    }

    func answer(_ a: Approval, yes: Bool) async {
        let r = await call(["op": "approve", "id": a.id, "yes": yes])
        if r["ok"] as? Bool != true { note("approve: \(r["error"] as? String ?? "?")") }
        await refresh()
    }

    // MARK: Q1 and Q2 tests

    /// The probe; x and y, in screenshot pixels, are where it taps 7 when
    /// it cannot read Calculator's elements.
    func runProbe(x: Int = 0, y: Int = 0) async {
        probe = "Probing…"
        let r = await call(["op": "probe", "app": "com.apple.calculator", "x": x, "y": y])
        if let res = r["result"], let d = try? JSONSerialization.data(withJSONObject: res, options: [.prettyPrinted, .sortedKeys]) {
            probe = String(data: d, encoding: .utf8) ?? ""
        } else {
            probe = "error: \(r["error"] as? String ?? "?")"
        }
    }

    func startSoak(minutes: Int, every: Int, tap: Bool) async {
        let r = await call(["op": "soak_start", "app": "com.apple.calculator", "tap": tap, "every_s": every, "minutes": minutes])
        if r["ok"] as? Bool == true {
            startActivity("Soak test: Calculator, \(minutes) min")
            keepActivityUntil = .now
        } else {
            note("soak: \(r["error"] as? String ?? "?")")
        }
    }

    func stopSoak() async {
        _ = await call(["op": "soak_stop"])
        endActivities()
    }

    #if DEBUG
    /// Runs a Q1, Q2 or Q3 measurement from a Mac, after the person has
    /// allowed the VPN configuration once, so it needs no taps:
    ///
    ///     xcrun devicectl device process launch --terminate-existing --device <udid> \
    ///       cc.diygod.pinky.standalone -- -pinkyRun soak -host tunnel -soakMinutes 20
    ///
    /// Arguments: -pinkyRun probe|soak|task, -host tunnel|app, -audio YES,
    /// -probeX and -probeY (where the probe taps 7 without elements),
    /// -soakMinutes, -soakEvery (seconds), -soakTap NO; for a task -task,
    /// -model and -baseURL, with the key in the launch environment as
    /// PINKY_MODEL_KEY. It restarts the loopback so the engine runs only
    /// where -host says. A task's approvals still need the person.
    private func testRun() async {
        let d = UserDefaults.standard
        guard let run = d.string(forKey: "pinkyRun") else { return }
        note("test run: \(run) in \(host.label)")
        #if !NO_TUNNEL
        if manager?.connection.status != .disconnected && manager?.connection.status != .invalid {
            disconnect()
            for _ in 0..<40 {
                if manager?.connection.status == .disconnected { break }
                try? await Task.sleep(for: .milliseconds(250))
            }
        }
        #endif
        await connect()
        var ready = false
        for _ in 0..<120 {
            let st = await call(["op": "status"])
            if (st["result"] as? [String: Any])?["state"] as? String == "ready" {
                ready = true
                break
            }
            try? await Task.sleep(for: .milliseconds(500))
        }
        guard ready else {
            note("test run: the engine did not get ready")
            return
        }
        if d.bool(forKey: "audio") { backgroundAudio = true }
        switch run {
        case "probe":
            await runProbe(x: d.integer(forKey: "probeX"), y: d.integer(forKey: "probeY"))
        case "soak":
            await startSoak(
                minutes: max(1, d.integer(forKey: "soakMinutes")),
                every: d.object(forKey: "soakEvery") == nil ? 10 : max(2, d.integer(forKey: "soakEvery")),
                tap: d.object(forKey: "soakTap") == nil || d.bool(forKey: "soakTap"))
        case "task":
            // The key comes in the launch environment, never in a file.
            let key = ProcessInfo.processInfo.environment["PINKY_MODEL_KEY"] ?? ""
            await startTask(d.string(forKey: "task") ?? "", key: key,
                            model: d.string(forKey: "model") ?? "", baseURL: d.string(forKey: "baseURL") ?? "")
        default:
            note("test run: unknown \(run)")
        }
    }
    #endif

    // MARK: Live Activity

    private func startActivity(_ text: String) {
        endActivities()
        keepActivityUntil = .distantFuture
        guard ActivityAuthorizationInfo().areActivitiesEnabled else {
            note("Live Activities are off for Pinky")
            return
        }
        do {
            _ = try Activity.request(
                attributes: PinkyActivityAttributes(task: text),
                content: ActivityContent(state: .init(started: .now, note: ""), staleDate: nil))
            note("Live Activity on")
        } catch {
            note("Live Activity: \(error.localizedDescription)")
        }
    }

    private func endActivities() {
        for a in Activity<PinkyActivityAttributes>.activities {
            Task { await a.end(nil, dismissalPolicy: .immediate) }
        }
    }
}
