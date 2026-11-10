import Foundation
import SwiftUI
import VisionKit
import AVFoundation
import UniformTypeIdentifiers
import WebKit
private var simulatorPreviewEnabled: Bool {
    #if targetEnvironment(simulator)
    return true
    #else
    return false
    #endif
}

struct ConnectedDevice: Identifiable {
    let id: String
    var name: String
    let connection: CMConnection?
    let webView: WKWebView
}

final class DeviceWebBridge: NSObject, WKScriptMessageHandler {
    weak var model: ConnectionModel?
    let id: String
    let origin: URL
    init(model: ConnectionModel, id: String, origin: URL) {self.model = model; self.id = id; self.origin = origin}
    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        guard let model, model.devices.contains(where: {$0.id == id && $0.webView === message.webView}),
              message.frameInfo.isMainFrame, let url = message.frameInfo.request.url,
              url.scheme == origin.scheme, url.host == origin.host, url.port == origin.port,
              let body = message.body as? [String: Any], let action = body["action"] as? String else {return}
        if action == "name" {
            if let name = body["name"] as? String {model.nameDevice(id, name)}
            return
        }
        guard model.showingWorkspace, model.selectedDeviceID == id else {return}
        if action == "home" {model.leaveWorkspace()}
    }
}

final class ConnectionModel: ObservableObject {

    @Published var relay = CMConnection.defaultRelay()
    @Published var pin = ""
    @Published var fingerprint = ""
    @Published var verified = false
    @Published var hasInvitation = false
    @Published var hasSaved = false
    @Published var needsTrust = false
    @Published var status = ""
    @Published var connecting = false
    @Published var session: CMConnection?
    @Published var devices: [ConnectedDevice] = []
    @Published var selectedDeviceID: String?
    @Published var showingWorkspace = false
    var selectedDevice: ConnectedDevice? {devices.first {$0.id == selectedDeviceID}}

    private var invitation: Invitation?
    private var imported: HostAccess?
    private var trusted: Host?
    private var generation = 0
    private var openArchivedAfterConnect = false
    private let trustKey = "independently-verified-host-v1"
    private let linkQueue = DispatchQueue(label: "CageMobile.connection")
    var canForget: Bool { trusted != nil }
    var canConnect: Bool { !connecting && !needsTrust && !fingerprint.isEmpty && (hasInvitation || hasSaved) }

    init() {
        if let data = UserDefaults.standard.data(forKey: trustKey), let host = try? JSONDecoder().decode(Host.self, from: data) {
            trusted = host; relay = host.relay; pin = host.pin
        }
        do {
            if let saved = try HostKeychain.read() {
                trusted = saved.host; relay = saved.host.relay; pin = saved.host.pin; hasSaved = true
            }
        } catch {
            status = "无法读取已保存的连接，请解锁手机后重新打开。"
        }
        if let selected = UserDefaults.standard.string(forKey:"preferred-relay") {relay = selected}
        DispatchQueue.global(qos: .userInitiated).async {
            do {
                let value = try CMConnection.deviceFingerprint()
                DispatchQueue.main.async { self.fingerprint = value }
            } catch {
                DispatchQueue.main.async { self.status = error.localizedDescription }
            }
        }
    }
    func pair(code: String) {
        guard !connecting else {return}
        connecting = true; generation += 1
        let attempt = generation
        let address = UserDefaults.standard.string(forKey: "preferred-relay") ?? trusted?.relay ?? CMConnection.defaultRelay()
        status = "正在与电脑配对…"
        linkQueue.async {
            let result = Result { () throws -> HostAccess in
                let data = Data(try CMConnection.pair(code: code, relay: address).utf8)
                guard case .persistent(let access) = try ConnectionFile.parse(data) else {throw InputError.invalid}
                return access
            }
            DispatchQueue.main.async {
                guard attempt == self.generation else {return}
                self.connecting = false
                do {
                    let saved = try result.get()
                    try HostKeychain.write(saved)
                    UserDefaults.standard.set(saved.host.relay,forKey:"preferred-relay")
                    self.trusted = saved.host; self.pin = saved.host.pin; self.relay = saved.host.relay
                    self.imported = nil; self.invitation = nil; self.hasInvitation = false
                    self.hasSaved = true; self.needsTrust = false; self.verified = false
                    self.connect()
                } catch { self.status = error.localizedDescription }
            }
        }
    }
    func saveRelay() {
        let input = relay.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let u = URLComponents(string: input), u.scheme?.lowercased() == "https",
              let host=u.host, !host.isEmpty, u.user==nil, u.password==nil, u.query==nil, u.fragment==nil,
              (u.path=="" || u.path=="/"), (1...65535).contains(u.port ?? 443) else {status="请填写有效的 HTTPS 地址。";return}
        relay=input.hasSuffix("/") ? String(input.dropLast()) : input
        UserDefaults.standard.set(relay,forKey:"preferred-relay")
        status="中继设置已保存。"
    }
    func saveTrust() {
        guard verified, let host = try? Host(relay: relay, pin: pin), let data = try? JSONEncoder().encode(host) else {
            status = "请填写有效的中继地址，核对电脑指纹后再保存。"; return
        }
        do {
            if let record = imported {
                guard record.host == host else { status = "填写内容与连接文件不一致，请重新核对。"; return }
                try HostKeychain.write(record)
                hasSaved = true
            } else if trusted != host {
                hasSaved = try HostKeychain.read(host: host) != nil
            }
        } catch { status = "无法安全保存主机，请解锁手机后重试。"; return }
        trusted = host; UserDefaults.standard.set(host.relay,forKey:"preferred-relay"); UserDefaults.standard.set(data, forKey: trustKey); verified = false
        imported = nil; needsTrust = false
        status = hasSaved ? "已保存这台电脑，可以重新连接。" : "已保存主机指纹。"
    }
    func loadInvitation(_ file: URL) {
        invitation = nil; imported = nil; needsTrust = false; hasInvitation = false; verified = false
        do {
            switch try ConnectionFile.load(file) {
            case .temporary(let room):
                invitation = room; hasInvitation = true; status = "已导入本次临时邀请。"
            case .persistent(let saved):
                imported = saved; needsTrust = true; relay = saved.host.relay; pin = saved.host.pin
                status = "请与电脑上独立显示的指纹核对，再保存这台主机。"
            }
        } catch { status = "文件无效或临时邀请已过期，请重新导入。" }
    }
    func forget() {
        do { try HostKeychain.forget(host: trusted) } catch { status = "移除未完成，请解锁手机后重试。"; return }
        UserDefaults.standard.removeObject(forKey: trustKey)
        trusted = nil; imported = nil; invitation = nil; relay = UserDefaults.standard.string(forKey:"preferred-relay") ?? CMConnection.defaultRelay(); pin = ""
        hasSaved = false; needsTrust = false; verified = false; hasInvitation = false
        status = "已忘记主机。"
    }
    func connect() {
        connect(openArchived: false)
    }
    private func connect(openArchived: Bool) {
        guard !connecting, !needsTrust, let host = trusted else {
            status = "请先核对并保存这台主机。"; return
        }
        openArchivedAfterConnect = openArchived
        if let existing = devices.first(where: {$0.id == host.relay + "|" + host.pin}) {
            selectDevice(existing.id)
            if openArchivedAfterConnect {
                openArchivedAfterConnect = false
                existing.webView.evaluateJavaScript("window.dispatchEvent(new Event('cage-open-archives'))")
            }
            return
        }
        let item = invitation

        guard item != nil || hasSaved else { status = "请导入连接文件或有效的临时邀请。"; return }
        if let item, (item.expiry ?? .distantPast) <= Date() { invitation = nil; hasInvitation = false; status = "临时邀请已过期，请重新连接或导入新邀请。"; return }
        guard let directory = Bundle.main.url(forResource: "mobile-ui", withExtension: nil) else { status = "安装包缺少工作台资源。"; return }
        invitation = nil; hasInvitation = false; connecting = true; generation += 1
        let attempt = generation
        status = "正在验证电脑并连接…"
        linkQueue.async {
            let result = Result {
                if let item { return try CMConnection.connect(relay: host.relay, room: item.id, token: item.client_token, pin: host.pin, assets: directory.path) }
                guard let saved = try HostKeychain.read(host: host), saved.host == host else { throw InputError.unavailable }
                return try CMConnection.connectSaved(relay: host.relay, lookup: saved.lookup, pin: host.pin, assets: directory.path)
            }
            DispatchQueue.main.async {
                guard attempt == self.generation else {
                    if case .success(let old) = result { DispatchQueue.global().async { old.close() } }
                    return
                }
                self.connecting = false
                switch result {
                case .success(let session): self.status = ""; self.addDevice(session, host: host)
                case .failure(let error): self.status = error.localizedDescription
                }
            }
        }
    }
    func leaveWorkspace() {showingWorkspace = false}
    func openArchivedSessions() {
        if let device = selectedDevice {
            selectDevice(device.id)
            device.webView.evaluateJavaScript("window.dispatchEvent(new Event('cage-open-archives'))")
        } else {
            connect(openArchived: true)
        }
    }
    func selectDevice(_ id: String) {
        guard devices.contains(where: {$0.id == id}) else {return}
        selectedDeviceID = id; showingWorkspace = true
        session = selectedDevice?.connection
        publishDevices()
    }
    func nameDevice(_ id: String, _ value: String) {
        guard let index = devices.firstIndex(where: {$0.id == id}) else {return}
        let name = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, devices[index].name != name else {return}
        devices[index].name = String(name.prefix(120)); publishDevices()
    }
    private func addDevice(_ connection: CMConnection, host: Host) {
        let id = host.relay + "|" + host.pin
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        config.allowsInlineMediaPlayback = true
        config.preferences.javaScriptCanOpenWindowsAutomatically = false
        config.applicationNameForUserAgent = "CageMobile/1"
        let delegate = WorkspaceNavigation(connection.launchURL)
        config.userContentController.add(DeviceWebBridge(model: self, id: id, origin: connection.launchURL), name: "cageDevices")
        config.userContentController.addUserScript(WKUserScript(source: deviceScript(id), injectionTime: .atDocumentStart, forMainFrameOnly: true))
        let web = WKWebView(frame: .zero, configuration: config)
        web.scrollView.contentInsetAdjustmentBehavior = .never
        web.navigationDelegate = delegate
        // WKWebView holds its navigation delegate weakly.
        navigationDelegates[id] = delegate
        devices.append(ConnectedDevice(id: id, name: "已连接电脑", connection: connection, webView: web))
        selectDevice(id)
        var launch = connection.launchURL
        if openArchivedAfterConnect {
            var parts = URLComponents(url: launch, resolvingAgainstBaseURL: false)!
            parts.queryItems = (parts.queryItems ?? []) + [URLQueryItem(name: "page", value: "archived")]
            launch = parts.url!
            openArchivedAfterConnect = false
        }
        web.load(URLRequest(url: launch))
    }
    private var navigationDelegates: [String: WKNavigationDelegate] = [:]
    private func deviceScript(_ current: String) -> String {
        let value: [String: Any] = ["current": current, "devices": devices.map {["id": $0.id, "name": $0.name]}]
        let data = try! JSONSerialization.data(withJSONObject: value, options: [.sortedKeys])
        return "window.__cageDevices=" + String(decoding: data, as: UTF8.self) + ";window.dispatchEvent(new CustomEvent('cage-devices',{detail:window.__cageDevices}));"
    }
    func publishDevices() {
        for device in devices {
            let script = deviceScript(device.id)
            device.webView.configuration.userContentController.removeAllUserScripts()
            device.webView.configuration.userContentController.addUserScript(WKUserScript(source: script, injectionTime: .atDocumentStart, forMainFrameOnly: true))
            // The preview fixture is installed separately and must survive navigation/reloads.
            #if targetEnvironment(simulator)
            if device.connection == nil {
                device.webView.configuration.userContentController.addUserScript(WKUserScript(source: SimulatorWorkspacePreview.fixture, injectionTime: .atDocumentStart, forMainFrameOnly: true))
            }
            #endif
            device.webView.evaluateJavaScript(script)
        }
    }
    func disconnectDevice(_ id: String) {
        guard let old = devices.first(where: {$0.id == id}) else {return}
        old.webView.stopLoading(); old.webView.navigationDelegate = nil
        old.webView.configuration.userContentController.removeScriptMessageHandler(forName: "cageDevices")
        devices.removeAll {$0.id == id}; navigationDelegates.removeValue(forKey: id)
        if selectedDeviceID == id {
            selectedDeviceID = devices.first?.id; session = selectedDevice?.connection
            if selectedDeviceID == nil {showingWorkspace = false}
        }
        if let connection = old.connection {linkQueue.async {connection.close()}}
        publishDevices()
    }
    func disconnect() {if let id = selectedDeviceID {disconnectDevice(id)}}
    func reconnect() {disconnect(); connect()}
    func startPreview() {
        #if targetEnvironment(simulator)
        for (id, name) in [("preview-mac", "我的电脑 · 模拟器预览"), ("preview-linux", "Linux 工作站 · 模拟器预览")] {
            if !devices.contains(where: {$0.id == id}) {
                let preview = SimulatorWorkspacePreview.makeWebView(model: self, id: id)
                devices.append(ConnectedDevice(id: id, name: name, connection: nil, webView: preview.web))
                navigationDelegates[id] = preview.delegate
            }
        }
        selectDevice("preview-mac")
        #endif
    }
}

private final class WorkspaceNavigation: NSObject, WKNavigationDelegate {
    let origin: URL
    init(_ origin: URL) {self.origin = origin}
    func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                 decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
        guard let url = action.request.url, action.targetFrame?.isMainFrame == true,
              url.scheme == origin.scheme, url.host == origin.host, url.port == origin.port,
              url.user == nil, url.password == nil else {decisionHandler(.cancel); return}
        decisionHandler(.allow)
    }
}

private struct Workspace: UIViewRepresentable {
    let webView: WKWebView
    func makeUIView(context: Context) -> WKWebView {webView}
    func updateUIView(_ view: WKWebView, context: Context) {}
}

private enum CageStyle {
    static let paper = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark ? UIColor(white: 0.075, alpha: 1) : UIColor(red: 0.982, green: 0.978, blue: 0.967, alpha: 1)
    })
    static let ink = Color(uiColor: .label)
    static let inverse = Color(uiColor: .systemBackground)
}

private struct CageButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var enabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .opacity(enabled ? (configuration.isPressed ? 0.9 : 1) : 0.5)
    }
}

private struct ActionLabel: View {
    let title: String
    let symbol: String
    var primary = true
    var height: CGFloat = 52
    var body: some View {
        Label(title, systemImage: symbol)
            .font(.headline).frame(maxWidth: .infinity, minHeight: height)
            .foregroundStyle(primary ? CageStyle.inverse : CageStyle.ink)
            .background(primary ? CageStyle.ink : Color(uiColor: .secondarySystemFill),
                        in: RoundedRectangle(cornerRadius: 16))
            .contentShape(Rectangle())
    }
}

private struct QRScanner: UIViewControllerRepresentable {
    let region: CGRect
    @Binding var hint: String
    let result: (String?) -> Void
    final class Delegate: NSObject, DataScannerViewControllerDelegate {
        let result: (String?) -> Void
        var hint: Binding<String>
        var finished = false
        init(_ result: @escaping (String?) -> Void, hint: Binding<String>) {self.result = result; self.hint = hint}
        func dataScanner(_ scanner: DataScannerViewController, didAdd items: [RecognizedItem], allItems: [RecognizedItem]) {
            for case .barcode(let barcode) in items {
                guard !finished, let code = barcode.payloadStringValue else {continue}
                guard code.hasPrefix("CAGE1:"), code.utf8.count <= 4096 else {
                    hint.wrappedValue = "请扫描电脑「设置 → 远程连接」中的 Cage 配对二维码"; continue
                }
                UINotificationFeedbackGenerator().notificationOccurred(.success)
                finished = true; scanner.stopScanning(); result(code); return
            }
        }
        func dataScanner(_ scanner: DataScannerViewController, becameUnavailableWithError error: DataScannerViewController.ScanningUnavailable) {
            if !finished {finished = true; result(nil)}
        }
    }
    func makeCoordinator() -> Delegate { Delegate(result, hint: $hint) }
    func makeUIViewController(context: Context) -> DataScannerViewController {
        let scanner = DataScannerViewController(recognizedDataTypes: [.barcode(symbologies: [.qr])], qualityLevel: .balanced, isGuidanceEnabled: false, isHighlightingEnabled: false)
        scanner.delegate = context.coordinator
        DispatchQueue.main.async {
            guard !context.coordinator.finished else {return}
            scanner.regionOfInterest = region
            do { try scanner.startScanning() } catch {context.coordinator.finished = true; result(nil)}
        }
        return scanner
    }
    func updateUIViewController(_ scanner: DataScannerViewController, context: Context) {scanner.regionOfInterest = region}
    static func dismantleUIViewController(_ scanner: DataScannerViewController, coordinator: Delegate) {coordinator.finished = true; scanner.stopScanning()}
}

private struct FinderMask: Shape {
    let box: CGRect
    func path(in rect: CGRect) -> Path {
        var path = Path(rect)
        path.addRoundedRect(in: box, cornerSize: CGSize(width: 18, height: 18))
        return path
    }
}
private struct FinderCorners: Shape {
    func path(in rect: CGRect) -> Path {
        var path = Path()
        for x in [CGFloat(0), CGFloat(1)] {for y in [CGFloat(0), CGFloat(1)] {
            var corner = Path()
            corner.move(to: CGPoint(x: 0, y: 30)); corner.addLine(to: CGPoint(x: 0, y: 18))
            corner.addQuadCurve(to: CGPoint(x: 18, y: 0), control: .zero); corner.addLine(to: CGPoint(x: 30, y: 0))
            let transform = CGAffineTransform(a: x == 0 ? 1 : -1, b: 0, c: 0, d: y == 0 ? 1 : -1, tx: x * rect.width, ty: y * rect.height)
            path.addPath(corner, transform: transform)
        }}
        return path
    }
}
private struct QRScannerScreen: View {
    let result: (String?) -> Void
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var sweeping = false
    @State private var hint = "将 Cage 配对二维码放入框内\n在电脑「设置 → 远程连接」中生成"
    private let accent = Color(red: 0.91, green: 0.72, blue: 0.62)
    var body: some View {
        GeometryReader { geometry in
            let side = max(96, min(280, min(geometry.size.width - 56, geometry.size.height - 190)))
            let box = CGRect(x: (geometry.size.width - side) / 2, y: 24 + max(0, (geometry.size.height - 190 - side) / 2), width: side, height: side)
            ZStack(alignment: .bottom) {
                #if targetEnvironment(simulator)
                Color(white: 0.15)
                Image(systemName: "qrcode").resizable().interpolation(.none).scaledToFit()
                    .foregroundStyle(.white.opacity(0.8)).frame(width: side * 0.65, height: side * 0.65)
                    .position(x: box.midX, y: box.midY).accessibilityHidden(true)
                #else
                QRScanner(region: box, hint: $hint, result: result)
                #endif
                FinderMask(box: box).fill(Color(red: 0.08, green: 0.09, blue: 0.09).opacity(0.72), style: FillStyle(eoFill: true))
                RoundedRectangle(cornerRadius: 18).stroke(.white.opacity(0.45), lineWidth: 1)
                    .frame(width: side, height: side).position(x: box.midX, y: box.midY)
                FinderCorners().stroke(accent, style: StrokeStyle(lineWidth: 4, lineCap: .round))
                    .frame(width: side, height: side).position(x: box.midX, y: box.midY)
                VStack(spacing: 0) {
                    LinearGradient(colors: [accent.opacity(0), accent.opacity(0.22)], startPoint: .top, endPoint: .bottom).frame(height: 26)
                    Capsule().fill(accent).frame(height: 2)
                }.frame(width: side - 24)
                    .position(x: box.midX, y: reduceMotion ? box.midY : box.minY + 24 + (sweeping ? side - 48 : 0))
                    .animation(reduceMotion ? nil : .linear(duration: 2.4).repeatForever(autoreverses: true), value: sweeping)
                    .accessibilityHidden(true)
                VStack(spacing: 12) {
                    Text("连接你的电脑").font(.title2.bold())
                    Text(hint).font(.subheadline).foregroundStyle(.white.opacity(0.88)).multilineTextAlignment(.center)
                    Text(simulatorPreviewEnabled ? "模拟器预览 · 即将进入示例工作台" : "仅识别 Cage 配对码")
                        .font(.caption).foregroundStyle(.white.opacity(0.6))
                }.foregroundStyle(.white).padding(.horizontal, 24).padding(.bottom, 24).accessibilityElement(children: .combine)
            }.background(Color.black).onAppear {sweeping = true}
                .task {
                    #if targetEnvironment(simulator)
                    do {try await Task.sleep(for: .seconds(2))} catch {return}
                    result(nil)
                    #endif
                }
        }.toolbarColorScheme(.dark, for: .navigationBar)
            .toolbarBackground(Color(red: 0.08, green: 0.09, blue: 0.09), for: .navigationBar).toolbarBackground(.visible, for: .navigationBar)
    }
}

private struct ConnectionSheet: View {
    @ObservedObject var model: ConnectionModel
    let previewConnected: () -> Void
    @State private var manual = false
    @State private var scanning = false
    @State private var disconnecting: ConnectedDevice?
    @State private var previewScanned = false
    @State private var code = ""
    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 12) {
                    Image("PairDevices").resizable().scaledToFit().frame(maxWidth: .infinity)
                        .frame(height: 150).accessibilityHidden(true)
                    Text("连接电脑").font(.title.bold())
                    Text("在电脑的 Cage 设置中打开「远程连接」，生成配对二维码或复制配对码。")
                        .font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
                        .padding(.bottom, 10)
                    Button {
                        Task { @MainActor in
                            if simulatorPreviewEnabled {scanning = true; return}
                            guard DataScannerViewController.isSupported,
                                  await AVCaptureDevice.requestAccess(for: .video),
                                  DataScannerViewController.isAvailable else {
                                model.status = "相机暂不可用，请检查相机权限或使用配对码。"; return
                            }
                            scanning = true
                        }
                    } label: {ActionLabel(title: "扫码连接", symbol: "qrcode.viewfinder")}
                    Button {manual = true} label: {ActionLabel(title: "使用配对码", symbol: "key", primary: false)}
                    if model.hasSaved || model.hasInvitation {
                        Button("重新连接", action: model.connect).frame(minHeight: 44).disabled(!model.canConnect)
                    }
                    if !model.devices.isEmpty {
                        Divider().padding(.vertical, 8)
                        Text("已连接设备").font(.headline).frame(maxWidth: .infinity, alignment: .leading)
                        Text("长按设备可取消连接").font(.footnote).foregroundStyle(.secondary)
                            .frame(maxWidth: .infinity, alignment: .leading)
                        ForEach(model.devices) {device in
                            Button {if disconnecting == nil {model.selectDevice(device.id)}} label: {
                                HStack {Image(systemName: "desktopcomputer"); Text(device.name); Spacer(); Image(systemName: "chevron.right")}
                                    .frame(minHeight: 48).contentShape(Rectangle())
                            }
                            .simultaneousGesture(LongPressGesture().onEnded { _ in disconnecting = device })
                            .accessibilityAction(named: Text("取消连接")) {disconnecting = device}
                        }
                    }
                    status
                }.padding(24).frame(maxWidth: 520)
                    .frame(maxWidth: .infinity)
            }.background(CageStyle.paper).buttonStyle(CageButtonStyle())
                .navigationDestination(isPresented: $manual) {
                    Form {
                        Section {
                            SecureField("粘贴电脑上的一次性配对码", text: $code)
                                .textInputAutocapitalization(.never).autocorrectionDisabled()
                            Button("连接电脑") {model.pair(code: code); code = ""}
                                .accessibilityIdentifier("redeem-pair-code")
                                .disabled(code.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || model.connecting)
                        }
                        if !model.status.isEmpty {Section {status}}
                    }.navigationTitle("配对码")
                }
                .sheet(item: $disconnecting) {device in
                    VStack(spacing: 20) {
                        Text("取消连接？").font(.title2.bold())
                        Text(device.name).foregroundStyle(.secondary)
                        Text("将关闭这台设备的连接。已保存的配对信息会保留。")
                            .font(.subheadline).foregroundStyle(.secondary).multilineTextAlignment(.center)
                        Button("取消连接", role: .destructive) {
                            model.disconnectDevice(device.id)
                            disconnecting = nil
                        }.frame(maxWidth: .infinity, minHeight: 48)
                        Button("保留连接") {disconnecting = nil}.frame(minHeight: 44)
                    }.padding(24).presentationDetents([.height(300)]).presentationDragIndicator(.visible)
                }
                .sheet(isPresented: $scanning, onDismiss: {
                    if previewScanned {previewScanned = false; previewConnected()}
                }) {
                    NavigationStack {
                        QRScannerScreen { result in
                            scanning = false
                            if simulatorPreviewEnabled {previewScanned = true}
                            else if let result {model.pair(code: result)}
                            else {model.status = "无法读取二维码，请重试或使用配对码。"}
                        }.navigationTitle("扫描电脑二维码").navigationBarTitleDisplayMode(.inline)
                        .toolbar {Button("取消") {scanning = false}}
                    }
                }
        }.disabled(model.connecting).interactiveDismissDisabled(model.connecting)
            .presentationDetents([.fraction(0.72), .large]).presentationDragIndicator(.visible)
    }
    @ViewBuilder private var status: some View {
        if !model.status.isEmpty {
            Text(model.status).font(.footnote).foregroundStyle(.secondary)
                .accessibilityAddTraits(.updatesFrequently)
        }
    }
}

private enum CageTheme: String, CaseIterable {
    case system = "跟随系统", light = "浅色", dark = "深色"
    var scheme: ColorScheme? {
        switch self {case .system: return nil; case .light: return .light; case .dark: return .dark}
    }
}

private struct SettingsScreen: View {
    @ObservedObject var model: ConnectionModel
    @Binding var theme: CageTheme
    let connect: () -> Void
    @State private var relaySettings = false
    @State private var advanced = false
    @State private var themes = false
    @State private var picking = false
    @State private var confirmingForget = false

    var body: some View {
        Form {
            Section("外观") {
                DisclosureGroup(isExpanded: $themes) {
                    ForEach(CageTheme.allCases, id: \.self) {option in
                        Button {
                            theme = option; themes = false
                        } label: {
                            HStack {Text(option.rawValue); Spacer(); if theme == option {Image(systemName: "checkmark")}}
                                .frame(minHeight: 44).contentShape(Rectangle())
                        }.accessibilityAddTraits(theme == option ? .isSelected : [])
                    }
                } label: {
                    HStack {
                        Label("主题", systemImage: "circle.lefthalf.filled")
                        Spacer(); Text(theme.rawValue).foregroundStyle(.secondary)
                    }
                }
            }
            Section("电脑连接") {
                Button("已归档会话", action: model.openArchivedSessions)
                    .disabled(model.selectedDevice == nil && !model.canConnect)
                DisclosureGroup("中继设置", isExpanded: $relaySettings) {
                    Toggle("使用自建中继", isOn: Binding(get: {model.relay != CMConnection.defaultRelay()},
                                                     set: {model.relay = $0 ? "" : CMConnection.defaultRelay()}))
                    if model.relay != CMConnection.defaultRelay() {
                        TextField("自建 VPS 的 HTTPS 地址", text: $model.relay)
                            .keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                    } else {Text("Cage 官方中继").foregroundStyle(.secondary)}
                    Button("保存中继设置", action: model.saveRelay)
                }
                DisclosureGroup("指纹与连接文件", isExpanded: $advanced) {
                    Text(CMConnection.usesSimulatorIdentity ? "模拟器测试身份的指纹" : "这台手机的指纹").font(.headline)
                    Text(model.fingerprint).font(.caption.monospaced()).textSelection(.enabled)
                    Button("复制手机指纹") {UIPasteboard.general.string = model.fingerprint; model.status = "手机指纹已复制"}
                        .disabled(model.fingerprint.isEmpty)
                    Button("导入连接文件") {picking = true}
                    TextField("电脑指纹 sha256:…", text: $model.pin).textInputAutocapitalization(.never).autocorrectionDisabled()
                    Toggle("已核对电脑指纹", isOn: $model.verified)
                    Button("保存已核对的主机", action: model.saveTrust).disabled(!model.verified)
                    if model.hasInvitation || model.hasSaved {Button("连接工作台", action: connect).disabled(!model.canConnect)}
                }
                if model.canForget {Button("忘记主机", role: .destructive) {confirmingForget = true}}
            }
            if !model.status.isEmpty {Section {Text(model.status).font(.footnote).foregroundStyle(.secondary)}}
        }.navigationTitle("设置").disabled(model.connecting)
            .alert("忘记主机？", isPresented: $confirmingForget) {
                Button("取消", role: .cancel) {}
                Button("忘记主机", role: .destructive, action: model.forget)
            } message: {Text("将移除这台电脑的地址、指纹和重连凭据。")}
            .fileImporter(isPresented: $picking, allowedContentTypes: [.json, .plainText]) {result in
                if case .success(let file) = result {model.loadInvitation(file)}
            }
    }
}

@main
struct CageMobileApp: App {
    @StateObject private var model = ConnectionModel()
    @AppStorage("appearance-theme") private var theme = CageTheme.system
    @State private var showingSettings = false
    @State private var pairing = false

    var body: some Scene {
        WindowGroup {
            NavigationStack {
                Group {
                    if model.showingWorkspace, let device = model.selectedDevice {
                        Workspace(webView: device.webView).id(device.id)
                    } else {
                        home
                    }
                }
                .navigationDestination(isPresented: $showingSettings) {
                    SettingsScreen(model: model, theme: $theme, connect: model.connect)
                }
            }.buttonStyle(CageButtonStyle()).tint(CageStyle.ink).preferredColorScheme(theme.scheme)
                .sheet(isPresented: $pairing) {
                    ConnectionSheet(model: model, previewConnected: {
                        pairing = false; showingSettings = false; model.startPreview()
                    }).buttonStyle(CageButtonStyle()).tint(CageStyle.ink).preferredColorScheme(theme.scheme)
                }
                .onChange(of: model.showingWorkspace) { _, connected in
                    if connected {pairing = false; showingSettings = false}
                }
                .onChange(of: model.relay) {model.verified = false}
                .onChange(of: model.pin) {model.verified = false}
        }
    }
    private var home: some View {
        GeometryReader { geometry in
            let brandSpacing = min(60, max(28, geometry.size.height * 0.075)) + 32
            ScrollView {
                VStack(spacing: 0) {
                    Spacer(minLength: 12)
                    VStack(spacing: brandSpacing) {
                        VStack(spacing: 12) {
                            Image("CageMark").resizable().scaledToFit().frame(width: 120, height: 120)
                                .clipShape(RoundedRectangle(cornerRadius: 16)).accessibilityHidden(true)
                            Text("Cage").font(.system(size: 36, weight: .semibold, design: .rounded))
                        }
                        Button {pairing = true} label: {
                            ActionLabel(title: "连接电脑", symbol: "desktopcomputer", height: 60)
                        }
                    }.frame(maxWidth: 276).padding(.top, 32)
                    Spacer(minLength: 12)
                }.padding(.horizontal, 24).frame(maxWidth: .infinity).frame(minHeight: geometry.size.height)
            }.scrollBounceBehavior(.basedOnSize)
        }.background(CageStyle.paper.ignoresSafeArea()).buttonStyle(CageButtonStyle())
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {showingSettings = true} label: {Image(systemName: "gearshape")}
                        .accessibilityLabel("设置")
                }
            }
    }
}
