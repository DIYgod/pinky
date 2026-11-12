import SwiftUI

/// Pinky's one screen: what waits for the person, the task, and a new
/// task; History and Settings from the toolbar.
struct ContentView: View {
    @EnvironmentObject var m: AppModel
    @State private var text = ""
    @State private var showSettings = false
    @State private var showHistory = false

    var body: some View {
        NavigationStack {
            List {
                connection
                ForEach(m.state?.needs ?? []) { NeedCard(need: $0) }
                if let a = m.state?.agent { AgentCard(agent: a, task: m.state?.task) }
                if m.state?.agent?.running != true { composer }
                if let p = m.problem {
                    Section { Text(p).foregroundStyle(.red) }
                }
            }
            .navigationTitle("Pinky")
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
                Text("Pinky is not paired with this iPhone yet.")
                Text("Pair it once from a Mac with Pinky (see ios/README.md).").font(.footnote).foregroundStyle(.secondary)
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
        } else if let st = m.state, !st.device.ready {
            Section {
                Label(L.reason(m.reason), systemImage: "iphone.slash")
                if let d = st.device.detail, !d.isEmpty, m.reason == "failed" {
                    Text(d).font(.footnote).foregroundStyle(.secondary)
                }
            }
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
            .disabled(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || m.state?.device.ready != true)
        } header: {
            Text("New task")
        } footer: {
            Text("Pinky leaves this app and works in the others. You can watch, answer here when it asks, and stop it from the Live Activity.")
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
                Text("Working · \(agent.steps) steps · \(agent.seconds) s · \(cost)")
                    .font(.footnote.monospacedDigit()).foregroundStyle(.secondary)
            } else {
                Text(L.ended(agent.end ?? "")).font(.subheadline.bold())
                if let s = agent.summary, !s.isEmpty { Text(s).font(.callout) }
                Text("\(agent.steps) steps · \(agent.seconds) s · \(cost)")
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
