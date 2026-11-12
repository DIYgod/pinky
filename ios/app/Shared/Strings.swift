import Foundation

/// Words the app and its extensions build in code, from the string catalog
/// (Localizable.xcstrings). SwiftUI's literal texts come from the same
/// catalog by themselves.
enum L {
    static var asks: String { String(localized: "Pinky asks", comment: "Notification title: an approval waits") }
    static var openToAnswer: String {
        String(localized: "Open Pinky to answer.", comment: "Notification body, after the approval's title")
    }

    /// The title of a task that ended, by how it ended (pilot.End).
    static func ended(_ end: String) -> String {
        switch end {
        case "done": String(localized: "Pinky finished", comment: "A task ended: done")
        case "stopped": String(localized: "Pinky stopped", comment: "A task ended: the person stopped it")
        case "failed", "refused": String(localized: "Pinky could not do it", comment: "A task ended: failed or refused")
        case "steps", "time", "cost": String(localized: "Pinky reached its limit", comment: "A task ended: a step, time or cost limit")
        case "no_key", "bad_key": String(localized: "The model did not accept the key", comment: "A task ended: no or bad API key")
        default: String(localized: "Pinky stopped with an error", comment: "A task ended: an error")
        }
    }

    /// What the person must do while the phone is not ready, by the core's
    /// reason (onphone.Reason*).
    static func reason(_ r: String?) -> String {
        switch r {
        case "vpn_off": String(localized: "Pinky's VPN is off. Turn it on to let Pinky reach this iPhone.", comment: "Phone not ready")
        case "not_paired": String(localized: "Pinky is not paired with this iPhone yet.", comment: "Phone not ready")
        case "unpaired": String(localized: "This iPhone no longer trusts Pinky's pairing. Pair again.", comment: "Phone not ready")
        case "failed": String(localized: "Pinky cannot reach this iPhone.", comment: "Phone not ready")
        default: String(localized: "Connecting to this iPhone…", comment: "Phone not ready")
        }
    }

    /// A start or decide the core refused, by its code.
    static func refused(_ code: String?, _ error: String?) -> String {
        switch code {
        case "no_device": String(localized: "Pinky is not connected to this iPhone yet.", comment: "Start refused")
        case "no_key": String(localized: "Add the model's API key in Settings first.", comment: "Start refused")
        case "no_task": String(localized: "Type a task first.", comment: "Start refused")
        case "no_model": String(localized: "Choose a model in Settings first.", comment: "Start refused")
        case "busy": String(localized: "Pinky is already working on a task.", comment: "Start refused")
        case "gone": String(localized: "This was already answered, or the time to answer ran out.", comment: "Decide refused")
        default: error ?? String(localized: "Something went wrong.", comment: "Unknown error")
        }
    }
}
