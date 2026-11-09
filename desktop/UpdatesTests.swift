import Cocoa

@main
enum UpdatesTests {
    static func expect(_ condition: @autoclosure () -> Bool, _ message: String) throws {
        if !condition() { throw CageUpdateError("TEST FAILED: " + message) }
    }
    static func rejects(_ message: String, _ body: () throws -> Void) throws {
        var rejected = false
        do { try body() } catch { rejected = true }
        try expect(rejected, message)
    }
    static func main() throws {
        try expect(CageUpdateVersion.isNewer("0.2.0-alpha.10", than: "0.2.0-alpha.9"), "numeric prerelease ordering")
        try expect(CageUpdateVersion.isNewer("0.2.0", than: "0.2.0-alpha.10"), "stable promotion")
        for invalid in ["0.2.0-alpha.01", "../0.3.0", "1.0", "1.0.0/../../"] {
            try expect(!CageUpdateVersion.isNewer(invalid, than: "0.1.0"), "invalid version rejected")
        }
        try expect(!CageUpdateVersion.isNewer("0.2.0", than: "0.2.0"), "same version rejected")
        try expect(!CageUpdateVersion.isNewer("0.1.0", than: "0.2.0"), "downgrade rejected")

        let root = FileManager.default.temporaryDirectory.appendingPathComponent("cage-updates-test-" + UUID().uuidString)
        try CageUpdateFiles.privateDirectory(root)
        defer { try? FileManager.default.removeItem(at: root) }
        do {
            let saved = ProcessInfo.processInfo.environment["CLAUDE_CAGE_CONFIG"]
            let directory = FileManager.default.currentDirectoryPath
            defer {
                _ = FileManager.default.changeCurrentDirectoryPath(directory)
                if let saved { setenv("CLAUDE_CAGE_CONFIG", saved, 1) } else { unsetenv("CLAUDE_CAGE_CONFIG") }
            }
            setenv("CLAUDE_CAGE_CONFIG", "", 1)
            try expect(CageProfile.configURL() == FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(".claude-cage/config.json"), "empty profile agrees with backend default")
            try expect(FileManager.default.changeCurrentDirectoryPath(root.path), "enter isolated launch directory")
            let launchDirectory = FileManager.default.currentDirectoryPath
            setenv("CLAUDE_CAGE_CONFIG", "selected profile/config.json", 1)
            CageProfile.pinEnvironment()
            let pinned = CageProfile.configURL()
            try expect(pinned.path == launchDirectory + "/selected profile/config.json", "relative profile pinned at launch")
            try expect(FileManager.default.changeCurrentDirectoryPath(directory), "simulate helper working directory")
            try expect(CageProfile.configURL() == pinned, "helper keeps original profile after cwd changes")
            let inherited = try CageUpdateFiles.run("/bin/sh", ["-c", "printf '%s' \"$CLAUDE_CAGE_CONFIG\""])
            try expect(String(data: inherited, encoding: .utf8) == pinned.path, "actual child process inherits absolute profile")
        }
        let personalFiles = [
            "account.json", "favorites.json", "projects.json", "appearance.json", "workbench.json",
            "state/lima/persistent/disk", "webview/data/Local Storage/leveldb/000001.log",
            "workspace/project.txt"
        ]
        let profile = root.appendingPathComponent("profile")
        var personalData: [String: Data] = [:]
        for name in personalFiles {
            let file = profile.appendingPathComponent(name)
            try CageUpdateFiles.privateDirectory(file.deletingLastPathComponent())
            let bytes = Data("Keep \(name): 历史、收藏和登录状态\n\0".utf8)
            try CageUpdateFiles.write(bytes, to: file)
            personalData[name] = bytes
        }
        func verifyPersonalData() throws {
            for (name, expected) in personalData {
                let actual = try Data(contentsOf: profile.appendingPathComponent(name))
                try expect(actual == expected, "application replacement or rollback changed personal data: \(name)")
            }
        }
        // Exercise the production lipo invocation against a real Mach-O binary.
        // lipo consumes every argument after -verify_arch as an architecture.
        try CageUpdateFiles.verifyArchitecture(URL(fileURLWithPath: CommandLine.arguments[0]))
        let hashFile = root.appendingPathComponent("content")
        try CageUpdateFiles.write(Data("abc".utf8), to: hashFile)
        try rejects("non-Mach-O executable refused") { try CageUpdateFiles.verifyArchitecture(hashFile) }
        let hash = try CageUpdateFiles.digest(hashFile)
        try expect(hash == "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad", "stream digest")
        let permissions = try FileManager.default.attributesOfItem(atPath: hashFile.path)
        try expect((permissions[.posixPermissions] as? NSNumber)?.intValue == 0o600, "private file mode")
        let escaped = root.appendingPathComponent("escaped.app")
        try CageUpdateFiles.privateDirectory(escaped)
        try FileManager.default.createSymbolicLink(at: escaped.appendingPathComponent("outside"), withDestinationURL: hashFile)
        try rejects("app symlink escape") { try CageUpdateFiles.validateTree(escaped) }

        let old = root.appendingPathComponent("Cage.app"), next = root.appendingPathComponent("next.app"), backup = root.appendingPathComponent("backup.app")
        for location in [old, next, backup] { try CageUpdateFiles.privateDirectory(location) }
        try Data("old".utf8).write(to: old.appendingPathComponent("marker"))
        try Data("old".utf8).write(to: backup.appendingPathComponent("marker"))
        try Data("new".utf8).write(to: next.appendingPathComponent("marker"))
        try CageUpdateFiles.replaceAndVerify(target: old, candidate: next, backup: backup, manager: .default) {
            let marker = try String(contentsOf: $0.appendingPathComponent("marker"), encoding: .utf8)
            try expect(marker == "new", "new content reaches final install path")
        }
        try verifyPersonalData()
        try CageUpdateFiles.restore(backup: backup, target: old, manager: .default)
        try verifyPersonalData()
        let restored = try String(contentsOf: old.appendingPathComponent("marker"), encoding: .utf8)
        try expect(restored == "old", "launch failure can restore original")
        try CageUpdateFiles.privateDirectory(next)
        try Data("invalid".utf8).write(to: next.appendingPathComponent("marker"))
        try rejects("post-install validation must roll back") {
            try CageUpdateFiles.replaceAndVerify(target: old, candidate: next, backup: backup, manager: .default) { _ in throw CageUpdateError("verification failed") }
        }
        let rollback = try String(contentsOf: old.appendingPathComponent("marker"), encoding: .utf8)
        try expect(rollback == "old", "failed validation leaves original installed")
        try verifyPersonalData()
        try CageUpdateFiles.privateDirectory(next)
        try Data("invalid-after-replace".utf8).write(to: next.appendingPathComponent("marker"))
        var replaced = false
        try rejects("post-install and rollback failure must remain a changed transaction") {
            try CageUpdateFiles.replaceAndVerify(target: old, candidate: next, backup: backup, manager: .default,
                onReplace: { replaced = true }, restoreOriginal: { throw CageUpdateError("injected restore I/O failure") }) { _ in
                    throw CageUpdateError("injected validation failure")
                }
        }
        try expect(replaced, "replacement must be recorded before validation or rollback can fail")
        let failedTarget = try String(contentsOf: old.appendingPathComponent("marker"), encoding: .utf8)
        let retainedBackup = try String(contentsOf: backup.appendingPathComponent("marker"), encoding: .utf8)
        try expect(failedTarget == "invalid-after-replace" && retainedBackup == "old", "double failure must retain the original backup and report the changed target")
        try CageUpdateFiles.restore(backup: backup, target: old, manager: .default)
        try verifyPersonalData()
        try rejects("unsigned application refused") { _ = try CageUpdateFiles.signingInfo(old, bundle: true) }

        // A real child that ignores SIGTERM must not keep cancellation pending.
        let running = root.appendingPathComponent("validation-running")
        let task = CageUpdateValidation()
        DispatchQueue.global().async {
            defer { task.finish() }
            _ = try? CageUpdateFiles.run("/bin/sh", ["-c", "trap '' TERM; touch \"$1\"; exec /bin/sleep 30", "validation", running.path], validation: task)
        }
        let startedDeadline = Date().addingTimeInterval(5)
        while !FileManager.default.fileExists(atPath: running.path) && Date() < startedDeadline { Thread.sleep(forTimeInterval: 0.01) }
        try expect(FileManager.default.fileExists(atPath: running.path), "real validation child started")
        let cancelledAt = Date()
        task.cancel(); task.wait()
        try expect(Date().timeIntervalSince(cancelledAt) < 5, "unresponsive verification process is killed and joined")

        // Mount only this test's own tiny image. Cancellation inside the real
        // mount scope must still execute hdiutil detach before completion.
        let contents = root.appendingPathComponent("image-source")
        try CageUpdateFiles.privateDirectory(contents)
        try Data("fixture".utf8).write(to: contents.appendingPathComponent("marker"))
        let archive = root.appendingPathComponent("fixture.dmg")
        _ = try CageUpdateFiles.run("/usr/bin/hdiutil", ["create", "-size", "20m", "-fs", "HFS+", "-volname", "CageUpdateQA", "-srcfolder", contents.path, archive.path])
        let mountTask = CageUpdateValidation()
        try rejects("cancelled mounted verification") {
            try CageUpdateFiles.withMountedImage(archive: archive, directory: root.appendingPathComponent("verification"), validation: mountTask) { mount in
                try expect(FileManager.default.fileExists(atPath: mount.appendingPathComponent("marker").path), "test image mounted")
                mountTask.cancel()
                try mountTask.checkCancellation()
            }
        }
        mountTask.finish(); mountTask.wait()
        let mounts = try CageUpdateFiles.run("/usr/bin/hdiutil", ["info", "-plist"])
        let info = try PropertyListSerialization.propertyList(from: mounts, format: nil) as? [String: Any]
        let images = info?["images"] as? [[String: Any]] ?? []
        try expect(!images.contains(where: { $0["image-path"] as? String == archive.path }), "cancellation detached its own image")

        // A just-installed instance can be rolled back natively until the
        // trusted workbench explicitly confirms its API initialization.
        let savedEnvironment = ProcessInfo.processInfo.environment
        let confirmation = UUID().uuidString
        setenv("CAGE_UPDATE_CONFIRMATION", confirmation, 1)
        setenv("CLAUDE_CAGE_CONFIG", root.appendingPathComponent("profile/config.json").path, 1)
        defer {
            for key in ["CAGE_UPDATE_CONFIRMATION", "CLAUDE_CAGE_CONFIG"] {
                if let value = savedEnvironment[key] { setenv(key, value, 1) } else { unsetenv(key) }
            }
        }
        let updater = CageAppUpdater(bundleURL: old, onChange: { _ in }, quit: {}, reconnect: {})
        try expect(updater.awaitingInitialWorkbench, "newly installed uninitialized window permits native rollback termination")
        updater.markLaunchReady()
        try expect(!updater.awaitingInitialWorkbench, "initialized workbench keeps ordinary quit confirmation")
        try expect(FileManager.default.fileExists(atPath: root.appendingPathComponent("profile/updates/launch-\(confirmation).json").path), "explicit workbench readiness writes installer acknowledgement")

        if CommandLine.arguments.count == 2 {
            let signed = URL(fileURLWithPath: CommandLine.arguments[1])
            let team = try CageUpdateFiles.signingInfo(signed, bundle: true)
            let version = Bundle(url: signed)?.object(forInfoDictionaryKey: "CageVersion") as? String ?? ""
            try CageUpdateFiles.verifyApp(signed, version: version, team: team, gatekeeper: false)
            try rejects("different publisher refused") { _ = try CageUpdateFiles.signingInfo(signed, bundle: true, expectedTeam: "AAAAAAAAAA") }
            let tampered = root.appendingPathComponent("tampered.app")
            try FileManager.default.copyItem(at: signed, to: tampered)
            let plist = tampered.appendingPathComponent("Contents/Info.plist")
            try Data("tampered".utf8).write(to: plist)
            try rejects("real signature tampering refused") { _ = try CageUpdateFiles.signingInfo(tampered, bundle: true, expectedTeam: team) }
            print("PASS: real App architecture and Developer ID signature, wrong publisher, and tampered App checks")
        }
        print("PASS: absolute profile inheritance, personal data preservation, versions, containment, replacement/double-failure rollback, process cancellation, and temporary image cleanup")
    }
}
