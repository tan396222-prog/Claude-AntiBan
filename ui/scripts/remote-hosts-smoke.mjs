// Real client service and browser interaction, using only a fresh temp profile.
// No cloud host or personal Cage configuration is accessed.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { mkdtemp, readFile, mkdir, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const executable = process.env.CAGE_CLIENT_BINARY;
if (!executable) throw new Error("Set CAGE_CLIENT_BINARY to the newly built client binary");
const profile = await mkdtemp(join(tmpdir(), "cage-remote-host-ui-"));
const artifacts = resolve(process.env.CAGE_REMOTE_QA_DIR || join(profile, "artifacts"));
await mkdir(artifacts, { recursive: true });
const service = spawn(executable, ["serve"], { env: { ...process.env, CLAUDE_CAGE_CONFIG: join(profile, "config.json") }, stdio: "ignore" });
let browser;
let page;
try {
  let control;
  for (let i = 0; i < 200; i++) {
    try { control = JSON.parse(await readFile(join(profile, "control.json"), "utf8")); break; }
    catch { if (service.exitCode !== null) throw new Error("Client service exited before publishing its private control record"); await new Promise((r) => setTimeout(r, 50)); }
  }
  assert.ok(control, "client service did not start");
  const origin = `http://localhost:${control.port}`;
  const launched = await fetch(`${origin}/api/launch`, { method: "POST", headers: { "Content-Type": "application/json", Origin: origin }, body: JSON.stringify({ token: control.token }) });
  assert.equal(launched.status, 200);
  const entry = await launched.json();
  browser = await chromium.launch({ headless: true, executablePath: process.env.CAGE_TEST_BROWSER_PATH });
  page = await browser.newPage({ viewport: { width: 1280, height: 850 } });
  const errors = [];
  page.on("pageerror", (error) => errors.push(error.message));
  await page.goto(entry.url);
  await page.getByRole("complementary", { name: "SSH 主机", exact: true }).waitFor();
  assert.equal(await page.getByRole("button", { name: "使用本机" }).count(), 0, "thin client must not offer local workloads");
  assert.equal(await page.getByRole("button", { name: "创建本地账户" }).count(), 0, "local account must not block remote setup");
  await page.getByRole("button", { name: "添加远程主机" }).click();
  await page.getByLabel("主机名称", { exact: true }).fill("隔离 UI 测试主机");
  await page.getByLabel("服务器地址", { exact: true }).fill("127.0.0.1");
  await page.getByLabel("SSH 端口", { exact: true }).fill("9");
  await page.getByLabel("SSH 用户名", { exact: true }).fill("cage-test");
  const auth = page.getByRole("combobox", { name: "SSH 登录方式" });
  const keyField = page.getByLabel("本机私钥文件", { exact: true });
  // The centered dialog itself moves when its height changes. Compare gaps
  // inside the dialog to check whether the list takes up layout space.
  const gap = async () => (await keyField.boundingBox()).y - (await auth.boundingBox()).y;
  const before = await gap();
  await auth.click();
  await page.getByRole("option", { name: "本机 SSH agent" }).waitFor();
  const expanded = await keyField.boundingBox();
  assert.ok(await gap() > before + 30, "inline choices must push subsequent fields down");
  const list = await page.getByRole("listbox").boundingBox();
  assert.ok(list.y + list.height <= expanded.y, "choices must not cover the next field");
  await auth.press("Escape");
  assert.equal(await auth.getAttribute("aria-expanded"), "false");
  await page.getByRole("listbox").waitFor({state:"hidden"});
  assert.ok(Math.abs(await gap() - before) < 1, "closing choices restores layout");
  await auth.press("ArrowDown");
  await auth.press("ArrowDown");
  await auth.press("Enter");
  assert.equal(await auth.getAttribute("aria-expanded"), "false");
  assert.equal(await keyField.count(), 0, "keyboard selection switches authentication method");
  await auth.click();
  await page.getByRole("option", { name: "本机密钥文件" }).click();
  await page.screenshot({ path: join(artifacts, "remote-host-form-desktop.png"), fullPage: true });
  await page.setViewportSize({ width: 375, height: 900 });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, "mobile form should not overflow horizontally");
  await page.screenshot({ path: join(artifacts, "remote-host-form-mobile.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 850 });
  await page.getByLabel("本机私钥文件", { exact: true }).fill(join(profile, "missing-test-key"));
  await page.getByRole("button", { name: "保存主机", exact: true }).click();
  await page.getByText("隔离 UI 测试主机", { exact: true }).waitFor();
  await page.reload();
  await page.getByText("隔离 UI 测试主机", { exact: true }).waitFor();
  const saved = JSON.parse(await readFile(join(profile, "remote-hosts.json"), "utf8"));
  assert.equal(saved.hosts.length, 1); assert.equal(saved.hosts[0].port, 9);
  await page.getByRole("button", { name: "连接此主机" }).click();
  await page.getByText("SSH 密钥不存在或文件权限不安全", { exact: true }).waitFor();
  assert.equal(await page.getByRole("complementary", { name: "SSH 主机", exact: true }).count(), 1, "failed remote connection must retain host management");
  assert.equal(await page.getByRole("button", { name: "创建本地账户" }).count(), 0, "failure must not fall back to a local account");
  await page.screenshot({ path: join(artifacts, "remote-host-error.png"), fullPage: true });
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await page.getByLabel("主机名称", { exact: true }).fill("修改后的测试主机");
  await page.getByRole("button", { name: "保存主机", exact: true }).click();
  await page.getByText("修改后的测试主机", { exact: true }).waitFor();
  await page.getByRole("button", { name: "移除连接", exact: true }).click();
  await page.getByRole("button", { name: "取消", exact: true }).click();
  assert.equal(await page.getByText("修改后的测试主机", { exact: true }).count(), 1);
  await page.getByRole("button", { name: "移除连接", exact: true }).click();
  await page.getByRole("button", { name: "确认移除", exact: true }).click();
  await page.getByText("还没有远程主机", { exact: true }).waitFor();
  assert.deepEqual(JSON.parse(await readFile(join(profile, "remote-hosts.json"), "utf8")).hosts, []);
  assert.deepEqual(errors, []);
  const result = { ok: true, scope: "真实本地 cage-client 服务和 Chromium；独立临时配置；连接失败路径", checks: ["thin-client-no-local-account", "host-crud-and-persistence", "inline-dropdown-layout", "keyboard-choice", "mobile-layout", "connection-error-no-local-fallback", "remove-cancel-and-confirm"], notTested: ["真实云端服务器连接与容器工作负载", "macOS、Windows、Linux 原生窗口交互"], pageErrors: errors };
  await writeFile(join(artifacts, "result.json"), JSON.stringify(result, null, 2));
  console.log(JSON.stringify({ ...result, artifacts }));
} catch (error) {
  if (page) await page.screenshot({ path: join(artifacts, "failure.png"), fullPage: true }).catch(() => {});
  console.error(`UI evidence: ${artifacts}`);
  throw error;
} finally {
  await browser?.close();
  service.kill("SIGTERM");
}
