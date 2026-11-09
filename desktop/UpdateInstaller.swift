import Cocoa
import Darwin

@main
final class CageUpdateInstaller: NSObject, NSApplicationDelegate {
    private var plan: CageUpdatePlan!
    private var parent: NSRunningApplication!
    private var manager = FileManager.default
    private var didReplace = false
    private var originalVersion = ""

    static func main() {
        CageProfile.pinEnvironment()
        let app = NSApplication.shared
        let delegate = CageUpdateInstaller(); app.delegate = delegate
        app.setActivationPolicy(.accessory)
        withExtendedLifetime(delegate) { app.run() }
    }
    func applicationDidFinishLaunching(_ notification: Notification) {
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                try self.readPlan()
                let directory = URL(fileURLWithPath: self.plan.target).deletingLastPathComponent()
                DispatchQueue.main.async {
                    if FileManager.default.isWritableFile(atPath: directory.path) { self.prepare() }
                    else {
                        NSWorkspace.shared.requestAuthorization(to: .replaceFile) { authorization, _ in
                            DispatchQueue.main.async {
                                guard let authorization else { self.finishBeforeCommit("未获得安装权限，原应用已保留"); return }
                                self.manager = FileManager(authorization: authorization)
                                self.prepare()
                            }
                        }
                    }
                }
            } catch { self.finishBeforeCommit(error.localizedDescription) }
        }
    }
    private func readPlan() throws {
        guard CommandLine.arguments.count == 2 else { throw CageUpdateError("安装任务无效") }
        let location = URL(fileURLWithPath: CommandLine.arguments[1]).resolvingSymlinksInPath()
        let directory = location.deletingLastPathComponent()
        let attributes = try FileManager.default.attributesOfItem(atPath: directory.path)
        guard (attributes[.ownerAccountID] as? NSNumber)?.uint32Value == getuid(),
              ((attributes[.posixPermissions] as? NSNumber)?.intValue ?? 0) & 0o077 == 0 else { throw CageUpdateError("安装任务目录权限无效") }
        let data = try Data(contentsOf: location)
        guard data.count <= 16384 else { throw CageUpdateError("安装任务过大") }
        plan = try JSONDecoder().decode(CageUpdatePlan.self, from: data)
        guard plan.parentPID == getppid(), let running = NSRunningApplication(processIdentifier: plan.parentPID),
              running.bundleURL?.resolvingSymlinksInPath().path == plan.target else { throw CageUpdateError("安装任务不属于当前 Cage 应用") }
        parent = running
        let selfURL = URL(fileURLWithPath: CommandLine.arguments[0]).resolvingSymlinksInPath()
        let ownTeam = try CageUpdateFiles.signingInfo(selfURL, bundle: false)
        guard ownTeam == plan.team,
              URL(fileURLWithPath: plan.candidate).resolvingSymlinksInPath().path == directory.appendingPathComponent("Cage.app").path,
              plan.backup == directory.appendingPathComponent("previous.app").path,
              plan.resultPath == directory.deletingLastPathComponent().appendingPathComponent("last-result.json").path,
              selfURL.deletingLastPathComponent() == directory else { throw CageUpdateError("安装任务路径或签名不匹配") }
        let target = URL(fileURLWithPath: plan.target)
        guard target == target.resolvingSymlinksInPath(), target.pathExtension == "app", !target.path.hasPrefix("/Volumes/") else { throw CageUpdateError("安装位置不支持自动更新") }
        _ = try CageUpdateFiles.signingInfo(target, bundle: true, expectedTeam: ownTeam)
        try CageUpdateFiles.verifyApp(URL(fileURLWithPath: plan.candidate), version: plan.version, team: ownTeam)
        let info = try PropertyListSerialization.propertyList(from: Data(contentsOf: target.appendingPathComponent("Contents/Info.plist")), format: nil) as? [String: Any]
        guard let current = info?["CageVersion"] as? String, CageUpdateVersion.isNewer(plan.version, than: current) else { throw CageUpdateError("更新版本必须高于当前安装版本") }
        originalVersion = current
    }
    private func prepare() {
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                let backup = URL(fileURLWithPath: self.plan.backup)
                if FileManager.default.fileExists(atPath: backup.path) { try FileManager.default.removeItem(at: backup) }
                try FileManager.default.copyItem(at: URL(fileURLWithPath: self.plan.target), to: backup)
                _ = try CageUpdateFiles.signingInfo(backup, bundle: true, expectedTeam: self.plan.team)
                self.send(["status": "ready"])
                let command = FileHandle.standardInput.readDataToEndOfFile()
                guard command == Data("COMMIT\n".utf8) else { self.exitHelper(); return }
                let deadline = Date().addingTimeInterval(90)
                while !self.parent.isTerminated && Date() < deadline { Thread.sleep(forTimeInterval: 0.1) }
                guard self.parent.isTerminated else { throw CageUpdateError("Cage 尚未退出，更新未安装") }
                try self.replace()
                DispatchQueue.main.async { self.openInstalled() }
            } catch {
                if self.parent?.isTerminated == true { self.recover(error.localizedDescription) }
                else { self.finishBeforeCommit(error.localizedDescription) }
            }
        }
    }
    private func replace() throws {
        let target = URL(fileURLWithPath: plan.target)
        let candidate = URL(fileURLWithPath: plan.candidate)
        _ = try CageUpdateFiles.signingInfo(target, bundle: true, expectedTeam: plan.team)
        try CageUpdateFiles.verifyApp(candidate, version: plan.version, team: plan.team)
        try CageUpdateFiles.replaceAndVerify(target: target, candidate: candidate, backup: URL(fileURLWithPath: plan.backup), manager: manager, onReplace: { self.didReplace = true }) {
            try CageUpdateFiles.verifyApp($0, version: plan.version, team: plan.team)
        }
    }
    private func openInstalled() {
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.activates = true
        configuration.createsNewApplicationInstance = true
        let token = UUID().uuidString
        var environment = inheritedProfile(); environment["CAGE_UPDATE_CONFIRMATION"] = token
        configuration.environment = environment
        NSWorkspace.shared.openApplication(at: URL(fileURLWithPath: plan.target), configuration: configuration) { app, error in
            guard error == nil, let app else { self.recover("新版本未能启动，正在恢复原应用"); return }
            let acknowledgement = URL(fileURLWithPath: self.plan.resultPath).deletingLastPathComponent().appendingPathComponent("launch-\(token).json")
            DispatchQueue.global(qos: .utility).async {
                let deadline = Date().addingTimeInterval(45)
                while !app.isTerminated && Date() < deadline {
                    if let data = try? Data(contentsOf: acknowledgement), data.count < 1024,
                       let value = try? JSONSerialization.jsonObject(with: data) as? [String: String], value["version"] == self.plan.version {
                        try? FileManager.default.removeItem(at: acknowledgement)
                        self.record(success: true, message: "Cage 已更新至 \(self.plan.version)")
                        self.cleanSuccessfulTask(); self.exitHelper(); return
                    }
                    Thread.sleep(forTimeInterval: 0.2)
                }
                DispatchQueue.main.async { _ = app.terminate() }
                let terminationDeadline = Date().addingTimeInterval(10)
                while !app.isTerminated && Date() < terminationDeadline { Thread.sleep(forTimeInterval: 0.1) }
                if app.isTerminated { self.recover("新版本工作台未能就绪，正在恢复原应用") }
                else {
                    self.record(success: false, message: "新版本未确认就绪，请退出 Cage 后重试；原应用备份已保留")
                    self.exitHelper()
                }
            }
        }
    }
    private func inheritedProfile() -> [String: String] {
        if let path = ProcessInfo.processInfo.environment["CLAUDE_CAGE_CONFIG"] { return ["CLAUDE_CAGE_CONFIG": path] }
        return [:]
    }
    private func recover(_ reason: String) {
        DispatchQueue.global(qos: .userInitiated).async {
            var message = reason
            var canOpen = false
            let target = URL(fileURLWithPath: self.plan.target), backup = URL(fileURLWithPath: self.plan.backup)
            do {
                let originalIntact = (try? CageUpdateFiles.verifyApp(target, version: self.originalVersion, team: self.plan.team, gatekeeper: false)) != nil
                if self.didReplace || !originalIntact {
                    try CageUpdateFiles.verifyApp(backup, version: self.originalVersion, team: self.plan.team, gatekeeper: false)
                    // A failed target may contain invalid executables. Use only
                    // the verified original controller to release the backend.
                    let control = backup.appendingPathComponent("Contents/Resources/cage")
                    let handoffData = try CageUpdateFiles.run(control.path, ["prepare-update", "--json"], timeout: 90)
                    guard let handoff = try JSONSerialization.jsonObject(with: handoffData) as? [String: Any], handoff["status"] as? String == "prepared" else {
                        throw CageUpdateError("新版本后台尚未安全退出，原应用备份已保留")
                    }
                    if !originalIntact { try CageUpdateFiles.restore(backup: backup, target: target, manager: self.manager) }
                    try CageUpdateFiles.verifyApp(target, version: self.originalVersion, team: self.plan.team, gatekeeper: false)
                    message += "。已恢复原应用，可稍后重试"
                } else { message += "。原应用已保留" }
                canOpen = true
            } catch { message += "。自动恢复未完成，未启动未经验证的应用。备份位置：\(backup.path)" }
            self.record(success: false, message: message)
            DispatchQueue.main.async {
                guard canOpen else {
                    let alert = NSAlert(); alert.messageText = "Cage 更新与自动恢复未完成"; alert.informativeText = message; alert.runModal()
                    self.exitHelper(); return
                }
                let configuration = NSWorkspace.OpenConfiguration(); configuration.activates = true; configuration.createsNewApplicationInstance = true; configuration.environment = self.inheritedProfile()
                NSWorkspace.shared.openApplication(at: URL(fileURLWithPath: self.plan.target), configuration: configuration) { app, _ in
                    if app == nil {
                        let alert = NSAlert(); alert.messageText = "Cage 更新未完成"; alert.informativeText = message; alert.runModal()
                    }
                    self.exitHelper()
                }
            }
        }
    }
    private func record(success: Bool, message: String) {
        guard let data = try? JSONSerialization.data(withJSONObject: ["success": success, "message": message, "version": plan.version]) else { return }
        try? CageUpdateFiles.write(data, to: URL(fileURLWithPath: plan.resultPath))
    }
    private func cleanSuccessfulTask() {
        let directory = URL(fileURLWithPath: plan.backup).deletingLastPathComponent()
        try? FileManager.default.removeItem(at: directory)
    }
    private func finishBeforeCommit(_ message: String) { send(["status": "error", "message": message]); exitHelper() }
    private func send(_ value: [String: Any]) {
        if var data = try? JSONSerialization.data(withJSONObject: value) { data.append(10); try? FileHandle.standardOutput.write(contentsOf: data) }
    }
    private func exitHelper() { DispatchQueue.main.async { NSApp.terminate(nil) } }
}
