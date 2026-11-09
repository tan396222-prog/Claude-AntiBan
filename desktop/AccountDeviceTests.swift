import Foundation
import Security
import LocalAuthentication

@main
enum AccountDeviceTests {
    static func check(_ value: Bool, line: UInt = #line) throws {
        if !value { throw CageAccountError("AccountDeviceTests failed at line \(line)") }
    }
    static func main() throws {
        let source = "cage-account-test-" + UUID().uuidString
        let destination = "cage-account-test-" + UUID().uuidString
        let credential = ["id": UUID().uuidString, "secret": UUID().uuidString]
        let kinds = CommandLine.arguments.contains("--biometric") ? ["automatic", "biometric"] : ["automatic"]
        for kind in kinds {
            defer {
                try? CageAccountKeychain.remove(binding: source, kind: kind)
                try? CageAccountKeychain.remove(binding: destination, kind: kind)
            }
            let present = kind == "biometric" ? ["available", "authentication_required"] : ["available"]
            try CageAccountKeychain.store(credential, binding: source, kind: kind)
            try check(present.contains(CageAccountKeychain.status(binding: source, kind: kind, expectedID: credential["id"])))
            // The same server record at a new directory must not inherit the
            // old path's enrollment. Checking metadata must not prompt.
            try check(CageAccountKeychain.status(binding: destination, kind: kind, expectedID: credential["id"]) == "missing")
            let mismatched = CageAccountKeychain.status(binding: source, kind: kind, expectedID: "other-id")
            try check(mismatched == "missing" || (kind == "biometric" && mismatched == "authentication_required"))
            try check(CageAccountKeychain.status(binding: source, kind: kind, expectedID: nil) == "disabled")
            try CageAccountKeychain.store(credential, binding: destination, kind: kind)
            try check(present.contains(CageAccountKeychain.status(binding: destination, kind: kind, expectedID: credential["id"])))
            if kind == "automatic" {
                let read = try CageAccountKeychain.read(binding: destination, kind: kind)
                try check(read["id"] as? String == credential["id"])
                try check(read["secret"] as? String == credential["secret"])
            }
            try CageAccountKeychain.remove(binding: destination, kind: kind)
            try check(CageAccountKeychain.status(binding: destination, kind: kind, expectedID: credential["id"]) == "missing")
            print("PASS: \(kind) binding, migration, metadata/authentication status, re-enrollment and removal")
        }
        // A legacy item without ID metadata remains compatible at its binding.
        try check(CageAccountKeychain.availability(status: errSecSuccess, attributes: [:], expectedID: "legacy", kind: "automatic") == "available")
        for error in [errSecInteractionNotAllowed, errSecNotAvailable, errSecMissingEntitlement, errSecAuthFailed, errSecUserCanceled] {
            try check(CageAccountKeychain.availability(status: error, attributes: nil, expectedID: "id", kind: "automatic") == "unavailable")
        }
        print("PASS: legacy metadata and transient/permission errors are not treated as missing credentials")
    }
}
