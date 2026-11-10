// Frontend interaction checks with synthetic files and isolated API fixtures.
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import path from "node:path";
import { createWorkbench } from "./workbench-fixture.mjs";

const engine = process.env.CAGE_TEST_BROWSER || "chromium";
const output = path.resolve(process.env.CAGE_YAML_QA_OUTPUT || "/tmp/cage-yaml-drop-qa", engine);
await fs.mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench({ viewport: { width: 1280, height: 1000 } });
const yaml = "proxies:\n  - {name: 本地测试, type: socks5, server: 127.0.0.1, port: 1080}\n";
const proxy = { active: false, sources: [], port: 17896, core_available: true };
const imports = [];
try {
  await page.route("**/api/proxy", (route) => route.fulfill({ json: proxy }));
  await page.route("**/api/proxy/import", (route) => {
    const body = route.request().postDataJSON();
    imports.push(body);
    const job = { id: "yaml-drop-import", running: false, message: "已导入" };
    state.status.job = job;
    proxy.sources.push({ id: "drop-source", name: body.name, nodes: [{ id: "drop-node", name: "本地测试", type: "socks5" }], remote: false, updated_at: new Date().toISOString() });
    return route.fulfill({ json: job });
  });
  const open = async () => {
    await page.getByRole("button", { name: /^设置/ }).click();
    await page.getByRole("button", { name: "网络与代理", exact: true }).click();
    await page.getByRole("tab", { name: "订阅", exact: true }).click();
    await page.getByRole("button", { name: "添加来源", exact: true }).click();
    await page.getByRole("button", { name: "本地 YAML", exact: true }).click();
  };
  await open();
  const region = page.locator(".proxy-yaml-input");
  const dropzone = page.locator(".proxy-yaml-dropzone");
  const editor = page.getByRole("textbox", { name: "节点 YAML", exact: true });
  const name = page.getByLabel("来源名称", { exact: true });
  const submit = page.getByRole("button", { name: "导入并验证", exact: true });
  const expectText = async (locator, value) => {
    await locator.filter({ hasText: value }).waitFor();
  };
  const dataTransfer = async (files) => page.evaluateHandle((entries) => {
    const transfer = new DataTransfer();
    for (const entry of entries) transfer.items.add(new File([entry.content || ""], entry.name, { type: entry.type || "application/yaml" }));
    return transfer;
  }, files);
  const drop = async (files, target = dropzone) => {
    const transfer = await dataTransfer(files);
    await target.dispatchEvent("dragenter", { dataTransfer: transfer });
    await target.dispatchEvent("dragover", { dataTransfer: transfer });
    await target.dispatchEvent("drop", { dataTransfer: transfer });
    await transfer.dispose();
  };
  const payload = (filename = "日常代理.yaml", content = yaml) => ({ name: filename, mimeType: "application/yaml", buffer: Buffer.from(content) });
  const fileInput = region.locator('input[type="file"]');
  assert.equal(await submit.isDisabled(), true);
  await dropzone.scrollIntoViewIfNeeded();
  await page.screenshot({ path: path.join(output, "empty.png"), fullPage: true });

  // Keyboard activation reaches the actual file input, including same-file reselection.
  await dropzone.focus();
  const firstChooser = page.waitForEvent("filechooser");
  await dropzone.press("Enter");
  await (await firstChooser).setFiles(payload());
  await expectText(region.getByRole("status"), "文件已读取");
  assert.equal(await editor.inputValue(), yaml);
  assert.equal(await name.inputValue(), "日常代理");
  assert.equal(await submit.isEnabled(), true);
  await editor.fill(yaml + "# 手动编辑\n");
  await expectText(region.getByRole("status"), "内容已编辑");
  await dropzone.focus();
  const secondChooser = page.waitForEvent("filechooser");
  await dropzone.press("Space");
  await (await secondChooser).setFiles(payload());
  await page.waitForFunction((expected) => document.querySelector('[aria-label="节点 YAML"]').value === expected, yaml);
  await name.fill("我的来源");

  // Nested drag events must keep the highlight until the pointer leaves the region.
  const transfer = await dataTransfer([{ name: "替换.YML", content: yaml + "# 拖入\n" }]);
  await dropzone.dispatchEvent("dragenter", { dataTransfer: transfer });
  await dropzone.locator("strong").dispatchEvent("dragenter", { dataTransfer: transfer });
  await dropzone.dispatchEvent("dragleave", { dataTransfer: transfer });
  assert.match(await region.getAttribute("class"), /is-dragging/);
  await expectText(dropzone, "松开以读取配置文件");
  await page.screenshot({ path: path.join(output, "dragging.png"), fullPage: true });
  await editor.dispatchEvent("drop", { dataTransfer: transfer });
  await transfer.dispose();
  await expectText(dropzone, "替换.YML");
  assert.equal(await editor.inputValue(), yaml + "# 拖入\n");
  assert.equal(await name.inputValue(), "我的来源", "replacement preserves the explicit source name");
  assert.doesNotMatch(await region.getAttribute("class"), /is-dragging/);

  const preserved = await editor.inputValue();
  for (const [files, message] of [
    [[{ name: "image.png", content: "image" }], ".yaml、.yml 或 .json"],
    [[{ name: "a.yaml", content: yaml }, { name: "b.yml", content: yaml }], "一次只能导入一个"],
    [[{ name: "empty.yaml", content: "  " }], "文件内容为空"],
  ]) {
    await drop(files);
    await expectText(region.getByRole("alert"), message);
    assert.equal(await editor.inputValue(), preserved);
  }
  await fileInput.setInputFiles(payload("large.yaml", "x".repeat(8 * 1024 * 1024 + 1)));
  await expectText(region.getByRole("alert"), "文件超过 8 MiB");
  assert.equal(await editor.inputValue(), preserved);
  await page.screenshot({ path: path.join(output, "error.png"), fullPage: true });
  await page.evaluate(() => {
    window.originalFileText = File.prototype.text;
    File.prototype.text = function () { return Promise.reject(new Error("Synthetic read failure")); };
  });
  await drop([{ name: "unreadable.yaml", content: yaml }]);
  await expectText(region.getByRole("alert"), "文件读取失败");
  assert.equal(await editor.inputValue(), preserved);
  await page.evaluate(() => { File.prototype.text = window.originalFileText; });

  // A newer file must win even if an earlier asynchronous read completes later.
  await page.evaluate(() => {
    File.prototype.text = function () {
      if (this.name === "slow.yaml") return new Promise((resolve) => { window.finishSlowRead = resolve; });
      return window.originalFileText.call(this);
    };
  });
  await drop([{ name: "slow.yaml", content: yaml }]);
  await expectText(region.getByRole("status"), "正在读取");
  assert.equal(await submit.isDisabled(), true);
  await drop([{ name: "最新.json", content: '{"proxies": []}' }]);
  await expectText(dropzone, "最新.json");
  await page.evaluate(() => window.finishSlowRead("stale content"));
  assert.equal(await editor.inputValue(), '{"proxies": []}');
  await drop([{ name: "slow.yaml", content: yaml }]);
  await expectText(region.getByRole("status"), "正在读取");
  await page.getByRole("button", { name: "订阅链接", exact: true }).click();
  await page.evaluate(() => window.finishSlowRead("stale content"));
  await page.getByRole("button", { name: "本地 YAML", exact: true }).click();
  assert.equal(await editor.inputValue(), "");
  await page.evaluate(() => { File.prototype.text = window.originalFileText; });

  const origin = page.url();
  await drop([{ name: "outside.yaml", content: yaml }], page.locator("h4").filter({ hasText: "添加节点来源" }));
  assert.equal(page.url(), origin);
  assert.equal(await editor.inputValue(), "");
  await drop([{ name: "最终配置.yaml", content: yaml }]);
  await expectText(dropzone, "最终配置.yaml");
  await editor.fill(yaml + "# 已编辑\n");
  await page.locator(".proxy-import").evaluate((element) => element.scrollIntoView({ block: "start" }));
  await page.screenshot({ path: path.join(output, "selected.png"), fullPage: true });
  await page.locator(".proxy-import").screenshot({ path: path.join(output, "preview.png") });

  // Test the component's narrow layout in the existing settings shell.
  await page.setViewportSize({ width: 375, height: 1000 });
  await dropzone.scrollIntoViewIfNeeded();
  const box = await dropzone.boundingBox();
  assert.ok(box.x >= 0 && box.x + box.width <= 376, "dropzone stays within the narrow viewport");
  await page.screenshot({ path: path.join(output, "narrow.png"), fullPage: true });
  await page.setViewportSize({ width: 1280, height: 1000 });
  await page.emulateMedia({ colorScheme: "dark" });
  await page.waitForFunction(() => document.documentElement.dataset.theme === "dark");
  await page.locator(".proxy-import").evaluate((element) => element.scrollIntoView({ block: "start" }));
  await page.screenshot({ path: path.join(output, "dark.png"), fullPage: true });
  await submit.click();
  await expectText(page.locator(".proxy-notice"), "已导入 1 个节点");
  assert.equal(imports.length, 1);
  assert.equal(imports[0].content, yaml + "# 已编辑\n");
  assert.equal(imports[0].name, "我的来源");
  assert.equal(imports[0].interval_hours, 0);
  await expectText(page.locator(".proxy-source"), "我的来源");
  assert.deepEqual(errors, []);
  const result = { engine, passed: true, imports: imports.length, browserErrors: errors, scope: "Synthetic browser file drops and file chooser; mocked API. Native OS drag and backend validation are outside this check." };
  await fs.writeFile(path.join(output, "result.json"), JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result));
} finally {
  await browser.close();
}
