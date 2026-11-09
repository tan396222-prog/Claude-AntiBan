import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { chooseInline, createWorkbench } from "./workbench-fixture.mjs";
import {
  presets,
  themeCatalog,
  parseThemeDocument,
  sameTheme,
} from "../src/appearance-state.mjs";
const output = fileURLToPath(
  new URL(
    "../../docs/test-reports/2026-11-02-builtin-themes/",
    import.meta.url,
  ),
);
await fs.mkdir(output, { recursive: true });
let exported;
const { browser, page, state, errors } = await createWorkbench({
  nativeRequest: (body) => {
    if (body.action === "exportTheme") exported = body;
    return true;
  },
});
const checks = [];
const open = async () => {
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "外观", exact: true }).click();
  await page.getByRole("heading", { name: "外观", exact: true }).waitFor();
};
const saved = () =>
  page
    .locator(".appearance-save-state")
    .filter({ hasText: "更改自动保存，即时生效" })
    .waitFor();
const select = async (variant, id) => {
  await chooseInline(page
    .getByLabel((variant === "light" ? "浅色" : "深色") + "主题预设", {
      exact: true,
    }), id);
  await saved();
};
const shot = async (name) => {
  await page.locator(".settings-content").evaluate((el) => el.scrollTo(0, 0));
  await page.screenshot({
    path: output + name + ".png",
    animations: "disabled",
  });
};
try {
  await page.emulateMedia({ colorScheme: "light" });
  await open();
  for (const variant of ["light", "dark"]) {
    const picker = page.getByLabel(
      (variant === "light" ? "浅色" : "深色") + "主题预设",
      { exact: true },
    );
    await picker.click();
    const list = page.getByRole("listbox");
    assert.equal(await list.locator(".inline-select-group").count(), 4);
    assert.equal(
      await list.getByRole("option").count(),
      Object.keys(presets[variant]).length,
    );
    await picker.press("Escape");
    // Every palette goes through the real picker, not just the model helper.
    for (const id of Object.keys(presets[variant])) {
      await select(variant, id);
      const theme = state.appearance[variant + "Theme"];
      assert.equal(theme.preset, id);
      assert.deepEqual(theme.colors, presets[variant][id].colors);
      assert.deepEqual(theme.terminal, presets[variant][id].terminal);
    }
  }
  await select("light", "vscode-modern");
  await select("dark", "vscode-modern");
  assert.equal(
    await page.evaluate(
      () =>
        getComputedStyle(document.querySelector(".settings-sidebar"))
          .backgroundColor,
    ),
    "rgb(248, 248, 248)",
  );
  await shot("vscode-modern-light");
  await page.getByRole("button", { name: "深色", exact: true }).click();
  await saved();
  await shot("vscode-modern-dark");
  await select("light", "sepia");
  await select("dark", "night");
  await page.getByRole("button", { name: "浅色", exact: true }).click();
  await saved();
  await shot("reading-sepia");
  checks.push(
    `真实下拉选取全部 ${Object.keys(presets.light).length + Object.keys(presets.dark).length} 套配色，四组分类、界面及终端色板完整应用`,
  );
  await select("light", "color-vision");
  await select("dark", "color-vision");
  assert.equal(
    await page.evaluate(() => document.documentElement.dataset.translucent),
    "false",
  );
  for (const label of ["浅色主题状态配色预览", "深色主题状态配色预览"]) {
    const preview = page.getByLabel(label, { exact: true });
    for (const text of ["成功", "警告", "错误"])
      await preview.getByText(text, { exact: true }).waitFor();
    assert.equal(await preview.locator("svg").count(), 3);
  }
  assert.equal(
    await page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--success"),
    ),
    "#005a9c",
  );
  assert.equal(
    await page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--danger"),
    ),
    "#a63b00",
  );
  await shot("color-vision-light");
  await page.getByRole("button", { name: "深色", exact: true }).click();
  await saved();
  await shot("color-vision-dark");
  await select("light", "high-contrast");
  await select("dark", "high-contrast");
  await shot("high-contrast-dark");
  await page.getByRole("button", { name: "浅色", exact: true }).click();
  await saved();
  await shot("high-contrast-light");
  // Switching away must clear accessibility colors as well as earlier user overrides.
  await select("light", "paper");
  assert.equal(state.appearance.lightTheme.colors.success, undefined);
  assert.equal(state.appearance.lightTheme.terminal.green, "#426e37");
  await select("light", "color-vision");
  await select("dark", "color-vision");
  await page.getByLabel("主题名称", { exact: true }).fill("我的蓝橙搭配");
  await page.getByRole("button", { name: "另存为主题", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "主题已保存" }).waitFor();
  const snapshot = structuredClone(state.appearance);
  await page.getByRole("button", { name: "导出主题", exact: true }).click();
  await page.getByRole("status").filter({ hasText: "主题已导出" }).waitFor();
  assert.ok(sameTheme(parseThemeDocument(exported.text), snapshot));
  await fs.writeFile(
    output + "accessible-export.cage-theme.json",
    exported.text,
  );
  await page.reload();
  await open();
  assert.equal(
    await page.getByLabel("浅色主题预设", { exact: true }).getAttribute("value"),
    "color-vision",
  );
  assert.equal(
    await page.getByLabel("已保存的主题", { exact: true }).getAttribute("value"),
    state.appearance.customThemes[0].id,
  );
  assert.ok(sameTheme(state.appearance, snapshot));
  checks.push(
    "蓝橙状态图标与文字、高对比不透明窗口、跨预设清除旧覆盖、命名保存/导出/刷新读回",
  );
  await page.setViewportSize({ width: 820, height: 558 });
  await shot("builtin-minimum");
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
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
  await page.waitForFunction(
    () =>
      getComputedStyle(document.querySelector(".xterm-scrollable-element"))
        .backgroundColor === "rgb(255, 255, 255)",
  );
  const connections = state.terminalConnections;
  await open();
  await select("light", "high-contrast");
  await page.getByRole("button", { name: "深色", exact: true }).click();
  await saved();
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  await page.waitForFunction(
    () =>
      getComputedStyle(document.querySelector(".xterm-scrollable-element"))
        .backgroundColor === "rgb(18, 27, 36)",
  );
  assert.equal(state.terminalConnections, connections);
  await page.setViewportSize({ width: 1280, height: 850 });
  await page.screenshot({
    path: output + "terminal-color-vision.png",
    animations: "disabled",
  });
  checks.push("820px 无横向溢出；明暗切换实时更新已有终端，连接保持");
  assert.deepEqual(errors, []);
  assert.ok(
    !state.requests.some(
      (request) => request.path === "/settings" && request.method === "POST",
    ),
  );
  const result = {
    passed: true,
    families: themeCatalog.length,
    light: Object.keys(presets.light).length,
    dark: Object.keys(presets.dark).length,
    checks,
    scope: "真实前端、xterm 和隔离 API；不连接日常 VM",
  };
  await fs.writeFile(output + "result.json", JSON.stringify(result, null, 2));
  console.log(JSON.stringify(result));
} finally {
  await browser.close();
}
