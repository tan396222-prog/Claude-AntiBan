import Cocoa
import CryptoKit
import Security
import Darwin

let cageUpdateBundleID = "io.github.lee-xeo.claude-cage"

enum CageProfile {
    static var clientOnly: Bool { Bundle.main.object(forInfoDictionaryKey: "CageClientOnly") as? Bool == true }
    static var macGuestTrial: Bool { Bundle.main.bundleIdentifier == cageUpdateBundleID + ".macos-trial" }
    static func configURL() -> URL {
        if macGuestTrial {
            return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".claude-cage/macos-trial/config.json")
        }
        if let path = ProcessInfo.processInfo.environment["CLAUDE_CAGE_CONFIG"], !path.isEmpty {
            return URL(fileURLWithPath: path).standardizedFileURL
        }
        return FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(clientOnly ? ".claude-cage/client/config.json" : ".claude-cage/config.json")
    }

    // Every helper and relaunched app inherits the same absolute profile, even
    // when its working directory differs from the original launch directory.
    static func pinEnvironment() { setenv("CLAUDE_CAGE_CONFIG", configURL().path, 1) }
}

final class CageUpdateValidation {
    private let lock = NSLock()
    private let completion = DispatchGroup()
    private var cancelled = false
    private var process: Process?

    init() { completion.enter() }
    func checkCancellation() throws {
        lock.lock(); defer { lock.unlock() }
        if cancelled { throw CageUpdateError("更新验证已取消") }
    }
    func start(_ value: Process) throws {
        lock.lock(); defer { lock.unlock() }
        if cancelled { throw CageUpdateError("更新验证已取消") }
        try value.run()
        process = value
    }
    func released(_ value: Process) {
        lock.lock(); defer { lock.unlock() }
        if process === value { process = nil }
    }
    func cancel() {
        lock.lock(); cancelled = true; let active = process; lock.unlock()
        if let active { CageUpdateFiles.stop(active) }
    }
    func finish() { completion.leave() }
    func wait() { completion.wait() }
}

struct CageUpdateError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
    init(_ message: String) { self.message = message }
}

struct CageUpdatePlan: Codable {
    let version: String
    let team: String
    let target: String
    let candidate: String
    let backup: String
    let parentPID: Int32
    let resultPath: String
}

enum CageUpdateVersion {
    static func isNewer(_ candidate: String, than current: String) -> Bool {
        func parts(_ text: String) -> ([Int], [String])? {
            guard text.range(of: "^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$", options: .regularExpression) != nil else { return nil }
            let sections = text.split(separator: "-", maxSplits: 1).map(String.init)
            let numbers = sections[0].split(separator: ".").compactMap { Int($0) }
            guard numbers.count == 3 else { return nil }
            let pre = sections.count == 2 ? sections[1].split(separator: ".").map(String.init) : []
            if pre.contains(where: { $0.count > 1 && $0.first == "0" && $0.allSatisfy(\.isNumber) }) { return nil }
            return (numbers, pre)
        }
        guard let (a, ap) = parts(candidate), let (b, bp) = parts(current) else { return false }
        for i in 0..<3 where a[i] != b[i] { return a[i] > b[i] }
        if ap.isEmpty || bp.isEmpty { return ap.isEmpty && !bp.isEmpty }
        for i in 0..<min(ap.count, bp.count) where ap[i] != bp[i] {
            let x = ap[i], y = bp[i], xn = x.allSatisfy(\.isNumber), yn = y.allSatisfy(\.isNumber)
            if xn && yn { return x.count == y.count ? x > y : x.count > y.count }
            if xn != yn { return !xn }
            return x > y
        }
        return ap.count > bp.count
    }
}

enum CageUpdateFiles {
    static func restore(backup: URL, target: URL, manager: FileManager) throws {
        let copy = backup.deletingLastPathComponent().appendingPathComponent("restore-" + UUID().uuidString + ".app")
        try FileManager.default.copyItem(at: backup, to: copy)
        defer { try? FileManager.default.removeItem(at: copy) }
        if FileManager.default.fileExists(atPath: target.path) {
            _ = try manager.replaceItemAt(target, withItemAt: copy, backupItemName: nil, options: [.usingNewMetadataOnly])
        } else {
            try manager.moveItem(at: copy, to: target)
        }
    }
    static func replaceAndVerify(target: URL, candidate: URL, backup: URL, manager: FileManager, onReplace: () -> Void = {}, restoreOriginal: (() throws -> Void)? = nil, verify: (URL) throws -> Void) throws {
        do {
            _ = try manager.replaceItemAt(target, withItemAt: candidate, backupItemName: nil, options: [.usingNewMetadataOnly])
            onReplace()
            try verify(target)
        } catch {
            do {
                if let restoreOriginal { try restoreOriginal() }
                else { try restore(backup: backup, target: target, manager: manager) }
            }
            catch { throw CageUpdateError("安装与自动恢复均未完成，原应用备份已保留") }
            throw CageUpdateError("安装验证失败，已恢复原应用")
        }
    }
    static func privateDirectory(_ url: URL) throws {
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        let values = try url.resourceValues(forKeys: [.isSymbolicLinkKey, .isDirectoryKey])
        guard values.isDirectory == true, values.isSymbolicLink != true else { throw CageUpdateError("更新目录无效") }
        try FileManager.default.setAttributes([.posixPermissions: 0o700], ofItemAtPath: url.path)
    }
    static func write(_ data: Data, to url: URL) throws {
        try data.write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }
    static func digest(_ url: URL, validation: CageUpdateValidation? = nil) throws -> String {
        let file = try FileHandle(forReadingFrom: url)
        defer { try? file.close() }
        var hash = SHA256()
        while let data = try file.read(upToCount: 1024 * 1024), !data.isEmpty { try validation?.checkCancellation(); hash.update(data: data) }
        return hash.finalize().map { String(format: "%02x", $0) }.joined()
    }
    static func stop(_ process: Process) {
        guard process.isRunning else { return }
        process.terminate()
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 2) {
            if process.isRunning { Darwin.kill(process.processIdentifier, SIGKILL) }
        }
    }
    static func run(_ executable: String, _ arguments: [String], timeout: TimeInterval = 90, validation: CageUpdateValidation? = nil) throws -> Data {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments
        let pipe = Pipe(); process.standardOutput = pipe; process.standardError = pipe
        let expiration = DispatchWorkItem { stop(process) }
        if let validation { try validation.start(process) } else { try process.run() }
        defer { validation?.released(process) }
        DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + timeout, execute: expiration)
        let data = pipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit(); expiration.cancel()
        try validation?.checkCancellation()
        guard process.terminationStatus == 0, data.count <= 1024 * 1024 else { throw CageUpdateError("本机更新验证未完成，请重试") }
        return data
    }
    static func signingInfo(_ url: URL, bundle: Bool, expectedTeam: String? = nil) throws -> String {
        var code: SecStaticCode?
        guard SecStaticCodeCreateWithPath(url as CFURL, [], &code) == errSecSuccess, let code else { throw CageUpdateError("无法读取应用签名") }
        var requirement: SecRequirement?
        let identity = bundle ? " and identifier \"\(cageUpdateBundleID)\"" : ""
        let team = expectedTeam.map { " and certificate leaf[subject.OU] = \"\($0)\"" } ?? ""
        let expression = "anchor apple generic and certificate 1[field.1.2.840.113635.100.6.2.6] exists and certificate leaf[field.1.2.840.113635.100.6.1.13] exists\(identity)\(team)"
        guard SecRequirementCreateWithString(expression as CFString, [], &requirement) == errSecSuccess else { throw CageUpdateError("无法建立发布者签名要求") }
        let flags = SecCSFlags(rawValue: kSecCSStrictValidate | kSecCSCheckAllArchitectures | kSecCSCheckNestedCode)
        guard SecStaticCodeCheckValidity(code, flags, requirement) == errSecSuccess else { throw CageUpdateError("更新应用的 Apple 签名无效或发布者不匹配") }
        var information: CFDictionary?
        guard SecCodeCopySigningInformation(code, SecCSFlags(rawValue: kSecCSSigningInformation), &information) == errSecSuccess,
              let info = information as? [String: Any], let actual = info[kSecCodeInfoTeamIdentifier as String] as? String,
              actual.range(of: "^[A-Z0-9]{10}$", options: .regularExpression) != nil else { throw CageUpdateError("当前开发构建没有可验证的发布者，无法自动安装更新") }
        if let expectedTeam, actual != expectedTeam { throw CageUpdateError("更新应用的发布者不匹配") }
        return actual
    }
    static func verifyArchitecture(_ executable: URL, validation: CageUpdateValidation? = nil) throws {
        _ = try run("/usr/bin/lipo", [executable.path, "-verify_arch", "arm64"], validation: validation)
    }
    static func verifyApp(_ url: URL, version: String, team: String, gatekeeper: Bool = true, validation: CageUpdateValidation? = nil) throws {
        try validation?.checkCancellation()
        let values = try url.resourceValues(forKeys: [.isDirectoryKey, .isSymbolicLinkKey])
        guard values.isDirectory == true, values.isSymbolicLink != true, url.pathExtension == "app" else { throw CageUpdateError("更新包中缺少有效的 Cage.app") }
        let plist = url.appendingPathComponent("Contents/Info.plist")
        guard let info = try PropertyListSerialization.propertyList(from: Data(contentsOf: plist), format: nil) as? [String: Any],
              info["CFBundleIdentifier"] as? String == cageUpdateBundleID,
              info["CageVersion"] as? String == version,
              info["CFBundleExecutable"] as? String == "Cage" else { throw CageUpdateError("更新包的应用身份或版本不匹配") }
        if let minimum = info["LSMinimumSystemVersion"] as? String {
            let parts = minimum.split(separator: ".").compactMap { Int($0) }
            guard minimum.range(of: "^[0-9]+(\\.[0-9]+){0,2}$", options: .regularExpression) != nil,
                  (1...3).contains(parts.count), ProcessInfo.processInfo.isOperatingSystemAtLeast(OperatingSystemVersion(majorVersion: parts[0], minorVersion: parts.count > 1 ? parts[1] : 0, patchVersion: parts.count > 2 ? parts[2] : 0)) else { throw CageUpdateError("此更新需要更高版本的 macOS") }
        }
        _ = try signingInfo(url, bundle: true, expectedTeam: team)
        try verifyArchitecture(url.appendingPathComponent("Contents/MacOS/Cage"), validation: validation)
        for name in ["cage", "mihomo", "CageUpdateInstaller"] {
            let nested = url.appendingPathComponent("Contents/Resources/\(name)")
            guard FileManager.default.fileExists(atPath: nested.path) else { throw CageUpdateError("更新包缺少必要的应用组件") }
            _ = try signingInfo(nested, bundle: false, expectedTeam: team)
        }
        if gatekeeper { _ = try run("/usr/sbin/spctl", ["--assess", "--type", "execute", "--verbose=2", url.path], validation: validation) }
        try validation?.checkCancellation()
    }
    static func validateTree(_ app: URL, validation: CageUpdateValidation? = nil) throws {
        let base = app.resolvingSymlinksInPath().path + "/"
        guard let items = FileManager.default.enumerator(at: app, includingPropertiesForKeys: [.isRegularFileKey, .fileSizeKey, .isSymbolicLinkKey], options: []) else { throw CageUpdateError("无法检查更新包内容") }
        var total: Int64 = 0
        var count = 0
        for case let entry as URL in items {
            try validation?.checkCancellation()
            count += 1
            guard count <= 50000, entry.resolvingSymlinksInPath().path.hasPrefix(base) else { throw CageUpdateError("更新包包含越界文件或过多文件") }
            let values = try entry.resourceValues(forKeys: [.isRegularFileKey, .fileSizeKey])
            if values.isRegularFile == true { total += Int64(values.fileSize ?? 0) }
            guard total <= 1024 * 1024 * 1024 else { throw CageUpdateError("解包后的应用超过 1 GiB") }
        }
    }
    static func withMountedImage<T>(archive: URL, directory: URL, validation: CageUpdateValidation? = nil, body: (URL) throws -> T) throws -> T {
        let mount = directory.appendingPathComponent("mount", isDirectory: true)
        try privateDirectory(mount)
        defer {
            if (try? run("/usr/bin/hdiutil", ["detach", mount.path], timeout: 30)) == nil {
                _ = try? run("/usr/bin/hdiutil", ["detach", "-force", mount.path], timeout: 30)
            }
        }
        _ = try run("/usr/bin/hdiutil", ["attach", "-readonly", "-nobrowse", "-noautoopen", "-mountpoint", mount.path, archive.path], validation: validation)
        try validation?.checkCancellation()
        return try body(mount)
    }
    static func extract(archive: URL, hash: String, version: String, team: String, directory: URL, validation: CageUpdateValidation? = nil) throws -> URL {
        guard try digest(archive, validation: validation) == hash else { throw CageUpdateError("安装包下载后发生变化，请重新下载") }
        return try withMountedImage(archive: archive, directory: directory, validation: validation) { mount in
            let source = mount.appendingPathComponent("Cage.app", isDirectory: true)
            try validateTree(source, validation: validation)
            try verifyApp(source, version: version, team: team, validation: validation)
            let target = directory.appendingPathComponent("Cage.app", isDirectory: true)
            _ = try run("/usr/bin/ditto", [source.path, target.path], validation: validation)
            try validateTree(target, validation: validation)
            try verifyApp(target, version: version, team: team, validation: validation)
            return target
        }
    }
}

/// Owns update state for one native window. All state transitions run on main.
final class CageAppUpdater {
    private let bundleURL: URL
    private let root: URL
    private let onChange: ([String: Any]) -> Void
    private let quit: () -> Void
    private let reconnect: () -> Void
    private var state: [String: Any]
    private var worker: Process?
    private var validationTask: CageUpdateValidation?
    private var installer: Process?
    private var installerInput: Pipe?
    private var prepared: (directory: URL, app: URL, version: String, team: String)?
    private var generation = 0
    private var launchAcknowledged = false
    private var workbenchInitialized = false
    private var waitingForTermination = false
    private var installationCommitted = false

    init(bundleURL: URL, onChange: @escaping ([String: Any]) -> Void, quit: @escaping () -> Void, reconnect: @escaping () -> Void) {
        self.bundleURL = bundleURL.resolvingSymlinksInPath()
        self.onChange = onChange; self.quit = quit; self.reconnect = reconnect
        root = CageProfile.configURL().deletingLastPathComponent().appendingPathComponent("updates", isDirectory: true)
        let version = Bundle(url: bundleURL)?.object(forInfoDictionaryKey: "CageVersion") as? String ?? "开发版本"
        state = ["phase": "idle", "currentVersion": version, "message": ""]
        if let data = try? Data(contentsOf: root.appendingPathComponent("last-result.json")), data.count < 16384,
           let result = try? JSONSerialization.jsonObject(with: data) as? [String: Any] {
            state["message"] = result["message"] as? String ?? ""
            if result["success"] as? Bool == false { state["phase"] = "failed"; state["retryable"] = true }
        }
    }
    func handle(_ operation: String) -> [String: Any] {
        if Bundle(url: bundleURL)?.bundleIdentifier == cageUpdateBundleID + ".macos-trial" {
            if state["phase"] as? String != "manual" {
                change(["phase": "manual", "message": "macOS 客机试用版通过本机安装更新。"])
            }
            return state
        }
        if Bundle(url: bundleURL)?.object(forInfoDictionaryKey: "CageClientOnly") as? Bool == true {
            if state["phase"] as? String != "manual" || operation != "status" {
                change(["phase": "manual", "message": "轻量客户端使用独立安装包更新；连接配置会保留，云端任务不受影响。"])
            }
            return state
        }
        let busy = ["checking", "downloading", "downloaded", "verifying", "installing"].contains(state["phase"] as? String ?? "")
        if ["check", "install"].contains(operation), let value = state["retryAt"] as? String,
           let date = ISO8601DateFormatter().date(from: value), date > Date() { return state }
        switch operation {
        case "status": break
        case "check": if !busy && worker == nil && validationTask == nil && installer == nil && prepared == nil { startDownload(checkOnly: true) }
        case "install":
            if !busy && worker == nil && validationTask == nil && installer == nil { if prepared != nil { beginInstallation() } else { startDownload(checkOnly: false) } }
        case "cancel":
            if installer == nil && state["phase"] as? String != "installing" { generation += 1; if let worker { CageUpdateFiles.stop(worker) }; validationTask?.cancel(); change(["phase": prepared == nil ? "available" : "ready", "message": "更新已暂停，可稍后继续", "retryable": true]) }
        default: break
        }
        return state
    }
    func cancelForTermination() {
        generation += 1
        if let worker { CageUpdateFiles.stop(worker) }
        validationTask?.cancel()
        try? installerInput?.fileHandleForWriting.close()
        if !installationCommitted, let installer { CageUpdateFiles.stop(installer) }
    }
    func prepareForTermination(_ completion: @escaping () -> Void) -> Bool {
        if installationCommitted { return false }
        if waitingForTermination { return true }
        let download = worker, validation = validationTask, helper = installer
        let directory = prepared?.directory
        cancelForTermination()
        guard download != nil || validation != nil || helper != nil || directory != nil else { return false }
        waitingForTermination = true
        change(["message": "正在取消更新并清理临时文件…"])
        DispatchQueue.global(qos: .utility).async {
            download?.waitUntilExit()
            validation?.wait()
            helper?.waitUntilExit()
            if let directory { try? FileManager.default.removeItem(at: directory) }
            DispatchQueue.main.async { self.waitingForTermination = false; completion() }
        }
        return true
    }
    var awaitingInitialWorkbench: Bool {
        guard let token = ProcessInfo.processInfo.environment["CAGE_UPDATE_CONFIRMATION"], UUID(uuidString: token) != nil else { return false }
        return !workbenchInitialized
    }
    func markLaunchReady() {
        workbenchInitialized = true
        guard !launchAcknowledged, let token = ProcessInfo.processInfo.environment["CAGE_UPDATE_CONFIRMATION"], UUID(uuidString: token) != nil,
              let data = try? JSONSerialization.data(withJSONObject: ["version": state["currentVersion"] as? String ?? ""]) else { return }
        do {
            try CageUpdateFiles.privateDirectory(root)
            try CageUpdateFiles.write(data, to: root.appendingPathComponent("launch-\(token).json"))
            launchAcknowledged = true
            change(["phase": "current", "message": "Cage 已更新至 \(state["currentVersion"] as? String ?? "")"])
        } catch { failure("更新已安装，但启动确认未能保存") }
    }
    private func change(_ values: [String: Any]) {
        if values["phase"] as? String == "current" { state.removeValue(forKey: "latestVersion") }
        for (key, value) in values { state[key] = value }
        onChange(state)
    }
    private func failure(_ message: String, retryAt: String = "") { change(["phase": "failed", "message": message, "retryable": true, "retryAt": retryAt]) }
    private var cage: URL { bundleURL.appendingPathComponent("Contents/Resources/cage") }
    private func startDownload(checkOnly: Bool) {
        generation += 1; let operation = generation
        change(["phase": "checking", "message": "正在通过所选代理检查更新", "receivedBytes": 0, "totalBytes": 0, "retryable": false, "retryAt": ""])
        let process = Process(); process.executableURL = cage; process.arguments = ["app-update", checkOnly ? "check" : "download"]
        let pipe = Pipe(); process.standardOutput = pipe; process.standardError = FileHandle.nullDevice
        worker = process
        do { try process.run() } catch { worker = nil; failure("无法启动更新检查，请确认应用组件完整"); return }
        DispatchQueue.global(qos: .utility).async {
            var pending = Data(); var last: [String: Any]?
            while true {
                let chunk = pipe.fileHandleForReading.availableData
                if chunk.isEmpty { break }
                pending.append(chunk)
                if pending.count > 65536 { process.terminate(); break }
                while let end = pending.firstIndex(of: 10) {
                    let line = pending.prefix(upTo: end); pending.removeSubrange(...end)
                    guard let value = try? JSONSerialization.jsonObject(with: line) as? [String: Any] else { continue }
                    last = value
                    var visible = value; visible.removeValue(forKey: "archivePath"); visible.removeValue(forKey: "sha256")
                    DispatchQueue.main.async { if self.generation == operation { self.change(visible) } }
                }
            }
            process.waitUntilExit()
            let result = last
            DispatchQueue.main.async {
                if self.worker === process { self.worker = nil }
                guard self.generation == operation else { return }
                if process.terminationStatus != 0 {
                    self.failure(result?["message"] as? String ?? "更新请求未完成，请重试", retryAt: result?["retryAt"] as? String ?? "")
                } else if !checkOnly, let result, result["phase"] as? String == "downloaded" {
                    self.verifyDownload(result, operation: operation)
                }
            }
        }
    }
    private func verifyDownload(_ result: [String: Any], operation: Int) {
        guard let path = result["archivePath"] as? String, let hash = result["sha256"] as? String,
              let version = result["latestVersion"] as? String, hash.range(of: "^[0-9a-f]{64}$", options: .regularExpression) != nil else { failure("下载进程返回了无效安装包"); return }
        let archive = URL(fileURLWithPath: path).resolvingSymlinksInPath()
        guard archive.path.hasPrefix(root.resolvingSymlinksInPath().path + "/download-"), archive.lastPathComponent == "Cage.dmg" else { failure("安装包路径不属于本次私有下载"); return }
        change(["phase": "verifying", "message": "正在验证 Apple 签名和应用内容", "retryable": false])
        let task = CageUpdateValidation()
        validationTask = task
        DispatchQueue.global(qos: .userInitiated).async {
            defer {
                task.finish()
                DispatchQueue.main.async { if self.validationTask === task { self.validationTask = nil } }
            }
            let directory = self.root.appendingPathComponent("verified-" + UUID().uuidString, isDirectory: true)
            do {
                try task.checkCancellation()
                let team = try CageUpdateFiles.signingInfo(self.bundleURL, bundle: true)
                try CageUpdateFiles.privateDirectory(directory)
                let app = try CageUpdateFiles.extract(archive: archive, hash: hash, version: version, team: team, directory: directory, validation: task)
                try task.checkCancellation()
                try? FileManager.default.removeItem(at: archive.deletingLastPathComponent())
                DispatchQueue.main.async {
                    if self.generation != operation { try? FileManager.default.removeItem(at: directory); return }
                    self.prepared = (directory, app, version, team)
                    self.change(["phase": "ready", "message": "更新已下载并验证", "retryable": true])
                    self.beginInstallation()
                }
            } catch {
                try? FileManager.default.removeItem(at: directory)
                try? FileManager.default.removeItem(at: archive.deletingLastPathComponent())
                DispatchQueue.main.async { if self.generation == operation { self.failure(error.localizedDescription) } }
            }
        }
    }
    private func beginInstallation() {
        guard let prepared, installer == nil else { return }
        change(["phase": "installing", "message": "正在准备安装更新", "retryable": false])
        let operation = generation
        let task = CageUpdateValidation()
        validationTask = task
        DispatchQueue.global(qos: .userInitiated).async {
            defer {
                task.finish()
                DispatchQueue.main.async { if self.validationTask === task { self.validationTask = nil } }
            }
            do {
                guard !self.bundleURL.path.hasPrefix("/Volumes/"), self.bundleURL.pathExtension == "app" else { throw CageUpdateError("请先将 Cage 安装到应用程序目录，再自动更新") }
                try CageUpdateFiles.verifyApp(prepared.app, version: prepared.version, team: prepared.team, validation: task)
                let helper = prepared.directory.appendingPathComponent("CageUpdateInstaller")
                let originalHelper = self.bundleURL.appendingPathComponent("Contents/Resources/CageUpdateInstaller")
                try? FileManager.default.removeItem(at: helper)
                try FileManager.default.copyItem(at: originalHelper, to: helper)
                _ = try CageUpdateFiles.signingInfo(helper, bundle: false, expectedTeam: prepared.team)
                let plan = CageUpdatePlan(version: prepared.version, team: prepared.team, target: self.bundleURL.path, candidate: prepared.app.path,
                    backup: prepared.directory.appendingPathComponent("previous.app").path, parentPID: ProcessInfo.processInfo.processIdentifier, resultPath: self.root.appendingPathComponent("last-result.json").path)
                let planURL = prepared.directory.appendingPathComponent("install.json")
                try CageUpdateFiles.write(JSONEncoder().encode(plan), to: planURL)
                try task.checkCancellation()
                DispatchQueue.main.async { if self.generation == operation { self.launchInstaller(helper: helper, plan: planURL) } }
            } catch { DispatchQueue.main.async { if self.generation == operation { self.failure(error.localizedDescription) } } }
        }
    }
    private func launchInstaller(helper: URL, plan: URL) {
        let process = Process(); process.executableURL = helper; process.arguments = [plan.path]
        let input = Pipe(), output = Pipe(); process.standardInput = input; process.standardOutput = output; process.standardError = FileHandle.nullDevice
        installer = process; installerInput = input
        do { try process.run() } catch { installer = nil; installerInput = nil; failure("无法启动安全安装程序"); return }
        DispatchQueue.global(qos: .userInitiated).async {
            var data = Data()
            while data.count < 16384 {
                let byte = output.fileHandleForReading.readData(ofLength: 1)
                if byte.isEmpty || byte == Data([10]) { break }
                data.append(byte)
            }
            let response = (try? JSONSerialization.jsonObject(with: data)) as? [String: Any]
            DispatchQueue.main.async {
                guard self.installer === process, !self.waitingForTermination else { return }
                if response?["status"] as? String == "ready" { self.prepareEnvironment(stop: false) }
                else { self.abortInstaller(); self.failure(response?["message"] as? String ?? "安全安装准备失败，原应用已保留") }
            }
        }
    }
    private func abortInstaller() {
        try? installerInput?.fileHandleForWriting.close()
        installerInput = nil
        if let helper = installer {
            CageUpdateFiles.stop(helper)
            DispatchQueue.global(qos: .utility).async {
                helper.waitUntilExit()
                DispatchQueue.main.async { if self.installer === helper { self.installer = nil } }
            }
        }
    }
    private func prepareEnvironment(stop: Bool) {
        change(["phase": "installing", "message": stop ? "正在停止环境，准备安装" : "正在检查环境是否可安全更新"])
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                let arguments = ["prepare-update"] + (stop ? ["--stop"] : []) + ["--json"]
                let process = Process(); process.executableURL = self.cage; process.arguments = arguments
                let output = Pipe(); process.standardOutput = output; process.standardError = FileHandle.nullDevice
                try process.run()
                let expiration = DispatchWorkItem { if process.isRunning { process.terminate() } }
                DispatchQueue.global(qos: .utility).asyncAfter(deadline: .now() + 180, execute: expiration)
                let data = output.fileHandleForReading.readDataToEndOfFile(); process.waitUntilExit(); expiration.cancel()
                guard data.count <= 16384, let result = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw CageUpdateError("无法确认环境已停止") }
                DispatchQueue.main.async {
                    guard !self.waitingForTermination else { return }
                    switch result["status"] as? String {
                    case "prepared" where process.terminationStatus == 0:
                        guard self.installer?.isRunning == true else { self.failure("安装程序已退出，原应用已保留"); self.reconnect(); return }
                        do {
                            try self.installerInput?.fileHandleForWriting.write(contentsOf: Data("COMMIT\n".utf8))
                            try self.installerInput?.fileHandleForWriting.close()
                            self.installationCommitted = true
                            self.change(["phase": "installing", "message": "正在安装并重启 Cage"])
                            self.quit()
                        } catch { self.abortInstaller(); self.failure("无法开始安装，原应用已保留"); self.reconnect() }
                    case "stop_required" where !stop:
                        self.prepareEnvironment(stop: true)
                    default:
                        self.abortInstaller(); self.failure(result["message"] as? String ?? "环境未能安全停止，更新未安装")
                    }
                }
            } catch { DispatchQueue.main.async { self.abortInstaller(); self.failure(error.localizedDescription) } }
        }
    }
}
