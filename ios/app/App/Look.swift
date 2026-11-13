import SwiftUI

extension L.Tone {
    var color: Color {
        switch self {
        case .done: .pinkyGreen
        case .stopped: .secondary
        case .failed: .pinkyAmber
        }
    }

    var symbol: String {
        switch self {
        case .done: "checkmark.circle.fill"
        case .stopped: "stop.circle.fill"
        case .failed: "exclamationmark.circle.fill"
        }
    }
}

/// One step of the setup: its number, or a tick once done; the next one
/// shows what it is for and its button.
struct SetupStep<Content: View>: View {
    let number: Int
    let title: LocalizedStringKey
    let done: Bool
    let now: Bool
    @ViewBuilder var content: () -> Content

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            ZStack {
                Circle().fill(done ? Color.pinkyGreen : now ? Color.pinkyAccent : Color.secondary.opacity(0.25))
                if done {
                    Image(systemName: "checkmark").font(.caption2.bold()).foregroundStyle(.white)
                } else {
                    Text(verbatim: "\(number)").font(.caption.bold()).foregroundStyle(now ? .white : .secondary)
                }
            }
            .frame(width: 22, height: 22)
            .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 5 }
            VStack(alignment: .leading, spacing: 8) {
                Text(title)
                    .font(.body.weight(now ? .semibold : .regular))
                    .foregroundStyle(done || !now ? .secondary : .primary)
                if now { content() }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
        }
        .padding(.vertical, 2)
    }
}
