import Foundation

/// A reply of the core (ios/onphone's package comment has the commands).
struct CoreReply: Decodable {
    var ok: Bool
    var error: String?
    var code: String?
    var rev: Int?
    var state: CoreState?
    var reason: String?
    var history: [HistoryTask]?
}

/// What Pinky's screens show (internal/standalone.State).
struct CoreState: Decodable, Equatable {
    var device: DeviceState
    var preset: Int
    var computer: ComputerState?
    var agent: AgentState?
    var task: TaskState?
    var needs: [Need]
}

/// Pinky on a computer while it holds the phone: no task starts meanwhile
/// (ios/onphone, claims.go).
struct ComputerState: Decodable, Equatable {
    var task: String?
    var stoppable: Bool
}

struct DeviceState: Decodable, Equatable {
    var ready: Bool
    var name: String?
    var status: String?
    var line: String?
    var detail: String?
}

struct AgentState: Decodable, Equatable {
    var task: String
    var model: String
    var running: Bool
    var end: String?
    var summary: String?
    var steps: Int
    var cost: Double
    var tokens: Int
    var seconds: Int
}

struct TaskState: Decodable, Equatable {
    var title: String
    var state: String
    var steps: [Step]?
}

struct Step: Decodable, Equatable, Hashable {
    var text: String
    var status: String
    var ms: Int
}

/// An approval or a handoff the agent waits for.
struct Need: Decodable, Equatable, Identifiable {
    var id: String
    var kind: String
    var title: String
    var detail: String?
    var app: String?
    var rule: String?
    var deadline_ms: Int64?
    var allow_task: Bool?

    var deadline: Date? { deadline_ms.map { Date(timeIntervalSince1970: Double($0) / 1000) } }
}

struct HistoryTask: Decodable, Equatable, Identifiable {
    var id: String
    var title: String
    var agent: String
    var state: String
    var started_ms: Int64
    var ended_ms: Int64?
    var steps: [Step]

    var started: Date { Date(timeIntervalSince1970: Double(started_ms) / 1000) }
}

/// When agents ask the person first (policy.Preset).
enum Preset: Int, CaseIterable, Identifiable {
    case commits = 1, payments = 2, every = 3
    var id: Int { rawValue }
}
