import Foundation
import Darwin

enum GuestDiagnostic {
    private static let limit = 1 << 20
    private static let sources = ["window", "clipboard"]

    static func record(_ source: String, _ reason: String) {
        guard sources.contains(source), reason.range(of: "^[a-zA-Z][a-zA-Z0-9_]{0,95}$", options: .regularExpression) != nil else { return }
        let directory = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Logs/Cage")
        try? append(source, reason, directory: directory)
    }

    static func append(_ source: String, _ reason: String, directory: URL) throws {
        guard sources.contains(source), reason.range(of: "^[a-zA-Z][a-zA-Z0-9_]{0,95}$", options: .regularExpression) != nil else { return }
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let root = open(directory.path, O_RDONLY | O_DIRECTORY | O_NOFOLLOW)
        guard root >= 0 else { throw CocoaError(.fileReadNoPermission) }
        defer { close(root) }
        var info = stat()
        guard fstat(root, &info) == 0, info.st_uid == getuid(), info.st_mode & 0o077 == 0 else { throw CocoaError(.fileReadNoPermission) }
        let lock = openat(root, source + ".lock", O_CREAT | O_RDWR | O_NOFOLLOW, 0o600)
        guard lock >= 0 else { throw CocoaError(.fileWriteNoPermission) }
        defer { close(lock) }
        guard fstat(lock, &info) == 0, info.st_mode & S_IFMT == S_IFREG, info.st_uid == getuid(), info.st_nlink == 1,
              flock(lock, LOCK_EX) == 0 else { throw CocoaError(.fileWriteNoPermission) }
        defer { flock(lock, LOCK_UN) }
        let now = Date()
        for index in 0...4 {
            let name = index == 0 ? source + ".jsonl" : source + "-\(index).jsonl"
            if fstatat(root, name, &info, AT_SYMLINK_NOFOLLOW) != 0 { continue }
            guard info.st_mode & S_IFMT == S_IFREG, info.st_uid == getuid(), info.st_nlink == 1 else { throw CocoaError(.fileReadNoPermission) }
            if Double(info.st_mtimespec.tv_sec) < now.addingTimeInterval(-14 * 86400).timeIntervalSince1970 { _ = unlinkat(root, name, 0) }
        }
        var data = try JSONSerialization.data(withJSONObject: ["time": ISO8601DateFormatter().string(from: now), "source": source, "reason": reason])
        data.append(10)
        let current = source + ".jsonl"
        if fstatat(root, current, &info, AT_SYMLINK_NOFOLLOW) == 0,
           info.st_size + Int64(data.count) > limit || info.st_birthtimespec.tv_sec / 86400 != Int(now.timeIntervalSince1970) / 86400 {
            _ = unlinkat(root, source + "-4.jsonl", 0)
            for index in stride(from: 3, through: 0, by: -1) {
                let previous = index == 0 ? current : source + "-\(index).jsonl"
                let next = source + "-\(index + 1).jsonl"
                if renameat(root, previous, root, next) != 0, errno != ENOENT { throw CocoaError(.fileWriteUnknown) }
            }
        }
        let fd = openat(root, current, O_WRONLY | O_CREAT | O_APPEND | O_NOFOLLOW, 0o600)
        guard fd >= 0 else { throw CocoaError(.fileWriteNoPermission) }
        defer { close(fd) }
        guard fstat(fd, &info) == 0, info.st_mode & S_IFMT == S_IFREG, info.st_uid == getuid(), info.st_nlink == 1 else { throw CocoaError(.fileWriteNoPermission) }
        try data.withUnsafeBytes { bytes in
            var offset = 0
            while offset < bytes.count {
                let count = Darwin.write(fd, bytes.baseAddress!.advanced(by: offset), bytes.count - offset)
                if count < 0 && errno == EINTR { continue }
                guard count > 0 else { throw CocoaError(.fileWriteUnknown) }
                offset += count
            }
        }
    }
}
