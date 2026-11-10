import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";
import { startDisplay } from "./rfb-wire-fixture.mjs";

const engine = process.env.CAGE_TEST_BROWSER || "chromium";
const output = new URL(`../../docs/test-reports/2026-11-07-browser-vertical-tabs/${engine}/`, import.meta.url);
await fs.mkdir(output, { recursive: true });
const display = await startDisplay(process.env.CAGE_UI_URL || "http://127.0.0.1:5198", { frameInterval: 200 });
const { browser, page, state, errors } = await createWorkbench({ url: display.url, mockWebSockets: engine === "webkit" });
page.setDefaultTimeout(10000);
const checks = [];
const tabs = page.getByRole("tablist", { name: "浏览器标签页", exact: true });
const settings = page.getByRole("region", { name: "浏览器设置", exact: true });
const rail = page.getByRole("complementary", { name: "竖排标签栏", exact: true });
const shot = name => page.screenshot({ path: fileURLToPath(new URL(name + ".png", output)) });
async function openSettings() {
  await page.getByRole("button", { name: "浏览器菜单", exact: true }).last().click();
  await page.getByRole("menuitem", { name: "设置", exact: true }).click();
  await settings.getByRole("navigation", { name: "浏览器设置分类" }).getByRole("button", { name: "外观", exact: true }).click();
}
async function closeSettings() {
  await page.getByRole("button", { name: "关闭 设置", exact: true }).click();
  await settings.waitFor({ state: "hidden" });
}
async function drag(from, to) {
  const a = await from.boundingBox(), b = await to.boundingBox();
  await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
  await page.mouse.down();
  await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 12 });
  await page.mouse.up();
}
try {
  state.status.ready = state.status.vm_running = true;
  state.tabs = Array.from({ length: 5 }, (_, i) => ({
    id: `tab-${i}`, kind: i === 4 ? "editor" : "web", title: `测试网页 ${i}`,
    workspace: i === 4 ? state.settings.workspace : undefined, url: `https://example.test/${i}`,
  }));
  state.active = "tab-0";
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  if (engine !== "webkit") await page.locator(".remote-canvas canvas").waitFor();
  assert.equal(await tabs.getAttribute("aria-orientation"), "horizontal");
  const surface = await page.locator(".remote-view").elementHandle();
  await openSettings();
  const picker = page.getByRole("combobox", { name: "标签栏布局", exact: true });
  const help = settings.getByText("浏览器设置页跟随 Cage 当前的字体与配色。", { exact: true });
  await settings.locator('.browser-settings-section').evaluate(element => Promise.all(element.getAnimations().map(animation => animation.finished)));
  const before = await help.boundingBox();
  await picker.click();
  const expanded = await help.boundingBox();
  assert.ok(Math.abs(expanded.y - before.y) < 1, "floating options must not move the following content");
  await page.keyboard.press("Escape");
  assert.ok(Math.abs((await help.boundingBox()).y - before.y) < 1, "closing options restores the layout");
  await picker.click();
  await page.getByRole("option", { name: "竖排（左侧）", exact: true }).click();
  await shot("browser-settings");
  assert.equal(await picker.getAttribute("aria-expanded"), "false");
  await closeSettings();
  await rail.waitFor();
  assert.equal(await tabs.getAttribute("aria-orientation"), "vertical");
  assert.ok(await surface.evaluate(node => node === document.querySelector(".remote-view")), "changing layout must retain the remote-view component");
  const first = tabs.getByRole("tab", { name: "测试网页 0", exact: true });
  const second = tabs.getByRole("tab", { name: "测试网页 1", exact: true });
  assert.ok((await second.boundingBox()).y > (await first.boundingBox()).y);
  const railBox = await rail.boundingBox(), bodyBox = await page.locator(".browser-lane .pane-body").boundingBox();
  assert.ok(railBox.x + railBox.width <= bodyBox.x + 1);
  await shot("vertical-desktop");
  await first.focus();
  await page.keyboard.press("ArrowDown");
  await page.waitForFunction(() => document.querySelector('[data-tab-id="tab-1"] [role="tab"]').getAttribute("aria-selected") === "true");
  await page.keyboard.press("End");
  await page.waitForFunction(() => document.querySelector('[data-tab-id="tab-4"] [role="tab"]').getAttribute("aria-selected") === "true");
  await page.keyboard.press("Home");
  await drag(first, tabs.getByRole("tab", { name: "测试网页 3", exact: true }));
  await page.waitForFunction(() => [...document.querySelectorAll('.browser-tabs [role="tab"]')].findIndex(t => t.textContent === "测试网页 0") === 2);
  await first.click({ button: "right" });
  await page.getByRole("menuitem", { name: "置顶标签", exact: true }).click();
  await page.waitForFunction(() => document.querySelector(".browser-tabs .browser-tab").dataset.tabId === "tab-0");
  const order = await tabs.getByRole("tab").allTextContents();
  await first.click({ button: "right" });
  await page.getByRole("menuitem", { name: "重命名标签", exact: true }).click();
  const rename = page.getByRole("dialog", { name: "重命名标签", exact: true });
  await rename.getByRole("textbox").fill("命名后的网页");
  await rename.getByRole("button", { name: "保存名称", exact: true }).click();
  order[0] = "命名后的网页";
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await rail.waitFor();
  assert.deepEqual(await tabs.getByRole("tab").allTextContents(), order);
  await page.getByRole("button", { name: "新建网页", exact: true }).click();
  await page.waitForFunction(() => document.querySelectorAll('.browser-tabs [role="tab"]').length === 6);
  await tabs.getByRole("tab", { name: "测试网页 2", exact: true }).click();
  await page.getByRole("button", { name: "关闭 测试网页 2", exact: true }).click();
  await tabs.getByRole("tab", { name: "测试网页 2", exact: true }).waitFor({ state: "hidden" });
  checks.push("横排默认值；设置下拉内嵌展开与收起；竖排切换保留画面组件；上下键和 Home/End；拖动排序、置顶、刷新保留、新建和关闭");

  // Drag from the rail into the page to exercise native dual-browser splitting.
  await drag(tabs.getByRole("tab", { name: "测试网页 3", exact: true }), page.locator(".browser-lane .pane-body"));
  await page.waitForFunction(() => document.querySelectorAll(".browser-header-cell").length === 2);
  assert.equal(await rail.count(), 1);
  assert.equal(await tabs.count(), 1);
  assert.ok(state.split?.left && state.split?.right);
  await page.locator(".browser-header-cell").first().getByRole("button", { name: "退出分屏", exact: true }).click();
  await page.waitForFunction(() => document.querySelectorAll(".browser-header-cell").length === 1);
  checks.push("竖排标签拖入网页进入双网页分屏；共享一列标签；退出分屏");

  for (let i = 5; i < 35; i++) state.tabs.push({ id: `tab-${i}`, kind: "web", title: `更多网页 ${i}`, url: `https://example.test/${i}` });
  await page.waitForFunction(() => document.querySelectorAll('.browser-tabs [role="tab"]').length === 35);
  await page.getByRole("button", { name: "收起侧栏", exact: true }).click();
  for (const width of [1280, 820, 375]) {
    await page.setViewportSize({ width, height: 850 });
    await tabs.getByRole("tab").first().focus();
    await page.keyboard.press("End");
    await page.waitForFunction(() => {
      const list = document.querySelector(".browser-tabs"), selected = list.querySelector('[aria-selected="true"]');
      const a = list.getBoundingClientRect(), b = selected.getBoundingClientRect();
      return list.scrollHeight > list.clientHeight && list.scrollWidth <= list.clientWidth + 1 && b.y >= a.y && b.bottom <= a.bottom + 1;
    });
    assert.ok((await page.locator(".browser-lane .pane-body").boundingBox()).width > 100);
    await shot("many-tabs-" + width);
  }
  checks.push("35 个标签在 1280/820/375 宽度下纵向滚动，键盘选中末项自动滚入视野，网页仍有可用空间");
  await page.setViewportSize({ width: 1280, height: 850 });
  await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
  await openSettings();
  await picker.focus();
  await page.keyboard.press("Enter");
  await page.keyboard.press("Home");
  await page.keyboard.press("Enter");
  assert.equal(await picker.getAttribute("aria-expanded"), "false");
  await closeSettings();
  await rail.waitFor({ state: "hidden" });
  assert.equal(await tabs.getAttribute("aria-orientation"), "horizontal");
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  assert.equal(await tabs.getAttribute("aria-orientation"), "horizontal");
  assert.equal(state.saves.length, 0, "layout preferences must not save or restart the guest environment");
  assert.deepEqual(errors, []);
  checks.push("键盘切回横排并刷新保留；未写入环境设置或重启环境；无页面异常");
  await fs.writeFile(new URL("result.json", output), JSON.stringify({ passed: true, checks, scope: `真实 React DOM 与存储；${engine === "webkit" ? "显示连接使用替身" : "WebSocket/RFB 使用隔离显示服务"}；客机网页与控制 API 使用隔离替身，未实测三平台原生应用。` }, null, 2));
  console.log(JSON.stringify({ passed: true, checks }, null, 2));
} catch (error) {
  await shot("failure");
  throw error;
} finally {
  await browser.close();
  await display.close();
}
