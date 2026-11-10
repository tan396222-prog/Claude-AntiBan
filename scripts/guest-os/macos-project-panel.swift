// Only a currently pending panel, attested by the installed project access
// agent and addressed by the host's active project grant, may be displayed.
import AppKit
import ScreenCaptureKit
import Security

struct ProjectPanel: Decodable, Equatable {
    let path, nonce: String
    let pid: pid_t
    let windowNumber: CGWindowID
}

private func matchesInstalledProjectApp(pid: pid_t) -> Bool {
    let path = "/Applications/CageProjectAccess.app"
    guard NSRunningApplication(processIdentifier: pid)?.bundleURL?.path == path else { return false }
    let url = URL(fileURLWithPath: path)
    var dynamic: SecCode?, installed: SecStaticCode?, running: SecStaticCode?, requirement: SecRequirement?
    let attributes = [kSecGuestAttributePid: pid] as CFDictionary
    var st = stat()
    guard lstat(path, &st) == 0, st.st_uid == 0, st.st_mode & 0o022 == 0,
          SecRequirementCreateWithString(#"identifier "dev.cage.guest.project-access""# as CFString, [], &requirement) == errSecSuccess,
          let requirement,
          SecCodeCopyGuestWithAttributes(nil, attributes, [], &dynamic) == errSecSuccess, let dynamic,
          SecCodeCheckValidity(dynamic, [], requirement) == errSecSuccess,
          SecCodeCopyStaticCode(dynamic, [], &running) == errSecSuccess, let running,
          SecStaticCodeCreateWithPath(url as CFURL, [], &installed) == errSecSuccess, let installed,
          SecStaticCodeCheckValidity(installed, [], requirement) == errSecSuccess else { return false }
    func hash(_ code: SecStaticCode) -> Data? {
        var info: CFDictionary?
        guard SecCodeCopySigningInformation(code, SecCSFlags(rawValue: kSecCSSigningInformation), &info) == errSecSuccess else { return nil }
        return (info as? [String: Any])?[kSecCodeInfoUnique as String] as? Data
    }
    guard let digest = hash(installed) else { return false }
    return digest == hash(running)
}

func pendingProjectPanel(_ path: String) throws -> ProjectPanel {
    let missing = Failure(7, "no_project_panel")
    let directory = URL(fileURLWithPath: NSHomeDirectory()).appendingPathComponent("Cage/Projects", isDirectory: true)
    let leaf = (path as NSString).lastPathComponent
    guard leaf.range(of: "^[a-f0-9]{24}$", options: .regularExpression) != nil,
          path == directory.appendingPathComponent(leaf).path else { throw missing }
    var addr = sockaddr_un()
    addr.sun_family = sa_family_t(AF_UNIX)
    let socketPath = NSHomeDirectory() + "/Library/Application Support/Cage/project-terminal.sock"
    let maxPath = MemoryLayout.size(ofValue: addr.sun_path)
    guard socketPath.utf8.count < maxPath else { throw missing }
    withUnsafeMutableBytes(of: &addr.sun_path) { dst in socketPath.utf8CString.withUnsafeBytes { dst.copyBytes(from: $0) } }
    let fd = socket(AF_UNIX, SOCK_STREAM, 0)
    guard fd >= 0 else { throw missing }
    let file = FileHandle(fileDescriptor: fd, closeOnDealloc: true)
    let connected = withUnsafePointer(to: &addr) { ptr in
        ptr.withMemoryRebound(to: sockaddr.self, capacity: 1) { connect(fd, $0, socklen_t(MemoryLayout<sockaddr_un>.size)) }
    }
    var user = uid_t(), group = gid_t(), peer = pid_t(), len = socklen_t(MemoryLayout<pid_t>.size)
    guard connected == 0, getpeereid(fd, &user, &group) == 0, user == geteuid(),
          getsockopt(fd, SOL_LOCAL, LOCAL_PEERPID, &peer, &len) == 0, matchesInstalledProjectApp(pid: peer) else { throw missing }
    var timeout = timeval(tv_sec: 2, tv_usec: 0)
    setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout.size(ofValue: timeout)))
    var request = try JSONSerialization.data(withJSONObject: ["action":"panel", "path":path])
    request.append(10)
    try file.write(contentsOf: request)
    var body = Data()
    while !body.contains(10), body.count < 4096, let chunk = try file.read(upToCount: 4096 - body.count), !chunk.isEmpty { body.append(chunk) }
    guard let result = try? JSONDecoder().decode(ProjectPanel.self, from: body), result.path == path, result.pid == peer,
          result.windowNumber > 0, UUID(uuidString: result.nonce) != nil else { throw missing }
    return result
}

@MainActor
func visibleProjectPanel(_ panel: ProjectPanel) async throws -> SCWindow {
    guard CGPreflightScreenCaptureAccess() else { throw Failure(6, "project_screen_permission") }
    let candidates = try await SCShareableContent.excludingDesktopWindows(true, onScreenWindowsOnly: true)
    guard let window = candidates.windows.first(where: { $0.windowID == panel.windowNumber && $0.isOnScreen }),
          let owner = window.owningApplication, NSWorkspace.shared.frontmostApplication?.processIdentifier == panel.pid,
          owner.processID == panel.pid, owner.bundleIdentifier == "dev.cage.guest.project-access" else {
        throw Failure(7, "no_project_panel")
    }
    return window
}
