import AppKit
import WebKit

// Owns only the host's marked text. The guest owns the document and selection.
// The invisible NSView supplies the real remote caret to the input manager.
final class CageNativeInput: NSView, NSTextInputClient {
    weak var web: WKWebView?
    var owner = ""
    private var marked = NSAttributedString(string: "")
    private var selection = NSRange(location: 0, length: 0)
    private var caret = NSRect.zero
    private var commandEvent: NSEvent?
    private var discarding = false
    private lazy var nativeContext = NSTextInputContext(client: self)

    override var acceptsFirstResponder: Bool { true }
    override var inputContext: NSTextInputContext? { nativeContext }
    override func hitTest(_ point: NSPoint) -> NSView? { nil }

    func configure(web: WKWebView) { self.web = web }

    func handle(_ body: [String: Any]) -> Bool {
        guard let operation = body["operation"] as? String,
              let requestedOwner = body["owner"] as? String,
              UUID(uuidString: requestedOwner) != nil else { return false }
        if operation == "activate" {
            if owner != requestedOwner { cancel(); owner = requestedOwner }
            updateCaret(body)
            return window?.makeFirstResponder(self) == true
        }
        guard owner == requestedOwner else { return false }
        switch operation {
        case "caret": updateCaret(body)
        case "cancel": cancel()
        case "deactivate":
            cancel()
            owner = ""
            if window?.firstResponder === self, let web { window?.makeFirstResponder(web) }
        default: return false
        }
        return true
    }

    private func updateCaret(_ body: [String: Any]) {
        guard let web, let x = body["x"] as? Double, let y = body["y"] as? Double,
              let height = body["height"] as? Double,
              x.isFinite, y.isFinite, height.isFinite, height > 0 else { return }
        // CSS starts at the top. WKWebView is normally flipped already;
        // converting its rect to window/screen coordinates performs that
        // inversion. Only unflipped views need an explicit local inversion.
        let localY = web.isFlipped ? y : web.bounds.height - y - height
        caret = web.convert(NSRect(x: x, y: localY,
                                   width: 0, height: height), to: nil)
        inputContext?.invalidateCharacterCoordinates()
    }

    private func emit(_ kind: String, _ values: [String: Any] = [:]) {
        guard !owner.isEmpty, let web else { return }
        var event = values
        event["owner"] = owner
        event["kind"] = kind
        guard let data = try? JSONSerialization.data(withJSONObject: event),
              let json = String(data: data, encoding: .utf8) else { return }
        web.evaluateJavaScript("window.dispatchEvent(new CustomEvent('cage-native-input',{detail:\(json)}));", completionHandler: nil)
    }

    private func clear() {
        marked = NSAttributedString(string: "")
        selection = NSRange(location: 0, length: 0)
    }

    private func cancel() {
        if hasMarkedText() { emit("compositionend", ["text": ""]) }
        clear()
        discarding = true
        inputContext?.discardMarkedText()
        discarding = false
    }

    override func resignFirstResponder() -> Bool {
        cancel()
        emit("deactivated")
        owner = ""
        return super.resignFirstResponder()
    }

    override func keyDown(with event: NSEvent) {
        let physical = MacKeyboard.codes[event.keyCode] ?? ""
        if event.modifierFlags.contains(.command) || (!hasMarkedText() && physical.hasPrefix("F")) {
            sendKey(event, down: true)
            return
        }
        commandEvent = event
        if inputContext?.handleEvent(event) != true { interpretKeyEvents([event]) }
        commandEvent = nil
    }
    override func keyUp(with event: NSEvent) { sendKey(event, down: false) }
    override func flagsChanged(with event: NSEvent) {
        let flags: [UInt16: NSEvent.ModifierFlags] = [54: .command, 55: .command,
            56: .shift, 60: .shift, 57: .capsLock, 58: .option, 61: .option, 59: .control, 62: .control]
        if let flag = flags[event.keyCode] { sendKey(event, down: event.modifierFlags.contains(flag)) }
    }

    private func sendKey(_ event: NSEvent, down: Bool, text: String? = nil) {
        let identity = MacKeyboard.identity(event.keyCode, characters: text ?? event.charactersIgnoringModifiers ?? "")
        emit(down ? "keydown" : "keyup", ["key": text ?? identity.key, "code": identity.code,
            "metaKey":event.modifierFlags.contains(.command),"ctrlKey":event.modifierFlags.contains(.control),
            "altKey":event.modifierFlags.contains(.option),"shiftKey":event.modifierFlags.contains(.shift),"repeat":event.isARepeat,
            "modifierCapsLock":event.modifierFlags.contains(.capsLock),"textKey":text != nil])
    }

    func hasMarkedText() -> Bool { marked.length > 0 }
    func markedRange() -> NSRange { hasMarkedText() ? NSRange(location: 0, length: marked.length) : NSRange(location: NSNotFound, length: 0) }
    func selectedRange() -> NSRange { selection }
    func validAttributesForMarkedText() -> [NSAttributedString.Key] { [.underlineStyle, .markedClauseSegment, .backgroundColor] }

    func setMarkedText(_ value: Any, selectedRange range: NSRange, replacementRange: NSRange) {
        guard !discarding else { return }
        let next = (value as? NSAttributedString) ?? NSAttributedString(string: value as? String ?? "")
        if next.length == 0 {
            if hasMarkedText() { clear(); emit("compositionend", ["text":""]) }
            return
        }
        if !hasMarkedText() { emit("compositionstart") }
        marked = next
        let start = min(range.location == NSNotFound ? marked.length : range.location, marked.length)
        selection = NSRange(location: start, length: min(range.length, marked.length - start))
        emit("compositionupdate", ["text":marked.string,"selectionStart":selection.location,"selectionEnd":NSMaxRange(selection)])
    }
    func unmarkText() {
        guard !discarding, hasMarkedText() else { return }
        let text = marked.string
        clear()
        emit("compositionend", ["text":text])
    }
    func insertText(_ value: Any, replacementRange: NSRange) {
        guard !discarding else { return }
        let text = (value as? NSAttributedString)?.string ?? value as? String ?? ""
        let wasMarked = hasMarkedText()
        clear()
        if !wasMarked, text.utf16.count == 1, let scalar = text.unicodeScalars.first,
           scalar.value >= 32, scalar.value <= 126, let event = commandEvent {
            sendKey(event, down: true, text: text)
            return
        }
        emit(wasMarked ? "compositionend" : "input", ["text":text])
    }
    override func doCommand(by selector: Selector) {
        if selector == #selector(NSResponder.cancelOperation(_:)), hasMarkedText() { cancel(); return }
        if let event = commandEvent { sendKey(event, down: true) }
    }
    func attributedSubstring(forProposedRange range: NSRange, actualRange: NSRangePointer?) -> NSAttributedString? {
        guard range.location != NSNotFound, range.location < marked.length else { return nil }
        let valid = NSIntersectionRange(range, NSRange(location: 0, length: marked.length))
        actualRange?.pointee = valid
        return marked.attributedSubstring(from: valid)
    }
    func characterIndex(for point: NSPoint) -> Int { NSNotFound }
    func firstRect(forCharacterRange range: NSRange, actualRange: NSRangePointer?) -> NSRect {
        actualRange?.pointee = NSRange(location: selection.location, length: 0)
        return window?.convertToScreen(caret) ?? .zero
    }
}
