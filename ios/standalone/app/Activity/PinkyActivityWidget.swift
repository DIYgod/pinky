import ActivityKit
import AppIntents
import SwiftUI
import WidgetKit

@main
struct PinkyWidgets: WidgetBundle {
    var body: some Widget { PinkyActivityWidget() }
}

struct PinkyActivityWidget: Widget {
    var body: some WidgetConfiguration {
        ActivityConfiguration(for: PinkyActivityAttributes.self) { context in
            HStack(spacing: 12) {
                Image(systemName: "hand.tap.fill").font(.title2).foregroundStyle(.pink)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Pinky is working").font(.headline)
                    Text(context.attributes.task).font(.subheadline).lineLimit(2).foregroundStyle(.secondary)
                    Text(timerInterval: context.state.started...Date.distantFuture, countsDown: false)
                        .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                }
                Spacer()
                Button(intent: StopIntent()) { Label("Stop", systemImage: "stop.fill") }
                    .tint(.red)
            }
            .padding()
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Label("Pinky", systemImage: "hand.tap.fill").foregroundStyle(.pink)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    Button(intent: StopIntent()) { Label("Stop", systemImage: "stop.fill") }.tint(.red)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    Text(context.attributes.task).lineLimit(2).font(.caption)
                }
            } compactLeading: {
                Image(systemName: "hand.tap.fill").foregroundStyle(.pink)
            } compactTrailing: {
                Text(timerInterval: context.state.started...Date.distantFuture, countsDown: false)
                    .monospacedDigit().frame(maxWidth: 44)
            } minimal: {
                Image(systemName: "hand.tap.fill").foregroundStyle(.pink)
            }
        }
    }
}
