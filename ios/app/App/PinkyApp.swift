import SwiftUI
import UserNotifications

@main
struct PinkyApp: App {
    @Environment(\.scenePhase) private var phase
    @StateObject private var model = AppModel()

    init() {
        // Banners for approvals while Pinky works in other apps.
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in }
    }

    var body: some Scene {
        WindowGroup {
            ContentView().environmentObject(model)
        }
        .onChange(of: phase) { _, p in model.phaseChanged(p) }
    }
}
