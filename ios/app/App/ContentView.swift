import SwiftUI

/// Pinky's one screen: what waits for the person, the task, and a new
/// task; History and Settings from the toolbar. Until Pinky can work, it
/// leads through the setup, one step at a time: pairing, the VPN, then the
/// model's key.
struct ContentView: View {
    @EnvironmentObject var m: AppModel
    @AppStorage("provider") private var provider = "anthropic"
    @State private var text = ""
    @State private var showSettings = false
    @State private var showHistory = false

    var body: some View {
        NavigationStack {
            List {
                if let e = Profile.endsSoon() {
                    Section {
                        Label(L.profileEnds(e), systemImage: "clock.badge.exclamationmark").foregroundStyle(.orange)
                    }
                }
                connection
                if m.paired && m.vpn == .on && !hasKey { keyStep }
                ForEach(m.state?.needs ?? []) { NeedCard(need: $0) }
                if let a = m.state?.agent { AgentCard(agent: a, task: m.state?.task) }
                if m.state?.agent?.running != true { composer }
                if let p = m.problem {
                    Section { Text(p).foregroundStyle(.red) }
                }
            }
            .navigationTitle(Text(verbatim: "Pinky"))
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button { showHistory = true } label: { Label("History", systemImage: "clock") }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { showSettings = true } label: { Label("Settings", systemImage: "gearshape") }
                }
            }
            .sheet(isPresented: $showSettings) { SettingsView().environmentObject(m) }
            .sheet(isPresented: $showHistory) { HistoryView().environmentObject(m) }
        }
    }

    @ViewBuilder private var connection: some View {
        if !m.paired {
            Section {
                if PhonePairing.available {
                    PairOnPhone()
                } else {
                    Text("Pinky is not paired with this iPhone yet.")
                    Text("Pair it once from a Mac with Pinky (see ios/README.md).").font(.footnote).foregroundStyle(.secondary)
                }
            } header: {
                Text("Set up")
            }
        } else if m.vpn != .on {
            Section {
                Text("Pinky reaches this iPhone through a VPN that never leaves the phone.")
                if m.inApp {
                    Text("Turn on LocalDevVPN, then come back.").foregroundStyle(.secondary)
                } else {
                    Button(m.vpn == .connecting ? "Turning on…" : "Turn on") { Task { await m.connect() } }
                        .disabled(m.vpn == .connecting)
                }
            }
        } else if m.state?.computer != nil {
            Section {
                Label("Pinky on your computer is using this iPhone", systemImage: "desktopcomputer")
                Text("Tasks on this iPhone wait until the computer is done with it, or until you choose Pause connection for this iPhone there.")
                    .font(.footnote).foregroundStyle(.secondary)
            }
        } else if let st = m.state, !st.device.ready {
            Section {
                Label(L.reason(m.reason), systemImage: "iphone.slash")
                if let d = st.device.detail, !d.isEmpty, m.reason == "failed" {
                    Text(d).font(.footnote).foregroundStyle(.secondary)
                }
            }
        }
    }

    /// Keychain is no observable store: the screen reads it again when
    /// Settings closes, which is where the key is typed.
    private var hasKey: Bool {
        _ = showSettings
        return !(Keychain.get(Keychain.keyAccount(provider)) ?? "").isEmpty
    }

    private var keyStep: some View {
        Section {
            Text("Last step: choose the model Pinky thinks with and add its API key.")
            Button("Choose the model and key") { showSettings = true }
        } header: {
            Text("Set up")
        }
    }

    private var composer: some View {
        Section {
            TextField("What should Pinky do on this iPhone?", text: $text, axis: .vertical)
                .lineLimit(2...6)
            Button {
                let t = text
                Task {
                    await m.startTask(t)
                    if m.problem == nil { text = "" }
                }
            } label: {
                Label("Start", systemImage: "play.fill")
            }
            .disabled(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || m.state?.device.ready != true
                || m.state?.computer != nil)
        } header: {
            Text("New task")
        } footer: {
            Text("Pinky leaves this app and works in the others. You can watch, answer here when it asks, and stop it from the Live Activity.")
        }
    }
}

/// Pairing Pinky with this iPhone from the iPhone itself (iOS 27): the
/// steps in Settings and the code to type there.
struct PairOnPhone: View {
    @EnvironmentObject var m: AppModel

    var body: some View {
        Text("Pinky needs to pair with this iPhone once. You do it in Settings, with a code Pinky shows here.")
        switch m.pairStep {
        case .idle, .paired:
            Button("Pair") { m.pairOnPhone() }
        case .failed(let why):
            Text(why).foregroundStyle(.red)
            Button("Try again") { m.pairOnPhone() }
        case .waiting, .code, .wrongCode, .retry:
            VStack(alignment: .leading, spacing: 6) {
                Text("1. Open Settings › Privacy & Security › Developer Mode.")
                Text("2. Choose to pair with “Pinky”.")
                Text("3. Type the code below. It also comes as a notification.")
            }
            .font(.callout)
            switch m.pairStep {
            case .code(let code):
                Text(verbatim: code)
                    .font(.system(.largeTitle, design: .monospaced).bold())
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity)
                    .accessibilityLabel(Text("Pairing code \(code)"))
            case .wrongCode:
                Text("The code did not match. Pair again from Settings: Pinky shows a new code.").foregroundStyle(.red)
            case .retry(let why):
                Text("Pairing did not finish. Try again from Settings.").foregroundStyle(.red)
                Text(verbatim: why).font(.footnote).foregroundStyle(.secondary)
            default:
                Label("Waiting for Settings…", systemImage: "hourglass")
                    .foregroundStyle(.secondary)
            }
            Button("Cancel", role: .cancel) { m.stopPairing() }
        }
    }
}

/// An approval or a handoff the agent waits for.
struct NeedCard: View {
    @EnvironmentObject var m: AppModel
    let need: Need

    var body: some View {
        Section {
            Text(need.title).font(.headline)
            if let d = need.detail, !d.isEmpty { Text(d).font(.callout) }
            if let r = need.rule, !r.isEmpty {
                Label { Text("Rule: \(r)") } icon: { Image(systemName: "checkmark.shield") }
                    .font(.footnote).foregroundStyle(.secondary)
            }
            if let dl = need.deadline {
                Label { Text("No answer by \(dl.formatted(date: .omitted, time: .shortened)) denies it") } icon: {
                    Image(systemName: "timer")
                }
                .font(.footnote).foregroundStyle(.secondary)
            }
            switch need.kind {
            case "approval":
                HStack {
                    Button("Deny", role: .destructive) { Task { await m.decide(need, "deny") } }
                    Spacer()
                    if need.allow_task == true {
                        Button("Allow for task") { Task { await m.decide(need, "allow_task") } }
                    }
                    Button { Task { await m.decide(need, "allow_once") } } label: {
                        Label("Allow once", systemImage: "faceid")
                    }
                    .buttonStyle(.borderedProminent)
                }
                .buttonStyle(.bordered)
            case "handoff":
                Text("Do it yourself in the app, then come back here.").font(.footnote).foregroundStyle(.secondary)
                HStack {
                    Button("Decline", role: .destructive) { Task { await m.decide(need, "deny") } }
                    Spacer()
                    Button("Done, hand back") { Task { await m.decide(need, "allow_once") } }
                        .buttonStyle(.borderedProminent)
                }
                .buttonStyle(.bordered)
            default:
                EmptyView()
            }
        } header: {
            Text("Pinky asks")
        }
    }
}

/// The agent's task: what it does, or how it ended.
struct AgentCard: View {
    @EnvironmentObject var m: AppModel
    let agent: AgentState
    let task: TaskState?

    var body: some View {
        Section {
            Text(agent.task).font(.headline)
            if agent.running {
                Text("Working · \(L.steps(agent.steps)) · \(agent.seconds) s · \(cost)")
                    .font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
            } else {
                Text(L.ended(agent.end ?? "")).font(.subheadline.bold())
                if let s = agent.summary, !s.isEmpty { Text(s).font(.callout) }
                Text("\(L.steps(agent.steps)) · \(agent.seconds) s · \(cost)")
                    .font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
            }
            ForEach(Array((task?.steps ?? []).suffix(8).enumerated()), id: \.offset) { _, s in
                StepRow(step: s)
            }
            if agent.running {
                Button(role: .destructive) { Task { await m.stop() } } label: { Label("Stop", systemImage: "stop.fill") }
            } else {
                Button("New task") { Task { await m.dismiss() } }
            }
        } header: {
            Text("Task")
        }
    }

    private var cost: String { agent.cost.formatted(.currency(code: "USD").precision(.fractionLength(2))) }
}

struct StepRow: View {
    let step: Step

    var body: some View {
        Label {
            Text(step.text).font(.callout)
        } icon: {
            Image(systemName: icon).foregroundStyle(color)
        }
    }

    private var icon: String {
        switch step.status {
        case "settled", "ok", "done": "checkmark.circle"
        case "running", "pending": "circle.dotted"
        case "approval_denied", "blocked_by_policy", "failed", "error": "xmark.circle"
        default: "circle"
        }
    }

    private var color: Color {
        switch step.status {
        case "approval_denied", "blocked_by_policy", "failed", "error": .red
        case "running", "pending": .secondary
        default: .green
        }
    }
}
