// A phone sees the same signed Claude window as the computer. Its lease and
// input focus belong to one GUI login, window and captured geometry.
import AppKit
import ScreenCaptureKit
import ImageIO

struct MacPhoneDesktopInput: Decodable {
    let id: String
    var width, height: Int?
    var pixel_ratio: Double?
    var type, document, focus, text, key: String?
    var x, y, dx, dy: Double?
}

func phoneDesktopJPEG(_ image: CGImage) throws -> Data {
    for quality in [0.9, 0.78, 0.62, 0.45] {
        let data = try encodedImage(image, type: .jpeg,
            properties: [kCGImageDestinationLossyCompressionQuality: quality] as CFDictionary)
        // Leave room for base64 expansion and the complete phone frame metadata.
        if data.count <= 2 << 20 { return data }
    }
    throw Failure(8, "frame_limit")
}

private func matchesPhoneFocus(_ requested: String?, token: String, previous: AXUIElement?, current: AXUIElement?, password: Bool) -> Bool {
    guard !token.isEmpty, requested == token, let previous, let current, !password else { return false }
    return CFEqual(previous, current)
}

@MainActor
final class MacPhoneDesktop {
    static let shared = MacPhoneDesktop()

    @MainActor private final class Session {
        let id, secret: String
        let epoch: UInt64
        let canvas = ClaudeCanvasSession()
        var width: Int
        var height: Int
        var ratio: Double
        var geometry = ""
        var document = ""
        var focus = ""
        var focusedElement: AXUIElement?
        var window: CGWindowID?
        var pid: pid_t?
        var touched = Date()

        init(_ input: MacPhoneDesktopInput, secret: String, epoch: UInt64) throws {
            let size = try MacPhoneDesktop.viewport(input)
            id = input.id
            self.secret = secret
            self.epoch = epoch
            width = size.0; height = size.1; ratio = size.2
        }
    }

    private var current: Session?

    private static func viewport(_ input: MacPhoneDesktopInput) throws -> (Int, Int, Double) {
        guard let width = input.width, let height = input.height,
              (240...1440).contains(width), (160...1600).contains(height) else {
            throw Failure(8, "invalid_input")
        }
        let ratio = input.pixel_ratio ?? 1
        guard ratio.isFinite, (1...8).contains(ratio) else { throw Failure(8, "invalid_input") }
        return (width, height, ratio)
    }

    private func validate(_ session: Session) throws {
        guard current === session, Date().timeIntervalSince(session.touched) < 120 else {
            throw Failure(7, "phone_session_ended")
        }
        do { try MacGUISession.shared.validate(session.epoch) }
        catch { current = nil; throw error }
        session.touched = Date()
    }

    func handle(_ request: Request) async throws -> [String: Any] {
        guard let input = request.phone, UUID(uuidString: input.id) != nil,
              let secret = request.phone_token, (32...128).contains(secret.utf8.count) else {
            throw Failure(8, "invalid_input")
        }
        if request.action == "phone-close" {
            if current?.id == input.id && current?.secret == secret { current = nil }
            return ["id": input.id]
        }
        if request.action == "phone-open" {
            let epoch = try MacGUISession.shared.validate()
            let session = try Session(input, secret: secret, epoch: epoch)
            current = session
            do {
                try await launchClaudeWindow()
                try validate(session)
            } catch {
                if current === session { current = nil }
                throw error
            }
            return ["id": session.id]
        }
        guard let session = current, session.id == input.id, session.secret == secret else {
            throw Failure(7, "phone_session_ended")
        }
        try validate(session)
        switch request.action {
        case "phone-frame":
            return try await frame(session)
        case "phone-action":
            if input.type == "resize" {
                let (width, height, ratio) = try Self.viewport(input)
                session.width = width; session.height = height; session.ratio = ratio
                session.document = ""; session.focus = ""; session.focusedElement = nil
                return ["id": session.id]
            }
            try await action(input, session: session)
            return ["id": session.id]
        default:
            throw Failure(8, "unknown_action")
        }
    }

    private func frame(_ session: Session) async throws -> [String: Any] {
        var (window, selection) = try await selectedWindow()
        try validate(session)
        let capture = selection ?? SCContentFilter(desktopIndependentWindow: window)
        let layout = try await session.canvas.layout(window, filter: capture,
            width: max(320, session.width), height: max(240, session.height), scale: min(2, session.ratio))
        try validate(session)
        if window.frame != layout.frame {
            (window, selection) = try await selectedWindow()
            guard window.windowID == layout.id, window.owningApplication?.processID == layout.pid else {
                throw Failure(7, "window_changed")
            }
        }
        let raw = try await session.canvas.capture(window,
            filter: selection ?? SCContentFilter(desktopIndependentWindow: window),
            layout: layout, binary: true, phoneJPEG: true)
        try validate(session)
        guard let data = raw["imageBytes"] as? Data, let geometry = raw["geometry"] as? String else {
            throw Failure(8, "capture_failed")
        }
        if session.geometry != geometry || session.document.isEmpty {
            session.document = UUID().uuidString
            session.focus = ""; session.focusedElement = nil
        }
        session.geometry = geometry; session.window = layout.id; session.pid = layout.pid
        let focus = editableFocus(window, session: session)
        if let element = focus.element {
            if session.focusedElement.map({ CFEqual($0, element) }) != true {
                session.focus = UUID().uuidString
            }
            session.focusedElement = element
        } else {
            session.focusedElement = nil; session.focus = ""
        }
        let reply: [String: Any] = ["id": session.id, "document": session.document,
            "image": data.base64EncodedString(), "width": session.width, "height": session.height,
            "content_width": layout.content.width, "content_height": layout.content.height,
            "pixel_width": raw["pixelWidth"] ?? 0, "pixel_height": raw["pixelHeight"] ?? 0,
            "requested_pixel_ratio": session.ratio, "pixel_ratio_control": true,
            "focus": session.focus, "password": focus.password, "window": "desktop", "shared": true,
            "login_supported": false]
        guard try JSONSerialization.data(withJSONObject: reply).count <= 3 << 20 else {
            throw Failure(8, "frame_limit")
        }
        return reply
    }

    private func editableFocus(_ window: SCWindow, session: Session) -> (element: AXUIElement?, password: Bool) {
        guard (try? session.canvas.inputGeometry(session.geometry, window: window)) != nil,
              let pid = window.owningApplication?.processID else { return (nil, false) }
        func attribute(_ element: AXUIElement, _ name: String) -> CFTypeRef? {
            var value: CFTypeRef?
            return AXUIElementCopyAttributeValue(element, name as CFString, &value) == .success ? value : nil
        }
        let application = AXUIElementCreateApplication(pid)
        guard let raw = attribute(application, kAXFocusedUIElementAttribute), CFGetTypeID(raw) == AXUIElementGetTypeID(),
              let owner = attribute(application, kAXFocusedWindowAttribute) else { return (nil, false) }
        let element = raw as! AXUIElement
        guard let elementWindow = attribute(element, kAXWindowAttribute), CFEqual(owner, elementWindow),
              let role = attribute(element, kAXRoleAttribute) as? String,
              [kAXTextFieldRole, kAXTextAreaRole, kAXComboBoxRole].contains(role) else { return (nil, false) }
        let password = attribute(element, kAXSubroleAttribute) as? String == kAXSecureTextFieldSubrole
        return (element, password)
    }

    private func action(_ input: MacPhoneDesktopInput, session: Session) async throws {
        guard !session.document.isEmpty, input.document == session.document else { throw Failure(7, "phone_document_changed") }
        let (window, _) = try await selectedWindow()
        try validate(session)
        guard window.windowID == session.window, window.owningApplication?.processID == session.pid else {
            throw Failure(7, "window_changed")
        }
        let canvas = try session.canvas.inputGeometry(session.geometry, window: window)
        var native = Request(action: "")
        var scrollX: Double = 0
        let typing = input.type == "text" || (input.type == "key" && !["Tab", "Escape", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].contains(input.key ?? ""))
        if typing {
            let fresh = editableFocus(window, session: session)
            guard matchesPhoneFocus(input.focus, token: session.focus, previous: session.focusedElement,
                                    current: fresh.element, password: fresh.password) else { throw Failure(7, "phone_focus_changed") }
        }
        switch input.type {
        case "text":
            native = Request(action: "text", text: input.text)
        case "key":
            if let key = input.key, key.utf8.count == 1, let scalar = key.unicodeScalars.first, (32...126).contains(scalar.value) {
                native = Request(action: "text", text: key)
            } else { native = Request(action: "key", key: input.key) }
        case "tap", "scroll":
            guard let x = input.x, let y = input.y, x.isFinite, y.isFinite,
                  (0..<Double(canvas.content.width)).contains(x), (0..<Double(canvas.content.height)).contains(y) else {
                throw Failure(8, "invalid_input")
            }
            native = Request(action: input.type == "tap" ? "click" : "scroll",
                x: x / Double(canvas.content.width), y: y / Double(canvas.content.height), delta: input.dy)
            if input.type == "scroll" {
                guard let dx = input.dx, let dy = input.dy, dx.isFinite, dy.isFinite,
                      (-3200...3200).contains(dx), (-3200...3200).contains(dy) else { throw Failure(8, "invalid_input") }
                scrollX = dx
                native.delta = dy
            }
        default:
            throw Failure(8, "invalid_input")
        }
        native.window = window.windowID; native.pid = window.owningApplication?.processID
        try sendInput(native, window: window, canvas: canvas, maxTextBytes: 8192, scrollX: scrollX, maxScroll: 32000)
    }

    static func selfTest() throws {
        let local = AXUIElementCreateApplication(getpid())
        let other = AXUIElementCreateApplication(getpid() + 1)
        let accepts = matchesPhoneFocus("focus", token: "focus", previous: local, current: local, password: false)
        let rejects = [
            matchesPhoneFocus("", token: "", previous: local, current: local, password: false),
            matchesPhoneFocus("old", token: "focus", previous: local, current: local, password: false),
            matchesPhoneFocus("focus", token: "focus", previous: local, current: other, password: false),
            matchesPhoneFocus("focus", token: "focus", previous: local, current: nil, password: false),
            matchesPhoneFocus("focus", token: "focus", previous: local, current: local, password: true)
        ]
        guard accepts, rejects.allSatisfy({ !$0 }) else { throw Failure(8, "focus_test_failed") }
        guard let context = CGContext(data: nil, width: 640, height: 480, bitsPerComponent: 8,
            bytesPerRow: 0, space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue) else { throw Failure(8, "image_test_failed") }
        context.setFillColor(CGColor(red: 0.1, green: 0.2, blue: 0.3, alpha: 1))
        context.fill(CGRect(x: 0, y: 0, width: 640, height: 480))
        guard let image = context.makeImage() else { throw Failure(8, "image_test_failed") }
        let jpeg = try phoneDesktopJPEG(image)
        guard jpeg.starts(with: [0xff, 0xd8]), let source = CGImageSourceCreateWithData(jpeg as CFData, nil),
              let decoded = CGImageSourceCreateImageAtIndex(source, 0, nil), decoded.width == 640, decoded.height == 480,
              try JSONSerialization.data(withJSONObject: ["image": jpeg.base64EncodedString()]).count < 3 << 20 else { throw Failure(8, "jpeg_test_failed") }
        print("phone desktop focus token and real AX identity checks passed; JPEG 640x480 encoding and total JSON limit passed")
    }
}
