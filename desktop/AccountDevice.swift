import Foundation
import Security
import LocalAuthentication
import CryptoKit

struct CageAccountError: LocalizedError {
    let message: String
    var errorDescription: String? { message }
    init(_ message: String) { self.message = message }
}

// Secrets stay between the native launcher, macOS Keychain and the loopback
// controller. The web view receives status only, never the device credential.
final class CageAccountDevice: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    private lazy var session: URLSession = {
        let config = URLSessionConfiguration.ephemeral
        config.connectionProxyDictionary = [:]
        config.timeoutIntervalForRequest = 15
        return URLSession(configuration: config, delegate: self, delegateQueue: nil)
    }()
    private let base: URL
    private let ticket: String
    private let connection: String

    init(entry: URL, connection: String = "local") throws {
        guard entry.scheme == "http", entry.host == "localhost", let ticket = entry.fragment, !ticket.isEmpty else { throw CageAccountError("本地账户连接无效") }
        var parts = URLComponents(url: entry, resolvingAgainstBaseURL: false)!
        parts.fragment = nil; parts.path = ""; parts.query = nil
        base = parts.url!; self.ticket = ticket; self.connection = connection
        super.init()
    }
    deinit { session.invalidateAndCancel() }
    func urlSession(_ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse, newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void) { completionHandler(nil) }
    private func call(_ path: String, _ body: [String: Any]? = nil) async throws -> [String: Any] {
        var request = URLRequest(url: base.appendingPathComponent("api/account" + path))
        if path == "@session" { request.url = base.appendingPathComponent("api/session") }
        if path == "@context" { request.url = base.appendingPathComponent("api/client/context") }
        request.httpMethod = body == nil ? "GET" : "POST"
        request.setValue(base.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/")), forHTTPHeaderField: "Origin")
        request.setValue(connection, forHTTPHeaderField: "X-Cage-Connection")
        if let body { request.setValue("application/json", forHTTPHeaderField: "Content-Type"); request.httpBody = try JSONSerialization.data(withJSONObject: body) }
        let (data, response) = try await session.data(for: request)
        guard let response = response as? HTTPURLResponse, response.statusCode == 200 else {
            throw CageAccountError(String(data: data.prefix(512), encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "账户操作未完成")
        }
        guard let value = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw CageAccountError("本地账户响应无效") }
        return value
    }
}

enum CageAccountKeychain {
    private static let service = "io.github.lee-xeo.claude-cage.account"
    private static func query(binding: String, kind: String) -> [String: Any] {
        var result: [String: Any] = [kSecClass as String: kSecClassGenericPassword,
         kSecAttrService as String: service,
         kSecAttrAccount as String: binding + "." + kind]
        if kind == "biometric" {
            result[kSecUseDataProtectionKeychain as String] = true
            result[kSecAttrSynchronizable as String] = false
        }
        return result
    }
    static func remove(binding: String, kind: String) throws {
        let status = SecItemDelete(query(binding: binding, kind: kind) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw CageAccountError("无法移除本机钥匙串记录，请重试") }
    }
    static func store(_ credential: [String: Any], binding: String, kind: String) throws {
        var item = query(binding: binding, kind: kind)
        item[kSecValueData as String] = try JSONSerialization.data(withJSONObject: credential)
        if let id = credential["id"] as? String { item[kSecAttrGeneric as String] = Data(id.utf8) }
        item[kSecAttrLabel as String] = kind == "biometric" ? "Cage 指纹解锁" : "Cage 记住登录"
        if kind == "biometric" {
            var error: Unmanaged<CFError>?
            guard let access = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly, .biometryCurrentSet, &error) else { throw CageAccountError("这台 Mac 无法创建指纹保护的凭据") }
            item[kSecAttrAccessControl as String] = access
        }
        try remove(binding: binding, kind: kind)
        let status = SecItemAdd(item as CFDictionary, nil)
        guard status == errSecSuccess else { throw CageAccountError("macOS 未能保存快捷登录凭据（\(status)），密码登录仍然可用") }
    }
    // Inspect metadata only. Status checks must not read the secret or ask for
    // Touch ID, and must never search credentials belonging to another path.
    static func status(binding: String, kind: String, expectedID: String?) -> String {
        guard let expectedID else { return "disabled" }
        var item = query(binding: binding, kind: kind)
        item[kSecReturnAttributes as String] = true
        item[kSecMatchLimit as String] = kSecMatchLimitOne
        let context = LAContext()
        context.interactionNotAllowed = true
        item[kSecUseAuthenticationContext as String] = context
        var result: CFTypeRef?
        let status = SecItemCopyMatching(item as CFDictionary, &result)
        return availability(status: status, attributes: result as? [String: Any], expectedID: expectedID, kind: kind)
    }
    static func availability(status: OSStatus, attributes: [String: Any]?, expectedID: String, kind: String) -> String {
        if status == errSecItemNotFound { return "missing" }
        // A protected item can require Touch ID even to return attributes.
        // Preserve its explicit unlock button without claiming it is verified.
        if kind == "biometric" && status == errSecInteractionNotAllowed { return "authentication_required" }
        guard status == errSecSuccess, let attributes else { return "unavailable" }
        // Older entries have no ID metadata. They remain usable at their
        // original binding; unlock still validates the ID in the secret.
        if let id = attributes[kSecAttrGeneric as String] as? Data, id != Data(expectedID.utf8) { return "missing" }
        return "available"
    }
    static func read(binding: String, kind: String) throws -> [String: Any] {
        var item = query(binding: binding, kind: kind)
        item[kSecReturnData as String] = true; item[kSecMatchLimit as String] = kSecMatchLimitOne
        let context = LAContext()
        context.localizedReason = "解锁你的 Cage 工作空间"
        context.localizedFallbackTitle = ""
        if kind == "automatic" {
            // The login Keychain is local to this Mac and its ACL trusts the
            // signed app. Automatic reopen must never trigger an OS prompt.
            context.interactionNotAllowed = true
        }
        item[kSecUseAuthenticationContext as String] = context
        var result: CFTypeRef?
        let status = SecItemCopyMatching(item as CFDictionary, &result)
        if status == errSecUserCanceled { throw CageAccountError("已取消指纹验证，可继续使用密码") }
        guard status == errSecSuccess, let data = result as? Data,
              let value = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw CageAccountError("快捷登录不可用，请使用密码解锁后重新开启") }
        return value
    }
}

extension CageAccountDevice {
    func handle(operation: String, kind: String?) async throws -> [String: Any] {
        defer { session.invalidateAndCancel() }
        _ = try await call("@session", ["token": ticket])
        let account = try await call("")
        guard let accountBinding = account["device_binding"] as? String, accountBinding.count == 64 else { throw CageAccountError("当前后台不支持快捷登录，请先完成更新") }
        var binding = accountBinding
        if connection != "local" {
            let selected = try await call("@context")
            guard selected["connection_id"] as? String == connection else { throw CageAccountError("工作环境已切换，请重新解锁") }
            if selected["mode"] as? String == "remote" {
                guard let host = selected["host_id"] as? String, !host.isEmpty,
                      let fingerprint = selected["host_fingerprint"] as? String, fingerprint.hasPrefix("SHA256:"),
                      let instance = selected["instance_id"] as? String, instance.count == 64 else { throw CageAccountError("远程连接已经切换，请重新解锁当前主机") }
                let scoped = ["remote", host, fingerprint, instance, accountBinding].joined(separator: "\u{0}")
                binding = SHA256.hash(data: Data(scoped.utf8)).map { String(format: "%02x", $0) }.joined()
            } else if selected["mode"] as? String != "local" {
                throw CageAccountError("请先选择工作环境")
            }
        }
        let context = LAContext()
        let biometricAvailable = context.canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: nil)
        if operation == "status" {
            let devices = account["devices"] as? [String: String] ?? [:]
            let states = Dictionary(uniqueKeysWithValues: ["automatic", "biometric"].map { kind in
                (kind, CageAccountKeychain.status(binding: binding, kind: kind, expectedID: devices[kind]))
            })
            return ["supported": true, "biometric_available": biometricAvailable, "device_status": states]
        }
        guard let kind, ["automatic", "biometric"].contains(kind) else { throw CageAccountError("快捷登录类型无效") }
        switch operation {
        case "enable":
            if kind == "biometric" && !biometricAvailable { throw CageAccountError("请先在 macOS 系统设置中设置 Touch ID") }
            let credential = try await call("/device-enroll", ["kind": kind])
            do { try CageAccountKeychain.store(credential, binding: binding, kind: kind) }
            catch {
                _ = try? await call("/device-revoke", ["kind": kind])
                throw error
            }
        case "disable":
            _ = try await call("/device-revoke", ["kind": kind])
            try CageAccountKeychain.remove(binding: binding, kind: kind)
        case "unlock":
            // Check persisted manual-lock state before touching the Keychain.
            if kind == "automatic" && account["require_authentication"] as? Bool == true { throw CageAccountError("Cage 已手动锁定，请使用指纹或密码") }
            guard let devices = account["devices"] as? [String: String], let expectedID = devices[kind] else { throw CageAccountError("请先使用密码登录，再开启快捷登录") }
            let credential = try CageAccountKeychain.read(binding: binding, kind: kind)
            guard credential["id"] as? String == expectedID else { throw CageAccountError("快捷登录授权已失效，请使用密码") }
            _ = try await call("/device-unlock", ["kind": kind, "credential": credential])
        default: throw CageAccountError("快捷登录操作无效")
        }
        return ["ok": true]
    }
}
