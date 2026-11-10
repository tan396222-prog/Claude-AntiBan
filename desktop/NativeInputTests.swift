import AppKit
import WebKit

private final class RecordingWebView: WKWebView {
    var scripts: [String] = []
    override func evaluateJavaScript(_ javaScriptString: String, completionHandler: (@MainActor @Sendable (Any?, Error?) -> Void)? = nil) {
        scripts.append(javaScriptString)
        completionHandler?(nil, nil)
    }
}

@main
enum NativeInputTests {
    static func main() {
        for number in 1...20 {
            let name = "F\(number)"
            let code = MacKeyboard.keyCodes[name]!
            let identity = MacKeyboard.identity(code, characters: String(UnicodeScalar(0xf703 + number)!))
            precondition(identity.key == name && identity.code == name, "function key became private-use text")
        }
        precondition(MacKeyboard.identity(83, characters: "1").code == "Numpad1")
        precondition(MacKeyboard.identity(76, characters: "\r").key == "Enter")
        precondition(MacKeyboard.virtualKey(code: "KeyW", key: "z", shortcut: true) == 6, "logical shortcut followed a foreign keyboard layout")
        let app = NSApplication.shared
        app.setActivationPolicy(.prohibited)
        let window = NSWindow(contentRect: NSRect(x: 120, y: 180, width: 900, height: 700),
                              styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        let web = RecordingWebView(frame: window.contentView!.bounds)
        window.contentView!.addSubview(web)
        let input = CageNativeInput(frame: .zero)
        input.configure(web: web)
        window.contentView!.addSubview(input)
        let owner = UUID().uuidString
        func check(_ x: Double, _ y: Double, _ height: Double) -> NSRect {
            precondition(input.handle(["operation":"activate", "owner":owner,
                                       "x":x, "y":y, "height":height]))
            let screen = input.firstRect(forCharacterRange: NSRange(location: 0, length: 0), actualRange: nil)
            let top = window.convertToScreen(web.convert(NSRect(x: x, y: 0, width: 0, height: 0), to: nil)).minY
            // Expected bounds measured from the real WebView top on screen.
            precondition(abs(screen.minX - (window.convertToScreen(web.convert(.zero, to:nil)).minX + x)) < 0.5)
            precondition(abs(screen.maxY - (top - y)) < 0.5, "candidate caret was vertically mirrored")
            precondition(abs(screen.height - height) < 0.5)
            return screen
        }
        precondition(web.isFlipped, "WKWebView coordinate contract changed")
        let upper = check(120, 80, 24)
        let lower = check(340, 580, 32)
        precondition(lower.minY < upper.minY, "moving the caret down must move candidates down")
        window.setFrameOrigin(NSPoint(x: 320, y: 280))
        let moved = check(340, 580, 32)
        precondition(abs(moved.minX - lower.minX - 200) < 0.5)
        precondition(abs(moved.minY - lower.minY - 100) < 0.5)
        web.setFrameSize(NSSize(width: 1000, height: 800))
        _ = check(500, 680, 38)
        for number in 1...20 {
            let code = MacKeyboard.keyCodes["F\(number)"]!
            let character = String(UnicodeScalar(0xf703 + number)!)
            let event = NSEvent.keyEvent(with: .keyDown, location: .zero, modifierFlags: [], timestamp: 0,
                windowNumber: window.windowNumber, context: nil, characters: character,
                charactersIgnoringModifiers: character, isARepeat: false, keyCode: code)!
            web.scripts.removeAll()
            input.keyDown(with: event)
            input.keyUp(with: event)
            precondition(web.scripts.count == 2 && web.scripts.allSatisfy { $0.contains("\"code\":\"F\(number)\"") },
                         "function key was consumed by host text interpretation")
        }
        precondition(!input.handle(["operation":"deactivate", "owner":UUID().uuidString]))
        precondition(input.handle(["operation":"deactivate", "owner":owner]))
        print("Native input F1-F20 down/up, coordinates, window movement, resize, and stale owner checks passed")
    }
}
