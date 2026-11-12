import SwiftUI
import UserNotifications

@main
struct PinkyApp: App {
    init() {
        // In test mode the app only hosts WebDriverAgent (TestHost).
        guard !TestHost.active else { return }
        // Banners for approvals while Pinky works in other apps.
        UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge]) { _, _ in }
    }

    var body: some Scene {
        WindowGroup {
            if TestHost.active {
                HostingView().onAppear { TestHost.start() }
            } else {
                MainView()
            }
        }
    }
}

/// Pinky's own screens, with the model that connects to the core: never
/// made in test mode, so the app then starts no core and no VPN.
private struct MainView: View {
    @Environment(\.scenePhase) private var phase
    @StateObject private var model = AppModel()

    var body: some View {
        ContentView()
            .environmentObject(model)
            .onChange(of: phase) { _, p in model.phaseChanged(p) }
    }
}
