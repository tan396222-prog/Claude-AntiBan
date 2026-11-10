import Foundation

// The host and guest use the same physical key names. Text remains layout-aware.
enum MacKeyboard {
    static let codes: [UInt16: String] = [
        0:"KeyA", 1:"KeyS", 2:"KeyD", 3:"KeyF", 4:"KeyH", 5:"KeyG", 6:"KeyZ", 7:"KeyX", 8:"KeyC", 9:"KeyV",
        10:"IntlBackslash", 11:"KeyB", 12:"KeyQ", 13:"KeyW", 14:"KeyE", 15:"KeyR", 16:"KeyY", 17:"KeyT",
        18:"Digit1", 19:"Digit2", 20:"Digit3", 21:"Digit4", 22:"Digit6", 23:"Digit5", 24:"Equal", 25:"Digit9",
        26:"Digit7", 27:"Minus", 28:"Digit8", 29:"Digit0", 30:"BracketRight", 31:"KeyO", 32:"KeyU", 33:"BracketLeft",
        34:"KeyI", 35:"KeyP", 36:"Enter", 37:"KeyL", 38:"KeyJ", 39:"Quote", 40:"KeyK", 41:"Semicolon",
        42:"Backslash", 43:"Comma", 44:"Slash", 45:"KeyN", 46:"KeyM", 47:"Period", 48:"Tab", 49:"Space",
        50:"Backquote", 51:"Backspace", 53:"Escape", 54:"MetaRight", 55:"MetaLeft", 56:"ShiftLeft", 57:"CapsLock",
        58:"AltLeft", 59:"ControlLeft", 60:"ShiftRight", 61:"AltRight", 62:"ControlRight",
        64:"F17", 65:"NumpadDecimal", 67:"NumpadMultiply", 69:"NumpadAdd", 71:"NumLock", 75:"NumpadDivide",
        76:"NumpadEnter", 78:"NumpadSubtract", 79:"F18", 80:"F19", 81:"NumpadEqual", 82:"Numpad0", 83:"Numpad1",
        84:"Numpad2", 85:"Numpad3", 86:"Numpad4", 87:"Numpad5", 88:"Numpad6", 89:"Numpad7", 90:"F20",
        91:"Numpad8", 92:"Numpad9", 95:"NumpadComma", 96:"F5", 97:"F6", 98:"F7", 99:"F3", 100:"F8", 101:"F9", 103:"F11",
        105:"F13", 106:"F16", 107:"F14", 109:"F10", 111:"F12", 113:"F15", 114:"Insert", 115:"Home",
        116:"PageUp", 117:"Delete", 118:"F4", 119:"End", 120:"F2", 121:"PageDown", 122:"F1",
        123:"ArrowLeft", 124:"ArrowRight", 125:"ArrowDown", 126:"ArrowUp",
    ]
    static let keyCodes = Dictionary(uniqueKeysWithValues: codes.map { ($0.value, $0.key) })
    private static let names = [
        "MetaLeft":"Meta", "MetaRight":"Meta", "ControlLeft":"Control", "ControlRight":"Control",
        "ShiftLeft":"Shift", "ShiftRight":"Shift", "AltLeft":"Alt", "AltRight":"Alt", "Space":" ",
        "NumpadEnter":"Enter", "NumpadDecimal":".", "NumpadMultiply":"*", "NumpadAdd":"+",
        "NumpadDivide":"/", "NumpadSubtract":"-", "NumpadEqual":"=", "NumpadComma":",",
    ]
    private static let symbols = [
        " ":"Space", "=":"Equal", "+":"Equal", "-":"Minus", "_":"Minus", "[":"BracketLeft", "{":"BracketLeft",
        "]":"BracketRight", "}":"BracketRight", "'":"Quote", "\"":"Quote", ";":"Semicolon", ":":"Semicolon",
        "\\":"Backslash", "|":"Backslash", ",":"Comma", "<":"Comma", ".":"Period", ">":"Period",
        "/":"Slash", "?":"Slash", "`":"Backquote", "~":"Backquote",
    ]

    static func identity(_ keyCode: UInt16, characters: String) -> (key: String, code: String) {
        guard let code = codes[keyCode] else { return (characters.isEmpty ? "Unidentified" : characters, "Unidentified") }
        if let key = names[code] { return (key, code) }
        if code.hasPrefix("Key") || code.hasPrefix("Digit") || code.hasPrefix("Numpad") ||
            ["Equal", "Minus", "BracketLeft", "BracketRight", "Quote", "Semicolon", "Backslash", "IntlBackslash", "Comma", "Period", "Slash", "Backquote"].contains(code) {
            return (characters.isEmpty ? "Unidentified" : characters, code)
        }
        return (code, code)
    }

    static func virtualKey(code: String, key: String, shortcut: Bool) -> UInt16? {
        if shortcut, key.utf8.count == 1, let ascii = key.uppercased().utf8.first {
            if (65...90).contains(ascii) { return keyCodes["Key" + key.uppercased()] }
            if (48...57).contains(ascii) { return keyCodes["Digit" + key] }
            if let symbol = symbols[key] { return keyCodes[symbol] }
        }
        return keyCodes[code] ?? keyCodes[key] ?? symbols[key].flatMap { keyCodes[$0] }
    }
}
