import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { chooseInline, createWorkbench } from "./workbench-fixture.mjs";
import {
  themeDocument,
  parseThemeDocument,
  sameTheme,
} from "../src/appearance-state.mjs";
const output = fileURLToPath(
  new URL("../../docs/test-reports/2026-11-02-themes/", import.meta.url),
);
await fs.mkdir(output, { recursive: true });
let imported = "",
  exported,
  cancelExport = false;
const { browser, page, state, errors } = await createWorkbench({
  nativeRequest: (body) => {
    if (body.action === "importTheme") return imported;
    if (body.action === "exportTheme") {
      exported = body;
      return !cancelExport;
    }
    return true;
  },
});
const checks = [];
const open = async () => {
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "外观", exact: true }).click();
  await page.getByRole("heading", { name: "外观", exact: true }).waitFor();
  await page.locator(".appearance-fields").evaluate((el) => {
    if (el.disabled) throw new Error("appearance still loading");
  });
};
const saved = async () => {
  await page
    .locator(".appearance-save-state")
    .filter({ hasText: /更改自动保存|已同步 Codex 外观/ })
    .waitFor();
};
const edit = async (label, value) => {
  const input = page.getByLabel(label, { exact: true });
  await input.fill(value);
  await input.press("Enter");
  await saved();
};
const light = () => page.getByRole("region", { name: "浅色主题", exact: true });
const color = (key) =>
  page.evaluate(
    (key) => getComputedStyle(document.documentElement).getPropertyValue(key),
    key,
  );
const shot = async (name) => {
  await page.screenshot({
    path: output + name + ".png",
    animations: "disabled",
  });
};
try {
  await page.emulateMedia({ colorScheme: "light" });
  await open();
  await shot("themes-light");
  await edit("浅色主题背景", "#fdf6e3");
  await light().getByRole("button", { name: "自定义", exact: true }).click();
  await light().getByText("界面与层级", { exact: true }).click();
  await edit("浅色主题面板背景", "#e8dec3");
  assert.equal(await color("--chrome"), "#e8dec3");
  await light().getByText("按钮与交互", { exact: true }).click();
  await edit("浅色主题主按钮背景", "#426b52");
  await edit("浅色主题主按钮文字", "#ffffff");
  assert.equal(await color("--button"), "#426b52");
  await light().getByText("终端", { exact: true }).click();
  await edit("浅色主题终端背景", "#101820");
  await light().getByText("终端 ANSI 配色", { exact: true }).click();
  await edit("浅色主题终端红色", "#f08080");
  await shot("themes-colors");
  const originalChrome = state.appearance.lightTheme.colors.chrome;
  await page.getByLabel("浅色主题面板背景", { exact: true }).fill("#oops");
  await page.getByLabel("浅色主题面板背景", { exact: true }).press("Tab");
  assert.equal(state.appearance.lightTheme.colors.chrome, originalChrome);
  await page.getByLabel("浅色主题面板背景", { exact: true }).press("Escape");
  checks.push("基础色和元素色即时生效、终端和 ANSI 独立覆盖、无效 HEX 不提交");
  await page.getByLabel("主题名称", { exact: true }).fill("森林日光");
  await page.getByRole("button", { name: "另存为主题", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "主题已保存" }).waitFor();
  assert.equal(state.appearance.customThemes.length, 1);
  const savedTheme = structuredClone(state.appearance.customThemes[0]);
  await edit("浅色主题背景", "#eeeeee");
  await page
    .getByRole("button", { name: "恢复已保存配色", exact: true })
    .click();
  await saved();
  assert.equal(state.appearance.lightTheme.surface, "#fdf6e3");
  await page.getByRole("button", { name: "导出主题", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "主题已导出" }).waitFor();
  assert.ok(sameTheme(parseThemeDocument(exported.text), savedTheme));
  assert.equal(exported.filename, "森林日光.cage-theme.json");
  cancelExport = true;
  await page.getByRole("button", { name: "导出主题", exact: true }).click();
  await page.waitForFunction(
    () =>
      !Array.from(document.querySelectorAll("button")).find(
        (button) => button.textContent === "导出主题",
      )?.disabled,
  );
  assert.equal(
    await page.getByRole("status").filter({ hasText: "主题已导出" }).count(),
    0,
  );
  cancelExport = false;
  const original = JSON.stringify(state.appearance);
  imported = "";
  await page.getByRole("button", { name: "导入主题", exact: true }).click();
  assert.equal(JSON.stringify(state.appearance), original);
  imported = '{"format":"cage-theme","version":2}';
  await page.getByRole("button", { name: "导入主题", exact: true }).click();
  await page
    .getByRole("alert")
    .filter({ hasText: "不支持的主题格式或版本" })
    .waitFor();
  assert.equal(JSON.stringify(state.appearance), original);
  imported = exported.text;
  await page.getByRole("button", { name: "导入主题", exact: true }).click();
  await page
    .getByRole("status")
    .filter({ hasText: "主题已导入并应用" })
    .waitFor();
  assert.equal(state.appearance.customThemes.length, 2);
  assert.ok(sameTheme(state.appearance, savedTheme));
  checks.push(
    "命名保存、切换还原、原生导出内容、同名导入不覆盖、取消和无效文件保留设置",
  );
  const second = state.appearance.customThemes[1];
  await edit("浅色主题背景", "#f5efdd");
  await page.getByLabel("主题名称", { exact: true }).fill("森林日光 II");
  await page.getByRole("button", { name: "更新此主题", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "主题已保存" }).waitFor();
  assert.equal(state.appearance.customThemes[1].id, second.id);
  assert.equal(state.appearance.customThemes[1].name, "森林日光 II");
  assert.equal(state.appearance.customThemes[0].name, "森林日光");
  state.failAppearance = true;
  await page.getByLabel("浅色主题背景", { exact: true }).fill("#fafafa");
  await page.getByLabel("浅色主题背景", { exact: true }).press("Enter");
  await page
    .getByRole("alert")
    .filter({ hasText: "外观设置未能保存" })
    .waitFor();
  assert.equal(await color("--paper"), "#fafafa");
  assert.equal(state.appearance.lightTheme.surface, "#f5efdd");
  state.failAppearance = false;
  await page.getByRole("button", { name: "重试保存", exact: true }).click();
  await saved();
  await page.reload();
  await open();
  assert.equal(state.appearance.customThemes.length, 2);
  assert.equal(
    await page.getByLabel("浅色主题背景", { exact: true }).inputValue(),
    "#fafafa",
  );
  await chooseInline(page
    .getByLabel("已保存的主题", { exact: true }), second.id);
  await saved();
  assert.equal(
    await page.getByLabel("浅色主题背景", { exact: true }).inputValue(),
    "#f5efdd",
  );
  // Codex sync must preserve the user's library.
  await page
    .getByRole("button", { name: "从本机 Codex 同步", exact: true })
    .click();
  await saved();
  assert.equal(state.appearance.customThemes.length, 2);
  await chooseInline(page
    .getByLabel("已保存的主题", { exact: true }), second.id);
  await saved();
  page.once("dialog", (dialog) => dialog.dismiss());
  await page.getByRole("button", { name: "删除", exact: true }).click();
  assert.equal(state.appearance.customThemes.length, 2);
  page.once("dialog", (dialog) => dialog.accept());
  await page.getByRole("button", { name: "删除", exact: true }).click();
  await saved();
  assert.equal(state.appearance.customThemes.length, 1);
  assert.equal(state.appearance.lightTheme.surface, "#f5efdd");
  checks.push(
    "更新主题、失败保留预览并重试、刷新读回、Codex 同步保留主题库、删除确认和保留当前配色",
  );
  // Exercise the actual browser file input and download fallback as well.
  await page.evaluate(() => {
    delete window.webkit;
  });
  const downloadEvent = page.waitForEvent("download");
  await page.getByRole("button", { name: "导出主题", exact: true }).click();
  const download = await downloadEvent;
  const path = output + "browser-export.cage-theme.json";
  await download.saveAs(path);
  const data = parseThemeDocument(await fs.readFile(path, "utf8"));
  assert.ok(sameTheme(data, state.appearance));
  await page.getByLabel("选择主题文件", { exact: true }).setInputFiles(path);
  await page
    .getByRole("status")
    .filter({ hasText: "主题已导入并应用" })
    .waitFor();
  assert.equal(state.appearance.customThemes.length, 2);
  await page.getByRole("button", { name: "深色", exact: true }).click();
  await saved();
  await page.locator(".settings-content").evaluate((el) => el.scrollTo(0, 0));
  await shot("themes-dark");
  await page.setViewportSize({ width: 820, height: 558 });
  await shot("themes-minimum");
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await light().getByRole("button", { name: "自定义", exact: true }).click();
  await light().getByText("界面与层级", { exact: true }).click();
  const row = page.getByLabel("浅色主题面板背景", { exact: true });
  await row.scrollIntoViewIfNeeded();
  await shot("themes-minimum-colors");
  const bounds = await row.boundingBox();
  assert.ok(bounds.x + bounds.width <= 820);
  await page.getByRole("button", { name: "浅色", exact: true }).click();
  await saved();
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  await page
    .getByRole("button", { name: "启动 Cage 环境", exact: true })
    .click();
  await page
    .getByRole("button", { name: "新建终端", exact: true })
    .first()
    .click();
  await page.getByRole("menuitem", { name: "普通终端", exact: true }).click();
  await page.getByRole("tab", { name: "终端 1", exact: true }).waitFor();
  await page.waitForFunction(() =>
    [...document.querySelectorAll(".xterm-scrollable-element")].some(
      (el) => getComputedStyle(el).backgroundColor === "rgb(16, 24, 32)",
    ),
  );
  await shot("themes-terminal");
  const connections = state.terminalConnections;
  await open();
  await light().getByRole("button", { name: "自定义", exact: true }).click();
  await light().getByText("终端", { exact: true }).click();
  await edit("浅色主题终端背景", "#202830");
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  await page.waitForFunction(() =>
    [...document.querySelectorAll(".xterm-scrollable-element")].some(
      (el) => getComputedStyle(el).backgroundColor === "rgb(32, 40, 48)",
    ),
  );
  assert.equal(state.terminalConnections, connections);
  checks.push(
    "浏览器文件上传与下载往返、浅深色与 820px 无溢出、已有终端实时改色且不重连",
  );
  assert.deepEqual(errors, []);
  assert.ok(
    !state.requests.some(
      (request) => request.path === "/settings" && request.method === "POST",
    ),
  );
  await fs.writeFile(
    output + "result.json",
    JSON.stringify(
      {
        passed: true,
        checks,
        scope: "真实前端与 xterm，隔离控制接口；Go 写盘与原生编译另验",
      },
      null,
      2,
    ),
  );
  console.log(JSON.stringify({ passed: true, checks }));
} finally {
  await browser.close();
}
