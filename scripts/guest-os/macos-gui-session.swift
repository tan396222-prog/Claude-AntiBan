// One GUI epoch is shared by delayed file delivery and remote window input.
import AppKit

@MainActor
final class MacGUISession {
    static let shared = MacGUISession()
    private(set) var epoch: UInt64 = 1
    private var available: Bool
    private var timer: Timer?
    private var observers: [NSObjectProtocol] = []

    private init() {
        available = (try? consoleUser()) != nil
        timer = Timer.scheduledTimer(withTimeInterval: 0.25, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
        let center = NSWorkspace.shared.notificationCenter
        observers.append(center.addObserver(forName: NSWorkspace.sessionDidResignActiveNotification, object: nil, queue: .main) { [weak self] _ in
            Task { @MainActor in
                guard let self else { return }
                self.epoch &+= 1
                self.refresh()
            }
        })
    }

    private func refresh() {
        let current = (try? consoleUser()) != nil
        if current != available { available = current; epoch &+= 1 }
    }

    @discardableResult
    func validate(_ expected: UInt64? = nil) throws -> UInt64 {
        refresh()
        guard available else { throw Failure(4, "requires_gui_login") }
        guard expected == nil || expected == epoch else { throw Failure(7, "gui_session_changed") }
        return epoch
    }
}
