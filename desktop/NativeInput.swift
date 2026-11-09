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
        if event.modifierFlags.contains(.command) {
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
            56: .shift, 60: .shift, 58: .option, 61: .option, 59: .control, 62: .control]
        if let flag = flags[event.keyCode] { sendKey(event, down: event.modifierFlags.contains(flag)) }
    }

    private func sendKey(_ event: NSEvent, down: Bool, text: String? = nil) {
        let special: [UInt16: String] = [36:"Enter",48:"Tab",49:" ",51:"Backspace",
            53:"Escape",54:"Meta",55:"Meta",56:"Shift",57:"CapsLock",58:"Alt",59:"Control",
            60:"Shift",61:"Alt",62:"Control",115:"Home",116:"PageUp",117:"Delete",
            119:"End",121:"PageDown",123:"ArrowLeft",124:"ArrowRight",125:"ArrowDown",126:"ArrowUp"]
        let letters: [UInt16: String] = [0:"A",1:"S",2:"D",3:"F",4:"H",5:"G",6:"Z",7:"X",8:"C",9:"V",11:"B",12:"Q",13:"W",14:"E",15:"R",16:"Y",17:"T",31:"O",32:"U",34:"I",35:"P",37:"L",38:"J",40:"K",45:"N",46:"M"]
        let codes: [UInt16: String] = [36:"Enter",48:"Tab",49:"Space",51:"Backspace",53:"Escape",
            54:"MetaRight",55:"MetaLeft",56:"ShiftLeft",57:"CapsLock",58:"AltLeft",59:"ControlLeft",
            60:"ShiftRight",61:"AltRight",62:"ControlRight",115:"Home",116:"PageUp",117:"Delete",
            119:"End",121:"PageDown",123:"ArrowLeft",124:"ArrowRight",125:"ArrowDown",126:"ArrowUp",
            18:"Digit1",19:"Digit2",20:"Digit3",21:"Digit4",22:"Digit6",23:"Digit5",25:"Digit9",26:"Digit7",28:"Digit8",29:"Digit0",
            24:"Equal",27:"Minus",30:"BracketRight",33:"BracketLeft",39:"Quote",41:"Semicolon",42:"Backslash",43:"Comma",44:"Slash",47:"Period",50:"Backquote"]
        let key = text ?? special[event.keyCode] ?? event.charactersIgnoringModifiers ?? "Unidentified"
        let code = letters[event.keyCode].map { "Key" + $0 } ?? codes[event.keyCode] ?? "Unidentified"
        emit(down ? "keydown" : "keyup", ["key": key, "code": code,
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
