// Uses the built app, real workbench APIs, real Mihomo, and local CONNECT
// upstreams. No request reaches the user's running Cage or Clash instances.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import http from "node:http";
import net from "node:net";
import path from "node:path";
import { spawn, execFile } from "node:child_process";
import { promisify } from "node:util";
import { createRequire } from "node:module";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const run = promisify(execFile);
const root = path.resolve(import.meta.dirname, "../..");
const appBundle = process.env.CAGE_QA_APP_BUNDLE || path.join(root, "_output/Cage.app");
const executable = path.join(appBundle, "Contents/Resources/cage");
const output = path.join(root, "docs/test-reports/2026-10-31-managed-proxy");
await fs.mkdir(output, { recursive: true });
await fs.writeFile(path.join(output, "result.json"), JSON.stringify({ passed: false, state: "running" }));
const profile = await fs.mkdtemp("/private/tmp/cage-proxy-qa-");
const config = path.join(profile, "config.json");
const env = { ...process.env, CLAUDE_CAGE_CONFIG: config };
await run(executable, ["init"], { env });
const sockets = new Set();
async function upstream(label) {
  const target = http.createServer((_req, res) => res.end(`via-${label}`));
  await new Promise((resolve) => target.listen(0, "127.0.0.1", resolve));
  let requests = 0;
  const proxy = http.createServer();
  proxy.on("connect", (_req, client, head) => {
    requests++;
    const remote = net.connect(target.address().port, "127.0.0.1", () => {
      client.write("HTTP/1.1 200 Connection Established\r\n\r\n");
      if (head.length) remote.write(head);
      remote.pipe(client); client.pipe(remote);
    });
    for (const socket of [remote, client]) { sockets.add(socket); socket.on("close", () => sockets.delete(socket)); socket.on("error", () => { client.destroy(); remote.destroy(); }); }
  });
  await new Promise((resolve) => proxy.listen(0, "127.0.0.1", resolve));
  return { proxy, target, port: proxy.address().port, requests: () => requests };
}
const a = await upstream("A"), b = await upstream("B");
const fixture = `proxies:\n  - {name: QA A, type: http, server: 127.0.0.1, port: ${a.port}}\n  - {name: QA B, type: http, server: 127.0.0.1, port: ${b.port}}\n`;
const file = path.join(profile, "nodes.yaml"); await fs.writeFile(file, fixture);
const reserved = net.createServer(); await new Promise((resolve) => reserved.listen(0, "127.0.0.1", resolve));
const port = reserved.address().port; await new Promise((resolve) => reserved.close(resolve));
const log = await fs.open(path.join(profile, "server.log"), "w", 0o600);
const server = spawn(executable, ["serve"], { env, stdio: ["ignore", log.fd, log.fd] });
let browser;
const errors = [];
try {
  let control;
  for (let attempt = 0; attempt < 100; attempt++) {
    try { control = JSON.parse(await fs.readFile(path.join(profile, "control.json"))); break; } catch { await new Promise((r) => setTimeout(r, 100)); }
  }
  assert.ok(control, "control service did not start");
  const origin = `http://localhost:${control.port}`;
  const unauth = await fetch(`${origin}/api/proxy`); assert.equal(unauth.status, 401);
  const launch = await fetch(`${origin}/api/launch`, { method: "POST", headers: { Origin: origin, "Content-Type": "application/json" }, body: JSON.stringify({ token: control.token }) }).then((r) => r.json());
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ viewport: { width: 1280, height: 850 } });
  page.on("pageerror", (e) => errors.push(e.message));
  await page.goto(launch.url); console.log("QA: workbench loaded");
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "网络与代理", exact: true }).click();
  await page.getByRole("heading", { name: "内置代理", exact: true }).waitFor();
  await page.screenshot({ path: path.join(output, "empty.png") });
  await page.getByRole("button", { name: "添加来源", exact: true }).click();
  await page.getByRole("button", { name: "本地 YAML", exact: true }).click();
  await page.locator('input[type="file"]').setInputFiles(file);
  await page.getByRole("button", { name: "导入并验证", exact: true }).click();
  await page.getByText("nodes", { exact: true }).waitFor(); console.log("QA: file imported");
  await page.getByRole("tab", { name: "节点", exact: true }).click();
  await page.getByRole("button", { name: "选择节点 QA A", exact: true }).waitFor();
  await page.getByRole("tab", { name: "诊断", exact: true }).click();
  await page.getByLabel("独立代理端口", { exact: true }).fill(String(port));
  await page.getByRole("tab", { name: "节点", exact: true }).click();
  await page.getByRole("button", { name: "选择节点 QA A", exact: true }).click();
  await page.getByRole("button", { name: "应用代理", exact: true }).click();
  await page.getByText("核心运行中", { exact: false }).waitFor();
  await page.waitForFunction(() => !document.querySelector(".proxy-progress"));
  await page.getByText("没有未保存的修改", { exact: true }).waitFor();
  async function state() { return page.evaluate(() => fetch("/api/proxy").then((r) => r.json())); }
  let p = await state(); assert.equal(p.running, true); assert.equal(p.active, true); assert.equal(p.port, port);
  const firstRevision = p.revision;
  async function through() {
    const stored = JSON.parse(await fs.readFile(path.join(profile, "proxy/sources.json")));
    const file=path.join(profile,"curl.conf");await fs.writeFile(file,`proxy = "socks5h://cage:${stored.auth}@127.0.0.1:${stored.port}"\n`,{mode:0o600});
    return (await run("curl", ["--config",file,"--noproxy", "", "--fail", "--silent", "--show-error", "--max-time", "10", "http://example.com/"])).stdout;
  }
  console.log("QA: core selected A"); assert.equal(await through(), "via-A"); assert.ok(a.requests() > 0);
  await page.getByLabel("搜索节点", { exact: true }).fill("QA B");
  assert.equal(await page.getByRole("button", { name: "选择节点 QA A", exact: true }).count(), 0);
  await page.getByLabel("搜索节点", { exact: true }).fill("");
  await page.screenshot({ path: path.join(output, "nodes.png") });
  await page.getByRole("button", { name: "选择节点 QA B", exact: true }).click();
  await page.getByRole("button", { name: "应用代理", exact: true }).click();
  await page.waitForFunction(() => !document.querySelector(".proxy-progress"));
  p = await state(); assert.notEqual(p.revision, firstRevision);
  assert.equal(await through(), "via-B"); assert.ok(b.requests() > 0);
  await assert.rejects(run("curl", ["--noproxy", "", "--fail", "--silent", "--max-time", "5", "--proxy", `socks5h://127.0.0.1:${port}`, "http://example.com/"]));
  await page.getByRole("tab", { name: "订阅", exact: true }).click();
  await page.getByRole("button", { name: "编辑 nodes", exact: true }).click();
  await page.getByLabel("来源名称", { exact: true }).fill("QA imported");
  await page.getByRole("button", { name: "保存来源", exact: true }).click();
  await page.getByText("QA imported", { exact: true }).waitFor();
  await page.reload();
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "网络与代理", exact: true }).click();
  await page.getByRole("tab", { name: "节点", exact: true }).click();
  assert.equal((await state()).selected, p.selected);
  await page.getByRole("tab", { name: "诊断", exact: true }).click();
  await page.getByLabel("独立代理端口", { exact: true }).fill(String(a.port));
  await page.getByRole("button", { name: "应用端口并重启核心", exact: true }).click();
  await page.getByRole("region", { name: "设置", exact: true }).getByRole("alert").filter({ hasText: "Cage 代理端口已被占用，请更换独立端口" }).waitFor();
  assert.equal(a.proxy.listening, true);
  assert.equal((await state()).port, port, "port conflict replaced the saved profile");
  await page.getByRole("button", { name: "应用端口并重启核心", exact: true }).click();
  await page.waitForFunction(() => !document.querySelector(".proxy-progress"));
  assert.equal(await through(), "via-B");
  await page.getByRole("button", { name: "停止核心", exact: true }).click();
  await page.waitForFunction(() => !document.querySelector(".proxy-progress"));
  await assert.rejects(through());
  await page.screenshot({ path: path.join(output, "stopped.png") });
  let realHTTPS = false;
  if (process.env.CAGE_QA_REAL_CONFIG) {
    await page.getByRole("tab", { name: "订阅", exact: true }).click();
    await page.getByRole("button", { name: "添加来源", exact: true }).click();
    await page.getByRole("button", { name: "本地 YAML", exact: true }).click();
    await page.locator('input[type="file"]').setInputFiles(process.env.CAGE_QA_REAL_CONFIG);
    await page.getByLabel("来源名称", { exact: true }).fill("Real egress QA");
    await page.getByRole("button", { name: "导入并验证", exact: true }).click();
    await page.waitForFunction(() => !document.querySelector(".proxy-progress"));
    const importedJob = await page.evaluate(() => fetch("/api/status").then((r) => r.json()).then((s) => s.job));
    assert.equal(importedJob.error || "", "", "real node import rejected: " + (importedJob.error || ""));
    await page.getByText("Real egress QA", { exact: true }).waitFor();
    const imported = (await state()).sources.find((s) => s.name === "Real egress QA");
    await page.getByRole("tab", { name: "节点", exact: true }).click();
    await page.getByRole("button", { name: `选择节点 ${imported.nodes.at(-1).name}`, exact: true }).click();
  await page.getByRole("button", { name: "应用代理", exact: true }).click();
    await page.waitForFunction(() => !document.querySelector(".proxy-progress"));
    const stored = JSON.parse(await fs.readFile(path.join(profile, "proxy/sources.json")));
    const file=path.join(profile,"curl.conf");await fs.writeFile(file,`proxy = "socks5h://cage:${stored.auth}@127.0.0.1:${stored.port}"\n`,{mode:0o600});
    const trace = (await run("curl", ["--config",file,"--noproxy", "", "--fail", "--silent", "--show-error", "--max-time", "25", "https://1.1.1.1/cdn-cgi/trace"])).stdout;
    assert.match(trace, /^ip=/m); realHTTPS = true;
    console.log("QA: real HTTPS through imported node and bundled core verified (exit address withheld)");
  }
  assert.deepEqual(errors, []);
  await fs.writeFile(path.join(output, "result.json"), JSON.stringify({ passed: true, realCore: true, realWorkbench: true, browserErrors: errors, upstreamARequests: a.requests(), upstreamBRequests: b.requests(), realHTTPS, guestVMTested: false }, null, 2));
  console.log("Proxy UI and real bundled core: PASS; independent upstream switching, authentication, persistence and stop verified.");
} finally {
  if (browser) await browser.close();
  await run(executable, ["proxy-stop"], { env }).catch(() => {});
  server.kill("SIGTERM"); await log.close();
  for (const socket of sockets) socket.destroy();
  for (const item of [a, b]) { item.proxy.closeAllConnections(); item.target.closeAllConnections(); item.proxy.close(); item.target.close(); }
  // Keep only this test's private logs on failure for diagnosis. No credentials
  // or control URLs are copied into the repository report.
  console.log(`Private QA profile retained at ${profile}`);
}
