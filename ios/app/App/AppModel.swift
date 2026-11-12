import ActivityKit
import LocalAuthentication
import os
import SwiftUI
#if !NO_TUNNEL
import NetworkExtension
#endif

/// Pinky's app: the connection to this iPhone, the state of the core, and
/// what the person does with it.
@MainActor
final class AppModel: ObservableObject {
    /// The core's state, its revision, and why the phone is not ready.
    @Published private(set) var state: CoreState?
    @Published private(set) var reason: String?
    /// The VPN that reflects the phone's own connections: Pinky's own
    /// packet tunnel, or LocalDevVPN in a build without one.
    @Published private(set) var vpn = VPN.off
    /// A refused start or decide, in words, until the next action.
    @Published var problem: String?
    @Published private(set) var history: [HistoryTask] = []
    @Published private(set) var paired = Pairing.load() != nil
    /// Pairing from this iPhone itself (iOS 27), while it runs.
    @Published private(set) var pairStep = PhonePairing.Step.idle

    enum VPN { case off, connecting, on }

    private var rev = 0
    private var active = true
    private var loop: Task<Void, Never>?
    private let audio = SilentAudio()
    private let syslog = Logger(subsystem: Pinky.app, category: "app")
    private var addr: String?
    private var notifier: Notifier?
    private lazy var phonePairing = PhonePairing { [weak self] step, p in self?.pairChanged(step, p) }

    /// Where the core runs: in the packet-tunnel extension, which iOS keeps
    /// running while other apps are in front, or, in a build without one,
    /// in this app, which background audio keeps running during a task.
    #if NO_TUNNEL
    let inApp = true
    #else
    let inApp = false
    #endif

    init() {
        #if !NO_TUNNEL
        NotificationCenter.default.addObserver(forName: .NEVPNStatusDidChange, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in self?.vpnChanged() }
        }
        Task { await loadManager() }
        #else
        Task { await startInApp() }
        #endif
        listen()
    }

    // MARK: Pairing

    /// Starts pairing from this iPhone: Pinky waits for the person to pick
    /// it in Settings › Privacy & Security › Developer Mode.
    func pairOnPhone() {
        problem = nil
        phonePairing.start()
    }

    func stopPairing() {
        phonePairing.stop()
        pairStep = .idle
        keepAlive(isBusy)
    }

    private func pairChanged(_ step: PhonePairing.Step, _ p: Pairing?) {
        // A phone that looks at the advertisement and leaves is no failed try.
        if case .retry = step, pairStep == .waiting { return }
        pairStep = step
        keepAlive(isBusy)
        if let p { pairedOnPhone(p) }
    }

    /// Keeps the pairing made on the phone and connects with it: the VPN
    /// starts, or restarts so that the extension's core gets it; without a
    /// tunnel the core in the app takes it.
    private func pairedOnPhone(_ p: Pairing) {
        p.save()
        paired = true
        syslog.log("pinky paired on the phone")
        #if NO_TUNNEL
        Task {
            if PinkyCore.isStarted {
                let out = PinkyCore.start(["pairing": p.pairing, "udid": p.udid])
                if out["ok"] as? Bool != true { problem = out["error"] as? String }
            } else {
                await startInApp()
            }
        }
        #else
        if vpn == .off {
            Task { await connect() }
        } else {
            reconnect = true
            disconnect()
        }
        #endif
    }

    // MARK: Connection

    /// The address the core dials: remotepairingd through the reflector, at
    /// the port it advertises on the loopback interface (49152 is only the
    /// fallback).
    private func coreAddress() async -> String {
        if let addr { return addr }
        let found = await PairingPortFinder.find()
        found.notes.forEach { syslog.debug("pinky bonjour: \($0, privacy: .public)") }
        let a = "10.7.0.1:\(found.port ?? 49152)"
        syslog.log("pinky remotepairingd at \(a, privacy: .public)\(found.port == nil ? " (fallback)" : "", privacy: .public)")
        addr = a
        return a
    }

    #if !NO_TUNNEL
    private var manager: NETunnelProviderManager?
    /// Turn the VPN on again once it is off: the pairing changed.
    private var reconnect = false

    private func loadManager() async {
        manager = try? await NETunnelProviderManager.loadAllFromPreferences().first
        vpnChanged()
        if manager != nil && vpn == .off && paired { await connect() }
    }

    /// Turns the VPN on: saves its configuration (iOS asks the person to
    /// allow it the first time) and starts the tunnel with the core in it.
    func connect() async {
        problem = nil
        do {
            let m = manager ?? NETunnelProviderManager()
            let proto = NETunnelProviderProtocol()
            proto.providerBundleIdentifier = Pinky.tunnel
            proto.serverAddress = String(localized: "This iPhone", comment: "The VPN's server, in Settings › VPN")
            m.protocolConfiguration = proto
            m.localizedDescription = "Pinky"
            m.isEnabled = true
            try await m.saveToPreferences()
            try await m.loadFromPreferences()
            manager = m
            var opts: [String: NSObject] = ["core": NSNumber(value: true), "addr": await coreAddress() as NSString]
            if let p = Pairing.load() {
                opts["pairing"] = p.pairing as NSString
                opts["udid"] = p.udid as NSString
            }
            #if DEBUG
            // Test runs may try another tunnel address: -iface 10.7.1.1 -mask 255.255.255.0
            for k in ["iface", "mask"] {
                if let v = UserDefaults.standard.string(forKey: k) { opts[k] = v as NSString }
            }
            // ... and have the core log its memory every N seconds: -pinkyHeartbeat 1
            let beat = UserDefaults.standard.integer(forKey: "pinkyHeartbeat")
            if beat > 0 { opts["heartbeat"] = NSNumber(value: beat) }
            #endif
            try m.connection.startVPNTunnel(options: opts)
        } catch {
            problem = error.localizedDescription
        }
    }

    func disconnect() {
        manager?.connection.stopVPNTunnel()
    }

    private func vpnChanged() {
        vpn = switch manager?.connection.status ?? .invalid {
        case .connected: .on
        case .connecting, .reasserting: .connecting
        default: .off
        }
        if vpn == .off && reconnect {
            reconnect = false
            Task { await connect() }
        }
        guard vpn == .on else { return }
        // The core in the extension that just started has not heard that
        // Pinky's app is in front.
        if active { DarwinNote.post(Pinky.uiOnNote) }
        listen()
    }
    #else
    /// Without a tunnel of its own the core runs here, and LocalDevVPN
    /// reflects the packets.
    func connect() async { await startInApp() }

    func disconnect() {}

    private func startInApp() async {
        guard !PinkyCore.isStarted else { return }
        let p = Pairing.load()
        let a = await coreAddress()
        let out = PinkyCore.start(["host": "app", "addr": a, "pairing": p?.pairing ?? "", "udid": p?.udid ?? ""])
        if out["ok"] as? Bool != true {
            problem = out["error"] as? String
            return
        }
        PinkyUIActive(active ? 1 : 0)
        vpn = .on
        let n = Notifier()
        n.start()
        notifier = n
        listen()
    }
    #endif

    /// One request to the core, wherever it runs.
    func call(_ req: [String: Any]) async -> CoreReply {
        let data = try! JSONSerialization.data(withJSONObject: req)
        let out: Data
        if inApp {
            out = await Task.detached { PinkyCore.call(data: data) }.value
        } else {
            #if NO_TUNNEL
            out = Data()
            #else
            guard vpn == .on, let session = manager?.connection as? NETunnelProviderSession else {
                return CoreReply(ok: false, error: L.reason("vpn_off"), code: "no_device")
            }
            out = await withCheckedContinuation { cont in
                do {
                    try session.sendProviderMessage(data) { cont.resume(returning: $0 ?? Data()) }
                } catch {
                    cont.resume(returning: Data())
                }
            }
            #endif
        }
        return (try? JSONDecoder().decode(CoreReply.self, from: out)) ?? CoreReply(ok: false, error: "no reply from Pinky's core")
    }

    // MARK: State

    /// Keeps state current: a long poll that answers as soon as the core's
    /// state changes, so the app wakes only when there is news.
    private func listen() {
        guard loop == nil else { return }
        loop = Task { [weak self] in
            while !Task.isCancelled {
                guard let self else { return }
                let r = await self.call(["cmd": "wait", "rev": self.rev, "timeout_ms": 20000])
                guard r.ok, let st = r.state else {
                    try? await Task.sleep(for: .seconds(2))
                    continue
                }
                self.rev = r.rev ?? 0
                self.update(st, reason: r.reason)
            }
        }
    }

    private func update(_ st: CoreState, reason: String?) {
        let was = state
        state = st
        self.reason = reason
        keepAlive(isBusy)
        updateActivity(st)
        if was?.agent?.running == true && st.agent?.running != true { Task { await loadHistory() } }
        #if DEBUG
        debugRun(st)
        #endif
    }

    #if DEBUG
    /// A debug build starts a task from launch arguments once the phone is
    /// ready (README.md): -pinkyTask, and -provider, -model and -baseURL,
    /// which override the settings for this run only; the key comes in the
    /// launch environment (PINKY_MODEL_KEY) and stays in memory.
    private var debugTask = UserDefaults.standard.string(forKey: "pinkyTask")

    private func debugRun(_ st: CoreState) {
        guard let t = debugTask, st.device.ready, st.agent?.running != true, st.needs.isEmpty else { return }
        debugTask = nil
        let key = ProcessInfo.processInfo.environment["PINKY_MODEL_KEY"]
        Task { await startTask(t, key: key) }
    }
    #endif

    /// Background audio keeps the app running while a task runs or waits,
    /// so the Live Activity follows it to the end. A build without the
    /// tunnel needs it for the core itself.
    /// Pairing from the phone keeps it running too, from the start, since
    /// the person goes to Settings for it.
    private func keepAlive(_ busy: Bool) {
        audio.set((busy && !active) || pairing)
    }

    private var isBusy: Bool {
        state.map { ($0.agent?.running ?? false) || !$0.needs.isEmpty } ?? false
    }

    private var pairing: Bool {
        switch pairStep {
        case .waiting, .code, .wrongCode, .retry: true
        case .idle, .failed, .paired: false
        }
    }

    // MARK: Foreground and background

    /// Tells the core whether Pinky's own app is in front: it refuses input
    /// while it is, so an agent cannot tap Approve or Stop.
    func phaseChanged(_ p: ScenePhase) {
        active = p != .background
        DarwinNote.post(active ? Pinky.uiOnNote : Pinky.uiOffNote)
        if inApp && PinkyCore.isStarted { PinkyUIActive(active ? 1 : 0) }
        keepAlive(isBusy)
        if p == .active {
            #if !NO_TUNNEL
            vpnChanged()
            #endif
            listen()
            if let st = state { updateActivity(st) }
        }
    }

    // MARK: Tasks

    func startTask(_ text: String, key: String? = nil) async {
        problem = nil
        let d = UserDefaults.standard
        let provider = d.string(forKey: "provider") ?? "anthropic"
        let key = key ?? Keychain.get(Keychain.keyAccount(provider)) ?? ""
        // iOS starts a Live Activity only for the app in front, and the core
        // takes Pinky off the screen before the agent's first step.
        startActivity(text)
        let r = await call([
            "cmd": "start", "task": text, "key": key, "provider": provider,
            "model": d.string(forKey: "model") ?? "", "base_url": d.string(forKey: "baseURL") ?? "",
        ])
        if !r.ok {
            endActivities(nil)
            problem = L.refused(r.code, r.error)
        }
    }

    func stop() async {
        DarwinNote.post(Pinky.stopNote)
        _ = await call(["cmd": "stop"])
    }

    func dismiss() async {
        _ = await call(["cmd": "dismiss"])
        endActivities(nil)
    }

    /// Answers a need. Allowing asks for Face ID or the passcode first: an
    /// injected touch cannot pass that, on top of the core refusing input
    /// while Pinky is in front.
    func decide(_ n: Need, _ decision: String) async {
        problem = nil
        if decision != "deny" {
            let ctx = LAContext()
            let ok = (try? await ctx.evaluatePolicy(.deviceOwnerAuthentication, localizedReason: n.title)) ?? false
            guard ok else { return }
        }
        let r = await call(["cmd": "decide", "need": n.id, "decision": decision])
        if !r.ok { problem = L.refused(r.code, r.error) }
    }

    func setPreset(_ p: Preset) async {
        let r = await call(["cmd": "preset", "preset": p.rawValue])
        if !r.ok { problem = L.refused(r.code, r.error) }
    }

    func loadHistory(search: String = "") async {
        let r = await call(["cmd": "history", "search": search, "limit": 100])
        if r.ok { history = r.history ?? [] }
    }

    // MARK: Live Activity

    private func startActivity(_ text: String) {
        endActivities(nil)
        guard ActivityAuthorizationInfo().areActivitiesEnabled else { return }
        _ = try? Activity.request(
            attributes: PinkyActivityAttributes(task: text),
            content: ActivityContent(state: .init(started: .now, line: "", waiting: false, ended: ""), staleDate: nil))
    }

    /// Follows the task: its last step, whether it waits for the person,
    /// and its end, which the activity shows for a few seconds. With no
    /// task running, an activity left from before (the app was not running
    /// when its task ended) ends now.
    private func updateActivity(_ st: CoreState) {
        let acts = Activity<PinkyActivityAttributes>.activities
        guard !acts.isEmpty else { return }
        guard let a = st.agent, a.running else {
            endActivities(st.agent.map { L.ended($0.end ?? "") })
            return
        }
        let waiting = st.needs.first
        let line = waiting?.title ?? st.task?.steps?.last?.text ?? ""
        for act in acts {
            let s = PinkyActivityAttributes.ContentState(started: act.content.state.started, line: line,
                                                         waiting: waiting != nil, ended: "")
            if s != act.content.state { Task { await act.update(ActivityContent(state: s, staleDate: nil)) } }
        }
    }

    private func endActivities(_ ended: String?) {
        for act in Activity<PinkyActivityAttributes>.activities {
            var s = act.content.state
            s.ended = ended ?? ""
            s.waiting = false
            Task { await act.end(ActivityContent(state: s, staleDate: nil), dismissalPolicy: ended == nil ? .immediate : .after(.now + 8)) }
        }
    }
}
