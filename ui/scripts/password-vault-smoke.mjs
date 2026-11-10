import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";
const output = process.env.CAGE_VAULT_CAPTURE || fileURLToPath(new URL("../../docs/test-reports/2026-11-05-password-vault/", import.meta.url));
await fs.mkdir(output, { recursive: true });
let clipboard = "";
const { browser, page, state, errors } = await createWorkbench({ nativeRequest: async ({ action, text }) => {
  if (action === "clipboardWrite") { clipboard = text; return true; }
  return null;
} });
let entries = [], blocked = [], candidate = null, filled = null, sequence = 0;
await page.route("**/api/vault**", async route => {
  const req = route.request(), input = req.method() === "POST" ? req.postDataJSON() : {};
  if (!state.account.unlocked) return route.fulfill({ status: 423, body: "Cage 已锁定" });
  const redacted = value => { const { password, ...rest } = value; return rest; };
  if (new URL(req.url()).pathname === "/api/vault/browser") {
    if (input.action === "status") return route.fulfill({ json: {
      available: true, origin: "https://mail.example.test", document: "fixture-document",
      entries: entries.filter(e => e.origin === "https://mail.example.test").map(redacted),
      candidates: candidate ? [{ ...redacted(candidate), update: entries.some(e => e.origin === candidate.origin && e.username === candidate.username) }] : [],
    } });
    if (input.action === "fill") { assert.equal(input.document, "fixture-document"); filled = input.id; }
    if (input.action === "save") {
      const existing = entries.find(e => e.origin === candidate.origin && e.username === candidate.username);
      if (existing) existing.password = candidate.password;
      else entries.push({ ...candidate, id: `saved-${++sequence}` });
      candidate = null;
    }
    if (input.action === "never") { blocked.push(candidate.origin); candidate = null; }
    if (input.action === "dismiss") candidate = null;
    return route.fulfill({ json: { ok: true } });
  }
  if (req.method() === "GET") return route.fulfill({ json: { entries: entries.map(redacted), blocked } });
  if (input.action === "save") {
    const entry = input.entry;
    const existing = entries.find(e => e.id === entry.id);
    if (existing) Object.assign(existing, entry, { password: entry.password || existing.password });
    else entries.push({ ...entry, id: `saved-${++sequence}` });
  }
  if (input.action === "reveal") {
    if (input.account_password !== state.accountPassword) return route.fulfill({ status: 400, body: "密码不正确" });
    return route.fulfill({ json: { password: entries.find(e => e.id === input.id).password } });
  }
  if (input.action === "delete") entries = entries.filter(e => e.id !== input.id);
  if (input.action === "unblock") blocked = blocked.filter(e => e !== input.origin);
  return route.fulfill({ json: { ok: true } });
});
const settings = async () => {
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("navigation", { name: "设置分组" }).getByRole("button", { name: "密码保险箱", exact: true }).click();
  await page.getByRole("heading", { name: "密码保险箱", exact: true }).waitFor();
};
try {
  await settings();
  await page.getByText("还没有保存密码", { exact: true }).waitFor();
  await page.getByRole("button", { name: "添加密码", exact: true }).click();
  await page.getByLabel("网站地址", { exact: true }).fill("https://mail.example.test");
  await page.getByLabel("账号或邮箱").fill("alice@example.test");
  await page.getByLabel("网站密码", { exact: true }).fill("fixture-mail-secret");
  await page.getByRole("button", { name: "保存记录", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "密码记录已保存" }).waitFor();
  assert.equal(entries.length, 1);
  assert.equal(await page.getByText("fixture-mail-secret", { exact: true }).count(), 0);
  await page.getByRole("button", { name: "查看密码", exact: true }).click();
  await page.getByLabel("Cage 账户密码", { exact: true }).fill("wrong-password");
  await page.getByRole("button", { name: "验证并查看" }).click();
  await page.getByRole("alert").filter({ hasText: "密码不正确" }).waitFor();
  assert.equal(await page.getByLabel("已保存的网站密码").count(), 0);
  await page.getByLabel("Cage 账户密码", { exact: true }).fill(state.accountPassword);
  await page.getByRole("button", { name: "验证并查看" }).click();
  await page.getByLabel("已保存的网站密码").waitFor();
  assert.equal(await page.getByLabel("已保存的网站密码").textContent(), "fixture-mail-secret");
  await page.getByRole("button", { name: "隐藏密码" }).click();
  await page.getByRole("button", { name: "复制密码", exact: true }).click();
  await page.getByLabel("Cage 账户密码", { exact: true }).fill(state.accountPassword);
  await page.getByRole("button", { name: "验证并复制" }).click();
  await page.getByRole("status").filter({ hasText: "密码已复制" }).waitFor();
  assert.equal(clipboard, "fixture-mail-secret");
  await page.getByRole("button", { name: "编辑", exact: true }).click();
  await page.getByLabel("账号或邮箱").fill("work@example.test");
  await page.getByRole("button", { name: "保存记录", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "密码记录已保存" }).waitFor();
  assert.equal(entries[0].password, "fixture-mail-secret");
  await page.getByLabel("搜索网站或账号").fill("missing");
  await page.getByText("没有找到匹配的账号").waitFor();
  await page.getByLabel("搜索网站或账号").fill("");
  await page.screenshot({ path: output + "vault-settings.png" });
  await page.setViewportSize({ width: 820, height: 558 });
  await page.screenshot({ path: output + "vault-settings-small.png" });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true);
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  state.status.ready = true; state.status.vm_running = true;
  state.tabs = [{ id: "web-vault", title: "邮箱", kind: "web", url: "https://mail.example.test/login" }]; state.active = "web-vault";
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.getByRole("button", { name: "浏览器菜单", exact: true }).click();
  await page.getByRole("menuitem", { name: /密码 · 1 个账号/ }).waitFor();
  const trigger = page.getByRole("menuitem", { name: /密码 · 1 个账号/ });
  const before = await page.locator(".browser-chrome").boundingBox();
  await trigger.press("Enter");
  const picker = page.getByRole("region", { name: "当前网站的密码" });
  await picker.waitFor();
  const after = await page.locator(".browser-chrome").boundingBox();
  assert.ok(after.height > before.height + 60, "password picker did not occupy layout space");
  await picker.getByRole("button", { name: /work@example.test/ }).focus();
  await page.keyboard.press("Enter");
  await page.getByRole("status").filter({ hasText: "已填写" }).waitFor();
  assert.equal(filled, entries[0].id);
  const filledHeight = (await page.locator(".browser-chrome").boundingBox()).height;
  const pickerHeight = (await picker.boundingBox()).height;
  await trigger.press("Escape");
  await picker.waitFor({ state: "hidden" });
  assert.equal(Math.round((await page.locator(".browser-chrome").boundingBox()).height), Math.round(filledHeight - pickerHeight),
    "closing the password picker releases its space while preserving the fill confirmation");
  candidate = { id: "pending", tab: "web-vault", origin: "https://mail.example.test", username: "work@example.test", password: "new-fixture-secret" };
  await page.getByText("更新此账号的密码？", { exact: true }).waitFor();
  await page.screenshot({ path: output + "vault-save-prompt.png" });
  await page.getByRole("button", { name: "更新密码", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "密码已保存" }).waitFor();
  assert.equal(entries[0].password, "new-fixture-secret");
  candidate = { id: "ignored", tab: "web-vault", origin: "https://mail.example.test", username: "other", password: "ignored" };
  await page.getByRole("button", { name: "此网站不再提示", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "不再提示保存" }).waitFor();
  await settings();
  await page.getByRole("button", { name: "恢复提示", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "已恢复" }).waitFor();
  assert.deepEqual(blocked, []);
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await page.getByRole("button", { name: "删除密码", exact: true }).click();
  await page.getByText("还没有保存密码", { exact: true }).waitFor();
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  await page.getByRole("button", { name: /^账户菜单，/ }).click();
  await page.getByRole("menuitem", { name: /锁定 Cage/ }).click();
  await page.getByRole("region", { name: "解锁 Cage", exact: true }).waitFor();
  assert.equal(await page.locator(".vault-browser").count(), 0);
  assert.deepEqual(errors, []);
  console.log("PASS: vault UI CRUD, search, reauthentication, clipboard, inline keyboard picker, save/update/never, lock and small viewport");
} finally { await browser.close(); }
