import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { createRequire } from "node:module";
const run = promisify(execFile);
const cage = path.resolve(process.argv[2] || "_output/diagnostics-qa/cage-backend");
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const root = await fs.mkdtemp(path.join(os.tmpdir(), "cage-diagnostics-"));
const profile = path.join(root, "profile");
const env = { ...process.env, CLAUDE_CAGE_CONFIG: path.join(profile, "config.json") };
let browser;
const reportDirectory = process.env.CAGE_DIAGNOSTIC_REPORT
  ? path.resolve(process.env.CAGE_DIAGNOSTIC_REPORT)
  : await fs.mkdtemp(path.join(os.tmpdir(), "cage-diagnostics-report-"));
await fs.mkdir(reportDirectory, { recursive: true });
try {
  const { stdout } = await run(cage, ["desktop", "--json"], { env });
  const entry = JSON.parse(stdout);
  assert.equal(entry.status, "ready");
  browser = await chromium.launch({ headless: true });
  const page = await browser.newPage({ acceptDownloads: true, viewport: { width: 1280, height: 900 } });
  await page.goto(entry.url);
  await page.getByRole("heading", { name: "注册 Cage 账户", exact: true }).waitFor();
  await page.getByLabel("用户名", { exact: true }).fill("diagnostics_qa");
  await page.getByLabel("设置密码", { exact: true }).fill("Test-only-password-8712");
  await page.getByLabel("确认密码", { exact: true }).fill("Test-only-password-8712");
  await page.getByRole("button", { name: "创建本地账户", exact: true }).click();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("button", { name: "环境与恢复", exact: true }).click();
  await page.getByRole("heading", { name: "日志与诊断" }).waitFor();
  // Exercise browser event handlers, the actual authenticated endpoint and disk.
  const reports = Promise.all(["diagnostic-ui-sentinel", "diagnostic-rejection-sentinel"].map(marker =>
    page.waitForResponse(response => response.url().endsWith("/api/diagnostics/events") && response.request().postData()?.includes(marker) && response.ok())));
  await page.evaluate(() => {
    window.dispatchEvent(new ErrorEvent("error", { error: new Error("password=ui-private-secret diagnostic-ui-sentinel") }));
    window.dispatchEvent(new PromiseRejectionEvent("unhandledrejection", { promise: Promise.resolve(), reason: new Error("token=rejection-secret diagnostic-rejection-sentinel") }));
  });
  await reports;
  const downloaded = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出诊断包", exact: true }).click();
  await (await downloaded).saveAs(path.join(root, "browser.zip"));
  await page.getByRole("status").filter({ hasText: "诊断包已下载" }).waitFor();
  await page.screenshot({ path: path.join(reportDirectory, "settings.png") });
  // Native hosts use the same CLI command after their save picker completes.
  const nativeOutput = path.join(root, "native.zip");
  const saved = await run(cage, ["diagnostics", "--json", nativeOutput], { env });
  assert.equal(JSON.parse(saved.stdout).status, "ok");
  const selected = await page.evaluate(async () => (await fetch("/api/client/context")).json());
  const selectedOutput = path.join(root, "selected-native.zip");
  const selectedSave = await run(cage, ["diagnostics", "--json", "--connection", selected.connection_id, selectedOutput], { env });
  assert.equal(JSON.parse(selectedSave.stdout).status, "ok");
  // Stop only this isolated controller, then check the offline ZIP as well.
  await run(cage, ["prepare-update", "--json"], { env });
  const offline = await run(cage, ["diagnostics", "--json", path.join(root, "offline.zip")], { env });
  assert.equal(JSON.parse(offline.stdout).status, "ok");
  const checks = [];
  for (const filename of ["browser.zip", "native.zip", "selected-native.zip", "offline.zip"]) {
    const { stdout: contents } = await run("uv", ["run", "--no-project", "python", "-c", "import sys,zipfile,json; z=zipfile.ZipFile(sys.argv[1]); print(json.dumps({'names':z.namelist(),'text':'\\n'.join(z.read(n).decode() for n in z.namelist())}))", path.join(root, filename)]);
    const parsed = JSON.parse(contents);
    assert.match(parsed.text, /diagnostic-ui-sentinel/);
    assert.match(parsed.text, /diagnostic-rejection-sentinel/);
    for (const secret of ["ui-private-secret", "rejection-secret", "Test-only-password-8712"]) assert.ok(!parsed.text.includes(secret), `secret in ${filename}`);
    assert.ok(parsed.names.every(name => ["manifest.json", "README.txt", "desktop/manifest.json", "desktop/README.txt"].includes(name) || name.startsWith("logs/") || name.startsWith("desktop/logs/")));
    assert.ok(parsed.names.some(name => name.startsWith("desktop/logs/")), `desktop log owner missing from ${filename}`);
    checks.push(`${filename}: valid redacted ZIP`);
  }
  checks.push("real browser settings export", "frontend error and rejection captured", "offline CLI export", "no user files/config/terminal content");
  await fs.writeFile(path.join(reportDirectory, "browser-result.json"), JSON.stringify({ success: true, checks }, null, 2));
  console.log(JSON.stringify({ success: true, reportDirectory, checks }));
} finally {
  await browser?.close();
  await run(cage, ["prepare-update", "--json"], { env }).catch(() => {});
  await fs.rm(root, { recursive: true, force: true });
}
