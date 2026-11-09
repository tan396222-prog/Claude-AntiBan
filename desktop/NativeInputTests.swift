import AppKit
import WebKit

@main
enum NativeInputTests {
    static func main() {
        let app = NSApplication.shared
        app.setActivationPolicy(.prohibited)
        let window = NSWindow(contentRect: NSRect(x: 120, y: 180, width: 900, height: 700),
                              styleMask: [.titled, .resizable], backing: .buffered, defer: false)
        let web = WKWebView(frame: window.contentView!.bounds)
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
        precondition(!input.handle(["operation":"deactivate", "owner":UUID().uuidString]))
        precondition(input.handle(["operation":"deactivate", "owner":owner]))
        print("Native input coordinates: top, bottom, window movement, resize, and stale owner checks passed")
    }
}
