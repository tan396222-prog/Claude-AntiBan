// Local copy preview of the production workbench. API requests stay in this
// process; none are forwarded to Cage or used to install a guest.
import http from "node:http";
import { defaultAppearance } from "../src/appearance-state.mjs";

const port = Number(process.env.CAGE_INSTALL_PREVIEW_PORT || 5208);
const upstream = new URL(process.env.CAGE_UI_URL || "http://127.0.0.1:5198");
const hosts = {
  macos: { system: "macos", version: "26.5", build: "25F71", arch: "arm64", platform: "darwin", bytes: 19769944180, imageBuild: "25F71" },
  windows: { system: "windows-11", version: "25H2", build: "26200.9711", edition: "Professional", language: "zh-CN", arch: "amd64", platform: "windows", bytes: 5314951294, imageBuild: "26200.6584" },
  linux: { system: "ubuntu-22.04", version: "22.04", arch: "amd64", platform: "linux", bytes: 650000000 },
};
const sessions = new Map();

http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://127.0.0.1:${port}`);
  const saved = Object.fromEntries((req.headers.cookie || "").split(";").map(value => value.trim().split("=")));
  const selected = url.searchParams.get("host") || saved.preview_host || "macos";
  const scenario = url.searchParams.get("state") || saved.preview_state || "download";
  const host = hosts[selected] || hosts.macos;
  const system = host.system;
  const plan = {
    host, version: host.version, build: host.imageBuild, bytes: host.bytes,
    format: system === "macos" ? "ipsw" : system === "windows-11" ? "esd" : "qcow2",
    ...(scenario === "cached" ? { path: "/preview/cached-system-image" } : {}),
  };
  const sessionKey = `${selected}:${scenario}`;
  if (!sessions.has(sessionKey)) sessions.set(sessionKey, { required: true, tracked: true, started: ["resume", "progress", "failure", "ready"].includes(scenario), created: Date.now() });
  const session = sessions.get(sessionKey);
  const showProgress = scenario === "progress" || session.installing;
  const showFailure = scenario === "failure";
  const showReady = scenario === "ready";
  const job = showProgress || showFailure ? { id: "preview-install", kind: "guest-install", running: !showFailure, error: showFailure ? "系统镜像下载中断。请检查代理连接，已下载文件和虚拟磁盘会保留。" : "", message: "正在下载系统镜像", setup_progress: { system, phase: "image", operation: "download", label: `正在下载 ${system === "macos" ? "macOS" : system === "windows-11" ? "Windows 11" : "Ubuntu"} 系统镜像`, received: Math.floor(host.bytes * .43), total: host.bytes, bytes_per_second: 26 * 1048576 }, log: "正在通过主机系统代理或 TUN 下载官方系统镜像。" } : { running: false };
  const status = { local_vm_storage: true, ready: showReady, vm_running: showReady, safety: showReady ? { gate: { mode: "open", until: new Date(Date.now()+3600000).toISOString() } } : {}, keyboard: host.platform === "darwin" ? "mac" : "native", job, projects: [], environment_workspace: true, environment_directory: "/preview/workspace", workspace: "/preview/workspace" };
  const pending = scenario === "resume" ? { version: 1, id: "preview-install", system, source: plan, editor_terms: true } : undefined;
  if (url.pathname.startsWith("/api/")) {
    let value = {};
    if (url.pathname === "/api/client/context") value = { mode: "local", connection_id: "local", client_platform: host.platform, server_platform: host.platform, connection: { state: "disconnected" } };
    else if (url.pathname === "/api/account") value = { exists: true, unlocked: true, username: "安装界面预览" };
    else if (url.pathname === "/api/status") value = status;
    else if (url.pathname === "/api/environment/initialization") {
      if (req.method === "POST") {
        let body=""; for await (const part of req) body+=part;
        const action=JSON.parse(body || "{}").action;
        if(action === "install") session.started=true;
        if(action === "complete" && showReady) session.required=false;
      }
      value=session;
    }
    else if (url.pathname === "/api/environment/storage") value={directory: host.platform === "windows" ? "D:\\Cage\\CageData" : "/preview/cage-state",has_environment:false};
    else if (url.pathname === "/api/settings") value = { workspace: "/preview/workspace", browser_home_url: "https://www.bing.com/", timezone: "Asia/Shanghai", locale: "zh_CN.UTF-8", keyboard: status.keyboard };
    else if (url.pathname === "/api/appearance") value = { ...defaultAppearance, mode: "light" };
    else if (url.pathname === "/api/proxy") value = { active: true, selected: "preview-node", port: 17896, core_available: true, state: "prepared", sources: [{ id: "preview", name: "预览代理", nodes: [{ id: "preview-node", name: "预览节点", type: "http" }] }] };
    else if (url.pathname === "/api/environment/guest-systems") value = { fresh: !pending, available: true, system, pending, linux_systems: ["ubuntu-22.04"] };
    else if (url.pathname === "/api/environment/guest-systems/clone") value = { plan, key: "preview-only", enough_space: scenario !== "space", download_bytes: scenario === "cached" ? 0 : scenario === "resume" ? Math.floor(host.bytes / 3) : host.bytes, minimum_free_gib: 32 + Math.ceil(host.bytes / 1073741824), free_gib: scenario === "space" ? 10 : 120, installation_started: Boolean(pending) };
    else if (url.pathname === "/api/host/git") value = { state: "ok", ready: true, message: "本机 Git 安全保护已启用。" };
    else if (url.pathname === "/api/terminals") value = [];
    else if (url.pathname === "/api/browser/tabs") value = { tabs: [], active: "" };
    else if (url.pathname === "/api/start") { session.installing=true; value={ id: "preview-install", kind: "guest-install", running: true }; }
    else if (url.pathname === "/api/stop") { session.installing=false; value={ id: "preview-stop", kind: "stop", running:false }; }
    else if (url.pathname === "/api/diagnostics") value={bytes:0,files:0};
    else if (url.pathname.endsWith("/events")) { res.writeHead(200, { "Content-Type": "text/event-stream" }); res.end(": preview\n\n"); return; }
    res.writeHead(200, { "Content-Type": "application/json", "Cache-Control": "no-store" }); res.end(JSON.stringify(value)); return;
  }
  if (url.pathname === "/") res.setHeader("Set-Cookie", [`preview_host=${hosts[selected] ? selected : "macos"}; SameSite=Strict; Path=/`, `preview_state=${scenario}; SameSite=Strict; Path=/`]);
  const request = http.request(new URL(url.pathname + url.search, upstream), { headers: { ...req.headers, host: upstream.host } }, response => {
    const parts = [];
    response.on("data", part => parts.push(part));
    response.on("end", () => {
      let body = Buffer.concat(parts);
      const headers = { ...response.headers };
      if (url.pathname === "/") {
        const links = Object.keys(hosts).map(key => `<a href="/?host=${key}&state=${scenario}" ${key === selected ? 'aria-current="page"' : ""}>${key === "macos" ? "Mac" : key === "windows" ? "Windows" : "Linux"}</a>`).join("");
        const states = [["download", "首次配置"], ["cached", "已有镜像"], ["progress", "安装进度"], ["failure", "安装失败"], ["ready", "环境就绪"]].map(([key, label]) => `<a href="/?host=${selected}&state=${key}" ${key === scenario ? 'aria-current="page"' : ""}>${label}</a>`).join("");
        const banner = `<aside class="installation-preview"><strong>安装界面预览</strong><span>模拟数据 · 不会安装</span><nav>${links}</nav><nav>${states}</nav></aside>`;
        body = Buffer.from(body.toString().replace("</head>", `<style>.installation-preview{display:flex;align-items:center;gap:14px;flex-wrap:wrap;padding:10px 18px;background:#f5f5f5;border-bottom:1px solid #ddd;font:13px -apple-system,BlinkMacSystemFont,'PingFang SC',sans-serif}.installation-preview span{color:#777}.installation-preview nav{display:flex;gap:12px}.installation-preview a{color:#666;text-decoration:none}.installation-preview a[aria-current]{color:#111;text-decoration:underline;text-underline-offset:4px}.client-shell{height:calc(100dvh - 45px)!important}.client-shell>.app{height:auto;flex:1;min-height:0}</style><script>window.cageNativeBridge={postMessage:async()=>null};</script></head>`).replace('<div id="root">', banner + '<div id="root">'));
        delete headers["content-length"];
      }
      res.writeHead(response.statusCode, headers); res.end(body);
    });
  });
  request.on("error", () => { res.writeHead(502); res.end("Start the Cage frontend preview server first."); });
  req.pipe(request);
}).listen(port, "127.0.0.1", () => console.log(`Installation copy preview: http://127.0.0.1:${port}/?host=macos`));
