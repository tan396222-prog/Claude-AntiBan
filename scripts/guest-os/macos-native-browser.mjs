// The bound guest user's LaunchAgent owns the browser. SSH callers attach as clients.
import { installMacOSCLIBrowser } from "./macos-cli-browser.mjs";
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { mkdir, rm, writeFile } from "node:fs/promises";
import { homedir } from "node:os";
import path from "node:path";
import { chromium, request } from "/usr/local/libexec/cage-workbench-browser-4.145.0-pw1.65.0/playwright/index.mjs";
import { createNativeBrowserService } from "./windows-browser.mjs";
import { relayMacOSBrowser, serveMacOSBrowser } from "./macos-browser-service.mjs";
import { lstat, readFile } from "node:fs/promises";

const data = path.join(homedir(), "Library", "Application Support", "Cage");
const editors = new Map();

async function openEditor(id, restart) {
  if (!/^[a-f0-9]{24}$/.test(id)) throw new Error("项目标识无效");
  const folder = path.join(homedir(), "Cage", "Projects", id);
  let editor = editors.get(id);
  if (restart && editor) { editor.child.kill(); editor = null; }
  if (!editor) {
    const password = randomBytes(32).toString("hex");
    const directory = path.join(data, "EditorSessions");
    const config = path.join(directory, id + ".yaml");
    await mkdir(directory, { mode: 0o700, recursive: true });
    await writeFile(config, "bind-addr: 127.0.0.1:0\nauth: password\npassword: " + password + "\ncert: false\n", { mode: 0o600 });
    const child = spawn("/usr/local/libexec/cage-macos-terminal", ["--editor", folder], { stdio: "pipe" });
    child.stdin.on("error", () => {});
    editor = { child, password, config };
    editors.set(id, editor);
    child.once("exit", () => {
      if (editors.get(id) === editor) { editors.delete(id); void rm(config, { force: true }).catch(() => {}); }
    });
    editor.address = await new Promise((resolve, reject) => {
      let recent = "";
      const failed = () => finish();
      const read = chunk => {
        recent = (recent + chunk).slice(-6000);
        const address = recent.match(/HTTP server listening on (http:\/\/127\.0\.0\.1:[1-9][0-9]*)/);
        if (address) finish(address[1]);
      };
      const timer = setTimeout(failed, 15000);
      function finish(address) {
        clearTimeout(timer);
        child.off("exit", failed); child.off("error", failed);
        for (const stream of [child.stdout, child.stderr]) { stream.off("data", read); stream.resume(); }
        if (address) resolve(address);
        else { child.kill(); reject(new Error("项目编辑器启动失败")); }
      }
      child.stdout.on("data", read); child.stderr.on("data", read);
      child.once("error", failed); child.once("exit", failed);
    });
  }
  const client = await request.newContext();
  try {
    const login = await client.post(editor.address + "/login", { form: { password: editor.password }, maxRedirects: 0, timeout: 7000 });
    if (![302, 303].includes(login.status())) throw new Error("客机编辑器登录失败");
    const hostname = id + ".cage-editor.localhost";
    const url = new URL(editor.address);
    url.hostname = hostname;
    url.searchParams.set("folder", folder);
    const { cookies } = await client.storageState();
    return { url: url.href, folder, cookies: cookies.map(cookie => ({ ...cookie, domain: hostname })) };
  } finally { await client.dispose(); }
}

async function audit(context, { timezone, network }) {
  // Sample the real workbench tab in a fresh isolated world. Page scripts
  // cannot replace navigator or Intl in this execution context.
  const tab = context.pages().find(p => !p.url().startsWith("chrome-extension:"));
  const cdp = await context.newCDPSession(tab);
  let result;
  try {
    const {frameTree} = await cdp.send("Page.getFrameTree");
    const {executionContextId} = await cdp.send("Page.createIsolatedWorld", {frameId: frameTree.frame.id, worldName: "cage-workbench-settings"});
    const observed = await cdp.send("Runtime.evaluate", {contextId: executionContextId, returnByValue: true,
      expression: `({timezone:Intl.DateTimeFormat().resolvedOptions().timeZone, expected_timezone:new Intl.DateTimeFormat("en",{timeZone:${JSON.stringify(timezone)}}).resolvedOptions().timeZone,language:navigator.language,platform:navigator.platform})`});
    result = observed.result.value;
    if (!result?.platform) throw new Error("无法读取客机浏览器设置");
  } finally { await cdp.detach(); }
  if (!network) return result;
  const isolated = await context.browser().newContext({ serviceWorkers: "block" });
  try {
    const page = await isolated.newPage();
    await page.goto("http://198.18.0.254/", { waitUntil: "domcontentloaded", timeout: 12000 });
      result.network = await page.evaluate(async () => {
        if (location.origin !== "http://198.18.0.254") throw new Error("无效的诊断地址");
        try {
          const response = await fetch("/egress", { cache: "no-store", credentials: "omit", signal: AbortSignal.timeout(14000) });
          return { ...(await response.json()), status: response.status };
        } catch { return { status: 0 }; }
      });
    return result;
  } finally { await isolated.close(); }
}

const marker = await lstat("/var/db/cage-guest-os");
if (process.platform !== "darwin" || process.getuid() < 500 || !marker.isFile() || marker.uid !== 0 || (marker.mode & 0o022) ||
    (await readFile("/var/db/cage-guest-os", "utf8")).trim() !== "macos") throw new Error("Cage browser requires its bound guest user");
const directory = path.join(homedir(), "Library", "Caches", "dev.cage.GuestBrowser");
if (process.argv.length > 3 || process.argv[2] && process.argv[2] !== "--service" && process.argv[2] !== "--client")
  throw new Error("Unknown browser service operation");
if (process.argv[2] !== "--service") {
  // Older hosts invoke this file with no arguments. They attach to the same
  // service and receive ownership capability metadata with the first reply.
  await relayMacOSBrowser(directory);
} else {
  const service = createNativeBrowserService({
    platform: "darwin", chromium, editor: openEditor, audit,
    directory: path.join(data, "BrowserWorkbench"),
    profileDirectory: path.join(data, "Browser"),
    executablePath: "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
  });
  await service.request({ operation: "configure", input: { networkMode: "packet", displayScale: 1 } });
  const closeBrowser = async () => {
    for (const { child } of editors.values()) child.kill();
    await service.close();
  };
  const server = await serveMacOSBrowser({ directory, dispatch: (request, options) => service.request(request, options), disconnect: signal => service.releaseInput(signal), close: closeBrowser });
  let closeLogin;
  try { closeLogin = await installMacOSCLIBrowser(data, message => server.request(message)); }
  catch (error) { await server.close(); throw error; }
  const shutdown = async () => {
    try { await server.close(); }
    finally { await closeLogin(); }
  };
  process.once("SIGTERM", () => { void shutdown().catch(() => { process.exitCode = 1; }); });
  process.once("SIGINT", () => { void shutdown().catch(() => { process.exitCode = 1; }); });
}
