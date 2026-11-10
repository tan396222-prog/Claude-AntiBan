import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

// Isolated records and native bridge; no personal files or running environment.
const downloads = [
  {
    id: "report",
    name: "产品需求与界面验收报告.pdf",
    state: "saved",
    path: "/Users/test/Cage/Downloads/产品需求与界面验收报告.pdf",
  },
  {
    id: "video",
    name: "很长的视频文件名称_20261107_项目演示与操作录屏.mp4",
    state: "downloading",
    receivedBytes: 42,
    totalBytes: 100,
  },
  {
    id: "failed",
    name: "assets.zip",
    state: "failed",
    error: "保存失败，请检查连接后重试。",
    retryable: true,
  },
  { id: "canceled", name: "installer.dmg", state: "canceled" },
  ...Array.from({ length: 10 }, (_, index) => ({
    id: `history-${index}`,
    name: `历史文件 ${index + 1}.txt`,
    state: "saved",
    path: `/Users/test/Cage/Downloads/历史文件 ${index + 1}.txt`,
  })),
];
let records = downloads;
const output = process.env.CAGE_DOWNLOAD_CAPTURE || "/tmp/cage-download-panel";
await fs.mkdir(output, { recursive: true });
for (const platform of ["darwin", "win32", "linux"]) {
  const calls = [];
  const { browser, page, state, errors } = await createWorkbench({
    clientContext: {
      mode: "local",
      connection_id: "local",
      client_platform: platform,
      server_platform: platform,
      connection: { state: "disconnected" },
    },
    nativeRequest: async (body) => {
      calls.push(body);
      return body.action === "saveBrowserDownload"
        ? { canceled: true }
        : { ok: true };
    },
  });
  try {
    records = downloads;
    await page.route("**/api/browser/tabs", (route) =>
      route.fulfill({
        json: { tabs: state.tabs, active: state.active, downloads: records },
      }),
    );
    await page.route("**/api/browser/files/saved", (route) =>
      route.fulfill({ json: [] }),
    );
    await page.route("**/api/browser/download-retry", (route) => {
      calls.push({ action: "retry", ...route.request().postDataJSON() });
      return route.fulfill({ json: { ok: true } });
    });
    state.status.ready = state.status.vm_running = true;
    await page.reload();
    await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
    const trigger = page.getByRole("button", {
      name: "浏览器下载",
      exact: true,
    });
    const panel = page.getByRole("dialog", { name: "下载文件", exact: true });
    const toolbar = page.locator(".browser-chrome .navigation-row");
    assert.equal(
      await page
        .getByRole("button", { name: "设置", exact: true })
        .locator("svg.lucide-settings")
        .count(),
      1,
    );
    await page.getByRole("button", { name: "收起侧栏", exact: true }).click();
    for (const width of [1280, 375]) {
      await page.setViewportSize({ width, height: 850 });
      const before = await toolbar.boundingBox();
      await trigger.focus();
      await trigger.press("Enter");
      await panel.waitFor();
      const box = await panel.boundingBox(),
        after = await toolbar.boundingBox();
      assert.ok(
        box.x >= 0 &&
          box.x + box.width <= width &&
          box.y >= after.y + after.height,
        "download panel stays below the toolbar and inside the viewport",
      );
      assert.ok(
        box.y + box.height <= 850 && Math.abs(after.height - before.height) < 1,
        "panel stays inside the window without shifting the toolbar",
      );
      assert.equal(
        await page.locator(".modal-scrim").count(),
        0,
        "downloads do not dim the page",
      );
      assert.equal(
        await panel.getByRole("progressbar").getAttribute("value"),
        "42",
      );
      const list = panel.locator(".browser-download-list");
      assert.equal(
        await list.evaluate((el) => el.scrollHeight > el.clientHeight),
        true,
        "long history scrolls inside the panel",
      );
      assert.equal(
        await list.evaluate((el) => el.scrollWidth <= el.clientWidth),
        true,
        "long file names and paths do not widen the panel",
      );
      const row = panel.locator("article").first();
      await row.getByRole("button", { name: "打开文件", exact: true }).click();
      await row
        .getByRole("button", { name: "在文件夹中显示", exact: true })
        .click();
      await row.getByRole("button", { name: "另存为…", exact: true }).click();
      await panel
        .getByRole("button", { name: "重试保存", exact: true })
        .click();
      await panel
        .getByRole("button", { name: "重试保存", exact: true })
        .waitFor({ state: "visible" });
      await panel.getByRole("button", { name: "关闭面板", exact: true }).focus();
      await page.screenshot({ path: `${output}/${platform}-${width}.png` });
      if (platform === "darwin" && width === 1280) await panel.screenshot({ path: `${output}/panel.png` });
      await list.evaluate((el) => {
        el.scrollTop = el.scrollHeight;
      });
      await panel
        .getByText("历史文件 10.txt", { exact: true })
        .scrollIntoViewIfNeeded();
      await page.keyboard.press("Escape");
      await panel.waitFor({ state: "hidden" });
      assert.equal(
        await trigger.evaluate((el) => el === document.activeElement),
        true,
        "Escape restores the download button focus",
      );
      await trigger.click();
      await page.getByRole("textbox", { name: "网页地址或搜索" }).click();
      await panel.waitFor({ state: "hidden" });
      assert.equal(
        await page
          .getByRole("textbox", { name: "网页地址或搜索" })
          .evaluate((el) => el === document.activeElement),
        true,
        "outside clicks keep their intended focus",
      );
    }
    await page.setViewportSize({ width: 1280, height: 850 });
    if (platform === "darwin") {
      await page.emulateMedia({ colorScheme: "dark" });
      await page.waitForFunction(
        () => document.documentElement.dataset.theme === "dark",
      );
      await trigger.click();
      await panel.waitFor();
      await page.screenshot({ path: `${output}/dark.png` });
      await page.evaluate(() =>
        document.documentElement.style.setProperty("--ui-font-size", "28px"),
      );
      await page.screenshot({ path: `${output}/large-text.png` });
      assert.equal(
        await panel
          .locator(".browser-download-list")
          .evaluate((el) => el.scrollWidth <= el.clientWidth),
        true,
      );
      await page.setViewportSize({ width: 900, height: 600 });
      await page.waitForFunction(() => {
        const box = document.querySelector(".browser-download-panel").getBoundingClientRect();
        return box.right <= window.innerWidth && box.bottom <= window.innerHeight;
      });
      const resized = await panel.boundingBox();
      assert.ok(
        resized.x + resized.width <= 900 && resized.y + resized.height <= 600,
        "open panel follows window resizing",
      );
      await page.keyboard.press("Escape");
      await page.evaluate(() =>
        document.documentElement.style.removeProperty("--ui-font-size"),
      );
      await page.emulateMedia({ colorScheme: "light" });
      await page.setViewportSize({ width: 1280, height: 850 });
    }
    await page.getByRole("button", { name: "浏览器菜单", exact: true }).click();
    const menu = page.getByRole("menu", { name: "浏览器菜单", exact: true });
    assert.equal(
      await menu
        .getByRole("menuitem", { name: "设置", exact: true })
        .locator("svg.lucide-settings")
        .count(),
      1,
    );
    await menu
      .getByRole("menuitem", { name: "浏览器下载", exact: true })
      .click();
    await panel.waitFor();
    await page.keyboard.press("Escape");
    await panel.waitFor({ state: "hidden" });
    await page.getByRole("button", { name: "展开侧栏", exact: true }).click();
    await page.getByRole("button", { name: "下载文件", exact: true }).click();
    await panel.waitFor();
    assert.ok(
      (await panel.boundingBox()).x > 800,
      "sidebar also opens the panel at the upper right",
    );
    await panel.getByRole("button", { name: "关闭面板", exact: true }).click();
    records = [];
    await page.reload();
    await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
    await trigger.click();
    await panel.getByText("暂无下载", { exact: true }).waitFor();
    await page.screenshot({ path: `${output}/${platform}-empty.png` });
    assert.ok(
      calls.some(
        (call) =>
          call.action === "browserDownloadAction" && call.operation === "open",
      ),
    );
    assert.ok(
      calls.some(
        (call) =>
          call.action === "browserDownloadAction" &&
          call.operation === "reveal",
      ),
    );
    assert.ok(calls.some((call) => call.action === "saveBrowserDownload"));
    assert.ok(
      calls.some((call) => call.action === "retry" && call.id === "failed"),
    );
    assert.deepEqual(errors, []);
    console.log(
      `PASS: ${platform} context, toolbar/menu/sidebar downloads, 1280/375 layout, progress, scroll, empty state, file actions, retry, Escape/outside dismissal and gear icons.`,
    );
  } finally {
    await browser.close();
  }
}
console.log(`Screenshots: ${output}`);
