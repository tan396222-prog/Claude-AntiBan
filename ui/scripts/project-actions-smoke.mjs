import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
import { createWorkbench } from "./workbench-fixture.mjs";

const root = fileURLToPath(new URL("..", import.meta.url));
const vite = await createServer({ root, configFile: `${root}/vite.config.js`, server: { host: "127.0.0.1", port: 0 } });
await vite.listen();
const url = `http://127.0.0.1:${vite.httpServer.address().port}`;
try {
  for (const [platform, navigatorPlatform, modifier] of [
    ["darwin", "MacIntel", "Meta"],
    ["windows", "Win32", "Control"],
    ["linux", "Linux x86_64", "Control"],
  ]) {
    const fixture = await createWorkbench({
      url,
      clientContext: { mode: "local", connection_id: "local", client_platform: platform, server_platform: platform, connection: { state: "disconnected" } },
    });
    const { browser, page, state, errors } = fixture;
    try {
      await page.addInitScript(value => Object.defineProperty(navigator, "platform", { value }), navigatorPlatform);
      const projectA = "/projects/alpha";
      const projectB = "/projects/beta";
      Object.assign(state.status, { ready: true, vm_running: true, projects: [projectA, projectB], workspace: projectA });
      state.settings.workspace = projectA;
      await page.reload();
      await page.getByRole("region", { name: "项目 beta" }).waitFor();
      const header = page.locator("header.terminal-chrome");
      const checkHeader = async () => {
        assert.equal(await header.getByRole("button", { name: "打开文件", exact: true }).count(), 0);
        assert.equal(await header.getByRole("button", { name: "新建终端", exact: true }).count(), 0);
      };
      await checkHeader();
      await page.getByRole("button", { name: "打开 alpha 的编辑器" }).click();
      await page.getByRole("tab", { name: "文件 · alpha" }).waitFor();
      assert.equal(state.tabs.find(tab => tab.kind === "editor").workspace, projectA);
      await page.getByRole("button", { name: "在 beta 新建终端" }).click();
      await page.getByRole("menuitem", { name: "普通终端" }).click();
      await page.getByRole("tab", { name: "终端 1" }).waitFor();
      assert.equal(state.terminals[0].workspace, projectB);
      await checkHeader();
      await page.getByRole("button", { name: "收起侧栏" }).click();
      await page.keyboard.press(`${modifier}+Shift+Enter`);
      await page.getByRole("menuitem", { name: "普通终端" }).click();
      await page.waitForFunction(() => document.querySelector("header.terminal-chrome .current-title")?.textContent === "终端 2");
      assert.equal(state.terminals[1].workspace, projectB);
      await page.evaluate(() => window.cageNativeAction("newTerminal"));
      await page.getByRole("menuitem", { name: "Claude 终端" }).waitFor();
      await page.keyboard.press("Escape");
      assert.equal(await page.locator(":focus").getAttribute("title"), projectB);
      assert.deepEqual(errors, []);
      console.log(`PASS ${platform}: 项目入口、标题栏和收起侧栏后的快捷键`);
    } finally {
      await browser.close();
    }
  }
} finally {
  await vite.close();
}
