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
                StopButton(note: context.state.note)
            }
            .padding()
        } dynamicIsland: { context in
            DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Label("Pinky", systemImage: "hand.tap.fill").foregroundStyle(.pink)
                }
                DynamicIslandExpandedRegion(.trailing) {
                    StopButton(note: context.state.note)
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

/// Stop, or once the task has ended the note that says so, for the few
/// seconds the activity stays: a Stop that does nothing invites more taps.
struct StopButton: View {
    let note: String

    var body: some View {
        if note.isEmpty {
            Button(intent: StopIntent()) { Label("Stop", systemImage: "stop.fill") }.tint(.red)
        } else {
            Text(note).font(.headline).foregroundStyle(.secondary)
        }
    }
}
