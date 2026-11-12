import Foundation

/// What a task starts with: the service, the model, its address and the
/// limits, kept in UserDefaults (the key stays in the Keychain). The same
/// choices and defaults as the Android app's TaskSettings and desktop
/// Pinky's (ui/ask.go).
enum TaskSettings {
    static let anthropic = "anthropic"
    static let openai = "openai"
    static let defaultModel = "claude-opus-5-5"

    /// The models Settings offers for Anthropic's own API, the most
    /// capable first.
    static let claudeModels: [(id: String, name: String)] = [
        ("claude-opus-5-5", "Claude Opus 5.5"),
        ("claude-sonnet-5-5", "Claude Sonnet 5.5"),
        ("claude-haiku-5-5", "Claude Haiku 5.5"),
    ]

    static let stepChoices = [25, 50, 100, 200, 500]
    static let minuteChoices = [5, 10, 20, 30, 60]
    static let costChoices = [0.5, 1.0, 2.0, 5.0, 10.0]

    static let defaultSteps = 100
    static let defaultMinutes = 20
    static let defaultCost = 2.0

    // The models whose prices Pinky knows (internal/pilot/price.go).
    private static let priced = [
        "claude-opus-5-5", "claude-opus-5", "claude-sonnet-5-5", "claude-sonnet-5", "claude-fable-5-2", "claude-haiku-5-5",
    ]

    private static var d: UserDefaults { .standard }

    static var provider: String { d.string(forKey: "provider") ?? anthropic }
    static var baseURL: String { d.string(forKey: "baseURL") ?? "" }

    /// The model a task uses: the one typed, or Claude Opus for
    /// Anthropic's API.
    static var model: String {
        let m = (d.string(forKey: "model") ?? "").trimmingCharacters(in: .whitespaces)
        return m.isEmpty && provider == anthropic ? defaultModel : m
    }

    /// The model as the person reads it.
    static var modelLabel: String {
        let m = model
        return claudeModels.first { $0.id == m }?.name ?? m
    }

    /// Whether Settings offers Claude's models rather than a name to type:
    /// Anthropic's own API.
    static var claudeList: Bool { provider == anthropic && baseURL.isEmpty }

    /// Whether Pinky knows the model's prices, and so counts the cost
    /// limit in dollars.
    static var isPriced: Bool { provider == anthropic && priced.contains { model.hasPrefix($0) } }

    static var maxSteps: Int { positive(d.integer(forKey: "maxSteps")) ?? defaultSteps }
    static var maxMinutes: Int { positive(d.integer(forKey: "maxMinutes")) ?? defaultMinutes }
    static var maxCost: Double {
        let v = d.double(forKey: "maxCost")
        return v > 0 ? v : defaultCost
    }

    /// The limit the task box shows: "Up to US$2.00 a task".
    static var limitLine: String {
        let l = L.costLimit(priced: isPriced, maxCost: maxCost)
        return String(localized: "Up to \(l) a task", comment: "Under the task box: the cost limit of a task")
    }

    /// An address as it is kept: trimmed, without a trailing slash.
    static func cleanURL(_ v: String) -> String {
        var u = v.trimmingCharacters(in: .whitespacesAndNewlines)
        while u.hasSuffix("/") { u.removeLast() }
        return u
    }

    /// Whether an address may be saved: empty for the default, else http
    /// or https.
    static func urlOK(_ v: String) -> Bool {
        let u = cleanURL(v)
        return u.isEmpty || u.hasPrefix("https://") || u.hasPrefix("http://")
    }

    private static func positive(_ n: Int) -> Int? { n > 0 ? n : nil }
}
