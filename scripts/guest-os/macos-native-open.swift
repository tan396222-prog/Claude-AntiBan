import AppKit
import ApplicationServices
import Carbon
import Security
import UniformTypeIdentifiers

// These attributes come from the AppleEvent manager, never from the socket RPC.
struct MacOpenSender {
    let audit: Data
    let pid: pid_t

    init?(_ event: NSAppleEventDescriptor) {
        guard let data = event.attributeDescriptor(forKeyword: AEKeyword(keySenderAuditTokenAttr))?.data,
              data.count == MemoryLayout<audit_token_t>.size else { return nil }
        var token = audit_token_t()
        _ = withUnsafeMutableBytes(of: &token) { data.copyBytes(to: $0) }
        let process = audit_token_to_pid(token)
        guard process > 0, audit_token_to_euid(token) == geteuid(),
              event.attributeDescriptor(forKeyword: AEKeyword(keySenderPIDAttr))?.int32Value == process else { return nil }
        audit = data
        pid = process
    }

    func isOfficialClaude() -> Bool {
        guard let application = NSRunningApplication(processIdentifier: pid), !application.isTerminated,
              application.bundleIdentifier == "com.anthropic.claudefordesktop",
              application.bundleURL?.resolvingSymlinksInPath() == URL(fileURLWithPath: "/Applications/Claude.app").resolvingSymlinksInPath(),
              let requirement = try? claudeSigningRequirement() else { return false }
        var code: SecCode?
        guard SecCodeCopyGuestWithAttributes(nil, [kSecGuestAttributeAudit as String: audit] as CFDictionary, [], &code) == errSecSuccess,
              let code else { return false }
        return SecCodeCheckValidity(code, [], requirement) == errSecSuccess
    }
}

@MainActor
final class MacNativeOpen: NSObject {
    static let shared = MacNativeOpen()
    private(set) var openWithMetadata: Data?

    func install() {
        NSAppleEventManager.shared().setEventHandler(self, andSelector: #selector(openDocuments(_:reply:)),
            forEventClass: AEEventClass(kCoreEventClass), andEventID: AEEventID(kAEOpenDocuments))
    }

    static func fileURLs(_ event: NSAppleEventDescriptor) throws -> [URL] {
        guard let direct = event.paramDescriptor(forKeyword: AEKeyword(keyDirectObject)),
              (1...32).contains(direct.numberOfItems) else { throw Failure(8, "file_open_request_invalid") }
        return try (1...direct.numberOfItems).map { index in
            guard let descriptor = direct.atIndex(index)?.coerce(toDescriptorType: DescType(typeFileURL)),
                  descriptor.data.count <= 98304,
                  let url = URL(dataRepresentation: descriptor.data, relativeTo: nil), url.isFileURL,
                  url.host == nil || url.host == "" || url.host == "localhost",
                  url.user == nil, url.password == nil, url.query == nil, url.fragment == nil,
                  let encodedPath = URLComponents(url: url, resolvingAgainstBaseURL: false)?.percentEncodedPath,
                  let decodedPath = encodedPath.removingPercentEncoding, !decodedPath.contains("\0"),
                  url.path.hasPrefix("/"), url.path.utf8.count <= 32768 else {
                throw Failure(8, "file_open_request_invalid")
            }
            return url.standardizedFileURL
        }
    }

    static func selfTest() throws {
        func event(_ values: [String]) -> NSAppleEventDescriptor {
            let request = NSAppleEventDescriptor.appleEvent(withEventClass: AEEventClass(kCoreEventClass),
                eventID: AEEventID(kAEOpenDocuments), targetDescriptor: nil,
                returnID: AEReturnID(kAutoGenerateReturnID), transactionID: AETransactionID(kAnyTransactionID))
            let list = NSAppleEventDescriptor.list()
            for (index, value) in values.enumerated() {
                list.insert(NSAppleEventDescriptor(descriptorType: DescType(typeFileURL), data: Data(value.utf8))!, at: index + 1)
            }
            request.setParam(list, forKeyword: AEKeyword(keyDirectObject))
            return request
        }
        let valid = event(["file:///tmp/cage-open-self-test/a%20b.txt", "file://localhost/tmp/cage-open-self-test/c.txt"])
        guard try fileURLs(valid).map(\.path) == ["/tmp/cage-open-self-test/a b.txt", "/tmp/cage-open-self-test/c.txt"],
              MacOpenSender(valid) == nil else { throw Failure(8, "file_open_self_test_valid_paths") }
        let invalid = [[], ["https://example.invalid/file"], ["file://example.invalid/tmp/a"],
            ["file:///tmp/a?query=1"], ["file:///tmp/a#fragment"], ["file:///tmp/a%00b"],
            Array(repeating: "file:///tmp/cage-open-self-test/a", count: 33)]
        for (index, values) in invalid.enumerated() {
            if (try? fileURLs(event(values))) != nil { throw Failure(8, "file_open_self_test_invalid_\(index)") }
        }
        guard try fileURLs(event(Array(repeating: "file:///tmp/cage-open-self-test/a", count: 32))).count == 32 else {
            throw Failure(8, "file_open_self_test_failed")
        }
        valid.setAttribute(NSAppleEventDescriptor(int32: getpid()), forKeyword: AEKeyword(keySenderPIDAttr))
        valid.setAttribute(NSAppleEventDescriptor(descriptorType: DescType(typeAuditToken), data: Data(repeating: 0, count: 32))!,
            forKeyword: AEKeyword(keySenderAuditTokenAttr))
        guard MacOpenSender(valid) == nil else { throw Failure(8, "file_open_self_test_failed") }
        print("macOS native open parser and untrusted-event checks passed")
    }

    private static func focusedWindow(_ pid: pid_t) -> AXUIElement? {
        var value: CFTypeRef?
        guard AXUIElementCopyAttributeValue(AXUIElementCreateApplication(pid), kAXFocusedWindowAttribute as CFString, &value) == .success,
              let value, CFGetTypeID(value) == AXUIElementGetTypeID() else { return nil }
        return (value as! AXUIElement)
    }

    @objc private func openDocuments(_ event: NSAppleEventDescriptor, reply: NSAppleEventDescriptor) {
        do {
            let urls = try Self.fileURLs(event)
            guard let sender = MacOpenSender(event), sender.isOfficialClaude() else {
                for url in urls { try forwardToGuestApplication(url) }
                return
            }
            let epoch = try MacGUISession.shared.validate()
            guard AXIsProcessTrusted(), let window = Self.focusedWindow(sender.pid) else { throw Failure(7, "file_open_target_changed") }
            let validate = {
                try MacGUISession.shared.validate(epoch)
                guard sender.isOfficialClaude(), let current = Self.focusedWindow(sender.pid), CFEqual(current, window) else {
                    throw Failure(7, "file_open_target_changed")
                }
            }
            try MacDesktopFiles.shared.queueOpen(paths: urls.map(\.path), action: "open", validate: validate)
            let restoreFocus = {
                guard (try? validate()) != nil,
                      NSWorkspace.shared.frontmostApplication?.processIdentifier == getpid() else { return }
                if NSRunningApplication(processIdentifier: sender.pid)?.activate(options: []) != true {
                    GuestDiagnostic.record("files", "file_open_focus_restore_failed")
                }
            }
            restoreFocus()
            DispatchQueue.main.async { restoreFocus() }
        } catch {
            let reason = (error as? Failure)?.message ?? "file_open_failed"
            GuestDiagnostic.record("files", reason)
            reply.setParam(NSAppleEventDescriptor(int32: Int32(errAEEventFailed)), forKeyword: AEKeyword(keyErrorNumber))
            reply.setParam(NSAppleEventDescriptor(string: reason), forKeyword: AEKeyword(keyErrorString))
        }
    }

    private func forwardToGuestApplication(_ url: URL) throws {
        let values = try url.resourceValues(forKeys: [.contentTypeKey, .isDirectoryKey])
        let type = values.isDirectory == true ? UTType.folder : values.contentType ?? UTType.data
        let current = Bundle.main.bundleURL.resolvingSymlinksInPath()
        let preferred = NSWorkspace.shared.urlForApplication(toOpen: type)
        let candidates = [preferred].compactMap { $0 } + NSWorkspace.shared.urlsForApplications(toOpen: type)
        guard let target = candidates.first(where: {
            $0.resolvingSymlinksInPath() != current && Bundle(url: $0)?.bundleIdentifier != Bundle.main.bundleIdentifier
        }) else { throw Failure(8, "file_open_application_unavailable") }
        // An explicit target avoids resolving this file's OpenWith attribute again.
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.addsToRecentItems = false
        NSWorkspace.shared.open([url], withApplicationAt: target, configuration: configuration) { _, error in
            if error != nil { GuestDiagnostic.record("files", "file_open_application_failed") }
        }
    }

    func prepareMetadata() async throws {
        let bundle = Bundle.main.bundleURL.resolvingSymlinksInPath()
        guard bundle.path == "/Applications/Cage Guest Window.app", Bundle.main.bundleIdentifier == "dev.cage.GuestClaudeWindow",
              LSRegisterURL(bundle as CFURL, true) == noErr else { throw Failure(8, "file_open_registration_failed") }
        let cache = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Library/Caches/dev.cage.GuestClaudeWindow", isDirectory: true)
        var attributes = stat()
        guard lstat(cache.path, &attributes) == 0, attributes.st_mode & S_IFMT == S_IFDIR,
              attributes.st_uid == geteuid(), attributes.st_mode & 0o077 == 0 else { throw Failure(8, "file_open_cache_invalid") }
        let fixture = cache.appendingPathComponent("openwith-\(UUID().uuidString.lowercased()).txt")
        let fd = open(fixture.path, O_CREAT | O_EXCL | O_WRONLY | O_NOFOLLOW, 0o600)
        guard fd >= 0 else { throw Failure(8, "file_open_metadata_failed") }
        close(fd)
        defer { try? FileManager.default.removeItem(at: fixture) }
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            NSWorkspace.shared.setDefaultApplication(at: bundle, toOpenFileAt: fixture) { error in
                if let error { continuation.resume(throwing: error) }
                else { continuation.resume() }
            }
        }
        guard NSWorkspace.shared.urlForApplication(toOpen: fixture)?.resolvingSymlinksInPath() == bundle else {
            throw Failure(8, "file_open_registration_failed")
        }
        let name = "com.apple.LaunchServices.OpenWith"
        let length = getxattr(fixture.path, name, nil, 0, 0, XATTR_NOFOLLOW)
        guard (1...8192).contains(length) else { throw Failure(8, "file_open_metadata_failed") }
        var bytes = [UInt8](repeating: 0, count: length)
        guard getxattr(fixture.path, name, &bytes, bytes.count, 0, XATTR_NOFOLLOW) == length else {
            throw Failure(8, "file_open_metadata_failed")
        }
        let temporary = cache.appendingPathComponent("openwith-\(UUID().uuidString.lowercased()).data")
        let output = open(temporary.path, O_WRONLY | O_CREAT | O_EXCL | O_NOFOLLOW, 0o600)
        guard output >= 0 else { throw Failure(8, "file_open_metadata_failed") }
        let file = FileHandle(fileDescriptor: output, closeOnDealloc: true)
        defer { try? FileManager.default.removeItem(at: temporary) }
        try file.write(contentsOf: Data(bytes))
        try file.synchronize()
        try file.close()
        guard rename(temporary.path, cache.appendingPathComponent("openwith.data").path) == 0 else {
            throw Failure(8, "file_open_metadata_failed")
        }
        openWithMetadata = Data(bytes)
    }
}
