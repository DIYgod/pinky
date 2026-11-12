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
            let s = context.state
            HStack(spacing: 12) {
                Icon(state: s).font(.title2)
                VStack(alignment: .leading, spacing: 2) {
                    Text(headline(s)).font(.headline)
                    Text(context.attributes.task).font(.subheadline).lineLimit(1).foregroundStyle(.secondary)
                    if !s.line.isEmpty && s.ended.isEmpty {
                        Text(s.line).font(.caption).lineLimit(1).foregroundStyle(s.waiting ? .orange : .secondary)
                    }
                    if s.ended.isEmpty {
                        Text(timerInterval: s.started...Date.distantFuture, countsDown: false)
                            .font(.caption.monospacedDigit()).foregroundStyle(.secondary)
                    }
                }
                Spacer()
                StopButton(ended: s.ended)
            }
            .padding()
        } dynamicIsland: { context in
            let s = context.state
            return DynamicIsland {
                DynamicIslandExpandedRegion(.leading) {
                    Label { Text("Pinky") } icon: { Icon(state: s) }
                }
                DynamicIslandExpandedRegion(.trailing) {
                    StopButton(ended: s.ended)
                }
                DynamicIslandExpandedRegion(.bottom) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(context.attributes.task).lineLimit(1).font(.caption)
                        if !s.line.isEmpty && s.ended.isEmpty {
                            Text(s.line).lineLimit(1).font(.caption2).foregroundStyle(s.waiting ? .orange : .secondary)
                        }
                    }
                }
            } compactLeading: {
                Icon(state: s)
            } compactTrailing: {
                if s.ended.isEmpty {
                    Text(timerInterval: s.started...Date.distantFuture, countsDown: false)
                        .monospacedDigit().frame(maxWidth: 44)
                } else {
                    Image(systemName: "checkmark")
                }
            } minimal: {
                Icon(state: s)
            }
        }
    }

    private func headline(_ s: PinkyActivityAttributes.ContentState) -> String {
        if !s.ended.isEmpty { return s.ended }
        if s.waiting { return String(localized: "Pinky waits for you", comment: "Live Activity: an approval or handoff waits") }
        return String(localized: "Pinky is working", comment: "Live Activity: a task runs")
    }
}

struct Icon: View {
    let state: PinkyActivityAttributes.ContentState

    var body: some View {
        if state.waiting {
            Image(systemName: "hand.raised.fill").foregroundStyle(.orange)
        } else {
            Image(systemName: "hand.tap.fill").foregroundStyle(.pink)
        }
    }
}

/// Stop, or once the task has ended nothing: the headline says how it
/// ended, and a Stop that does nothing invites more taps.
struct StopButton: View {
    let ended: String

    var body: some View {
        if ended.isEmpty {
            Button(intent: StopIntent()) { Label("Stop", systemImage: "stop.fill") }.tint(.red)
        }
    }
}
