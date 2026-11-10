// In-memory design preview. Compiled only for the simulator; never pairs a host.
#if targetEnvironment(simulator)
import SwiftUI
import WebKit

enum SimulatorWorkspacePreview {
    final class Preview: NSObject, WKURLSchemeHandler, WKNavigationDelegate {
        func webView(_ webView: WKWebView, start urlSchemeTask: WKURLSchemeTask) {
            guard let url = urlSchemeTask.request.url,
                  let root = Bundle.main.url(forResource: "mobile-ui", withExtension: nil) else {return}
            let file = root.appendingPathComponent(url.path == "/" ? "mobile.html" : String(url.path.dropFirst())).standardizedFileURL
            guard file.path.hasPrefix(root.path + "/"), let data = try? Data(contentsOf: file) else {
                urlSchemeTask.didFailWithError(URLError(.fileDoesNotExist)); return
            }
            let types = ["html": "text/html", "js": "text/javascript", "css": "text/css", "png": "image/png"]
            urlSchemeTask.didReceive(URLResponse(url: url, mimeType: types[file.pathExtension] ?? "application/octet-stream",
                                                 expectedContentLength: data.count, textEncodingName: "utf-8"))
            urlSchemeTask.didReceive(data); urlSchemeTask.didFinish()
        }
        func webView(_ webView: WKWebView, stop urlSchemeTask: WKURLSchemeTask) {}
        func webView(_ webView: WKWebView, decidePolicyFor action: WKNavigationAction,
                     decisionHandler: @escaping (WKNavigationActionPolicy) -> Void) {
            decisionHandler(action.request.url?.scheme == "cage-preview" ? .allow : .cancel)
        }
    }
    static func makeWebView(model: ConnectionModel, id: String) -> (web: WKWebView, delegate: Preview) {
        let delegate = Preview()
        let origin = URL(string: "cage-preview://app/")!
        let config = WKWebViewConfiguration()
        config.websiteDataStore = .nonPersistent()
        config.applicationNameForUserAgent = "CageMobile/1"
        config.setURLSchemeHandler(delegate, forURLScheme: "cage-preview")
        config.userContentController.add(DeviceWebBridge(model: model, id: id, origin: origin), name: "cageDevices")
        config.userContentController.addUserScript(WKUserScript(source: fixture, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        let view = WKWebView(frame: .zero, configuration: config)
        view.scrollView.contentInsetAdjustmentBehavior = .never
        view.navigationDelegate = delegate
        view.load(URLRequest(url: origin))
        return (view, delegate)
    }

    static let fixture = #"""
    (() => {
      const projects = [{name: "Cage", path: "/示例项目/cage"}, {name: "个人网站", path: "/示例项目/website"}];
      let sequence = 0;
      const uuid = () => "preview-" + (++sequence);
      if (!crypto.randomUUID) crypto.randomUUID = uuid;
      const snapshot = (id, workspace, title, turns = []) => ({id, workspace, title, turns,
        revision: 1, generation: "simulator-preview", updated_at: new Date().toISOString()});
      const sessions = [
        snapshot("preview-ui", projects[0].path, "检查移动端界面", [{id: "turn-ui", prompt: "帮我检查移动端的布局和侧栏。",
          text: "这里是示例会话，可以查看消息排版、输入框和项目切换。\n\n你也可以新建会话，输入一条消息查看交互效果。", status: "completed"}]),
        snapshot("preview-approval", projects[0].path, "工具审批示例", [{id: "turn-approval", prompt: "查看项目目录。", text: "需要确认以下工具调用。",
          status: "running", approval: {id: "approval-demo", tool: "Bash", input: {command: "ls -la", description: "查看示例项目文件"}}}]),
        snapshot("preview-website", projects[1].path, "调整首页排版", [{id: "turn-website", prompt: "看看首页标题的间距。",
          text: "这是第二个项目的示例会话。", status: "completed"}])
      ];
      window.fetch = async (input, options = {}) => {
        const url = new URL(typeof input === "string" ? input : input.url, location.href);
        const path = url.pathname.replace("/api/mobile/cli", "");
        const body = options.body ? JSON.parse(options.body) : null;
        let value;
        if (path === "/projects") value = {host: window.__cageDevices?.devices.find(d => d.id === window.__cageDevices.current)?.name || "模拟器预览", ready: true, projects};
        else if (path === "/sessions" && !body) value = {sessions};
        else if (path === "/sessions") {
          value = snapshot(uuid(), body.workspace, "新会话"); sessions.unshift(value);
        } else {
          const [, , id, action] = path.split("/");
          value = sessions.find(s => s.id === id);
          if (!value) return new Response("示例会话不存在", {status: 404, headers: {"Content-Type": "text/plain"}});
          if (action === "metadata") {
            if (body.title !== undefined) value.title = value.custom_title = body.title;
            if (body.pinned !== undefined) value.pinned = body.pinned;
            value.metadata_set = true;
          } else if (action === "delete") {
            sessions.splice(sessions.indexOf(value), 1);
            return new Response(JSON.stringify({ok: true}), {headers: {"Content-Type": "application/json"}});
          } else if (action === "messages") {
            if (!value.custom_title) value.title = body.text.slice(0, 30);
            value.turns.push({id: body.request_id, prompt: body.text, status: "completed",
              text: "已收到示例消息。这是模拟器界面预览，没有连接电脑，也没有调用模型。"});
          } else if (action === "approval") {
            const turn = value.turns.at(-1);
            turn.status = "completed"; delete turn.approval;
            turn.text = body.allow ? "示例审批已允许。实际命令不会执行。" : "示例审批已拒绝。";
          } else if (action === "stop") value.turns.at(-1).status = "stopped";
          if (body) {value.revision++; value.updated_at = new Date().toISOString();}
        }
        return new Response(JSON.stringify(value), {headers: {"Content-Type": "application/json"}});
      };
    })();
    """#
}
#endif
