// Exercise the manual macOS installation path in a disposable user directory.
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import fs from "node:fs/promises";
import { createRequire } from "node:module";
import os from "node:os";
import path from "node:path";
import { seed, verify } from "./personal-data-fixture.mjs";

const source = process.argv[2];
if (process.platform !== "darwin" || !source || !path.isAbsolute(source) || !source.endsWith(".app")) {
  throw new Error("Usage on macOS: node scripts/test-macos-install.mjs /absolute/path/to/Cage.app");
}
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const root = await fs.mkdtemp(path.join(os.tmpdir(), "cage-macos-install-"));
const qaHome = path.join(root, "user");
const manifest = path.join(root, "personal-data.json");
// The 12 preservation fixtures contain marker bytes. Use a separate valid profile for login.
const profile = path.join(root, "runtime-profile");
const env = { ...process.env, HOME: qaHome, CLAUDE_CAGE_CONFIG: path.join(profile, "config.json") };
const installed = path.join(qaHome, "Applications", "Cage.app");
const mount = path.join(root, "mount");
const image = path.join(root, "manual-install.dmg");
const stage = path.join(root, "volume");
const run = (exe, args, options = {}) => execFileSync(exe, args, { encoding: "utf8", timeout: 180000, ...options });
const cli = (...args) => run(path.join(installed, "Contents/Resources/cage"), args, { env });
const hash = async file => createHash("sha256").update(await fs.readFile(file)).digest("hex");
const password = randomUUID() + "7z";
const username = "install_smoke";
const evidence = process.env.CAGE_REMOTE_QA_DIR;
let browser, mounted = false, running = false;

try {
  await seed(qaHome, manifest);
  await fs.mkdir(path.dirname(installed), { recursive: true });
  await fs.mkdir(stage);
  await fs.mkdir(mount);
  run("ditto", [source, path.join(stage, "Cage.app")]);
  run("hdiutil", ["create", "-quiet", "-volname", "Cage-Isolated-QA", "-srcfolder", stage, "-format", "UDZO", image]);
  run("hdiutil", ["attach", "-nobrowse", "-readonly", "-mountpoint", mount, image]);
  mounted = true;
  const result = {
    dmg_sha256: await hash(image),
    backend_sha256: await hash(path.join(source, "Contents/Resources/cage")),
    launcher_sha256: await hash(path.join(source, "Contents/MacOS/Cage")),
  };

  if (evidence) await fs.mkdir(evidence, { recursive: true });
  browser = await chromium.launch({ headless: true, executablePath: process.env.CAGE_TEST_BROWSER_PATH });
  let savedAccountHash;
  for (const phase of ["install", "reinstall"]) {
    if (phase === "reinstall") await fs.rm(installed, { recursive: true });
    run("ditto", [path.join(mount, "Cage.app"), installed]);
    if (savedAccountHash) assert.equal(await hash(path.join(profile, "account.json")), savedAccountHash, "reinstall changed the account file");
    run("codesign", ["--verify", "--deep", "--strict", installed]);
    assert.equal(await hash(path.join(installed, "Contents/Resources/cage")), result.backend_sha256);
    cli("verify-web-assets");
    if (process.arch === "arm64") cli("verify-macos-guest");
    running = true;
    const entry = JSON.parse(cli("desktop", "--json"));
    assert.equal(entry.status, "ready");
    const page = await browser.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    try {
      await page.goto(entry.url);
      if (phase === "install") {
        await page.getByRole("heading", { name: "注册 Cage 账户", exact: true }).waitFor();
        await page.getByRole("checkbox", { name: /^记住我/ }).uncheck();
        await page.getByLabel("用户名", { exact: true }).fill(username);
        await page.getByLabel("设置密码", { exact: true }).fill(password);
        await page.getByLabel("确认密码", { exact: true }).fill(password);
        await page.getByRole("button", { name: "创建本地账户", exact: true }).click();
      } else {
        await page.getByLabel("账户密码", { exact: true }).fill(password);
        await page.getByRole("button", { name: "解锁 Cage", exact: true }).click();
      }
      await page.getByRole("heading", { name: "为 Cage 准备独立环境", exact: true }).waitFor();
      assert.equal(await page.getByRole("navigation", { name: "主导航" }).count(), 0, "workbench must remain hidden before initialization");
      if (evidence) await page.screenshot({ animations: "disabled", path: path.join(evidence, `${phase}-initialization.png`) });
      await page.getByRole("button", { name: "下一步：配置代理", exact: true }).click();
      await page.getByRole("heading", { name: "连接你的网络代理", exact: true }).waitFor();
      assert.ok(await page.getByRole("button", { name: "开始安装", exact: true }).isDisabled(), "a fresh profile must require a proxy before installing");
      // Do not start a VM or configure real network credentials in package-preservation QA.
      await page.getByRole("button", { name: "上一步", exact: true }).click();
      await page.getByRole("heading", { name: "为 Cage 准备独立环境", exact: true }).waitFor();
      const account = await page.evaluate(async () => {
        const context = await (await fetch("/api/client/context")).json();
        return (await fetch("/api/account", { headers: { "X-Cage-Connection": context.connection_id } })).json();
      });
      assert.equal(account.username, username);
      assert.equal(account.unlocked, true);
      const status = await page.evaluate(async () => (await fetch("/api/client/local-status")).json());
      assert.ok(status.known && status.service_running && !status.running, "installation QA must leave the VM stopped");
      assert.deepEqual(errors, []);
      result[phase] = { original_account: true, initialization_wizard: true, proxy_required: true, vm_stopped: true, preserved: await verify(qaHome, manifest) };
    } finally {
      await page.close();
    }
    cli("prepare-update", "--json");
    running = false;
    savedAccountHash = await hash(path.join(profile, "account.json"));
  }
  const accountHash = await hash(path.join(profile, "account.json"));
  await fs.rm(installed, { recursive: true });
  assert.equal(await hash(path.join(profile, "account.json")), accountHash);
  result.uninstall = { account_preserved: true, preserved: await verify(qaHome, manifest) };
  if (evidence) await fs.writeFile(path.join(evidence, "result.json"), JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result, null, 2));
} finally {
  try {
    if (running) cli("prepare-update", "--json");
  } finally {
    try {
      if (browser) await browser.close();
    } finally {
      if (mounted) run("hdiutil", ["detach", mount]);
      await fs.rm(root, { recursive: true });
    }
  }
}
