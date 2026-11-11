import ActivityKit
import AppIntents
import Foundation

/// The Live Activity that shows while Pinky works: the task, how long it has
/// run, and Stop.
struct PinkyActivityAttributes: ActivityAttributes {
    struct ContentState: Codable, Hashable {
        var started: Date
        var note: String
    }

    var task: String
}

/// Stop, from the Live Activity. It runs in the app's process, which iOS
/// starts in the background if needed; the engine, wherever it runs, hears
/// the Darwin notification at once.
struct StopIntent: LiveActivityIntent {
    static var title: LocalizedStringResource = "Stop Pinky"
    static var description = IntentDescription("Stops the task Pinky is doing on this iPhone.")

    func perform() async throws -> some IntentResult {
        DarwinNote.post(Pinky.stopNote)
        #if !WIDGET_EXTENSION
        if PinkyCore.isStarted { PinkyStop() }
        #endif
        for a in Activity<PinkyActivityAttributes>.activities {
            await a.end(ActivityContent(state: .init(started: a.content.state.started, note: "Stopped"), staleDate: nil),
                        dismissalPolicy: .after(.now + 5))
        }
        return .result()
    }
}
