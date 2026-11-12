import SwiftUI

/// Settings, in the sections and words the Android app has too: the AI
/// model, the limits of a task, when Pinky asks, this iPhone, and about.
struct SettingsView: View {
    @EnvironmentObject var m: AppModel
    @Environment(\.dismiss) private var dismiss
    @AppStorage("provider") private var provider = TaskSettings.anthropic
    @AppStorage("model") private var model = ""
    @AppStorage("baseURL") private var baseURL = ""
    @AppStorage("maxSteps") private var maxSteps = TaskSettings.defaultSteps
    @AppStorage("maxMinutes") private var maxMinutes = TaskSettings.defaultMinutes
    @AppStorage("maxCost") private var maxCost = TaskSettings.defaultCost
    @State private var key = ""
    @State private var hasKey = false
    @State private var address = ""

    var body: some View {
        NavigationStack {
            Form {
                modelSection
                limitsSection
                askSection
                phoneSection
                Section("About") {
                    LabeledContent("Version", value: version)
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        saveKey()
                        saveAddress()
                        dismiss()
                    }
                }
            }
            .onAppear {
                hasKey = Keychain.get(Keychain.keyAccount(provider)) != nil
                address = baseURL
            }
            .onChange(of: provider) { _, p in
                key = ""
                hasKey = Keychain.get(Keychain.keyAccount(p)) != nil
                // A Claude model means nothing to another service.
                if p != TaskSettings.anthropic && TaskSettings.claudeModels.contains(where: { $0.id == model }) { model = "" }
            }
        }
        .tint(.pinkyAccent)
    }

    // MARK: AI model

    private var modelSection: some View {
        Section {
            Picker("Service", selection: $provider) {
                Text("Anthropic-compatible API").tag(TaskSettings.anthropic)
                Text("OpenAI-compatible API").tag(TaskSettings.openai)
            }
            if hasKey {
                LabeledContent("API key") { Text("Saved").foregroundStyle(Color.pinkyGreen) }
            }
            SecureField(hasKey ? "Change the API key" : "Paste the API key", text: $key)
                .autocorrectionDisabled().textInputAutocapitalization(.never)
                .onSubmit(saveKey)
            if hasKey {
                Button("Remove the API key", role: .destructive) {
                    Keychain.set(Keychain.keyAccount(provider), "")
                    hasKey = false
                }
            }
            if claudeList {
                Picker("Model", selection: claudeModel) {
                    ForEach(claudeChoices, id: \.id) { Text(verbatim: $0.name).tag($0.id) }
                }
            } else {
                LabeledContent("Model") {
                    TextField("Model name", text: $model)
                        .multilineTextAlignment(.trailing)
                        .autocorrectionDisabled().textInputAutocapitalization(.never)
                }
            }
            LabeledContent("API address") {
                TextField("Default", text: $address)
                    .multilineTextAlignment(.trailing)
                    .autocorrectionDisabled().textInputAutocapitalization(.never).keyboardType(.URL)
                    .onSubmit(saveAddress)
            }
            if !TaskSettings.urlOK(address) {
                Text("Enter an address that starts with https:// or http://.").font(.footnote).foregroundStyle(.red)
            }
        } header: {
            Text("AI model")
        } footer: {
            VStack(alignment: .leading, spacing: 6) {
                Text("Claude through Anthropic's API, or another model through a service compatible with OpenAI's. Each has its own key.")
                Text("The key is kept in this iPhone's Keychain and sent only to the service.")
                if provider == TaskSettings.openai {
                    Text("The model's name as the service gives it. It must read images and use tools.")
                    Text("The address of the service's OpenAI-compatible API, such as https://…/v1. OpenAI's own by default.")
                } else if claudeList {
                    Text("Opus is the most capable; Sonnet and Haiku cost less.")
                }
            }
        }
    }

    /// Claude's models to choose from on Anthropic's own API; the address
    /// as typed decides, before Done saves it.
    private var claudeList: Bool { provider == TaskSettings.anthropic && TaskSettings.cleanURL(address).isEmpty }

    private var claudeModel: Binding<String> {
        Binding(get: { model.isEmpty ? TaskSettings.defaultModel : model }, set: { model = $0 })
    }

    /// The models offered, with the one chosen before when it is not among them.
    private var claudeChoices: [(id: String, name: String)] {
        let list = TaskSettings.claudeModels
        let m = claudeModel.wrappedValue
        return list.contains { $0.id == m } ? list : list + [(m, m)]
    }

    // MARK: Limits

    private var limitsSection: some View {
        Section {
            Picker("Steps per task", selection: $maxSteps) {
                ForEach(TaskSettings.stepChoices, id: \.self) { Text(verbatim: L.steps($0)).tag($0) }
            }
            Picker("Time per task", selection: $maxMinutes) {
                ForEach(TaskSettings.minuteChoices, id: \.self) { Text(verbatim: L.minutes($0)).tag($0) }
            }
            Picker("Cost per task", selection: $maxCost) {
                ForEach(TaskSettings.costChoices, id: \.self) { c in
                    Text(verbatim: L.costLimit(priced: priced, maxCost: c)).tag(c)
                }
            }
        } header: {
            Text("Limits")
        } footer: {
            VStack(alignment: .leading, spacing: 6) {
                Text("Each action on the phone is a step. Time spent waiting for you doesn't count.")
                if priced {
                    Text("Cost is counted from what the service reports, at Anthropic's prices.")
                } else {
                    Text("Pinky doesn't know this model's prices, so it counts a million tokens as US$1.")
                }
            }
        }
    }

    private var priced: Bool {
        _ = (provider, model)
        return TaskSettings.isPriced
    }

    // MARK: When Pinky asks

    private var askSection: some View {
        Section {
            ForEach(Preset.ordered, id: \.self) { p in
                Button { Task { await m.setPreset(p) } } label: {
                    HStack(alignment: .firstTextBaseline) {
                        VStack(alignment: .leading, spacing: 3) {
                            Text(p.title).foregroundStyle(.primary)
                            Text(p.detail).font(.footnote).foregroundStyle(.secondary)
                        }
                        Spacer()
                        if preset == p {
                            Image(systemName: "checkmark").fontWeight(.semibold).foregroundStyle(Color.pinkyAccent)
                        }
                    }
                }
                .disabled(m.state == nil)
            }
        } header: {
            Text("When should Pinky ask you?")
        } footer: {
            if m.state == nil { Text("Turn on Pinky's VPN to change this.") }
        }
    }

    private var preset: Preset? { m.state.flatMap { Preset(rawValue: $0.preset) } }

    // MARK: This iPhone

    private var phoneSection: some View {
        Section {
            LabeledContent("VPN", value: vpnText)
            LabeledContent("Pinky's core runs in", value: m.inApp ? String(localized: "This app") : String(localized: "The VPN"))
            if !m.inApp {
                if m.vpn == .off {
                    Button("Turn on") { Task { await m.connect() } }
                } else {
                    Button("Turn off", role: .destructive) { m.disconnect() }
                }
            }
        } header: {
            Text("This iPhone")
        } footer: {
            Text("Pinky reaches this iPhone through a VPN that never leaves the phone.")
        }
    }

    private var vpnText: String {
        switch m.vpn {
        case .on: String(localized: "On")
        case .connecting: String(localized: "Turning on…")
        case .off: String(localized: "Off")
        }
    }

    private var version: String {
        let i = Bundle.main.infoDictionary
        let v = i?["CFBundleShortVersionString"] as? String ?? ""
        let b = i?["CFBundleVersion"] as? String ?? ""
        return b.isEmpty || b == v ? v : "\(v) (\(b))"
    }

    private func saveKey() {
        let k = key.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !k.isEmpty else { return }
        hasKey = Keychain.set(Keychain.keyAccount(provider), k)
        key = ""
    }

    private func saveAddress() {
        guard TaskSettings.urlOK(address) else { return }
        baseURL = TaskSettings.cleanURL(address)
        address = baseURL
    }
}

extension Preset {
    /// The choices in the order Settings lists them: the default first.
    static let ordered: [Preset] = [.commits, .payments, .every]

    var title: LocalizedStringKey {
        switch self {
        case .commits: "Ask before payments, messages and deletions"
        case .payments: "Ask before payments only"
        case .every: "Ask before every action"
        }
    }

    var detail: LocalizedStringKey {
        switch self {
        case .commits: "Also asks before calls, account changes and installing apps."
        case .payments: "Messages and deletions go through without asking."
        case .every: "Slower, but you see every tap first."
        }
    }
}
