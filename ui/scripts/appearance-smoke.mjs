import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { chooseInline, createWorkbench } from "./workbench-fixture.mjs";
const output = fileURLToPath(
  new URL("../../docs/test-reports/2026-10-31-appearance/", import.meta.url),
);
await fs.mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench();
const checks = [];
const showAppearance = async () => {
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("button", { name: "外观", exact: true }).click();
  await page.getByRole("heading", { name: "外观", exact: true }).waitFor();
};
const saved = () =>
  page
    .getByRole("status")
    .filter({ hasText: /更改自动保存|已同步 Codex 外观/ })
    .waitFor();
const shot = async (name) => {
  await page.locator(".settings-content").evaluate((el) => el.scrollTo(0, 0));
  await page.screenshot({
    path: output + name + ".png",
    animations: "disabled",
  });
};
try {
  await page.emulateMedia({ colorScheme: "light" });
  await showAppearance();
  await page
    .getByRole("button", { name: "从本机 Codex 同步", exact: true })
    .click();
  await page
    .getByRole("status")
    .filter({ hasText: "已同步 Codex 外观" })
    .waitFor();
  assert.equal(state.appearance.lightTheme.preset, "solarized");
  assert.equal(state.appearance.darkTheme.preset, "ayu");
  assert.equal(
    await page.getByLabel("界面字号", { exact: true }).inputValue(),
    "16",
  );
  assert.equal(
    await page.getByLabel("代码字号", { exact: true }).inputValue(),
    "13",
  );
  await shot("appearance-light");
  await page.getByRole("button", { name: "深色", exact: true }).click();
  await saved();
  assert.equal(
    await page.evaluate(() => document.documentElement.dataset.theme),
    "dark",
  );
  assert.equal(
    await page.evaluate(() =>
      getComputedStyle(document.documentElement).getPropertyValue("--paper"),
    ),
    "#10141c",
  );
  await shot("appearance-dark");
  await page.getByRole("button", { name: "跟随系统", exact: true }).click();
  await saved();
  await page.emulateMedia({ colorScheme: "light" });
  await page.waitForFunction(
    () => document.documentElement.dataset.theme === "light",
  );
  await page.emulateMedia({ colorScheme: "dark" });
  await page.waitForFunction(
    () => document.documentElement.dataset.theme === "dark",
  );
  checks.push("导入本机设置、明暗主题独立保存、跟随系统实时切换、16/13 字号");
  await page
    .getByRole("region", { name: "深色主题", exact: true })
    .getByRole("button", { name: "自定义", exact: true })
    .click();
  await page.getByLabel("深色主题代码字体", { exact: true }).fill("Menlo");
  await saved();
  await page.getByLabel("深色主题半透明侧栏", { exact: true }).uncheck();
  await saved();
  assert.equal(
    await page.evaluate(() => document.documentElement.dataset.translucent),
    "false",
  );
  await page.getByRole("switch", { name: "使用指针光标", exact: true }).check();
  await saved();
  assert.equal(
    await page
      .getByRole("button", { name: "深色", exact: true })
      .evaluate((el) => getComputedStyle(el).cursor),
    "pointer",
  );
  await chooseInline(page.getByLabel("减少动态效果", { exact: true }), "on");
  await saved();
  assert.equal(
    await page.evaluate(() => document.documentElement.dataset.reducedMotion),
    "true",
  );
  state.failAppearance = true;
  await page.getByLabel("代码字号", { exact: true }).fill("15");
  await page
    .getByRole("alert")
    .filter({ hasText: "外观设置未能保存" })
    .waitFor();
  assert.equal(
    await page.getByLabel("代码字号", { exact: true }).inputValue(),
    "15",
  );
  assert.equal(state.appearance.codeFontSize, 13);
  state.failAppearance = false;
  await page.getByRole("button", { name: "重试保存", exact: true }).click();
  await saved();
  await page.getByRole("button", { name: "浅色", exact: true }).click();
  await page.getByRole("button", { name: "深色", exact: true }).click();
  await page.getByRole("button", { name: "跟随系统", exact: true }).click();
  await saved();
  await page.reload();
  await showAppearance();
  assert.equal(
    await page.getByLabel("代码字号", { exact: true }).inputValue(),
    "15",
  );
  assert.equal(
    await page
      .getByRole("button", { name: "跟随系统", exact: true })
      .getAttribute("aria-pressed"),
    "true",
  );
  assert.equal(state.appearance.darkTheme.fonts.code, "Menlo");
  checks.push(
    "自定义字体/半透明/指针/动态效果、失败保留输入与重试、快速切换最后写入胜出、刷新读回",
  );
  await page.setViewportSize({ width: 820, height: 558 });
  await shot("appearance-minimum");
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  await page.getByRole("button", { name: "启动 Cage 环境", exact: true }).click();
  await page
    .getByRole("button", { name: "新建终端", exact: true })
    .first()
    .click();
  await page.getByRole("menuitem", { name: "普通终端", exact: true }).click();
  await page.getByRole("tab", { name: "终端 1", exact: true }).waitFor();
  const count = state.terminalConnections;
  await showAppearance();
  await page.getByRole("button", { name: "浅色", exact: true }).click();
  await saved();
  await page.getByRole("button", { name: "返回工作台", exact: true }).click();
  assert.equal(state.terminalConnections, count);
  assert.equal(
    await page
      .locator(".xterm-char-measure-element")
      .first()
      .evaluate((el) => getComputedStyle(el).fontSize),
    "15px",
  );
  assert.ok(
    !state.requests.some(
      (request) => request.path === "/settings" && request.method === "POST",
    ),
  );
  await page.setViewportSize({ width: 1280, height: 850 });
  await page.screenshot({
    path: output + "terminal-solarized.png",
    animations: "disabled",
  });
  checks.push(
    "820px 设置可滚动且无横向溢出、切换外观保留终端连接、未调用环境设置接口",
  );
  assert.deepEqual(errors, []);
  await fs.writeFile(
    output + "smoke-result.json",
    JSON.stringify(
      {
        passed: true,
        checks,
        scope: "隔离控制接口；源配置导入和写盘另用真实服务验证",
      },
      null,
      2,
    ),
  );
  console.log(JSON.stringify({ passed: true, checks }));
} finally {
  await browser.close();
}
