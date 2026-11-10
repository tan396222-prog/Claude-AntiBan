import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = new URL(
  "../../docs/test-reports/2026-10-31-environment/",
  import.meta.url,
);
await fs.mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench();
const shot = (name) =>
  page.screenshot({
    path: fileURLToPath(new URL(name + ".png", output)),
    animations: "disabled",
  });
try {
  assert.equal(
    await page.getByRole("button", { name: "添加项目", exact: true }).isDisabled(),
    true,
  );
  assert.equal(
    await page.getByRole("button", { name: /^打开浏览器/ }).isDisabled(),
    true,
  );
  assert.equal(await page.locator(".environment-shield.healthy").count(), 0);
  assert.equal(await page.locator(".recent-projects").count(), 0);
  await shot("stopped");
  await page
    .getByRole("button", { name: "启动 Cage 环境", exact: true })
    .click();
  await page.getByRole("heading", { name: "Cage 环境已就绪" }).waitFor();
  await page.getByText("环境运行正常", { exact: true }).waitFor();
  assert.ok(
    (await page
      .getByRole("img", { name: "环境运行正常，网络检查通过" })
      .count()) >= 2,
  );
  assert.equal(state.saves.length, 0);
  const links = await page
    .getByRole("navigation", { name: "主导航" })
    .getByRole("button")
    .allTextContents();
  assert.match(links[0], /工作台/);
  assert.match(links[1], /浏览器/);
  await shot("running");
  await page.getByRole("button", { name: /^打开浏览器/ }).click();
  await page.getByLabel("网页地址或搜索").waitFor();
  await page.waitForFunction(
    () =>
      document.querySelector('[aria-label="网页地址或搜索"]')?.value ===
      "https://www.bing.com/",
  );
  assert.equal(state.tabs[0].url, "https://www.bing.com/");
  await page.getByRole("button", { name: "浏览器菜单", exact: true }).click();
  await page.getByRole("menuitem", { name: "设置", exact: true }).click();
  await page.getByLabel("新标签页地址", { exact: true }).fill("https://example.com/start");
  assert.equal(
    await page.getByRole("button", { name: "保存并重启", exact: true }).count(),
    0,
  );
  const starts = state.requests.filter((r) => r.path === "/start").length;
  await shot("settings");
  await page.getByRole("button", { name: "保存地址", exact: true }).click();
  await page.getByText("已保存", { exact: true }).waitFor();
  await page.getByRole("button", { name: "关闭 设置", exact: true }).click();
  assert.equal(state.settings.browser_home_url, "https://example.com/start");
  assert.equal(
    state.requests.filter((r) => r.path === "/start").length,
    starts,
  );
  await page.reload();
  await page.getByRole("heading", { name: "Cage 环境已就绪" }).waitFor();
  await page.getByRole("button", { name: /^打开浏览器/ }).click();
  await page.getByRole("button", { name: "新建网页", exact: true }).click();
  await page.waitForFunction(
    () =>
      document.querySelector('[aria-label="网页地址或搜索"]')?.value ===
      "https://example.com/start",
  );
  state.status.safety.gate.until = new Date(Date.now() - 1000).toISOString();
  await page.getByText("离线可用", { exact: true }).waitFor();
  assert.equal(await page.locator(".environment-shield.healthy").count(), 0);
  await page.getByRole("button", { name: "工作台", exact: true }).click();
  await shot("offline");
  state.status.ready = false;
  state.status.vm_running = false;
  await page.getByRole("heading", { name: "先启动 Cage 环境" }).waitFor();
  assert.equal(await page.locator(".environment-shield.healthy").count(), 0);
  await page.setViewportSize({ width: 820, height: 558 });
  await shot("minimum");
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await page.getByRole("button", { name: "收起侧栏" }).click();
  await page.setViewportSize({ width: 375, height: 812 });
  await shot("compact");
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  assert.deepEqual(errors, []);
  const result = {
    passed: true,
    checks: [
      "先启动环境，无需选择项目",
      "浏览器入口紧随工作台",
      "有效放行时显示绿色带对钩盾牌，过期及停止时撤销",
      "默认 Bing、自定义网站保存并刷新读回、下次打开生效",
      "默认网站保存无需重启",
      "820×558 和 375px 布局",
    ],
    scope: "Playwright + mock API; VM behavior is verified separately.",
  };
  await fs.writeFile(
    new URL("smoke-result.json", output),
    JSON.stringify(result, null, 2),
  );
  console.log(JSON.stringify(result));
} finally {
  await browser.close();
}
