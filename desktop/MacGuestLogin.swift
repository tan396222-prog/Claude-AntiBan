import Cocoa
import Security

// The trial credential belongs to this local VM. It is never passed to the
// WebKit page, the control service, logs, command-line arguments or iCloud.
enum MacGuestLoginCredential {
    private static func item() throws -> [String: Any] {
        guard CageProfile.macGuestTrial else { throw CageUpdateError("此操作只适用于本机 macOS 试用版。") }
        struct Binding: Decodable { let machineID: String; let user: String }
        let url = CageProfile.configURL().deletingLastPathComponent().appendingPathComponent("macos-guest.json")
        let binding = try JSONDecoder().decode(Binding.self, from: Data(contentsOf: url))
        return [kSecClass as String: kSecClassGenericPassword,
                kSecAttrService as String: "io.github.lee-xeo.claude-cage.macos-trial.guest-login",
                kSecAttrAccount as String: binding.user + "@" + binding.machineID,
                kSecAttrSynchronizable as String: false]
    }
    static func importFromPipe() throws {
        guard isatty(STDIN_FILENO) == 0 else { throw CageUpdateError("请通过私有管道提供试用客机凭据。") }
        let secret = FileHandle.standardInput.readData(ofLength: 1025)
        guard !secret.isEmpty, secret.count <= 1024 else { throw CageUpdateError("客机凭据为空或过长。") }
        var query = try item()
        query[kSecAttrLabel as String] = "Cage macOS 试用客机登录"
        query[kSecValueData as String] = secret
        let status = SecItemAdd(query as CFDictionary, nil)
        if status == errSecDuplicateItem, try read() == secret { return }
        guard status == errSecSuccess else { throw CageUpdateError("无法保存本机试用客机凭据（\(status)）。") }
    }
    private static func read() throws -> Data {
        var query = try item()
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var value: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &value)
        guard status == errSecSuccess, let data = value as? Data else { throw CageUpdateError("本机钥匙串中没有可用的试用客机密码。") }
        return data
    }
    static func copyForLogin() throws {
        guard let text = String(data: try read(), encoding: .utf8) else { throw CageUpdateError("客机密码格式无效。") }
        let item = NSPasteboardItem()
        item.setString(text, forType: .string)
        item.setData(Data(), forType: .init("org.nspasteboard.ConcealedType"))
        item.setData(Data(), forType: .init("org.nspasteboard.TransientType"))
        let board = NSPasteboard.general
        board.clearContents()
        guard board.writeObjects([item]) else { throw CageUpdateError("无法写入剪贴板。") }
        let change = board.changeCount
        DispatchQueue.main.asyncAfter(deadline: .now() + 60) {
            if board.changeCount == change { board.clearContents() }
        }
    }
}
