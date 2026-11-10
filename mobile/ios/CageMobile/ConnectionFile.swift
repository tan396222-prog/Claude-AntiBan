import Foundation
import Security

enum InputError: LocalizedError {
    case invalid, unavailable
    var errorDescription: String? {
        switch self {
        case .invalid: return "连接文件或指纹格式无效。"
        case .unavailable: return "无法读取已保存的凭据，请解锁手机或重新导入连接文件。"
        }
    }
}

struct Host: Codable, Equatable {
    let relay: String
    let pin: String

    init(relay: String, pin: String) throws {
        guard var u = URLComponents(string: relay.trimmingCharacters(in: .whitespacesAndNewlines)),
              u.scheme?.lowercased() == "https", let host = u.host, !host.isEmpty,
              u.user == nil, u.password == nil, u.query == nil, u.fragment == nil,
              u.path.isEmpty || u.path == "/", (1...65535).contains(u.port ?? 443) else { throw InputError.invalid }
        u.scheme = "https"; u.path = ""
        let pin = pin.trimmingCharacters(in: .whitespacesAndNewlines)
        let encoded = String(pin.dropFirst(7))
        guard pin.hasPrefix("sha256:"), let bytes = Data(base64Encoded: encoded), bytes.count == 32,
              bytes.base64EncodedString() == encoded, let address = u.url?.absoluteString else { throw InputError.invalid }
        self.relay = address; self.pin = pin
    }
}

struct Invitation: Decodable {
    let id: String
    let client_token: String
    let expires_at: String

    var expiry: Date? {
        let iso = ISO8601DateFormatter()
        iso.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        return iso.date(from: expires_at) ?? ISO8601DateFormatter().date(from: expires_at)
    }
}

struct HostAccess: Codable {
    let host: Host
    let lookup: String
}

enum ConnectionFile {
    case temporary(Invitation)
    case persistent(HostAccess)

    static func load(_ file: URL) throws -> Self {
        let access = file.startAccessingSecurityScopedResource()
        defer { if access { file.stopAccessingSecurityScopedResource() } }
        let handle = try FileHandle(forReadingFrom: file)
        defer { try? handle.close() }
        let data = try handle.read(upToCount: 4097) ?? Data()
        return try parse(data)
    }
    static func parse(_ data: Data) throws -> Self {
        guard data.count <= 4096, let object = try JSONSerialization.jsonObject(with: data) as? [String: Any] else { throw InputError.invalid }
        let keys = Set(object.keys)
        if keys == Set(["id", "client_token", "expires_at"]) {
            let value = try JSONDecoder().decode(Invitation.self, from: data)
            guard capability(value.id), capability(value.client_token), (value.expiry ?? .distantPast) > Date() else { throw InputError.invalid }
            return .temporary(value)
        }
        struct Record: Decodable { let version: Int; let relay, host_pin, lookup_token: String }
        guard keys == Set(["version", "relay", "host_pin", "lookup_token"]) else { throw InputError.invalid }
        let record = try JSONDecoder().decode(Record.self, from: data)
        guard record.version == 1, capability(record.lookup_token) else { throw InputError.invalid }
        return .persistent(HostAccess(host: try Host(relay: record.relay, pin: record.host_pin), lookup: record.lookup_token))
    }
    static func capability(_ s: String) -> Bool {
        guard s.utf8.count == 43 else { return false }
        let base = s.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/") + "="
        guard let bytes = Data(base64Encoded: base), bytes.count == 32 else { return false }
        return bytes.base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
            .trimmingCharacters(in: CharacterSet(charactersIn: "=")) == s
    }
}

enum HostKeychain {
    private static var query: [String: Any] {
        [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: "io.github.lee-xeo.claude-cage.mobile.saved-host",
         kSecAttrAccount as String: "v1", kSecAttrSynchronizable as String: false]
    }
    private static var collectionQuery: [String: Any] {
        var q = query; q[kSecAttrAccount as String] = "v2-hosts"; return q
    }
    private static func data(_ item: [String: Any]) throws -> Data? {
        var q = item
        q[kSecReturnData as String] = true
        q[kSecMatchLimit as String] = kSecMatchLimitOne
        var found: CFTypeRef?
        let status = SecItemCopyMatching(q as CFDictionary, &found)
        if status == errSecItemNotFound {return nil}
        guard status == errSecSuccess, let data = found as? Data else {throw InputError.unavailable}
        return data
    }
    static func readAll() throws -> [HostAccess] {
        let records: [HostAccess]
        if let data = try data(collectionQuery) {
            records = try JSONDecoder().decode([HostAccess].self, from: data)
        } else if let data = try data(query) {
            records = [try JSONDecoder().decode(HostAccess.self, from: data)]
        } else {return []}
        for record in records {
            guard ConnectionFile.capability(record.lookup),
                  try Host(relay: record.host.relay, pin: record.host.pin) == record.host else {throw InputError.invalid}
        }
        return records
    }
    static func read(host: Host? = nil) throws -> HostAccess? {
        let records = try readAll()
        if let host {return records.first {$0.host == host}}
        return records.last
    }
    private static func store(_ records: [HostAccess]) throws {
        let values: [String:Any] = [kSecValueData as String: try JSONEncoder().encode(records),
                                   kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly]
        var status = SecItemUpdate(collectionQuery as CFDictionary, values as CFDictionary)
        if status == errSecItemNotFound {
            status = SecItemAdd(collectionQuery.merging(values) {_, value in value} as CFDictionary, nil)
        }
        guard status == errSecSuccess else {throw InputError.unavailable}
        // The v2 collection is durable before removing its migrated v1 copy.
        let legacy = SecItemDelete(query as CFDictionary)
        guard legacy == errSecSuccess || legacy == errSecItemNotFound else {throw InputError.unavailable}
    }
    static func write(_ record: HostAccess) throws {
        var records = try readAll(); records.removeAll {$0.host == record.host}; records.append(record)
        try store(records)
    }
    static func forget(host: Host? = nil) throws {
        var records = try readAll()
        if let host {records.removeAll {$0.host == host}} else {records = []}
        // Writing the collection also prevents an old v1 record from reappearing.
        try store(records)
    }
}
