// Production UI + real local controller. Uses only a freshly created profile;
// never opens or starts a VM, and stores OS trust under its temporary HOME.
import assert from "node:assert/strict";
import { randomBytes } from "node:crypto";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";

const cage = process.argv[2];
assert.ok(path.isAbsolute(cage || ""), "Supply an absolute Cage helper path");
const run = promisify(execFile);
const root = await fs.mkdtemp(path.join(os.tmpdir(), "cage-remember-live-"));
const env = { ...process.env, HOME: root, LOCALAPPDATA: root, CLAUDE_CAGE_CONFIG: path.join(root, "profile", "config.json") };
const output = new URL("../../docs/test-reports/2026-11-08-remember-device/", import.meta.url);
await fs.mkdir(output, { recursive: true });
const require = createRequire(import.meta.url);
const { chromium, webkit } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const engine = process.env.CAGE_TEST_BROWSER === "webkit" ? webkit : chromium;
const browser = await engine.launch({ headless: true });
let context, page;
const errors = [];
const command = async (...args) => JSON.parse((await run(cage, args, { env, timeout: 60000 })).stdout);
const account = () => page.evaluate(async () => {
  const context = await (await fetch("/api/client/context")).json();
  return (await fetch("/api/account", { headers: { "X-Cage-Connection": context.connection_id } })).json();
});
const menu = () => page.getByRole("button", { name: /^账户菜单，/ });
const remember = () => page.getByRole("checkbox", { name: /^记住我/ });
const password = randomBytes(24).toString("base64url");
const restart = async () => {
  await context?.close();
  await command("prepare-update", "--json");
  const entry = await command("desktop", "--json");
  context = await browser.newContext();
  page = await context.newPage();
  page.on("pageerror", e => errors.push(e.message));
  await page.goto(entry.url);
};
const unlock = async (enabled) => {
  await remember().setChecked(enabled);
  await page.getByLabel("账户密码", { exact: true }).fill(password);
  const response = page.waitForResponse(r => r.url().endsWith("/api/client/trusted-login"));
  await page.getByRole("button", { name: "解锁 Cage", exact: true }).click();
  assert.equal((await response).status(), 200);
  await menu().waitFor();
};
try {
  await restart();
  await page.getByRole("heading", { name: "注册 Cage 账户", exact: true }).waitFor();
  await remember().waitFor();
  assert.equal(await remember().isChecked(), true);
  await remember().uncheck();
  await page.getByLabel("用户名", { exact: true }).fill("remember_live_qa");
  await page.getByLabel("设置密码", { exact: true }).fill(password);
  await page.getByLabel("确认密码", { exact: true }).fill(password);
  const created = page.waitForResponse(r => r.url().endsWith("/api/client/trusted-login"));
  await page.getByRole("button", { name: "创建本地账户", exact: true }).click();
  assert.equal((await created).status(), 200);
  await menu().waitFor();
  assert.equal(Boolean((await account()).trusted_login), false);

  await restart();
  await remember().waitFor();
  assert.equal(await remember().isChecked(), false);
  await unlock(true);
  assert.equal((await account()).trusted_login, true);
  const record = JSON.parse(await fs.readFile(path.join(root, "profile", "account.json"), "utf8"));
  assert.equal(record.devices?.automatic, undefined);
  assert.equal(record.trusted_unlock.sealed_key.length > 0, true);

  await restart();
  await menu().waitFor();
  assert.equal((await account()).unlocked, true);
  await menu().click();
  await page.getByRole("menuitem", { name: /锁定 Cage/ }).click();
  await page.getByRole("region", { name: "解锁 Cage", exact: true }).waitFor();
  await restart();
  await remember().waitFor();
  assert.equal((await account()).unlocked, false);
  assert.equal((await account()).require_authentication, true);
  await page.screenshot({ path: fileURLToPath(new URL(`live-${engine === webkit ? "webkit" : "chromium"}-locked.png`, output)) });
  await unlock(false);
  assert.equal(Boolean((await account()).trusted_login), false);
  await restart();
  await remember().waitFor();
  assert.equal(await remember().isChecked(), false);
  assert.equal((await account()).unlocked, false);
  await unlock(true);
  await menu().click();
  await page.getByRole("menuitem", { name: "退出账户", exact: true }).click();
  await page.getByRole("dialog").getByRole("button", { name: "停止环境并退出账户", exact: true }).click();
  await page.getByRole("region", { name: "解锁 Cage", exact: true }).waitFor();
  assert.equal(Boolean((await account()).trusted_login), false);
  const final = JSON.parse(await fs.readFile(path.join(root, "profile", "account.json"), "utf8"));
  assert.equal(final.sealed_key, record.sealed_key, "workspace password-wrapped key changed");
  assert.deepEqual(errors, []);
  console.log("PASS: real production UI/controller, isolated OS trust, create opt-out, password opt-in, controller restart, remembered login, persistent manual lock, opt-out restart, logout revocation and unchanged workspace key");
} finally {
  await browser.close();
  await command("prepare-update", "--json");
  await fs.rm(root, { recursive: true, force: true });
}
