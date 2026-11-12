import SwiftUI

/// The model and its key, when Pinky asks, and the connection.
struct SettingsView: View {
    @EnvironmentObject var m: AppModel
    @Environment(\.dismiss) private var dismiss
    @AppStorage("provider") private var provider = "anthropic"
    @AppStorage("model") private var model = ""
    @AppStorage("baseURL") private var baseURL = ""
    @State private var key = ""
    @State private var hasKey = false

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Picker("Provider", selection: $provider) {
                        Text(verbatim: "Anthropic").tag("anthropic")
                        Text("OpenAI-compatible").tag("openai")
                    }
                    TextField(provider == "anthropic" ? "Model (empty: claude-opus-5-5)" : "Model", text: $model)
                        .autocorrectionDisabled().textInputAutocapitalization(.never)
                    TextField("Address (empty: the provider's own)", text: $baseURL)
                        .autocorrectionDisabled().textInputAutocapitalization(.never).keyboardType(.URL)
                    SecureField(hasKey ? "API key saved; type to replace it" : "API key", text: $key)
                        .autocorrectionDisabled().textInputAutocapitalization(.never)
                        .onSubmit(saveKey)
                    if hasKey {
                        Button("Delete the key", role: .destructive) {
                            Keychain.set(Keychain.keyAccount(provider), "")
                            hasKey = false
                        }
                    }
                } header: {
                    Text("Model")
                } footer: {
                    Text("The key stays in this iPhone's Keychain. Pinky sends it only to the model's address, with what the task needs.")
                }

                Section {
                    Picker("Ask me before", selection: Binding(
                        get: { Preset(rawValue: m.state?.preset ?? 1) ?? .commits },
                        set: { p in Task { await m.setPreset(p) } })) {
                        Text("Payments").tag(Preset.payments)
                        Text("Payments, messages and deletions").tag(Preset.commits)
                        Text("Every action").tag(Preset.every)
                    }
                    .pickerStyle(.inline)
                    .labelsHidden()
                } header: {
                    Text("Ask me before")
                }

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
                    Text("Connection")
                }
            }
            .navigationTitle("Settings")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        saveKey()
                        dismiss()
                    }
                }
            }
            .onAppear { hasKey = Keychain.get(Keychain.keyAccount(provider)) != nil }
            .onChange(of: provider) { _, p in
                key = ""
                hasKey = Keychain.get(Keychain.keyAccount(p)) != nil
            }
        }
    }

    private var vpnText: String {
        switch m.vpn {
        case .on: String(localized: "On")
        case .connecting: String(localized: "Turning on…")
        case .off: String(localized: "Off")
        }
    }

    private func saveKey() {
        let k = key.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !k.isEmpty else { return }
        hasKey = Keychain.set(Keychain.keyAccount(provider), k)
        key = ""
    }
}
