// Chromium is controlled through inherited pipes, never an exposed CDP port.
import { spawn, execFile } from "node:child_process";
import { promisify } from "node:util";
const runFile = promisify(execFile);
import { readFileSync, writeFileSync, mkdirSync, existsSync, rmSync, constants } from "node:fs";
import { open } from "node:fs/promises";
import http from "node:http";
import { auditBrowser } from "./browser-audit.mjs";
import { BrowserVault } from "./browser-vault.mjs";
import { LoginBrowser } from "./browser-login.mjs";
import { BrowserClipboard } from "./browser-clipboard.mjs";
import { nativeInput } from "./native-input.mjs";
import { DownloadManager } from "./browser-downloads.mjs";
import { UploadManager, uploadLimit } from "./browser-uploads.mjs";
import { createHash, timingSafeEqual } from "node:crypto";

const config = JSON.parse(
  readFileSync("/etc/claude-cage/browser-control.json", "utf8"),
);
const selectionFile = "/home/cage-browser/workbench-state.json";
const homepageFile = "/home/cage-browser/homepage.json";
let homeURL = "https://www.bing.com/";
try {
  homeURL = checkedURL(JSON.parse(readFileSync(homepageFile, "utf8")).url);
} catch {}
let restoreURL = "";
try {
  restoreURL = JSON.parse(readFileSync(selectionFile, "utf8")).url || "";
} catch {}
function rememberSelection() {
  const tab = targets.get(activeId);
  if (tab && !tab.login)
    try {
      writeFileSync(selectionFile, JSON.stringify({ url: tab.url }), {
        mode: 0o600,
      });
    } catch {}
}
const downloadPath = "/home/cage-browser/Downloads";
mkdirSync(downloadPath, { recursive: true });
const splitDirectory = "/usr/local/lib/cage-split";
const extensionId = createHash("sha256").update(splitDirectory).digest("hex").slice(0, 32)
  .replace(/./g, c => String.fromCharCode(97 + parseInt(c, 16)));
const splitURL = `chrome-extension://${extensionId}/control.html`;
let splitView = null, splitSession, splitOpening, resizeTimer;
const favicons = new Map(), faviconRequests = new Map(), documents = new Map();

function isBrowserTab(target) {
  return target.type === "page" && target.url !== splitURL &&
    !target.url.startsWith("devtools://") && !auditContexts.has(target.browserContextId);
}

const flags = [
  `--load-extension=${splitDirectory}`,
  "--user-data-dir=/home/cage-browser/profile",
  "--remote-debugging-pipe",
  // The pipe manages tabs for a human-operated browser. Keep it private while
  // preventing its presence alone from enabling Blink's automation marker.
  "--disable-blink-features=AutomationControlled",
  // Kiosk mode blocks Chromium's DevTools UI, including F12. Ordinary
  // fullscreen keeps the guest browser chrome hidden while allowing inspection.
  "--start-fullscreen",
  "--no-first-run",
  "--restore-last-session",
  "--no-default-browser-check",
  "--disable-quic",
  "--disable-session-crashed-bubble",
  "--window-size=1440,900",
  "--force-device-scale-factor=2",
  `--lang=${process.env.CAGE_LANGUAGE || "en-US"}`,
];
if (!existsSync("/home/cage-browser/profile/Default/Sessions"))
  flags.push(homeURL);
const chrome = spawn("/usr/bin/chromium", flags, {
  stdio: ["ignore", "ignore", "inherit", "pipe", "pipe"],
});
let sequence = 0,
  buffer = Buffer.alloc(0),
  activeId = "",
  ready = false;
let shuttingDown = false;
const events = new Set();
let navigation = { id: "", back: false, forward: false }, navigationSequence = 0;
const auditContexts = new Set();
const loginBrowser = new LoginBrowser(cdp);
const pending = new Map(),
  targets = new Map(),
  sessions = new Map();
const browserClipboard = new BrowserClipboard(cdp, ({ id, text, context }) => {
  if (id !== activeId || !targets.has(id)) return;
  const payload = "event: clipboard\ndata: " + JSON.stringify({ id, text, context }) + "\n\n";
  for (const stream of events) stream.write(payload);
});
const vault = new BrowserVault({ cdp, active: () => activeId, tab: id => targets.get(id) });
const downloads = new DownloadManager({
  directory: "/home/cage-browser",
  changed: () => broadcastState(),
  cancel: id => cdp("Browser.cancelDownload", { guid: id }),
  save: async (id, name) => {
    let output;
    try {
      output = (await runFile("/usr/bin/sudo", ["/usr/local/bin/cage-save-download", id, name], { maxBuffer: 16384 })).stdout;
    } catch (error) {
      output = error.stdout || '{"ok":false,"error":"下载保存未完成，请检查共享目录后重试。"}';
    }
    return JSON.parse(output);
  },
});
const uploadDirectory = "/home/cage-browser/.cage-uploads";
rmSync(uploadDirectory, { recursive: true, force: true });
const uploadSessions = new Map();
const uploads = new UploadManager({ directory: uploadDirectory, cdp, changed: () => broadcastState() });
function cdp(method, params = {}, sessionId, timeout = 4500) {
  return new Promise((resolve, reject) => {
    const id = ++sequence;
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`Browser command timed out: ${method}`));
    }, timeout);
    pending.set(id, { resolve, reject, timer });
    chrome.stdio[3].write(
      JSON.stringify({
        id,
        method,
        params,
        ...(sessionId ? { sessionId } : {}),
      }) + "\0",
    );
  });
}
chrome.stdio[4].on("data", (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  let end;
  while ((end = buffer.indexOf(0)) >= 0) {
    const raw = buffer.subarray(0, end);
    buffer = buffer.subarray(end + 1);
    let event;
    try {
      event = JSON.parse(raw);
    } catch {
      continue;
    }
    if (event.id) {
      const call = pending.get(event.id);
      if (!call) continue;
      pending.delete(event.id);
      clearTimeout(call.timer);
      event.error
        ? call.reject(new Error(event.error.message))
        : call.resolve(event.result);
      continue;
    }
    vault.event(event);
    const p = event.params || {};
    browserClipboard.event(event);
    if (event.sessionId === splitSession && event.method === "Runtime.bindingCalled" &&
        p.name === "cageFaviconChanged") refreshFavicon(p.payload);
    if (event.sessionId === splitSession && event.method === "Runtime.bindingCalled" &&
        p.name === "cagePaneActivated" && splitView &&
        [splitView.left, splitView.right].includes(p.payload) && p.payload !== activeId) {
      activeId = p.payload;
      rememberSelection();
      broadcastState(true);
    }
    if (event.sessionId === splitSession && event.method === "Runtime.bindingCalled" &&
        p.name === "cagePaneSeparated" && splitView && [splitView.left, splitView.right].includes(p.payload)) {
      splitView = null;
      broadcastState();
    }
    if (splitView && ["Page.frameResized", "Page.frameNavigated", "Page.navigatedWithinDocument"].includes(event.method))
      scheduleSplitMeasure();
    if (
      event.method === "Target.targetCreated" ||
      event.method === "Target.targetInfoChanged"
    ) {
      const t = p.targetInfo;
      if (isBrowserTab(t)) {
        const fresh = !targets.has(t.targetId);
        targets.set(t.targetId, loginBrowser.tab(t, targets.get(t.targetId)));
        if (fresh) void session(t.targetId).catch(() => {});
        if (t.targetId === activeId) rememberSelection();
        broadcastState();
        if (!activeId) activeId = t.targetId;
        if (ready && fresh && t.openerId)
          void activate(t.targetId, true).catch(() => {});
      }
    }
    if (event.method === "Target.targetDestroyed") {
      if (!targets.has(p.targetId)) continue;
      vault.remove(p.targetId);
      targets.delete(p.targetId);
      browserClipboard.detachTab(p.targetId);
      favicons.delete(p.targetId);
      documents.delete(p.targetId);
      faviconRequests.delete(p.targetId);
      if (splitView && [splitView.left, splitView.right].includes(p.targetId)) splitView = null;
      uploads.cancelTab(p.targetId);
      for (const [sid, tab] of uploadSessions) if (tab === p.targetId) uploadSessions.delete(sid);
      sessions.delete(p.targetId);
      broadcastState();
      if (activeId === p.targetId) {
        activeId = [...targets.keys()][0] || "";
        if (activeId) void activate(activeId).catch(() => {});
      }
    }
    if (event.method === "Browser.downloadWillBegin")
      downloads.begin(p.guid, p.suggestedFilename);
    if (event.method === "Browser.downloadProgress") downloads.progress(p);
    const uploadTab = uploadSessions.get(event.sessionId);
    if (uploadTab && event.method === "Page.frameNavigated" && !p.frame?.parentId) {
      documents.set(uploadTab, p.frame.loaderId);
      favicons.delete(uploadTab);
      faviconRequests.delete(uploadTab);
      broadcastState();
    }
    if (uploadTab && event.method === "Page.loadEventFired") refreshFavicon(uploadTab);
    if (uploadTab === activeId && (
      event.method === "Page.loadEventFired" ||
      event.method === "Page.navigatedWithinDocument" ||
      (event.method === "Page.frameNavigated" && !p.frame?.parentId)
    )) void refreshNavigation();
    if (uploadTab && event.method === "Page.fileChooserOpened")
      void uploads.opened(uploadTab, event.sessionId, p).catch(() => {});
    if (uploadTab && event.method === "Page.frameNavigated" && !p.frame?.parentId)
      uploads.cancelTab(uploadTab);
  }
});
chrome.on("exit", () => {
  ready = false;
  for (const p of pending.values()) {
    clearTimeout(p.timer);
    p.reject(new Error("Browser stopped"));
  }
  pending.clear();
  process.exit(shuttingDown ? 0 : 1);
});
for (const channel of chrome.stdio.slice(3))
  channel.on("error", () => {
    if (!shuttingDown) process.exit(1);
  });
async function session(id) {
  if (!targets.has(id)) throw new Error("Tab no longer exists");
  if (!sessions.has(id))
    sessions.set(
      id,
      cdp("Target.attachToTarget", { targetId: id, flatten: true }).then(async (r) => {
        uploadSessions.set(r.sessionId, id);
        await cdp("Page.enable", {}, r.sessionId);
        await cdp("Page.setInterceptFileChooserDialog", { enabled: true }, r.sessionId);
        await browserClipboard.attach(r.sessionId, id).then(result => {
          if (result?.skipped.length) console.warn("Browser copy observer skipped a modified or replaced document; reload the page to enable copy buttons.");
        }).catch(() => console.warn("Browser copy observer unavailable for this page."));
        await vault.attach(id, r.sessionId).catch(() => {});
        return r.sessionId;
      }).catch(error => { sessions.delete(id); throw error; }),
    );
  return sessions.get(id);
}
function scheduleSplitMeasure() {
  clearTimeout(resizeTimer);
  resizeTimer = setTimeout(async () => {
    const pair = splitView;
    if (!pair) return;
    try {
      const pages = await Promise.all([pair.left, pair.right].map(async id => {
        const sid = await session(id);
        const [metrics, history] = await Promise.all([
          cdp("Page.getLayoutMetrics", {}, sid), cdp("Page.getNavigationHistory", {}, sid),
        ]);
        return { width: metrics.cssLayoutViewport.clientWidth, back: history.currentIndex > 0,
          forward: history.currentIndex < history.entries.length - 1 };
      }));
      if (pair !== splitView) return;
      const fraction = pages[0].width / (pages[0].width + pages[1].width);
      if (fraction > 0 && fraction < 1) pair.fraction = fraction;
      pair.history = Object.fromEntries([pair.left, pair.right].map((id, index) =>
        [id, { back: pages[index].back, forward: pages[index].forward }]));
      broadcastState();
    } catch { /* A closing tab may already have lost its renderer. */ }
  }, 80);
}

async function splitController() {
  if (splitSession) return splitSession;
  if (splitOpening) return splitOpening;
  splitOpening = openSplitController().finally(() => { splitOpening = null; });
  return splitOpening;
}

async function openSplitController() {
  const { targetId } = await cdp("Target.createTarget", { url: splitURL, hidden: true, background: true });
  try {
    const { sessionId } = await cdp("Target.attachToTarget", { targetId, flatten: true });
    await cdp("Runtime.enable", {}, sessionId);
    await cdp("Runtime.addBinding", { name: "cagePaneActivated" }, sessionId);
    await cdp("Runtime.addBinding", { name: "cagePaneSeparated" }, sessionId);
    await cdp("Runtime.addBinding", { name: "cageFaviconChanged" }, sessionId);
    const loaded = await cdp("Runtime.evaluate", { awaitPromise: true, returnByValue: true,
      expression: `new Promise(resolve => {
        const ready = () => resolve(typeof setCageSplit === "function");
        document.readyState !== "complete" ? addEventListener("DOMContentLoaded", ready, { once: true }) : ready();
      })` }, sessionId);
    if (!loaded.result.value) throw new Error("Split controller unavailable");
    splitSession = sessionId;
    for (const id of targets.keys()) refreshFavicon(id);
    return sessionId;
  } catch (error) {
    await cdp("Target.closeTarget", { targetId }).catch(() => {});
    throw error;
  }
}

function refreshFavicon(id) {
  if (!targets.has(id) || !splitSession) return;
  const request = {};
  faviconRequests.set(id, request);
  void cdp("Runtime.evaluate", {
    expression: `readCageFavicon(${JSON.stringify(id)})`, awaitPromise: true, returnByValue: true,
  }, splitSession).then(({ result }) => {
    if (faviconRequests.get(id) !== request) return;
    const icon = result.value;
    if (!icon || icon.url !== targets.get(id)?.url || typeof icon.favicon !== "string" ||
        icon.favicon.length > 8192 || (icon.favicon && !/^data:image\/png;base64,[A-Za-z0-9+/=]+$/.test(icon.favicon))) return;
    if ((favicons.get(id) || "") === icon.favicon) return;
    favicons.set(id, icon.favicon);
    broadcastState();
  }).catch(() => {}).finally(() => {
    if (faviconRequests.get(id) === request) faviconRequests.delete(id);
  });
}

async function setSplit({ left, right, id }) {
  if (left && (!targets.has(left) || !targets.has(right) || left === right ||
      ![left, right].includes(id))) throw new Error("Invalid split tabs");
  const sid = await splitController();
  const result = await cdp("Runtime.evaluate", { awaitPromise: true, returnByValue: true,
    expression: `setCageSplit(${JSON.stringify(left || "")}, ${JSON.stringify(right || "")})`,
  }, sid);
  splitView = null;
  if (result.exceptionDetails) {
    broadcastState();
    throw new Error("Cannot split these Chromium tabs");
  }
  if (left) {
    splitView = { left, right, fraction: 0.5 };
    scheduleSplitMeasure();
  }
  if (id && targets.has(id)) await activate(id);
  else broadcastState();
  return { split: splitView };
}

async function activate(id, focus = false) {
  if (!targets.has(id)) throw new Error("Tab no longer exists");
  // Restored background tabs may have no live renderer until activated.
  // Page.enable must not block the command that wakes that renderer.
  await cdp("Target.activateTarget", { targetId: id });
  await session(id);
  activeId = id;
  void refreshNavigation();
  rememberSelection();
  broadcastState(focus);
  const { windowId } = await cdp("Browser.getWindowForTarget", {
    targetId: id,
  });
  await cdp("Browser.setWindowBounds", {
    windowId,
    bounds: { windowState: "fullscreen" },
  }).catch(() => {});
}
function checkedURL(value) {
  if (value === "about:blank") return value;
  const u = new URL(value);
  if (!["http:", "https:"].includes(u.protocol) || u.username || u.password)
    throw new Error("Only HTTP(S) URLs are accepted");
  return u.href;
}
async function newTab(url = homeURL) {
  const { targetId } = await cdp("Target.createTarget", {
    url: checkedURL(url),
    background: false,
  });
  // The response and discovery event may arrive in either order.
  if (!targets.has(targetId))
    targets.set(targetId, { targetId, type: "page", url, title: "" });
  await activate(targetId, true);
  return { id: targetId };
}
async function editor(workspace = config.workspace, reload = false) {
  if (typeof workspace !== "string" || !workspace.startsWith("/")) throw new Error("Invalid workspace");
  const editorURL = `http://127.0.0.1:18080/?folder=${encodeURIComponent(workspace)}`;
  const existing = [...targets.values()].find((t) =>
    t.url === editorURL,
  );
  if (existing && !reload) {
    await activate(existing.targetId);
    return { id: existing.targetId };
  }
  const response = await fetch("http://127.0.0.1:18080/login", {
    method: "POST",
    redirect: "manual",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ password: config.editorPassword }),
    signal: AbortSignal.timeout(5000),
  });
  const cookies = response.headers.getSetCookie().map((raw) => {
    const first = raw.split(";")[0],
      index = first.indexOf("=");
    return {
      name: first.slice(0, index),
      value: first.slice(index + 1),
      domain: "127.0.0.1",
      path: "/",
      httpOnly: true,
      sameSite: "Lax",
    };
  });
  if (!cookies.length) throw new Error("Editor authentication is unavailable");
  await cdp("Storage.setCookies", { cookies });
  if (existing) {
    await activate(existing.targetId);
    await cdp("Page.reload", {}, await session(existing.targetId));
    return { id: existing.targetId };
  }
  return newTab(editorURL);
}
async function restartEditor(workspace) {
  await runFile("/usr/bin/sudo", ["/usr/bin/systemctl", "restart", "cage-editor.service"], { timeout: 18000, maxBuffer: 4096 });
  const deadline = Date.now() + 5000;
  while (Date.now() < deadline) {
    try {
      const response = await fetch("http://127.0.0.1:18080/healthz", { signal: AbortSignal.timeout(1000) });
      if (response.ok) return editor(workspace || config.workspace, true);
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 200));
  }
  throw new Error("编辑器尚未恢复，请稍后重试。");
}
async function tabState() {
  const { targetInfos } = await cdp("Target.getTargets");
  for (const tab of targetInfos)
    if (isBrowserTab(tab))
      targets.set(tab.targetId, loginBrowser.tab(tab, targets.get(tab.targetId)));
  await refreshNavigation();
  return publicState();
}
async function refreshNavigation() {
  const id = activeId, sequence = ++navigationSequence;
  if (!targets.has(id)) return;
  try {
    const history = await cdp("Page.getNavigationHistory", {}, await session(id));
    // A slow response for an earlier navigation must not replace newer state.
    if (id !== activeId || sequence !== navigationSequence || !targets.has(id)) return;
    navigation = {
      id,
      back: history.currentIndex > 0,
      forward: history.currentIndex < history.entries.length - 1,
    };
    broadcastState();
  } catch {
    // Keep the last confirmed state while the browser is temporarily unavailable.
  }
}
function displayTitle(tab) {
  if (tab.login) return "账号授权";
  if (tab.url === "about:blank") return "新标签页";
  const title = tab.title;
  if (
    !title ||
    title === tab.url ||
    title === tab.url.replace(/^https?:\/\//, "")
  ) {
    try {
      return new URL(tab.url).hostname;
    } catch {
      return "新标签页";
    }
  }
  return title.slice(0, 160);
}
function publicState() {
  return {
    active: activeId,
    split: splitView,
    back: navigation.id === activeId && targets.has(activeId) && navigation.back,
    forward: navigation.id === activeId && targets.has(activeId) && navigation.forward,
    downloads: downloads.snapshot(),
    uploads: uploads.snapshot(),
    tabs: [...targets.values()].map((t) => ({
      id: t.targetId,
      title: displayTitle(t),
      url: t.url,
      document: documents.get(t.targetId) || "",
      favicon: favicons.get(t.targetId) || "",
      kind: t.url.startsWith("http://127.0.0.1:18080/") ? "editor" : "web",
      workspace: t.url.startsWith("http://127.0.0.1:18080/") ? new URL(t.url).searchParams.get("folder") || config.workspace : undefined,
    })),
  };
}
function broadcastState(focus = false, desktopFocus = false) {
  const payload =
    "data: " + JSON.stringify({ ...publicState(), focus, desktopFocus }) + "\n\n";
  for (const stream of events) stream.write(payload);
}
setInterval(() => {
  for (const stream of events) stream.write(": heartbeat\n\n");
}, 10000).unref();
let displaySize = "";
async function resizeDisplay(width, height) {
  if (
    !Number.isInteger(width) ||
    !Number.isInteger(height) ||
    width < 320 ||
    height < 480 ||
    width > 8192 ||
    height > 8192
  )
    throw new Error("Invalid display size");
  const name = `cage-${width}x${height}`;
  if (displaySize === name) return { ok: true };
  const { stdout } = await runFile("/usr/bin/xrandr", ["--query"]);
  if (!stdout.includes(name)) {
    const rate = (((width + 32) * (height + 8) * 60) / 1000000).toFixed(3);
    await runFile("/usr/bin/xrandr", [
      "--newmode",
      name,
      rate,
      String(width),
      String(width + 8),
      String(width + 16),
      String(width + 32),
      String(height),
      String(height + 1),
      String(height + 2),
      String(height + 8),
      "+HSync",
      "-VSync",
    ]);
    await runFile("/usr/bin/xrandr", ["--addmode", "VNC-0", name]);
  }
  await runFile("/usr/bin/xrandr", ["--output", "VNC-0", "--mode", name]);
  displaySize = name;
  for (const old of stdout.matchAll(/^\s+(cage-\d+x\d+)/gm))
    if (old[1] !== name) {
      await runFile("/usr/bin/xrandr", ["--delmode", "VNC-0", old[1]]).catch(
        () => {},
      );
      await runFile("/usr/bin/xrandr", ["--rmmode", old[1]]).catch(() => {});
    }
  return { ok: true };
}
let mutations = Promise.resolve();
function mutate(fn) {
  const result = mutations.then(fn);
  mutations = result.catch(() => {});
  return result;
}

function pasteImage(png) {
  if (typeof png !== "string" || png.length > 27962028) throw new Error("Image too large");
  const data = Buffer.from(png, "base64");
  if (!data.subarray(0, 8).equals(Buffer.from([137,80,78,71,13,10,26,10]))) throw new Error("Invalid PNG");
  return new Promise((resolve, reject) => {
    const child = spawn("/usr/local/bin/cage-image-input", ["paste"], {
      stdio: ["pipe", "ignore", "ignore"], timeout: 15000,
    });
    child.on("error", reject);
    child.stdin.on("error", reject);
    child.on("close", (code) => code === 0 ? resolve() : reject(new Error("Image paste failed")));
    child.stdin.end(data);
  });
}

const server = http.createServer(async (req, res) => {
  res.setHeader("Cache-Control", "no-store");
  res.setHeader("Content-Type", "application/json");
  const auth = Buffer.from(req.headers.authorization || ""),
    expected = Buffer.from("Bearer " + config.token);
  if (auth.length !== expected.length || !timingSafeEqual(auth, expected)) {
    res.writeHead(401);
    res.end("{}");
    return;
  }
  if (req.headers.origin) {
    res.writeHead(403);
    res.end("{}");
    return;
  }
  try {
    if (!ready) throw new Error("Browser is starting");
    const route = new URL(req.url, "http://localhost");
    if (req.method === "POST" && route.pathname === "/upload-file") {
      if (Number(req.headers["content-length"]) > uploadLimit) throw new Error("每个文件不能超过 2 GB");
      res.end(JSON.stringify(await uploads.receive(route.searchParams.get("id"), route.searchParams.get("name"), req)));
      return;
    }
    if (req.method === "GET" && route.pathname === "/download-file") {
      const source = downloads.sourceFile(route.searchParams.get("id"));
      const file = await open(source, constants.O_RDONLY | constants.O_NOFOLLOW);
      const stat = await file.stat();
      if (!stat.isFile()) { await file.close(); throw new Error("下载文件不可用"); }
      res.setHeader("Content-Type", "application/octet-stream");
      res.setHeader("Content-Length", stat.size);
      const stream = file.createReadStream();
      res.on("close", () => stream.destroy());
      stream.on("error", () => res.destroy());
      stream.pipe(res);
      return;
    }
    if (req.method === "GET" && req.url === "/health") {
      res.end('{"ready":true}');
      return;
    }
    if (req.method === "GET" && req.url === "/events") {
      res.setHeader("Content-Type", "text/event-stream");
      res.setHeader("Connection", "keep-alive");
      res.flushHeaders();
      events.add(res);
      res.write("data: " + JSON.stringify(publicState()) + "\n\n");
      res.on("close", () => events.delete(res));
      return;
    }
    if (req.method === "GET" && req.url === "/tabs") {
      res.end(JSON.stringify(await tabState()));
      return;
    }
    if (req.method !== "POST") {
      res.writeHead(404);
      res.end("{}");
      return;
    }
    let text = "";
    for await (const chunk of req) {
      text += chunk;
      if (text.length > (req.url === "/paste-image" ? 28 * 1024 * 1024 : 65536)) throw new Error("Request too large");
    }
    const input = JSON.parse(text || "{}");
    if (req.url === "/desktop-focus") {
      broadcastState(false, true);
      res.end('{"ok":true}');
      return;
    }
    if (req.url === "/upload-start" || req.url === "/upload-finish") {
      const result = await mutate(async () => {
        const tabId = req.url === "/upload-start" ? input.id : uploads.request(input.id).tabId;
        if (!targets.has(tabId) || activeId !== tabId) throw new Error("文件接收网页已切换，请返回原网页重试");
        return req.url === "/upload-start"
          ? uploads.start(tabId, await session(tabId), input)
          : uploads.finish(input.id);
      });
      res.end(JSON.stringify(result)); return;
    }
    if (req.url === "/upload-cancel") { uploads.cancel(input.id); res.end('{"ok":true}'); return; }
    if (req.url === "/download-retry") {
      res.end(JSON.stringify(downloads.retry(input.id)));
      return;
    }
    if (req.url === "/audit") {
      if (typeof input.timezone !== "string" || input.timezone.length > 128) throw new Error("诊断时区无效");
      res.end(JSON.stringify(await auditBrowser(cdp, auditContexts, input.network === true, input.timezone)));
      return;
    }
    const result = await mutate(async () => {
      if (req.url === "/vault") {
        try { return await vault.request(input); }
        catch (error) { return { vaultError: error.message }; }
      }
      if (req.url === "/login-open") {
        return loginBrowser.open(input.login, input.url, async tab => {
          if (!targets.has(tab.targetId)) targets.set(tab.targetId, loginBrowser.tab(tab));
          await activate(tab.targetId, true);
        });
      }
      if (req.url === "/login-finish") {
        await loginBrowser.finish(input.login);
        return { ok: true };
      }
      if (req.url === "/homepage") {
        const url = checkedURL(input.url);
        if (url === "about:blank") throw new Error("Homepage must be an HTTP(S) URL");
        writeFileSync(homepageFile, JSON.stringify({ url }), { mode: 0o600 });
        homeURL = url;
        for (const tab of targets.values()) {
          if (tab.url === "about:blank" || /^chrome:\/\/(newtab|new-tab-page|new-tab-page-third-party)\/?$/.test(tab.url)) {
            const sid = await session(tab.targetId);
            await cdp("Page.navigate", { url: homeURL }, sid);
          }
        }
        return { ok: true };
      }
      if (req.url === "/split") return setSplit(input);
      if (req.url === "/resize")
        return resizeDisplay(input.width, input.height);
      if (req.url === "/new") return newTab(input.url || undefined);
      if (req.url === "/editor") return editor(input.workspace || config.workspace);
      if (req.url === "/editor-restart") return restartEditor(input.workspace);
      if (req.url === "/ime") {
        if (!["browser", "desktop"].includes(input.target)) throw new Error("Input target is invalid");
        if (input.target === "browser" && input.mode !== "cancel" &&
            (input.id !== activeId || !targets.has(input.id))) throw new Error("Input target changed");
        return nativeInput(input);
      }
      const id = input.id || activeId;
      if (!targets.has(id)) throw new Error("Tab no longer exists");
      switch (req.url) {
        case "/copy-context": {
          if (!/^[a-f0-9]{32}$/.test(input.context || "") || id !== activeId)
            throw new Error("Copy target changed");
          const ids = splitView ? [splitView.left, splitView.right] : [id];
          for (const tab of ids) {
            await session(tab);
            await browserClipboard.setContext(tab, input.context);
          }
          break;
        }
        case "/paste-image":
          if (input.id !== activeId) throw new Error("Image target changed");
          await pasteImage(input.png);
          break;
        case "/insert-text":
          if (input.id !== activeId || typeof input.text !== "string" ||
              !input.text || Buffer.byteLength(input.text, "utf8") > 8192)
            throw new Error("Input target or text is invalid");
          await cdp("Input.insertText", { text: input.text }, await session(id));
          break;
        case "/activate":
          await activate(id);
          break;
        case "/close":
          if (targets.size === 1) await newTab();
          await cdp("Target.closeTarget", { targetId: id });
          break;
        case "/navigate":
          await cdp(
            "Page.navigate",
            { url: checkedURL(input.url) },
            await session(id),
          );
          break;
        case "/reload":
          await cdp("Page.reload", {}, await session(id));
          break;
        case "/back":
        case "/forward": {
          const sid = await session(id),
            h = await cdp("Page.getNavigationHistory", {}, sid),
            entry = h.entries[h.currentIndex + (req.url === "/back" ? -1 : 1)];
          if (entry)
            await cdp(
              "Page.navigateToHistoryEntry",
              { entryId: entry.id },
              sid,
            );
          break;
        }
        default:
          throw new Error("Unknown browser action");
      }
      return { ok: true };
    });
    res.end(JSON.stringify(result));
  } catch (error) {
    res.writeHead(400);
    res.end(JSON.stringify({ error: error.message }));
  }
});
server.listen(18500, "127.0.0.1");
(async () => {
  await cdp("Browser.getVersion");
  await cdp("Target.setDiscoverTargets", { discover: true });
  const { targetInfos } = await cdp("Target.getTargets");
  for (const t of targetInfos)
    if (isBrowserTab(t)) targets.set(t.targetId, t);
  await cdp("Browser.setDownloadBehavior", {
    behavior: "allowAndName",
    downloadPath,
    eventsEnabled: true,
  });
  if (!targets.size) await newTab();
  else
    await activate(
      [...targets.values()].find((t) => t.url === restoreURL)?.targetId ||
        [...targets.keys()][0],
    );
  await setSplit({}).catch(() => {});
  ready = true;
})().catch(error => {
  console.error("Browser startup failed:", error.message);
  process.exit(1);
});
process.on("SIGTERM", () => {
  shuttingDown = true;
  server.close();
  cdp("Browser.close").catch(() => {});
  setTimeout(() => process.exit(0), 2000).unref();
});
