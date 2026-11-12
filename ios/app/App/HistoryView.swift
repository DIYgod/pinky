import SwiftUI

/// The tasks Pinky did on this iPhone, newest first, with their steps.
struct HistoryView: View {
    @EnvironmentObject var m: AppModel
    @Environment(\.dismiss) private var dismiss
    @State private var search = ""

    var body: some View {
        NavigationStack {
            List(m.history) { t in
                NavigationLink {
                    List {
                        Section {
                            LabeledContent("Started", value: t.started.formatted(date: .abbreviated, time: .shortened))
                            LabeledContent("State", value: t.state)
                        }
                        Section("Steps") {
                            ForEach(Array(t.steps.enumerated()), id: \.offset) { _, s in StepRow(step: s) }
                        }
                    }
                    .navigationTitle(t.title)
                } label: {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(t.title).lineLimit(2)
                        Text(t.started.formatted(date: .abbreviated, time: .shortened))
                            .font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            .overlay {
                if m.history.isEmpty { ContentUnavailableView("No tasks yet", systemImage: "clock") }
            }
            .searchable(text: $search)
            .onSubmit(of: .search) { Task { await m.loadHistory(search: search) } }
            .task { await m.loadHistory() }
            .navigationTitle("History")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }
}
