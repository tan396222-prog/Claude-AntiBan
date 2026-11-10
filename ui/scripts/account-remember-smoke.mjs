import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = new URL("../../docs/test-reports/2026-11-08-remember-device/", import.meta.url);
await fs.mkdir(output, { recursive: true });

for (const platform of ["windows", "darwin", "linux"]) {
  let fixture, failTrust = false;
  const actions = [];
  const clientContext = { mode: "local", desktop_gateway: true, connection_id: "local", client_platform: platform, server_platform: platform, connection: { state: "connected" } };
  fixture = await createWorkbench({
    clientContext,
    // Automatic trust must work without the macOS-only Keychain bridge.
    nativeRequest: async ({ action }) => action === "accountDevice" ? { supported: false } : null,
    clientRequest: async (route, path, body) => {
      if (path === "/client/context") return route.fulfill({ json: clientContext });
      if (path !== "/client/trusted-login") return route.fulfill({ json: {} });
      const a = fixture.state.account;
      actions.push(body.action);
      if (failTrust) return route.fulfill({ status: 409, contentType: "text/plain; charset=utf-8", body: "测试：无法保存本机信任凭据" });
      if (body.action === "enable") {
        assert.equal(body.password, fixture.state.accountPassword);
        a.trusted_login = true;
        delete (a.devices ||= {}).automatic;
      } else if (body.action === "disable") {
        a.trusted_login = false;
        delete (a.devices ||= {}).automatic;
      } else if (body.action === "resume") {
        assert.equal(a.require_authentication, false, "automatic login bypassed manual lock");
        assert.equal(a.trusted_login, true);
        a.unlocked = true;
      } else throw Error(`Unexpected trust action: ${body.action}`);
      return route.fulfill({ json: { ok: true } });
    },
  });
  const { browser, page, state, errors } = fixture;
  const workbenchURL = page.url();
  const reopenLocked = async () => {
    await page.goto("about:blank");
    state.account.unlocked = false;
    await page.goto(workbenchURL);
  };
  const menu = () => page.getByRole("button", { name: /^账户菜单，/ });
  const remember = () => page.getByRole("checkbox", { name: /^记住我/ });
  const trustReply = () => page.waitForResponse(response => response.url().endsWith("/api/client/trusted-login"));
  const login = async (password = "fixture-password") => {
    await page.getByLabel("账户密码", { exact: true }).fill(password);
    const saved = password === "fixture-password" ? trustReply() : null;
    await page.getByRole("button", { name: "解锁 Cage", exact: true }).click();
    if (saved) await (await saved).finished();
  };
  const settings = async () => {
    await menu().click();
    await page.getByRole("menuitem", { name: "设置", exact: true }).click();
  };
  try {
    Object.assign(state.account, { unlocked: false, require_authentication: false });
    await page.reload();
    await remember().waitFor();
    assert.equal(await remember().isChecked(), false, "existing account was opted in without consent");
    await remember().check();
    await login("incorrect-password");
    await page.getByRole("alert").filter({ hasText: "密码不正确" }).waitFor();
    assert.deepEqual(actions, []);
    await page.screenshot({ path: fileURLToPath(new URL(`${platform}-login.png`, output)) });
    await login();
    assert.equal(state.account.trusted_login, true);
    await menu().waitFor();
    await reopenLocked();
    await menu().waitFor();
    assert.equal(actions.filter(a => a === "resume").length, 1);

    await menu().click();
    await page.getByRole("menuitem", { name: /锁定 Cage/ }).click();
    await page.getByRole("region", { name: "解锁 Cage", exact: true }).waitFor();
    await page.reload();
    await remember().waitFor();
    assert.equal(await remember().isChecked(), true);
    assert.equal(actions.filter(a => a === "resume").length, 1);
    await remember().uncheck();
    assert.equal(await remember().isChecked(), false);
    await login();
    assert.equal(state.account.trusted_login, false);
    await reopenLocked();
    await remember().waitFor();
    assert.equal(await remember().isChecked(), false);
    assert.equal(actions.filter(a => a === "resume").length, 1);
    await login();
    await settings();
    await page.getByLabel("验证账户密码", { exact: true }).fill("fixture-password");
    await page.getByRole("button", { name: "信任此电脑", exact: true }).click();
    await page.getByRole("button", { name: "关闭记住我", exact: true }).waitFor();
    await page.screenshot({ path: fileURLToPath(new URL(`${platform}-settings.png`, output)) });
    await page.getByRole("button", { name: "关闭记住我", exact: true }).click();
    await page.getByLabel("验证账户密码", { exact: true }).waitFor();

    // Existing macOS grants share the same opt-out control.
    state.account.devices = { automatic: "legacy-keychain", biometric: "touch-id" };
    await page.reload();
    await settings();
    await page.getByRole("button", { name: "关闭记住我", exact: true }).click();
    await page.getByLabel("验证账户密码", { exact: true }).waitFor();
    assert.deepEqual(state.account.devices, { biometric: "touch-id" });

    // A trust-storage failure must not undo a successful password login.
    await reopenLocked();
    await remember().check();
    failTrust = true;
    await login();
    await menu().waitFor();
    await settings();
    await page.getByText(/已登录，记住我未能开启/).waitFor();
    assert.equal(Boolean(state.account.trusted_login), false);
    failTrust = false;

    Object.assign(state.account, { exists: false, unlocked: false, username: "", devices: {}, require_authentication: false });
    await page.reload();
    await page.getByRole("heading", { name: "注册 Cage 账户", exact: true }).waitFor();
    await remember().waitFor();
    assert.equal(await remember().isChecked(), true);
    await page.getByLabel("用户名", { exact: true }).fill("测试工作区");
    await page.getByLabel("设置密码", { exact: true }).fill("fixture-password");
    await page.getByLabel("确认密码", { exact: true }).fill("fixture-password");
    const createdTrust = trustReply();
    await page.getByRole("button", { name: "创建本地账户", exact: true }).click();
    await (await createdTrust).finished();
    assert.equal(state.account.trusted_login, true);
    await menu().click();
    await page.getByRole("menuitem", { name: "退出账户", exact: true }).click();
    await page.getByRole("dialog").getByRole("button", { name: "停止环境并退出账户", exact: true }).click();
    // The real backend revokes the grant after a successful environment stop.
    state.account.trusted_login = false;
    await page.reload();
    await remember().waitFor();
    assert.equal(await remember().isChecked(), false);
    assert.deepEqual(errors, []);
    console.log(`PASS ${platform}: create, password login, remember, reopen, manual lock, opt-out, legacy migration, settings, storage failure and logout`);
  } catch (error) {
    await page.screenshot({ path: fileURLToPath(new URL(`${platform}-failure.png`, output)) });
    console.error(await page.locator("body").innerText());
    console.error({ actions, account: state.account });
    throw error;
  } finally { await browser.close(); }
}

const remote = await createWorkbench({
  clientContext: { mode: "remote", desktop_gateway: true, connection_id: "remote", client_platform: "windows", server_platform: "linux", connection: { state: "connected" } },
  nativeRequest: async ({ action }) => action === "accountDevice" ? { supported: false } : null,
});
try {
  remote.state.account.unlocked = false;
  await remote.page.reload();
  await remote.page.getByLabel("账户密码", { exact: true }).waitFor();
  assert.equal(await remote.page.getByRole("checkbox", { name: /^记住我/ }).count(), 0);
  assert.deepEqual(remote.errors, []);
  console.log("PASS remote: local computer trust is unavailable for a remote host");
} finally { await remote.browser.close(); }
