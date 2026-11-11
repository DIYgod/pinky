import SwiftUI
import UserNotifications

@main
struct PinkyStandaloneApp: App {
    @Environment(\.scenePhase) private var phase
    @StateObject private var model = AppModel()

    init() {
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound]) { _, _ in }
    }

    var body: some Scene {
        WindowGroup {
            ContentView().environmentObject(model)
        }
        .onChange(of: phase) { _, p in model.phaseChanged(p) }
    }
}
