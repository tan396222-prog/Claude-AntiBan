// Attachments are bound to one verified Claude window and private guest caches.
import AppKit
import ScreenCaptureKit

private func fileAttribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
    var value: CFTypeRef?
    return AXUIElementCopyAttributeValue(element, name as CFString, &value) == .success ? value : nil
}

private func fileChildren(_ element: AXUIElement) -> [AXUIElement] {
    (fileAttribute(element, kAXChildrenAttribute) as? [AXUIElement] ?? []) +
        (fileAttribute(element, "AXSheets") as? [AXUIElement] ?? [])
}

private func filePanelButton(_ panel: AXUIElement, cancel: Bool = false) -> AXUIElement? {
    if let value = fileAttribute(panel, cancel ? kAXCancelButtonAttribute : kAXDefaultButtonAttribute), CFGetTypeID(value) == AXUIElementGetTypeID() {
        return (value as! AXUIElement)
    }
    var pending = fileChildren(panel), visited = 0
    while visited < pending.count && visited < 1024 {
        let element = pending[visited]; visited += 1
        if fileAttribute(element, kAXRoleAttribute) as? String == kAXButtonRole,
           fileAttribute(element, "AXIdentifier") as? String == (cancel ? "CancelButton" : "OKButton") { return element }
        pending.append(contentsOf: fileChildren(element))
    }
    return nil
}

private func filePanelPresent(_ panel: AXUIElement, pid: pid_t) -> Bool {
    var pending = fileAttribute(AXUIElementCreateApplication(pid), kAXWindowsAttribute) as? [AXUIElement] ?? [], visited = 0
    while visited < pending.count && visited < 1024 {
        let element = pending[visited]; visited += 1
        if CFEqual(element, panel) { return true }
        pending.append(contentsOf: fileChildren(element))
    }
    return false
}

private func filePanelFocused(_ panel: AXUIElement, pid: pid_t) -> Bool {
    var current = fileAttribute(AXUIElementCreateApplication(pid), kAXFocusedUIElementAttribute)
    for _ in 0..<32 {
        guard let value = current, CFGetTypeID(value) == AXUIElementGetTypeID() else { return false }
        let element = value as! AXUIElement
        if CFEqual(element, panel) { return true }
        current = fileAttribute(element, kAXParentAttribute)
    }
    return false
}

private func containsFileBrowser(_ element: AXUIElement) -> Bool {
    var pending = fileChildren(element)
    var visited = 0
    while !pending.isEmpty && visited < 1024 {
        let child = pending.removeLast()
        visited += 1
        if [kAXOutlineRole, kAXBrowserRole, kAXTableRole].contains(fileAttribute(child, kAXRoleAttribute) as? String ?? "") { return true }
        pending.append(contentsOf: fileChildren(child))
    }
    return false
}

private func selectedFilePaths(_ panel: AXUIElement, expected: Set<String>) -> Set<String> {
    var pending = [panel], selected: [AXUIElement] = []
    var visited = 0
    while !pending.isEmpty && visited < 2048 {
        let element = pending.removeLast()
        visited += 1
        selected.append(contentsOf: fileAttribute(element, kAXSelectedRowsAttribute) as? [AXUIElement] ?? [])
        selected.append(contentsOf: fileAttribute(element, kAXSelectedChildrenAttribute) as? [AXUIElement] ?? [])
        pending.append(contentsOf: fileChildren(element))
    }
    var paths = Set<String>()
    func selectedPath(_ value: String) {
        let path: String
        if let canonical = realpath(value, nil) {
            path = String(cString: canonical); free(canonical)
        } else { path = URL(fileURLWithPath: value).standardizedFileURL.path }
        let prefix = path.hasSuffix("/") ? path : path + "/"
        // Column view also selects each ancestor directory. Every other
        // selected file must participate in the exact selection comparison.
        if expected.contains(where: { $0.hasPrefix(prefix) }) { return }
        paths.insert(path)
    }
    visited = 0
    while !selected.isEmpty && visited < 4096 {
        let element = selected.removeLast()
        visited += 1
        for attribute in [kAXURLAttribute, kAXValueAttribute] {
            let raw = fileAttribute(element, attribute)
            if let url = raw as? URL, url.isFileURL { selectedPath(url.path) }
            if let value = raw as? String {
                if value.hasPrefix("/") { selectedPath(value) }
                if let url = URL(string: value), url.isFileURL { selectedPath(url.path) }
            }
        }
        selected.append(contentsOf: fileChildren(element))
    }
    return paths
}

private func openFilePanel(_ pid: pid_t) -> AXUIElement? {
    var pending = fileAttribute(AXUIElementCreateApplication(pid), kAXWindowsAttribute) as? [AXUIElement] ?? []
    var visited = 0
    while !pending.isEmpty && visited < 2048 {
        let element = pending.removeLast()
        visited += 1
        let role = fileAttribute(element, kAXRoleAttribute) as? String
        let subrole = fileAttribute(element, kAXSubroleAttribute) as? String
        if role == kAXSheetRole || subrole == kAXDialogSubrole,
           let button = filePanelButton(element), filePanelButton(element, cancel: true) != nil,
           let title = fileAttribute(button, kAXTitleAttribute) as? String,
           ["Open", "Choose", "Select", "打开", "開啟", "选择", "選擇"].contains(title), containsFileBrowser(element) {
            return element
        }
        pending.append(contentsOf: fileChildren(element))
    }
    return nil
}

private func saveFilePanel(_ pid: pid_t) -> (AXUIElement, AXUIElement)? {
    var pending = fileAttribute(AXUIElementCreateApplication(pid), kAXWindowsAttribute) as? [AXUIElement] ?? []
    var visited = 0
    while !pending.isEmpty && visited < 2048 {
        let panel = pending.removeLast()
        visited += 1
        let role = fileAttribute(panel, kAXRoleAttribute) as? String
        let subrole = fileAttribute(panel, kAXSubroleAttribute) as? String
        if (role == kAXSheetRole || subrole == kAXDialogSubrole),
           let button = filePanelButton(panel),
           let title = fileAttribute(button, kAXTitleAttribute) as? String,
           ["Save", "保存", "儲存", "Enregistrer", "Sichern", "Guardar", "Сохранить", "저장"].contains(title),
           filePanelButton(panel, cancel: true) != nil {
            var children = fileChildren(panel), count = 0
            while count < children.count && count < 1024 {
                let element = children[count]
                count += 1
                if fileAttribute(element, kAXRoleAttribute) as? String == kAXTextFieldRole,
                   fileAttribute(element, "AXIdentifier") as? String == "saveAsNameTextField",
                   let name = fileAttribute(element, kAXValueAttribute) as? String, MacDesktopDownloads.validName(name) {
                    return (panel, element)
                }
                children.append(contentsOf: fileChildren(element))
            }
        }
        pending.append(contentsOf: fileChildren(panel))
    }
    return nil
}

private func clipboardRequest(_ request: [String: Any]) async throws -> [String: Any] {
    let data = try JSONSerialization.data(withJSONObject: request) + Data([10])
    guard data.count <= 30 * 1024 * 1024 else { throw Failure(8, "attachment_size_limit") }
    return try await withCheckedThrowingContinuation { continuation in
        DispatchQueue.global(qos: .userInitiated).async {
            let process = Process()
            process.executableURL = URL(fileURLWithPath: "/Applications/Cage Guest Clipboard.app/Contents/MacOS/CageGuestClipboard")
            process.arguments = ["--client", "request"]
            let input = Pipe(), output = Pipe()
            process.standardInput = input
            process.standardOutput = output
            process.standardError = FileHandle.nullDevice
            let timeout = DispatchWorkItem { if process.isRunning { process.terminate() } }
            defer { timeout.cancel(); try? input.fileHandleForWriting.close(); if process.isRunning { process.terminate() } }
            do {
                try process.run()
                DispatchQueue.global().asyncAfter(deadline: .now() + 15, execute: timeout)
                try input.fileHandleForWriting.write(contentsOf: data)
                try input.fileHandleForWriting.close()
                var reply = Data()
                while let chunk = try output.fileHandleForReading.read(upToCount: 4096), !chunk.isEmpty {
                    reply.append(chunk)
                    guard reply.count <= 65536 else { throw Failure(7, "clipboard_reply_invalid") }
                }
                process.waitUntilExit()
                guard process.terminationStatus == 0,
                      let value = try JSONSerialization.jsonObject(with: reply) as? [String: Any], value["error"] == nil else {
                    throw Failure(7, "clipboard_operation_failed")
                }
                continuation.resume(returning: value)
            } catch { continuation.resume(throwing: error) }
        }
    }
}

@MainActor
final class MacDesktopFiles {
    static let shared = MacDesktopFiles()
    private final class Batch {
        let pid: pid_t
        let window: CGWindowID
        let kind: String
        let panel: AXUIElement?
        let focusedWindow: AXUIElement?
        let focusedElement: AXUIElement?
        let epoch: UInt64
        let created = Date()
        var files: [String] = []
        var busy = false
        init(pid: pid_t, window: CGWindowID, kind: String, epoch: UInt64, panel: AXUIElement? = nil) {
            self.pid = pid; self.window = window; self.kind = kind; self.panel = panel
            self.epoch = epoch
            if let value = fileAttribute(AXUIElementCreateApplication(pid), kAXFocusedWindowAttribute), CFGetTypeID(value) == AXUIElementGetTypeID() {
                focusedWindow = (value as! AXUIElement)
            } else { focusedWindow = nil }
            if let value = fileAttribute(AXUIElementCreateApplication(pid), kAXFocusedUIElementAttribute), CFGetTypeID(value) == AXUIElementGetTypeID() {
                focusedElement = (value as! AXUIElement)
            } else { focusedElement = nil }
        }
    }
    private var batches: [String: Batch] = [:]
    private var savingPanel: AXUIElement?
    private struct OpenRequest {
        let path: String
        let action: String
        let epoch: UInt64
        let validate: () throws -> Void
        let created = Date()
        var claimed = false
    }
    private var opens: [String: OpenRequest] = [:]

    private func expireOpens() {
        let epoch = try? MacGUISession.shared.validate()
        opens = opens.filter { _, request in
            epoch == request.epoch && Date().timeIntervalSince(request.created) < (request.claimed ? 1800 : 60)
        }
    }

    func queueOpen(path: String, action: String, validate: @escaping () throws -> Void) throws {
        try queueOpen(paths: [path], action: action, validate: validate)
    }

    func queueOpen(paths: [String], action: String, validate: @escaping () throws -> Void) throws {
        guard !paths.isEmpty, paths.count <= 32, paths.allSatisfy({ ($0 as NSString).isAbsolutePath && $0.utf8.count <= 32768 && !$0.contains("\0") }),
              ["open", "reveal"].contains(action) else { throw Failure(8, "file_open_request_invalid") }
        let epoch = try MacGUISession.shared.validate()
        expireOpens(); try validate()
        guard opens.count + paths.count <= 32 else { throw Failure(8, "file_open_request_limit") }
        for path in paths {
            opens[UUID().uuidString.lowercased()] = OpenRequest(path: URL(fileURLWithPath: path).standardizedFileURL.path, action: action, epoch: epoch, validate: validate)
        }
    }

    func openSnapshot() -> [[String: String]] {
        expireOpens()
        return opens.filter { !$0.value.claimed }.map { ["id": $0.key] }
    }

    func openAction(_ id: String, complete: Bool) throws -> [String: Any] {
        expireOpens()
        guard var request = opens[id] else { return [complete ? "ok" : "handled": true] }
        if complete {
            opens.removeValue(forKey: id)
            try? request.validate()
            return ["ok": true]
        }
        if request.claimed { return ["handled": true] }
        do { try request.validate() }
        catch { opens.removeValue(forKey: id); throw Failure(7, "file_open_target_changed") }
        var result: [String: Any] = ["id": id, "action": request.action]
        if let download = try MacDesktopDownloads.shared.find(path: request.path) {
            result["download_id"] = download.id; result["directory"] = download.directory
        } else { result["path"] = request.path }
        request.claimed = true; opens[id] = request
        return result
    }

    private func valid(_ batch: Batch) throws {
        try consoleUser()
        try MacGUISession.shared.validate(batch.epoch)
        guard AXIsProcessTrusted() else { throw Failure(9, "accessibility_permission") }
        guard Date().timeIntervalSince(batch.created) < 1800, try claudePIDs().contains(batch.pid),
              NSWorkspace.shared.frontmostApplication?.processIdentifier == batch.pid else { throw Failure(7, "window_changed") }
        if let panel = batch.panel {
            guard filePanelPresent(panel, pid: batch.pid), filePanelFocused(panel, pid: batch.pid) else { throw Failure(7, "file_dialog_changed") }
        } else {
            let windows = CGWindowListCopyWindowInfo([.optionIncludingWindow], batch.window) as? [[String: Any]]
            guard let window = windows?.first, window[kCGWindowOwnerPID as String] as? Int32 == batch.pid,
                  window[kCGWindowIsOnscreen as String] as? Bool == true else { throw Failure(7, "window_changed") }
            guard let focused = fileAttribute(AXUIElementCreateApplication(batch.pid), kAXFocusedWindowAttribute),
                  let original = batch.focusedWindow, CFEqual(focused, original),
                  let input = fileAttribute(AXUIElementCreateApplication(batch.pid), kAXFocusedUIElementAttribute),
                  let originalInput = batch.focusedElement, CFEqual(input, originalInput),
                  let position = fileAttribute(original, kAXPositionAttribute), CFGetTypeID(position) == AXValueGetTypeID(),
                  let size = fileAttribute(original, kAXSizeAttribute), CFGetTypeID(size) == AXValueGetTypeID(),
                  let bounds = window[kCGWindowBounds as String] as? [String: Any],
                  let frame = CGRect(dictionaryRepresentation: bounds as CFDictionary) else { throw Failure(7, "window_changed") }
            var origin = CGPoint.zero, extent = CGSize.zero
            guard AXValueGetValue(position as! AXValue, .cgPoint, &origin), AXValueGetValue(size as! AXValue, .cgSize, &extent),
                  abs(origin.x - frame.minX) < 2, abs(origin.y - frame.minY) < 2,
                  abs(extent.width - frame.width) < 2, abs(extent.height - frame.height) < 2 else { throw Failure(7, "window_changed") }
        }
    }

    private func batch(_ id: String) throws -> Batch {
        guard UUID(uuidString: id) != nil, let value = batches[id] else { throw Failure(8, "attachment_request_invalid") }
        try valid(value)
        return value
    }

    private func key(_ pid: pid_t, _ code: CGKeyCode, flags: CGEventFlags = [], panel: AXUIElement? = nil, epoch: UInt64? = nil) throws {
        guard AXIsProcessTrusted(), try claudePIDs().contains(pid), NSWorkspace.shared.frontmostApplication?.processIdentifier == pid else {
            throw Failure(7, "window_changed")
        }
        let source = CGEventSource(stateID: .privateState)
        for down in [true, false] {
            try consoleUser()
            try MacGUISession.shared.validate(epoch)
            guard NSWorkspace.shared.frontmostApplication?.processIdentifier == pid,
                  panel.map({ filePanelPresent($0, pid: pid) && filePanelFocused($0, pid: pid) }) ?? true else { throw Failure(7, "file_dialog_changed") }
            guard let event = CGEvent(keyboardEventSource: source, virtualKey: code, keyDown: down) else { throw Failure(8, "input_failed") }
            event.flags = flags
            if panel != nil {
                // AppKit's Open/Save panel runs in an XPC service. Events sent
                // only to the application PID do not reach its focused panel.
                guard CGPreflightPostEventAccess() else { throw Failure(9, "accessibility_permission") }
                event.post(tap: .cghidEventTap)
            } else { event.postToPid(pid) }
        }
    }

    func start(window: SCWindow, kind: String) throws -> [String: Any] {
        guard ["paste", "drop", "image"].contains(kind), let pid = window.owningApplication?.processID else { throw Failure(8, "attachment_request_invalid") }
        for (id, value) in batches where Date().timeIntervalSince(value.created) >= 1800 {
            batches.removeValue(forKey: id)
            if !value.files.isEmpty { Task { _ = try? await clipboardRequest(["operation": "discard", "files": value.files]) } }
        }
        guard batches.count < 12 else { throw Failure(8, "attachment_limit") }
        let value = Batch(pid: pid, window: window.windowID, kind: kind, epoch: try MacGUISession.shared.validate())
        try valid(value)
        let id = UUID().uuidString.lowercased()
        batches[id] = value
        return ["ok": true, "id": id]
    }

    func add(_ id: String, files: [String]) throws -> [String: Any] {
        let value = try batch(id)
        guard !value.busy, !files.isEmpty, value.files.count + files.count <= 100 else { throw Failure(8, "attachment_limit") }
        for path in files {
            var info = stat()
            let url = URL(fileURLWithPath: path)
            guard url.standardizedFileURL.path == path, path.hasPrefix("/"), !path.contains("\0"),
                  url.deletingLastPathComponent().lastPathComponent.hasPrefix("cage-clipboard-"),
                  lstat(path, &info) == 0, info.st_mode & S_IFMT == S_IFREG, info.st_uid == getuid(), info.st_size <= 2 * 1024 * 1024 * 1024 else {
                throw Failure(8, "attachment_path_invalid")
            }
        }
        value.files.append(contentsOf: files)
        return ["ok": true, "id": id]
    }

    func validate(_ id: String) throws -> [String: Any] {
        let value = try batch(id)
        guard !value.busy else { throw Failure(8, "attachment_busy") }
        return ["ok": true]
    }

    func cancel(_ id: String) async -> [String: Any] {
        guard let value = batches.removeValue(forKey: id) else { return ["ok": true] }
        if let panel = value.panel, (try? valid(value)) != nil,
           let button = filePanelButton(panel, cancel: true) {
            _ = AXUIElementPerformAction(button, kAXPressAction as CFString)
        }
        if !value.files.isEmpty { _ = try? await clipboardRequest(["operation": "discard", "files": value.files]) }
        return ["ok": true]
    }

    func finish(_ id: String) async throws -> [String: Any] {
        let value = try batch(id)
        guard value.kind != "image", !value.busy, !value.files.isEmpty else { throw Failure(8, "attachment_incomplete") }
        value.busy = true
        defer { value.busy = false }
        if let panel = value.panel {
            // The native panel's Go To Folder action selects the uploaded file
            // without asking the guest to read a caller-selected arbitrary path.
            let copied = try await clipboardRequest(["operation": "prepare-files", "files": value.files])
            guard let files = copied["paths"] as? [String], files.count == value.files.count, !files.isEmpty else { throw Failure(7, "clipboard_reply_invalid") }
            guard batches[id] === value else {
                _ = try? await clipboardRequest(["operation": "discard", "files": files])
                throw Failure(8, "attachment_cancelled")
            }
            let previous = value.files
            value.files = files
            _ = try? await clipboardRequest(["operation": "discard", "files": previous])
            guard batches[id] === value else { throw Failure(8, "attachment_cancelled") }
            try valid(value)
            try key(value.pid, 5, flags: [.maskCommand, .maskShift], panel: panel, epoch: value.epoch)
            let app = AXUIElementCreateApplication(value.pid)
            var field: AXUIElement?
            for _ in 0..<20 {
                try await Task.sleep(nanoseconds: 100_000_000)
                guard batches[id] === value else { throw Failure(8, "attachment_cancelled") }
                try valid(value)
                if let focus = fileAttribute(app, kAXFocusedUIElementAttribute), CFGetTypeID(focus) == AXUIElementGetTypeID(),
                   [kAXTextFieldRole, kAXComboBoxRole].contains(fileAttribute(focus as! AXUIElement, kAXRoleAttribute) as? String ?? "") {
                    field = (focus as! AXUIElement); break
                }
            }
            let destination = files.count == 1 ? files[0] : (files[0] as NSString).deletingLastPathComponent
            guard let field, AXUIElementSetAttributeValue(field, kAXValueAttribute as CFString, destination as CFString) == .success else {
                throw Failure(7, "file_dialog_navigation_failed")
            }
            try key(value.pid, 36, panel: panel, epoch: value.epoch)
            var resolved = false
            for _ in 0..<20 {
                try await Task.sleep(nanoseconds: 100_000_000)
                guard batches[id] === value else { throw Failure(8, "attachment_cancelled") }
                try valid(value)
                if let focus = fileAttribute(app, kAXFocusedUIElementAttribute), !CFEqual(focus, field) { resolved = true; break }
            }
            guard resolved else { throw Failure(7, "file_dialog_navigation_failed") }
            if files.count > 1 {
                try key(value.pid, 0, flags: .maskCommand, panel: panel, epoch: value.epoch)
                try await Task.sleep(nanoseconds: 150_000_000)
                guard batches[id] === value else { throw Failure(8, "attachment_cancelled") }
                try valid(value)
            }
            let expected = try Set(files.map { path -> String in
                guard let resolved = realpath(path, nil) else { throw Failure(7, "file_dialog_selection_mismatch") }
                defer { free(resolved) }
                return String(cString: resolved)
            })
            guard selectedFilePaths(panel, expected: expected) == expected else { throw Failure(7, "file_dialog_selection_mismatch") }
            guard let button = filePanelButton(panel),
                  fileAttribute(button, kAXEnabledAttribute) as? Bool == true,
                  AXUIElementPerformAction(button, kAXPressAction as CFString) == .success else {
                throw Failure(7, "file_dialog_navigation_failed")
            }
            var accepted = false
            for _ in 0..<20 {
                try await Task.sleep(nanoseconds: 100_000_000)
                if let current = openFilePanel(value.pid), CFEqual(current, panel) { continue }
                accepted = true; break
            }
            guard accepted else { throw Failure(7, "file_dialog_still_open") }
        } else {
            let written = try await clipboardRequest(["operation": "write", "files": value.files])
            guard batches[id] === value, let revision = written["revision"] as? Int, NSPasteboard.general.changeCount == revision else {
                throw Failure(8, "attachment_cancelled")
            }
            try valid(value)
            try key(value.pid, 9, flags: .maskCommand)
        }
        batches.removeValue(forKey: id)
        return ["ok": true]
    }

    private func save(_ panel: AXUIElement, field: AXUIElement, pid: pid_t, path: String, epoch: UInt64) async throws {
        func validate() throws {
            try MacGUISession.shared.validate(epoch)
            guard AXIsProcessTrusted(), try claudePIDs().contains(pid), NSWorkspace.shared.frontmostApplication?.processIdentifier == pid,
                  filePanelPresent(panel, pid: pid), filePanelFocused(panel, pid: pid) else { throw Failure(7, "file_dialog_changed") }
        }
        try validate()
        try key(pid, 5, flags: [.maskCommand, .maskShift], panel: panel, epoch: epoch)
        let app = AXUIElementCreateApplication(pid)
        var location: AXUIElement?
        for _ in 0..<20 {
            try await Task.sleep(nanoseconds: 100_000_000)
            try validate()
            if let focus = fileAttribute(app, kAXFocusedUIElementAttribute), CFGetTypeID(focus) == AXUIElementGetTypeID(),
               !CFEqual(focus, field), [kAXTextFieldRole, kAXComboBoxRole].contains(fileAttribute(focus as! AXUIElement, kAXRoleAttribute) as? String ?? "") {
                location = (focus as! AXUIElement); break
            }
        }
        guard let location, AXUIElementSetAttributeValue(location, kAXValueAttribute as CFString, (path as NSString).deletingLastPathComponent as CFString) == .success else {
            throw Failure(7, "file_dialog_navigation_failed")
        }
        try key(pid, 36, panel: panel, epoch: epoch)
        var resolved = false
        for _ in 0..<20 {
            try await Task.sleep(nanoseconds: 100_000_000)
            try validate()
            if let focus = fileAttribute(app, kAXFocusedUIElementAttribute), !CFEqual(focus, location) { resolved = true; break }
        }
        guard resolved, AXUIElementSetAttributeValue(field, kAXValueAttribute as CFString, (path as NSString).lastPathComponent as CFString) == .success,
              let button = filePanelButton(panel),
              fileAttribute(button, kAXEnabledAttribute) as? Bool == true else { throw Failure(7, "file_dialog_navigation_failed") }
        try validate()
        guard AXUIElementPerformAction(button, kAXPressAction as CFString) == .success else { throw Failure(7, "file_dialog_navigation_failed") }
        for _ in 0..<20 {
            try await Task.sleep(nanoseconds: 100_000_000)
            if let current = saveFilePanel(pid), CFEqual(current.0, panel) { continue }
            return
        }
        throw Failure(7, "file_dialog_still_open")
    }

    func poll() async throws -> [String: Any] {
        let epoch = try MacGUISession.shared.validate()
        for (id, value) in batches where value.epoch != epoch || Date().timeIntervalSince(value.created) >= 1800 {
            batches.removeValue(forKey: id)
            if !value.files.isEmpty { Task { _ = try? await clipboardRequest(["operation": "discard", "files": value.files]) } }
        }
        guard AXIsProcessTrusted(), let pid = try? claudePIDs().first else {
            return ["available": false, "uploads": [], "downloads": try MacDesktopDownloads.shared.snapshot(), "downloadAck": true, "opens": openSnapshot()]
        }
        if let (panel, field) = saveFilePanel(pid) {
            if savingPanel == nil || !CFEqual(savingPanel!, panel) {
                savingPanel = panel
                let name = fileAttribute(field, kAXValueAttribute) as? String ?? ""
                try await MacDesktopDownloads.shared.reserve(name: name, pid: pid) { path in
                    try await self.save(panel, field: field, pid: pid, path: path, epoch: epoch)
                }
            }
        } else { savingPanel = nil }
        guard let panel = openFilePanel(pid) else {
            for (id, value) in batches where value.panel != nil && !value.busy {
                batches.removeValue(forKey: id)
                if !value.files.isEmpty { Task { _ = try? await clipboardRequest(["operation": "discard", "files": value.files]) } }
            }
            return ["available": true, "uploads": [], "downloads": try MacDesktopDownloads.shared.snapshot(), "downloadAck": true, "opens": openSnapshot()]
        }
        let existing = batches.first { $0.value.pid == pid && $0.value.panel.map { CFEqual($0, panel) } == true }
        let id: String
        if let existing { id = existing.key }
        else {
            guard batches.count < 12 else { throw Failure(8, "attachment_limit") }
            id = UUID().uuidString.lowercased()
            batches[id] = Batch(pid: pid, window: 0, kind: "chooser", epoch: try MacGUISession.shared.validate(), panel: panel)
        }
        return ["available": true, "uploads": [["id": id, "kind": "chooser", "multiple": true, "directory": false, "target": "desktop"]], "downloads": try MacDesktopDownloads.shared.snapshot(), "downloadAck": true, "opens": openSnapshot()]
    }

    func pasteImage(_ data: Data, window: SCWindow) async throws -> [String: Any] {
        guard data.count <= 20 * 1024 * 1024, let pid = window.owningApplication?.processID else { throw Failure(8, "attachment_size_limit") }
        let value = Batch(pid: pid, window: window.windowID, kind: "paste", epoch: try MacGUISession.shared.validate())
        return try await deliverImage(data, batch: value)
    }

    func finishImage(_ id: String) async throws -> [String: Any] {
        let value = try batch(id)
        guard value.kind == "image", !value.busy, value.files.count == 1 else { throw Failure(8, "attachment_incomplete") }
        value.busy = true
        do {
            let file = try FileHandle(forReadingFrom: URL(fileURLWithPath: value.files[0]))
            defer { try? file.close() }
            let data = try file.read(upToCount: 20 * 1024 * 1024 + 1) ?? Data()
            guard !data.isEmpty, data.count <= 20 * 1024 * 1024 else { throw Failure(8, "attachment_size_limit") }
            let result = try await deliverImage(data, batch: value)
            _ = await cancel(id)
            return result
        } catch {
            _ = await cancel(id)
            throw error
        }
    }

    private func deliverImage(_ data: Data, batch value: Batch) async throws -> [String: Any] {
        try valid(value)
        let written = try await clipboardRequest(["operation": "write", "png": data.base64EncodedString()])
        guard let revision = written["revision"] as? Int, NSPasteboard.general.changeCount == revision else { throw Failure(8, "attachment_cancelled") }
        try valid(value)
        try key(value.pid, 9, flags: .maskCommand, epoch: value.epoch)
        return ["ok": true]
    }
}
