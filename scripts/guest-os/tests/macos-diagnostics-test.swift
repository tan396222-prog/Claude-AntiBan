import Foundation

@main
struct MacDiagnosticTest {
    static func main() throws {
        let temporary = FileManager.default.temporaryDirectory.appendingPathComponent("cage-native-diagnostics-" + UUID().uuidString)
        try FileManager.default.createDirectory(at: temporary, withIntermediateDirectories: false)
        defer { try? FileManager.default.removeItem(at: temporary) }
        let logs = temporary.appendingPathComponent("Library/Logs/Cage")
        try GuestDiagnostic.append("window", "window_changed", directory: logs)
        try GuestDiagnostic.append("clipboard", "clipboard_write_failed", directory: logs)
        let file = logs.appendingPathComponent("window.jsonl")
        let original = try Data(contentsOf: file)
        try GuestDiagnostic.append("window", "password=private-secret", directory: logs)
        guard try Data(contentsOf: file) == original else { throw CocoaError(.fileWriteUnknown) }
        for _ in 0..<7 {
            try Data(repeating: 120, count: 1 << 20).write(to: file)
            try GuestDiagnostic.append("window", "frame_unavailable", directory: logs)
        }
        let names = try FileManager.default.contentsOfDirectory(atPath: logs.path)
        guard names.filter({ $0.hasPrefix("window") && $0.hasSuffix(".jsonl") }).count == 5 else { throw CocoaError(.fileWriteUnknown) }
        let target = temporary.appendingPathComponent("preserved.txt")
        try Data("preserved".utf8).write(to: target)
        try FileManager.default.removeItem(at: file)
        try FileManager.default.createSymbolicLink(at: file, withDestinationURL: target)
        do {
            try GuestDiagnostic.append("window", "window_changed", directory: logs)
            throw CocoaError(.fileWriteUnknown)
        } catch CocoaError.fileReadNoPermission { }
        guard try Data(contentsOf: target) == Data("preserved".utf8) else { throw CocoaError(.fileWriteUnknown) }
        print("Native macOS diagnostic schema, rotation and link rejection passed.")
    }
}
