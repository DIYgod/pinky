import SwiftUI

struct ContentView: View {
    @EnvironmentObject var m: AppModel
    @State private var taskText = ""
    @State private var apiKey = ""
    @AppStorage("model") private var model = "claude-opus-5-5"
    @AppStorage("baseURL") private var baseURL = ""
    @State private var soakMinutes = 10
    @State private var soakEvery = 10
    @State private var soakTap = true

    var body: some View {
        NavigationStack {
            Form {
                if let a = m.pending { approval(a) }
                connection
                task
                tests
                Section("Log") {
                    ForEach(Array(m.log.enumerated()), id: \.offset) { _, line in
                        Text(line).font(.caption.monospaced())
                    }
                }
            }
            .navigationTitle("Pinky on iPhone")
        }
    }

    private func approval(_ a: Approval) -> some View {
        Section {
            Text(a.text).font(.headline)
            HStack {
                Button("Deny", role: .destructive) { Task { await m.answer(a, yes: false) } }
                    .buttonStyle(.bordered)
                Spacer()
                Button { m.approve(a) } label: { Label("Approve", systemImage: "faceid") }
                    .buttonStyle(.borderedProminent)
            }
        } header: {
            Text("Pinky asks")
        } footer: {
            Text("Approving needs Face ID or your passcode.")
        }
    }

    private var connection: some View {
        Section {
            LabeledContent("Loopback", value: m.connection)
            #if !NO_TUNNEL
            Picker("Engine runs in", selection: $m.host) {
                ForEach(EngineHost.allCases) { Text($0.label).tag($0) }
            }
            .disabled(m.connected)
            #endif
            TextField("remotepairingd address", text: $m.addr)
                .font(.body.monospaced()).autocorrectionDisabled().textInputAutocapitalization(.never)
                .disabled(m.connected)
            if m.connected {
                #if !NO_TUNNEL
                Button("Turn off", role: .destructive) { m.disconnect() }
                #endif
            } else {
                Button("Turn on") { Task { await m.connect() } }
            }
            if !m.state.isEmpty { LabeledContent("Phone", value: m.state) }
            if !m.heartbeat.isEmpty { Text(m.heartbeat).font(.caption2.monospaced()).foregroundStyle(.secondary) }
            if m.host == .app {
                Toggle("Keep running with background audio", isOn: $m.backgroundAudio)
            }
        } header: {
            Text("Connection")
        }
    }

    private var task: some View {
        Section {
            TextField("What should Pinky do?", text: $taskText, axis: .vertical)
            SecureField("API key (kept in memory only)", text: $apiKey)
                .autocorrectionDisabled().textInputAutocapitalization(.never)
            TextField("Model", text: $model)
                .autocorrectionDisabled().textInputAutocapitalization(.never)
            TextField("Messages API address (empty: Anthropic)", text: $baseURL)
                .autocorrectionDisabled().textInputAutocapitalization(.never).keyboardType(.URL)
            HStack {
                Button("Start") { Task { await m.startTask(taskText, key: apiKey, model: model, baseURL: baseURL) } }
                    .disabled(!m.connected || taskText.isEmpty || apiKey.isEmpty)
                Spacer()
                Button("Stop", role: .destructive) { Task { await m.stop() } }
            }
            .buttonStyle(.bordered)
            if !m.task.isEmpty {
                let t = m.task
                LabeledContent("Steps", value: "\(t["steps"] as? Int ?? 0)")
                LabeledContent("Cost", value: String(format: "US$%.3f", t["cost"] as? Double ?? 0))
                if let d = t["doing"] as? String, !d.isEmpty { LabeledContent("Doing", value: d) }
                if let e = t["end"] as? String { LabeledContent("Ended", value: e) }
                if let s = t["summary"] as? String, !s.isEmpty { Text(s).font(.callout) }
            }
        } header: {
            Text("Task")
        }
    }

    private var tests: some View {
        Section {
            Button("Probe Calculator (Q1)") { Task { await m.runProbe() } }
                .disabled(!m.connected)
            if !m.probe.isEmpty { Text(m.probe).font(.caption2.monospaced()) }
            Stepper("Soak \(soakMinutes) min", value: $soakMinutes, in: 1...120, step: 1)
            Stepper("Every \(soakEvery) s", value: $soakEvery, in: 2...120, step: 2)
            Toggle("Tap digits", isOn: $soakTap)
            HStack {
                Button("Start soak (Q2)") { Task { await m.startSoak(minutes: soakMinutes, every: soakEvery, tap: soakTap) } }
                    .disabled(!m.connected)
                Spacer()
                Button("Stop soak", role: .destructive) { Task { await m.stopSoak() } }
            }
            .buttonStyle(.bordered)
            if !m.soak.isEmpty {
                let s = m.soak
                Text("rounds \(s["rounds"] as? Int ?? 0) · ok \(s["ok"] as? Int ?? 0) · away \(s["away"] as? Int ?? 0)\((s["done"] as? Bool ?? false) ? " · done" : "")")
                    .font(.caption.monospaced())
                if let l = s["last"] as? String, !l.isEmpty { Text(l).font(.caption2.monospaced()).foregroundStyle(.secondary) }
            }
        } header: {
            Text("Tests")
        } footer: {
            Text("Probe and soak go to Calculator: they press Home first, then open it.")
        }
    }
}
