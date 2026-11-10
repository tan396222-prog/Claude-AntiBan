// Per-user GUI clipboard agent and its private SSH client for a Cage macOS guest.
import AppKit
import ApplicationServices
import ImageIO
import Security
import UniformTypeIdentifiers

private let wireLimit = 30 * 1024 * 1024
private let textLimit = 2 * 1024 * 1024
private let imageLimit = 20 * 1024 * 1024
private let label = "dev.cage.GuestClipboard"

private struct ClipboardFailure: Error {
    let reason: String
    init(_ reason: String) { self.reason = reason }
}

private func requireGuest() throws {
    var model = [CChar](repeating: 0, count: 256), size = 256
    guard sysctlbyname("hw.model", &model, &size, nil, 0) == 0,
          String(cString: model).range(of: #"^VirtualMac[0-9]+,[0-9]+$"#, options: .regularExpression) != nil,
          getuid() >= 500 else { throw ClipboardFailure("not_guest") }
    let fd = open("/var/db/cage-guest-os", O_RDONLY | O_NOFOLLOW | O_NONBLOCK)
    guard fd >= 0 else { throw ClipboardFailure("not_guest") }
    defer { close(fd) }
    var info = stat()
    guard fstat(fd, &info) == 0, info.st_mode & S_IFMT == S_IFREG, info.st_uid == 0,
          info.st_mode & 0o022 == 0, (5...6).contains(info.st_size) else { throw ClipboardFailure("not_guest") }
    let data = try FileHandle(fileDescriptor: fd, closeOnDealloc: false).read(upToCount: 6) ?? Data()
    guard [Data("macos".utf8), Data("macos\n".utf8)].contains(data) else { throw ClipboardFailure("not_guest") }
}

private func guiAvailable() -> Bool {
    var info = stat(), attributes: SessionAttributeBits = []
    return stat("/dev/console", &info) == 0 && info.st_uid == getuid() && info.st_uid >= 500 &&
        SessionGetInfo(callerSecuritySession, nil, &attributes) == errSecSuccess && attributes.contains(.sessionHasGraphicAccess) &&
        (CGSessionCopyCurrentDictionary() as? [String: Any])?["CGSSessionScreenIsLocked"] as? Bool != true
}

private func onGUI<T>(_ body: () throws -> T) rethrows -> T {
    if Thread.isMainThread { return try body() }
    return try DispatchQueue.main.sync(execute: body)
}

private func writeAll(_ fd: Int32, _ data: Data) throws {
    try data.withUnsafeBytes { bytes in
        var position = 0
        while position < bytes.count {
            let count = Darwin.write(fd, bytes.baseAddress!.advanced(by: position), bytes.count - position)
            if count < 0 && errno == EINTR { continue }
            guard count > 0 else { throw ClipboardFailure("connection_closed") }
            position += count
        }
    }
}

private final class Reader {
    let fd: Int32
    private var buffer = Data()
    init(_ fd: Int32) { self.fd = fd }
    func line(limit: Int = wireLimit) throws -> Data? {
        while true {
            if let newline = buffer.firstIndex(of: 10) {
                let value = Data(buffer[..<newline])
                buffer.removeSubrange(...newline)
                guard value.count <= limit else { throw ClipboardFailure("message_too_large") }
                return value
            }
            guard buffer.count <= limit else { throw ClipboardFailure("message_too_large") }
            var bytes = [UInt8](repeating: 0, count: min(65536, limit + 1 - buffer.count))
            let count = Darwin.read(fd, &bytes, bytes.count)
            if count < 0 && errno == EINTR { continue }
            if count == 0 { if buffer.isEmpty { return nil }; throw ClipboardFailure("incomplete_message") }
            guard count > 0 else { throw ClipboardFailure("connection_closed") }
            buffer.append(contentsOf: bytes.prefix(count))
        }
    }
    func exact(_ length: Int) throws -> Data {
        var output = Data()
        if !buffer.isEmpty {
            let count = min(length, buffer.count)
            output.append(buffer.prefix(count)); buffer.removeFirst(count)
        }
        while output.count < length {
            var bytes = [UInt8](repeating: 0, count: min(65536, length - output.count))
            let count = Darwin.read(fd, &bytes, bytes.count)
            if count < 0 && errno == EINTR { continue }
            guard count > 0 else { throw ClipboardFailure("incomplete_message") }
            output.append(contentsOf: bytes.prefix(count))
        }
        return output
    }
    func copy(to output: FileHandle) throws {
        if !buffer.isEmpty { try output.write(contentsOf: buffer); buffer.removeAll() }
        while true {
            var bytes = [UInt8](repeating: 0, count: 65536)
            let count = Darwin.read(fd, &bytes, bytes.count)
            if count < 0 && errno == EINTR { continue }
            if count == 0 { return }
            guard count > 0 else { throw ClipboardFailure("connection_closed") }
            try output.write(contentsOf: Data(bytes.prefix(count)))
        }
    }
}

private func json(_ data: Data) throws -> [String: Any] {
    guard let value = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw ClipboardFailure("invalid_message") }
    return value
}

private func jsonData(_ value: [String: Any]) throws -> Data {
    var data = try JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
    guard data.count <= wireLimit else { throw ClipboardFailure("message_too_large") }
    data.append(10)
    return data
}

private func paths(_ value: Any?) throws -> [String] {
    guard let values = value as? [String], (1...100).contains(values.count) else { throw ClipboardFailure("invalid_files") }
    for path in values {
        guard path.hasPrefix("/"), !path.contains("\0"), URL(fileURLWithPath: path).standardizedFileURL.path == path else { throw ClipboardFailure("invalid_files") }
    }
    return values
}

private func image(_ data: Data, requirePNG: Bool = false) throws -> CGImage {
    if requirePNG {
        guard data.count <= imageLimit, data.starts(with: [137, 80, 78, 71, 13, 10, 26, 10]) else { throw ClipboardFailure("invalid_image") }
    }
    guard let source = CGImageSourceCreateWithData(data as CFData, nil),
          let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
          let width = properties[kCGImagePropertyPixelWidth] as? Int, let height = properties[kCGImagePropertyPixelHeight] as? Int,
          width > 0, height > 0, width <= 40_000_000 / height,
          let image = CGImageSourceCreateImageAtIndex(source, 0, nil) else { throw ClipboardFailure("invalid_image") }
    return image
}

private func png(_ image: CGImage) throws -> Data {
    let data = NSMutableData()
    guard let output = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil) else { throw ClipboardFailure("invalid_image") }
    CGImageDestinationAddImage(output, image, nil)
    guard CGImageDestinationFinalize(output), data.length <= imageLimit else { throw ClipboardFailure("image_too_large") }
    return data as Data
}

private func boardContent(_ board: NSPasteboard) throws -> [String: Any] {
    if let urls = board.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL], !urls.isEmpty {
        guard urls.allSatisfy({ $0.isFileURL && ($0.host == nil || $0.host == "" || $0.host == "localhost") }) else { throw ClipboardFailure("invalid_files") }
        return ["files": try paths(urls.map(\.path))]
    }
    if let value = board.data(forType: .png) {
        _ = try image(value, requirePNG: true)
        return ["png": value.base64EncodedString()]
    }
    if let value = board.data(forType: .tiff) { return ["png": try png(image(value)).base64EncodedString()] }
    let value = board.string(forType: .string) ?? ""
    guard value.utf8.count <= textLimit else { throw ClipboardFailure("text_too_large") }
    return ["text": value]
}

private func setBoard(_ board: NSPasteboard, _ value: [String: Any]) throws {
    if let text = value["text"] as? String {
        guard text.utf8.count <= textLimit else { throw ClipboardFailure("text_too_large") }
        board.clearContents()
        guard board.setString(text, forType: .string) else { throw ClipboardFailure("clipboard_write_failed") }
    } else if value["png"] != nil {
        guard let encoded = value["png"] as? String, let data = Data(base64Encoded: encoded) else { throw ClipboardFailure("invalid_image") }
        let bitmap = try image(data, requirePNG: true)
        let native = NSImage(cgImage: bitmap, size: .zero)
        board.clearContents()
        guard board.writeObjects([native]) else { throw ClipboardFailure("clipboard_write_failed") }
    } else {
        let files = try paths(value["files"])
        board.clearContents()
        guard board.writeObjects(files.map { NSURL(fileURLWithPath: $0) }) else { throw ClipboardFailure("clipboard_write_failed") }
    }
}

private final class Stream: @unchecked Sendable {
    private struct Snapshot { let data: Data; let completed: (Bool) -> Void }
    let fd: Int32
    private let outputLock = NSLock()
    private let stateLock = NSLock()
    private var closed = false
    private var publishing = false
    private var pending: Snapshot?
    var initialized = false // GUI-thread owned
    init(_ fd: Int32) { self.fd = fd }
    func send(_ value: [String: Any]) throws {
        try sendData(jsonData(value))
    }
    private func sendData(_ data: Data) throws {
        outputLock.lock(); defer { outputLock.unlock() }
        stateLock.lock(); let ended = closed; stateLock.unlock()
        guard !ended else { throw ClipboardFailure("connection_closed") }
        try writeAll(fd, data)
    }
    // Keep only the latest unsent snapshot. A slow client cannot block the GUI
    // thread or accumulate a queue of full-resolution clipboard images.
    func publish(_ value: [String: Any], completed: @escaping (Bool) -> Void = { _ in }) {
        guard let data = try? jsonData(value) else { completed(false); return }
        stateLock.lock()
        guard !closed else { stateLock.unlock(); completed(false); return }
        let superseded = pending
        pending = Snapshot(data: data, completed: completed)
        let start = !publishing
        publishing = true
        stateLock.unlock()
        superseded?.completed(false)
        guard start else { return }
        DispatchQueue.global(qos: .userInitiated).async {
            while true {
                self.stateLock.lock()
                guard let next = self.pending, !self.closed else {
                    self.publishing = false; self.stateLock.unlock(); return
                }
                self.pending = nil
                self.stateLock.unlock()
                do { try self.sendData(next.data); next.completed(true) }
                catch { next.completed(false); self.close(); return }
            }
        }
    }
    func close() {
        outputLock.lock()
        stateLock.lock()
        let discarded = pending
        if !closed { closed = true; pending = nil; shutdown(fd, SHUT_RDWR) }
        stateLock.unlock()
        outputLock.unlock()
        discarded?.completed(false)
    }
}

private final class SnapshotDelivery: @unchecked Sendable {
    var remaining: Int
    var published = false
    init(_ count: Int) { remaining = count }
}

private final class ClipboardAgent: @unchecked Sendable {
    private var streams: [Int32: Stream] = [:]
    private var records: [[String]] = []
    private var known = Set<String>()
    private var owned = Set<String>()
    private struct CachedCopy { let id: String; let paths: [String]; var pinned: Bool; let clipboard: Bool }
    private var copies: [String: CachedCopy] = [:]
    private var fileReaders: [UUID: [String]] = [:]
    private var copiesLoaded = false
    private var collectingCopies = false
    private var copiesNeedCollection = false
    private let cacheQueue = DispatchQueue(label: "cage.clipboard.cache")
    private var session = UUID().uuidString.lowercased()
    private let board: NSPasteboard
    private let gui: () -> Bool
    private let diagnostic: (String) -> Void
    private var available: Bool
    private var revision: Int
    private var snapshotPending = false
    private var transfers: [UUID: Process] = [:]
    private let helper: URL
    init(board: NSPasteboard = .general, gui: @escaping () -> Bool = guiAvailable,
         diagnostic: @escaping (String) -> Void = { GuestDiagnostic.record("clipboard", $0) }) {
        self.board = board
        self.gui = gui
        self.diagnostic = diagnostic
        available = gui()
        revision = board.changeCount
        helper = Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/cage-guest-files")
    }
    private func check(_ expectedSession: String? = nil) throws {
        guard gui(), available, expectedSession == nil || expectedSession == session else { throw ClipboardFailure("requires_gui_login") }
        guard FileManager.default.isExecutableFile(atPath: helper.path) else { throw ClipboardFailure("files_helper_missing") }
    }
    private func hello() -> [String: Any] {
        let gui = self.gui()
        let files = FileManager.default.isExecutableFile(atPath: helper.path)
        return ["hello": ["version": 1, "os": "darwin", "session": session, "formats": ["text", "png", "files"],
                          "available": gui && files,
                          "file_release": true,
                          "reason": !gui ? "requires_gui_login" : (!files ? "files_helper_missing" : "")]]
    }
    private func record(_ files: [String], copied: Bool = false) {
        records.append(files)
        if records.count > 128 { records.removeFirst(); known = Set(records.flatMap { $0 }) }
        else { known.formUnion(files) }
        if copied { owned.formUnion(files) }
    }
    private func cacheControl(_ action: String, id: String = "") throws -> [String: Any] {
        let process = Process(), output = Pipe()
        process.executableURL = helper
        process.arguments = ["cache", String(data: try JSONSerialization.data(withJSONObject: ["action": action, "cache_id": id]), encoding: .utf8)!]
        process.standardInput = FileHandle.nullDevice; process.standardOutput = output; process.standardError = FileHandle.nullDevice
        try process.run()
        let timeout = DispatchWorkItem { if process.isRunning { process.terminate() } }
        DispatchQueue.global().asyncAfter(deadline: .now() + 10, execute: timeout)
        defer { timeout.cancel() }
        let data = try output.fileHandleForReading.readToEnd() ?? Data()
        process.waitUntilExit()
        guard process.terminationStatus == 0, data.count <= wireLimit else { throw ClipboardFailure("clipboard_cache_failed") }
        return try json(data)
    }
    private func rememberCopy(_ result: [String: Any], clipboard: Bool) {
        guard let id = result["cache_id"] as? String, let paths = result["paths"] as? [String], !paths.isEmpty else { return }
        copies[id] = CachedCopy(id: id, paths: paths, pinned: true, clipboard: clipboard)
        cacheQueue.async { _ = try? self.cacheControl("publish", id: id) }
    }
    private func retireCopies(_ paths: [String]) {
        let selected = Set(paths)
        for (id, copy) in copies where !selected.isDisjoint(with: copy.paths) { copies[id]?.pinned = false }
        collectCopies()
    }
    private func collectCopies() {
        guard !collectingCopies else { copiesNeedCollection = true; return }
        collectingCopies = true
        cacheQueue.async {
            defer {
                onGUI {
                    self.collectingCopies = false
                    if self.copiesNeedCollection { self.copiesNeedCollection = false; self.collectCopies() }
                }
            }
            if !onGUI({ self.copiesLoaded }) {
                let previous = try? self.cacheControl("list")
                onGUI {
                    self.copiesLoaded = true
                    for item in previous?["selections"] as? [[String: Any]] ?? [] {
                        if let id = item["cache_id"] as? String, let paths = item["paths"] as? [String], !paths.isEmpty, self.copies[id] == nil {
                            self.copies[id] = CachedCopy(id: id, paths: paths, pinned: false, clipboard: true)
                        }
                    }
                }
            }
            for copy in onGUI({ Array(self.copies.values) }) {
                let retain = onGUI { () -> Bool in
                    guard self.copies[copy.id]?.pinned == false else { return true }
                    let selected = (self.board.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []).map(\.path)
                    let root = URL(fileURLWithPath: copy.paths[0]).deletingLastPathComponent().standardizedFileURL.path
                    return (selected + self.fileReaders.values.flatMap { $0 }).contains { value in
                        let path = URL(fileURLWithPath: value).standardizedFileURL.path
                        return path == root || path.hasPrefix(root + "/") || root.hasPrefix(path + "/")
                    }
                }
                if retain { continue }
                if (try? self.cacheControl("release", id: copy.id)) != nil {
                    onGUI { self.copies.removeValue(forKey: copy.id); self.owned.subtract(copy.paths); self.known.subtract(copy.paths) }
                }
            }
        }
    }
    private func beginTransfer(_ operation: String, input: FileHandle?, name: String? = nil) throws -> (Process, Pipe, UUID, String) {
        let epoch = try onGUI { () -> String in try check(); return session }
        let process = Process(); process.executableURL = helper
        process.arguments = [operation] + (name.map { [$0] } ?? (operation == "import" ? [] : ["--paths-stdin"]))
        let output = Pipe(); process.standardOutput = output; process.standardError = FileHandle.nullDevice
        process.standardInput = input ?? FileHandle.nullDevice
        let id = UUID()
        try onGUI {
            try check(epoch); transfers[id] = process
            do { try process.run() } catch { transfers.removeValue(forKey: id); throw error }
        }
        return (process, output, id, epoch)
    }
    private func finishTransfer(_ process: Process, id: UUID, epoch: String) throws {
        process.waitUntilExit()
        try onGUI { transfers.removeValue(forKey: id); try check(epoch) }
        guard process.terminationStatus == 0 else { throw ClipboardFailure("file_transfer_failed") }
    }
    private func fileJSON(_ operation: String, values: [String]) throws -> [String] {
        let reader = UUID()
        var recorded = false
        onGUI { fileReaders[reader] = values }
        defer { onGUI { fileReaders.removeValue(forKey: reader); if !recorded { copiesLoaded = false }; collectCopies() } }
        let input = Pipe()
        let (process, output, id, epoch) = try beginTransfer(operation, input: input.fileHandleForReading)
        defer { try? input.fileHandleForWriting.close(); if process.isRunning { process.terminate() }; process.waitUntilExit(); onGUI { _ = transfers.removeValue(forKey: id) } }
        try input.fileHandleForWriting.write(contentsOf: JSONSerialization.data(withJSONObject: values)); try input.fileHandleForWriting.close()
        let data = try output.fileHandleForReading.readToEnd() ?? Data()
        guard data.count <= 65536 else { throw ClipboardFailure("invalid_files") }
        let response = try json(data)
        let result = try paths(response["paths"])
        onGUI { record(result, copied: true); rememberCopy(response, clipboard: operation == "copy") }
        recorded = true
        do { try finishTransfer(process, id: id, epoch: epoch) }
        catch { onGUI { retireCopies(result) }; throw error }
        return result
    }
    private func snapshot() throws -> [String: Any] {
        let (epoch, captured, content) = try onGUI { () -> (String, Int, [String: Any]) in
            try check(); return (session, board.changeCount, try boardContent(board))
        }
        var result = content
        if let selected = content["files"] as? [String] {
            onGUI { record(selected) }
            // File snapshots are frozen in the GUI agent's readable cache.
            result["files"] = try fileJSON("copy", values: selected)
        }
        do { try onGUI { try check(epoch); guard board.changeCount == captured else { throw ClipboardFailure("superseded") } } }
        catch { if let files = result["files"] as? [String] { onGUI { retireCopies(files) } }; throw error }
        result["revision"] = captured
        result["source"] = "guest"
        return result
    }
    private func apply(_ value: [String: Any], echo: Bool) throws -> [String: Any] {
        try onGUI {
            if value["release"] != nil { retireCopies(try paths(value["release"])); return ["released": true] }
            try check()
            if let expected = value["revision"] as? Int, expected != board.changeCount { throw ClipboardFailure("superseded") }
            if value["files"] != nil {
                let selected = try paths(value["files"])
                guard selected.allSatisfy({ known.contains($0) }) else { throw ClipboardFailure("unregistered_files") }
            }
            try setBoard(board, value)
            if !echo { revision = board.changeCount }
            return ["ack": value["serial"] as? Int ?? 0, "revision": board.changeCount]
        }
    }
    private func copiedText(_ request: [String: Any]) throws -> [String: Any] {
        guard let expected = request["revision"] as? Int, let epoch = request["session"] as? String else { throw ClipboardFailure("invalid_copy_request") }
        let deadline = Date().addingTimeInterval(2)
        repeat {
            let value = try onGUI { () -> [String: Any]? in
                try check(epoch)
                let captured = board.changeCount
                guard captured != expected else { return nil }
                guard let text = board.string(forType: .string), text.utf8.count <= textLimit else { throw ClipboardFailure("clipboard_text_unavailable") }
                guard board.changeCount == captured else { return nil }
                return ["text": text, "revision": captured, "session": session]
            }
            if let value { return value }
            Thread.sleep(forTimeInterval: 0.025)
        } while Date() < deadline
        throw ClipboardFailure("clipboard_copy_not_observed")
    }
    func poll() {
        collectCopies()
        let current = gui()
        if current != available {
            available = current; session = UUID().uuidString.lowercased()
            revision = board.changeCount
            if !current { for process in transfers.values where process.isRunning { process.terminate() } }
            // Reconnect under a fresh GUI-session hello. Unsent snapshots from
            // the previous login must not cross the session transition.
            for stream in streams.values { stream.close() }
        }
        guard current, !snapshotPending, streams.values.contains(where: { $0.initialized }), revision != board.changeCount else { return }
        revision = board.changeCount
        snapshotPending = true
        let capturedSession = session
        DispatchQueue.global(qos: .userInitiated).async {
            let result: [String: Any]
            do { result = try self.snapshot() }
            catch { result = (error as? ClipboardFailure)?.reason == "superseded" ? [:] : ["error": (error as? ClipboardFailure)?.reason ?? "clipboard_read_failed"] }
            onGUI {
                self.snapshotPending = false
                guard !result.isEmpty else { return }
                let files = result["files"] as? [String] ?? []
                let recipients = self.streams.values.filter { $0.initialized }
                guard self.session == capturedSession, !recipients.isEmpty else { self.retireCopies(files); return }
                let delivery = SnapshotDelivery(recipients.count)
                for stream in recipients {
                    stream.publish(result) { sent in
                        DispatchQueue.main.async {
                            delivery.remaining -= 1
                            delivery.published = delivery.published || sent
                            if delivery.remaining == 0 && !delivery.published { self.retireCopies(files) }
                        }
                    }
                }
            }
        }
    }
    func serve(_ fd: Int32) {
        var stream: Stream?
        defer {
            stream?.close()
            onGUI {
                _ = streams.removeValue(forKey: fd)
                if stream != nil && streams.isEmpty { for (id, copy) in copies where copy.clipboard { copies[id]?.pinned = false }; collectCopies() }
            }
            close(fd)
        }
        var user = uid_t(), group = gid_t()
        guard getpeereid(fd, &user, &group) == 0, user == getuid() else { return }
        var timeout = timeval(tv_sec: 1800, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout.size(ofValue: timeout)))
        var outputTimeout = timeval(tv_sec: 2, tv_usec: 0)
        setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &outputTimeout, socklen_t(MemoryLayout.size(ofValue: outputTimeout)))
        let reader = Reader(fd)
        do {
            guard let first = try reader.line(), let operation = try json(first)["operation"] as? String else { return }
            let request = try json(first)
            switch operation {
            case "copy-prepare":
                let value = try onGUI { () -> [String: Any] in try check(); return ["revision": board.changeCount, "session": session] }
                try writeAll(fd, jsonData(value))
            case "copy-read":
                try writeAll(fd, jsonData(copiedText(request)))
            case "stream":
                let connection = Stream(fd)
                stream = connection
                onGUI { streams[fd] = connection }
                try connection.send(onGUI { hello() })
                while let line = try reader.line() {
                    do {
                        let reply = try apply(json(line), echo: false)
                        onGUI { connection.initialized = true }
                        try connection.send(reply)
                    } catch { try connection.send(["error": (error as? ClipboardFailure)?.reason ?? "clipboard_write_failed"]) }
                }
            case "read":
                let value = try snapshot()
                do { try writeAll(fd, jsonData(value)) }
                catch { if let files = value["files"] as? [String] { onGUI { retireCopies(files) } }; throw error }
            case "write":
                try writeAll(fd, jsonData(apply(request, echo: true)))
            case "prepare-files":
                let selected = try paths(request["files"])
                try onGUI { try check(); guard selected.allSatisfy({ owned.contains($0) && known.contains($0) }) else { throw ClipboardFailure("unregistered_files") } }
                let copied = try fileJSON("link-cached", values: selected)
                do { try writeAll(fd, jsonData(["paths": copied])) }
                catch { onGUI { retireCopies(copied) }; throw error }
            case "discard":
                let selected = try paths(request["files"])
                try onGUI {
                    guard selected.allSatisfy({ owned.contains($0) }) else { throw ClipboardFailure("unregistered_files") }
                    retireCopies(selected)
                    let tracked = Set(copies.values.flatMap(\.paths))
                    for file in selected where !tracked.contains(file) {
                        try? FileManager.default.removeItem(atPath: file)
                        owned.remove(file); known.remove(file)
                        // Remove only an empty cache directory owned by this transfer.
                        _ = rmdir((file as NSString).deletingLastPathComponent)
                    }
                }
                try writeAll(fd, jsonData(["ok": true]))
            case "import", "upload":
                let capturedStreams = onGUI { Set(streams.values.map(ObjectIdentifier.init)) }
                let name = operation == "upload" ? request["name"] as? String : nil
                if operation == "upload" && (name == nil || name!.utf8.count > 255) { throw ClipboardFailure("invalid_files") }
                let input = Pipe()
                let (process, output, id, epoch) = try beginTransfer(operation == "upload" ? "save" : "import", input: input.fileHandleForReading, name: name)
                var recorded = false
                defer {
                    try? input.fileHandleForWriting.close()
                    if process.isRunning { process.terminate() }
                    process.waitUntilExit()
                    onGUI { transfers.removeValue(forKey: id); if !recorded { copiesLoaded = false }; collectCopies() }
                }
                try reader.copy(to: input.fileHandleForWriting); try input.fileHandleForWriting.close()
                let data = try output.fileHandleForReading.readToEnd() ?? Data()
                guard data.count <= 65536 else { throw ClipboardFailure("invalid_files") }
                let response = try json(data)
                let imported = try paths(response["paths"])
                onGUI { record(imported, copied: true); rememberCopy(response, clipboard: operation == "import") }
                recorded = true
                do {
                    try finishTransfer(process, id: id, epoch: epoch)
                    try onGUI {
                        if operation == "import", !capturedStreams.isEmpty,
                           capturedStreams.isDisjoint(with: streams.values.map(ObjectIdentifier.init)) { throw ClipboardFailure("connection_closed") }
                    }
                    try writeAll(fd, jsonData(["paths": imported]))
                }
                catch { onGUI { retireCopies(imported) }; throw error }
            case "export":
                let selected = try paths(request["paths"])
                let readerID = UUID()
                try onGUI { try check(); guard selected.allSatisfy({ known.contains($0) }) else { throw ClipboardFailure("unregistered_files") }; fileReaders[readerID] = selected }
                defer { onGUI { fileReaders.removeValue(forKey: readerID); collectCopies() } }
                let input = Pipe()
                let (process, output, id, epoch) = try beginTransfer("export", input: input.fileHandleForReading)
                defer { if process.isRunning { process.terminate() }; onGUI { _ = transfers.removeValue(forKey: id) } }
                try input.fileHandleForWriting.write(contentsOf: JSONSerialization.data(withJSONObject: selected)); try input.fileHandleForWriting.close()
                while let data = try output.fileHandleForReading.read(upToCount: 65536), !data.isEmpty {
                    var count = UInt32(data.count).bigEndian
                    try writeAll(fd, Data(bytes: &count, count: 4)); try writeAll(fd, data)
                }
                var zero: UInt32 = 0
                try writeAll(fd, Data(bytes: &zero, count: 4))
                do { try finishTransfer(process, id: id, epoch: epoch); try writeAll(fd, jsonData(["ok": true])) }
                catch { try writeAll(fd, jsonData(["error": "file_transfer_failed"])) }
            default:
                throw ClipboardFailure("unknown_operation")
            }
        } catch {
            let reason = (error as? ClipboardFailure)?.reason ?? "clipboard_operation_failed"
            diagnostic(reason)
            try? writeAll(fd, jsonData(["error": reason]))
        }
    }
}

private func connect() throws -> Int32 {
    let folder = FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent("Library/Caches/" + label)
    var info = stat()
    guard lstat(folder.path, &info) == 0, info.st_uid == getuid(), info.st_mode & S_IFMT == S_IFDIR, info.st_mode & 0o077 == 0 else { throw ClipboardFailure("clipboard_service_unavailable") }
    let path = folder.appendingPathComponent("clipboard.sock").path
    guard lstat(path, &info) == 0, info.st_uid == getuid(), info.st_mode & S_IFMT == S_IFSOCK, info.st_mode & 0o077 == 0 else { throw ClipboardFailure("clipboard_service_unavailable") }
    var address = sockaddr_un(); address.sun_family = sa_family_t(AF_UNIX)
    let bytes = Array(path.utf8) + [UInt8(0)]
    guard bytes.count <= MemoryLayout.size(ofValue: address.sun_path) else { throw ClipboardFailure("clipboard_service_unavailable") }
    withUnsafeMutableBytes(of: &address.sun_path) { $0.copyBytes(from: bytes) }
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { throw ClipboardFailure("clipboard_service_unavailable") }
    let result = withUnsafePointer(to: &address) { $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) } }
    guard result == 0 else { close(fd); throw ClipboardFailure("clipboard_service_unavailable") }
    return fd
}

private func client(_ operation: String) throws {
    try requireGuest()
    let fd = try connect(); defer { close(fd) }
    let reader = Reader(fd)
    switch operation {
    case "stream":
        try writeAll(fd, jsonData(["operation": "stream"]))
        DispatchQueue.global().async {
            do { try Reader(fd).copy(to: .standardOutput); exit(0) }
            catch { exit(1) }
        }
        try Reader(STDIN_FILENO).copy(to: FileHandle(fileDescriptor: fd, closeOnDealloc: false))
        shutdown(fd, SHUT_WR)
        dispatchMain()
    case "import":
        try writeAll(fd, jsonData(["operation": "import"]))
        try Reader(STDIN_FILENO).copy(to: FileHandle(fileDescriptor: fd, closeOnDealloc: false)); shutdown(fd, SHUT_WR)
        guard let line = try reader.line(), try json(line)["error"] == nil else { throw ClipboardFailure("file_transfer_failed") }
        try writeAll(STDOUT_FILENO, line + Data([10]))
    case "upload":
        let input = Reader(STDIN_FILENO)
        guard let line = try input.line(limit: 4096), let name = try json(line)["name"] as? String, name.utf8.count <= 255 else {
            throw ClipboardFailure("invalid_files")
        }
        try writeAll(fd, jsonData(["operation": "upload", "name": name]))
        try input.copy(to: FileHandle(fileDescriptor: fd, closeOnDealloc: false)); shutdown(fd, SHUT_WR)
        guard let reply = try reader.line(), try json(reply)["error"] == nil else { throw ClipboardFailure("file_transfer_failed") }
        try writeAll(STDOUT_FILENO, reply + Data([10]))
    case "export":
        var data = Data()
        while let chunk = try FileHandle.standardInput.read(upToCount: 65536), !chunk.isEmpty {
            data.append(chunk)
            guard data.count <= 1 << 20 else { throw ClipboardFailure("invalid_files") }
        }
        let selected = try paths(JSONSerialization.jsonObject(with: data))
        try writeAll(fd, jsonData(["operation": "export", "paths": selected])); shutdown(fd, SHUT_WR)
        while true {
            let length = try reader.exact(4).reduce(UInt32(0)) { ($0 << 8) | UInt32($1) }
            if length == 0 { break }
            guard length <= 65536 else { throw ClipboardFailure("file_transfer_failed") }
            try writeAll(STDOUT_FILENO, reader.exact(Int(length)))
        }
        guard let line = try reader.line(), try json(line)["ok"] as? Bool == true else { throw ClipboardFailure("file_transfer_failed") }
    case "request":
        guard let line = try Reader(STDIN_FILENO).line(), let action = try json(line)["operation"] as? String, ["read", "write", "prepare-files", "discard", "copy-prepare", "copy-read"].contains(action) else { throw ClipboardFailure("unknown_operation") }
        try writeAll(fd, line + Data([10])); shutdown(fd, SHUT_WR)
        guard let reply = try reader.line() else { throw ClipboardFailure("clipboard_service_unavailable") }
        try writeAll(STDOUT_FILENO, reply + Data([10]))
        if try json(reply)["error"] != nil { throw ClipboardFailure("clipboard_operation_failed") }
    default: throw ClipboardFailure("unknown_operation")
    }
}

private final class ClipboardTestState: @unchecked Sendable {
    var available = true // Used only on the GUI thread.
    let lock = NSLock()
    var failure: Error?
}

private func testAgentTransport(_ board: NSPasteboard) throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent("cage-clipboard-selftest-" + UUID().uuidString)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: false)
    let previousTemp = getenv("TMPDIR").map { String(cString: $0) }
    setenv("TMPDIR", root.path, 1)
    defer {
        if let previousTemp { setenv("TMPDIR", previousTemp, 1) } else { unsetenv("TMPDIR") }
        try? FileManager.default.removeItem(at: root)
    }
    let state = ClipboardTestState()
    let agent = ClipboardAgent(board: board, gui: { state.available }, diagnostic: { _ in })
    let done = DispatchGroup()
    done.enter()
    DispatchQueue.global(qos: .userInitiated).async {
        defer { done.leave() }
        func connection() throws -> (Int32, DispatchGroup) {
            var sockets: [Int32] = [0, 0]
            guard socketpair(AF_UNIX, SOCK_STREAM, 0, &sockets) == 0 else { throw ClipboardFailure("test_socket_failed") }
            var timeout = timeval(tv_sec: 5, tv_usec: 0)
            setsockopt(sockets[1], SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout.size(ofValue: timeout)))
            let ended = DispatchGroup()
            ended.enter()
            let server = sockets[0]
            DispatchQueue.global().async { agent.serve(server); ended.leave() }
            return (sockets[1], ended)
        }
        func rpc(_ value: [String: Any], bytes: Data = Data()) throws -> [String: Any] {
            let (fd, ended) = try connection()
            defer { close(fd); ended.wait() }
            try writeAll(fd, jsonData(value))
            try writeAll(fd, bytes)
            shutdown(fd, SHUT_WR)
            guard let line = try Reader(fd).line() else { throw ClipboardFailure("test_reply_missing") }
            return try json(line)
        }
        do {
            let prepared = try rpc(["operation": "copy-prepare"])
            let unchanged = try rpc(["operation": "copy-read", "revision": prepared["revision"]!, "session": prepared["session"]!])
            guard unchanged["error"] as? String == "clipboard_copy_not_observed" else { throw ClipboardFailure("test_old_text_returned") }
            let largeText = String(repeating: "x", count: textLimit)
            onGUI { board.clearContents(); board.setString(largeText, forType: .string) }
            let fresh = try rpc(["operation": "copy-read", "revision": prepared["revision"]!, "session": prepared["session"]!])
            guard fresh["text"] as? String == largeText, fresh["session"] as? String == prepared["session"] as? String else { throw ClipboardFailure("test_web_copy_failed") }
            let (fd, ended) = try connection()
            defer { close(fd); ended.wait() }
            let reader = Reader(fd)
            try writeAll(fd, jsonData(["operation": "stream"]))
            guard let first = try reader.line(), let hello = try json(first)["hello"] as? [String: Any],
                  hello["os"] as? String == "darwin", hello["available"] as? Bool == true else { throw ClipboardFailure("test_hello_failed") }
            try writeAll(fd, jsonData(["text": "stream text 中文", "serial": 9]))
            guard let acknowledged = try reader.line(), try json(acknowledged)["ack"] as? Int == 9 else { throw ClipboardFailure("test_ack_failed") }
            onGUI { board.clearContents(); board.setString("guest pushed text", forType: .string); agent.poll() }
            guard let pushed = try reader.line(), try json(pushed)["text"] as? String == "guest pushed text" else { throw ClipboardFailure("test_push_failed") }

            var bytes = Data([0, 255, 128, 10])
            var length = UInt32(bytes.count).littleEndian
            bytes = Data(bytes: &length, count: 4) + bytes + Data(repeating: 0, count: 4)
            let saved = try rpc(["operation": "upload", "name": "中文 ' .bin"], bytes: bytes)
            guard let files = saved["paths"] as? [String], files.count == 1,
                  try Data(contentsOf: URL(fileURLWithPath: files[0])) == Data([0, 255, 128, 10]) else { throw ClipboardFailure("test_upload_failed") }
            let accepted = try rpc(["operation": "write", "files": files, "serial": 10])
            guard accepted["ack"] as? Int == 10 else { throw ClipboardFailure("test_file_paste_failed") }
            let copied = try rpc(["operation": "prepare-files", "files": files])
            guard let duplicates = copied["paths"] as? [String], duplicates.count == 1,
                  try Data(contentsOf: URL(fileURLWithPath: duplicates[0])) == Data([0, 255, 128, 10]) else { throw ClipboardFailure("test_copy_failed") }
            let denied = try rpc(["operation": "write", "files": ["/etc/passwd"]])
            guard denied["error"] as? String == "unregistered_files" else { throw ClipboardFailure("test_unregistered_file_accepted") }
            let discarded = try rpc(["operation": "discard", "files": duplicates])
            let cleanupDeadline = Date().addingTimeInterval(5)
            while FileManager.default.fileExists(atPath: duplicates[0]), Date() < cleanupDeadline {
                Thread.sleep(forTimeInterval: 0.01)
            }
            guard discarded["ok"] as? Bool == true, !FileManager.default.fileExists(atPath: duplicates[0]) else { throw ClipboardFailure("test_discard_failed") }
            onGUI { state.available = false; agent.poll() }
            guard try reader.line() == nil else { throw ClipboardFailure("test_old_session_survived") }
        } catch {
            state.lock.lock(); state.failure = error; state.lock.unlock()
        }
    }
    let deadline = Date().addingTimeInterval(30)
    while done.wait(timeout: .now()) != .success && Date() < deadline {
        _ = RunLoop.current.run(mode: .default, before: Date().addingTimeInterval(0.01))
    }
    guard done.wait(timeout: .now()) == .success else { throw ClipboardFailure("test_transport_timeout") }
    state.lock.lock(); let error = state.failure; state.lock.unlock()
    if let error { throw error }
}

private func selfTest() throws {
    let board = NSPasteboard(name: NSPasteboard.Name("cage-clipboard-qa-" + UUID().uuidString))
    defer { board.releaseGlobally() }
    let text = "中文🙂\nexact text"
    try setBoard(board, ["text": text]); guard try boardContent(board)["text"] as? String == text else { throw ClipboardFailure("text_test_failed") }
    let bitmap = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: 11, pixelsHigh: 7, bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false, colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    let value = bitmap.representation(using: .png, properties: [:])!
    try setBoard(board, ["png": value.base64EncodedString()]); let actual = try boardContent(board)
    guard let encoded = actual["png"] as? String, let data = Data(base64Encoded: encoded), try image(data).width == 11 else { throw ClipboardFailure("image_test_failed") }
    do { try setBoard(board, ["png": "not-png"]); throw ClipboardFailure("invalid_image_accepted") } catch let error as ClipboardFailure { guard error.reason == "invalid_image" else { throw error } }
    do { _ = try paths(["/tmp/../escape"]); throw ClipboardFailure("invalid_files_accepted") } catch let error as ClipboardFailure { guard error.reason == "invalid_files" else { throw error } }
    try testAgentTransport(board)
    print("macOS clipboard: named pasteboard text/image, private socket stream, framed file upload, registered-file checks, cleanup and GUI-session revocation passed")
}

@main
struct GuestClipboard {
    static func main() {
        _ = signal(SIGPIPE, SIG_IGN)
        do {
            if CommandLine.arguments.dropFirst() == ["--self-test"] { try selfTest(); return }
            if CommandLine.arguments.count == 3 && CommandLine.arguments[1] == "--client" { try client(CommandLine.arguments[2]); return }
            try requireGuest()
            let storage = UnsafeMutablePointer<UnsafeMutablePointer<Int32>>.allocate(capacity: 1)
            var count = 0
            guard launch_activate_socket("Clipboard", storage, &count) == 0, count == 1 else { throw ClipboardFailure("clipboard_service_unavailable") }
            let sockets = storage.pointee; storage.deallocate(); let listener = sockets[0]; free(sockets)
            GuestDiagnostic.record("clipboard", "started")
            NSApplication.shared.setActivationPolicy(.accessory)
            let agent = ClipboardAgent()
            let timer = Timer.scheduledTimer(withTimeInterval: 0.15, repeats: true) { _ in agent.poll() }
            DispatchQueue.global(qos: .userInitiated).async {
                let connections = DispatchSemaphore(value: 16)
                while true {
                    connections.wait()
                    let fd = accept(listener, nil, nil)
                    if fd < 0 { connections.signal(); if errno == EINTR { continue }; exit(2) }
                    DispatchQueue.global(qos: .userInitiated).async { agent.serve(fd); connections.signal() }
                }
            }
            NSApplication.shared.run(); timer.invalidate()
        } catch {
            GuestDiagnostic.record("clipboard", (error as? ClipboardFailure)?.reason ?? "clipboard_operation_failed")
            try? writeAll(STDOUT_FILENO, jsonData(["error": (error as? ClipboardFailure)?.reason ?? "clipboard_operation_failed"]))
            exit(1)
        }
    }
}
