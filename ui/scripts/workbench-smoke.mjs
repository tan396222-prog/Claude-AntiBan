import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = fileURLToPath(
  new URL(
    "../../docs/test-reports/2026-10-31-project-browser/",
    import.meta.url,
  ),
);
await fs.mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench();
const checks = [];
const shot = (name) =>
  page.screenshot({ path: output + name + ".png", animations: "disabled" });
const settings = async () => {
  await page.getByRole("button", { name: /^设置/ }).click();
  await page.getByRole("navigation", { name: "设置分组" }).getByRole("button", { name: "通用", exact: true }).click();
};
const tabs = () => page.getByRole("tablist", { name: "浏览器标签页" });
try {
  await shot("main");
  const navigation = page.getByRole("navigation", { name: "主导航" });
  assert.equal(await navigation.getByRole("button", { name: /打开项目|项目文件/ }).count(), 0);
  await page
    .getByRole("button", { name: "启动 Cage 环境", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Cage 环境已就绪", exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "添加项目", exact: true })
    .click();
  await page.getByRole("button", { name: "添加本机项目…", exact: true }).click();
  await page.waitForFunction(
    (path) => document.getElementById("project-path")?.value === path,
    state.chosenFolder,
  );
  assert.equal(
    await page.getByLabel("新项目文件夹", { exact: true }).inputValue(),
    state.chosenFolder,
  );
  state.failJob = true;
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "打开项目", exact: true })
    .click();

  await page
    .getByRole("dialog")
    .getByRole("alert")
    .filter({ hasText: "项目文件夹连接失败" })
    .waitFor();
  assert.equal(
    await page.getByLabel("新项目文件夹", { exact: true }).inputValue(),
    state.chosenFolder,
  );
  assert.ok(state.settings.workspace.endsWith(".claude-cage-workspace"));
  state.failJob = false;
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "打开项目", exact: true })
    .click();

  await page.getByRole("dialog").waitFor({ state: "hidden" });
  assert.equal(state.settings.workspace, state.chosenFolder);
  await page.reload();
  await page
    .getByRole("heading", { name: "Cage 环境已就绪", exact: true })
    .waitFor();
  await page
    .getByRole("region", { name: "项目 design-lab", exact: true })
    .waitFor();
  assert.ok(state.status.projects.includes(state.chosenFolder));
  await page.evaluate(() =>
    localStorage.setItem(
      "cage-projects",
      JSON.stringify(["/fake/local-history"]),
    ),
  );
  await page.reload();
  await page
    .getByRole("region", { name: "项目 design-lab", exact: true })
    .waitFor();
  assert.equal(
    await page.getByText("local-history", { exact: true }).count(),
    0,
  );
  assert.equal(await page.getByText("环境标签", { exact: true }).count(), 0);
  assert.equal(await page.getByText("最近项目", { exact: true }).count(), 0);
  checks.push("真实目录列表刷新读回、不使用本地历史、项目打开失败保留路径");
  await page
    .getByRole("heading", { name: "Cage 环境已就绪", exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "新建终端", exact: true })
    .first()
    .click();
  await page.getByRole("menuitem", { name: "普通终端", exact: true }).click();
  await page.getByRole("tab", { name: "终端 1", exact: true }).waitFor();
  const terminalHeader = page.locator("header.chrome");
  assert.equal(await terminalHeader.locator(".navigation-row").count(), 0);
  assert.equal(await terminalHeader.getByRole("button", { name: "新建终端", exact: true }).count(), 1);
  assert.equal(await terminalHeader.getByRole("button", { name: "design-lab", exact: true }).getAttribute("title"), state.chosenFolder);
  const terminalTab = page.getByRole("tab", { name: "终端 1", exact: true });
  const actions = page.getByRole("group", { name: "终端 1 的操作", exact: true });
  await terminalTab.hover();
  await actions.waitFor();
  await page.getByRole("button", { name: "工作台", exact: true }).hover();
  await actions.waitFor({ state: "hidden" });
  await terminalTab.focus();
  await page.keyboard.press("Tab");
  assert.equal(await page.locator(":focus").getAttribute("aria-label"), "置顶");
  await page.keyboard.press("Enter");
  await actions.getByRole("button", { name: "取消置顶", exact: true }).click();
  const renameButton = actions.getByRole("button", { name: "重命名", exact: true });
  await renameButton.hover();
  await actions.getByText("重命名", { exact: true }).waitFor();
  await renameButton.click();
  await page.getByLabel("标签名称").fill("开发终端");
  await page.getByRole("button", { name: "保存名称" }).click();
  await page.getByRole("tab", { name: "开发终端" }).waitFor();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.getByRole("tab", { name: "新标签页", exact: true }).waitFor();
  await page.getByLabel("网页地址或搜索").fill("localhost:3000");
  await page.getByRole("button", { name: "打开地址", exact: true }).click();
  await page.getByRole("tab", { name: "本地预览", exact: true }).waitFor();
  assert.equal(state.tabs[0].url, "http://localhost:3000/");
  await tabs()
    .getByRole("tab", { name: "本地预览", exact: true })
    .click({ button: "right" });
  await page.getByRole("menuitem", { name: "置顶标签", exact: true }).click();
  assert.equal(await tabs().getByRole("tab").first().innerText(), "本地预览");
  await page.getByRole("button", { name: "打开 design-lab 的编辑器", exact: true }).click();
  await page
    .getByRole("tab", { name: "文件 · design-lab", exact: true })
    .waitFor();
  await page
    .getByRole("button", { name: "关闭 文件 · design-lab", exact: true })
    .click();
  await page.getByRole("dialog", { name: "关闭文件编辑器？" }).waitFor();
  await page
    .getByRole("dialog")
    .last()
    .getByRole("button", { name: "取消", exact: true })
    .click();
  assert.equal(state.tabs.length, 2);
  assert.equal(
    await page.locator(".workspace-sidebar").getByRole("tab").count(),
    1,
  );
  assert.equal(await tabs().getByRole("tab").count(), 2);
  assert.equal(await page.locator(".content-breadcrumb").count(), 0);
  await page.getByRole("button", { name: "新建网页", exact: true }).click();
  await tabs().getByRole("tab", { name: "新标签页", exact: true }).waitFor();
  await tabs()
    .getByRole("tab", { name: "新标签页", exact: true })
    .locator("..")
    .dragTo(
      tabs()
        .getByRole("tab", { name: "文件 · design-lab", exact: true })
        .locator(".."),
    );
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  assert.deepEqual(await tabs().getByRole("tab").allTextContents(), [
    "本地预览",
    "新标签页",
    "文件 · design-lab",
  ]);
  await page.keyboard.press("Meta+2");
  await page.waitForFunction(
    () =>
      document.querySelector('.browser-tabs [aria-selected="true"]')
        ?.textContent === "新标签页",
  );
  await page
    .getByRole("button", { name: "关闭 新标签页", exact: true })
    .click();
  await page.waitForFunction(
    () =>
      document.querySelector('.browser-tabs [aria-selected="true"]')
        ?.textContent === "文件 · design-lab",
  );
  assert.equal(
    state.active,
    state.tabs.find((tab) => tab.kind === "editor").id,
  );
  await page.getByRole("tab", { name: "开发终端", exact: true }).click();
  await page.keyboard.press("Meta+1");
  await page.waitForFunction(
    () =>
      document.querySelector('.sidebar-tabs [aria-selected="true"]')
        ?.textContent === "开发终端",
  );
  const pageCount = state.tabs.length;
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  assert.equal(state.tabs.length, pageCount);
  assert.equal(
    await tabs()
      .getByRole("tab", { name: "文件 · design-lab", exact: true })
      .getAttribute("aria-selected"),
    "true",
  );
  await shot("workspace-browser");
  await page.getByRole("tab", { name: "开发终端", exact: true }).click();
  await shot("workspace-terminal");
  checks.push(
    "浏览器顶部标签、目录下仅终端、浏览器恢复原页、重命名/置顶/排序刷新、分组快捷键、关闭后激活相邻网页",
  );
  const connections = state.terminalConnections;
  await settings();
  await page.getByRole("heading", { name: "通用", exact: true }).waitFor();
  await shot("settings");
  await page.getByRole("radio", { name: /^原生 Linux/ }).check();
  await page
    .getByRole("button", { name: "返回工作台", exact: true })
    .first()
    .click();
  await page.getByRole("dialog", { name: "放弃尚未保存的修改？" }).waitFor();
  await page
    .getByRole("dialog")
    .last()
    .getByRole("button", { name: "取消", exact: true })
    .click();
  assert.equal(
    await page.getByRole("radio", { name: /^原生 Linux/ }).isChecked(),
    true,
  );
  state.failSave = true;
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await page.getByRole("alert").filter({ hasText: "保存失败" }).waitFor();
  assert.equal(
    await page.getByRole("radio", { name: /^原生 Linux/ }).isChecked(),
    true,
  );
  state.failSave = false;
  await page.getByRole("button", { name: "保存设置", exact: true }).click();
  await page
    .getByRole("region", { name: "设置", exact: true })
    .waitFor({ state: "hidden" });
  assert.equal(state.settings.keyboard, "native");
  assert.equal(state.terminalConnections, connections);
  await settings();
  assert.equal(
    await page.getByRole("radio", { name: /^原生 Linux/ }).isChecked(),
    true,
  );
  await page.getByRole("button", { name: "时区与语言", exact: true }).click();
  await page.getByRole("switch", { name: /跟随出口 IP 的时区/ }).uncheck();
  await page.getByLabel("系统时区", { exact: true }).fill("Etc/UTC");
  await page.getByRole("button", { name: "保存并重启", exact: true }).click();
  await page.getByRole("dialog", { name: "应用设置并重启环境？" }).waitFor();
  await page
    .getByRole("dialog")
    .last()
    .getByRole("button", { name: "取消", exact: true })
    .click();
  assert.equal("proxy_url" in state.settings, false);
  checks.push(
    "设置草稿离开确认、保存失败保留输入、保存成功读回、后台终端连接保留、重启取消",
  );
  await page.setViewportSize({ width: 820, height: 558 });
  await shot("settings-minimum");
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  await page.getByRole("button", { name: "时区与语言", exact: true }).click();
  await shot("settings-region-minimum");
  await page.keyboard.press("Escape");
  await page.getByRole("button", { name: "放弃修改", exact: true }).click();
  await page
    .getByRole("button", { name: "添加项目", exact: true })
    .click();
  await page.getByRole("button", { name: "添加本机项目…", exact: true }).click();
  await page
    .getByLabel("新项目文件夹", { exact: true })
    .fill("/Users/shihao/Projects/another-project");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "打开项目", exact: true })
    .click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  assert.equal(
    state.settings.workspace,
    "/Users/shihao/Projects/another-project",
  );
  assert.ok(state.terminals.length > 0);
  assert.ok(state.tabs.length > 0);
  assert.equal(
    await page.getByRole("tab", { name: "开发终端", exact: true }).count(),
    1,
  );
  await page
    .getByRole("button", { name: "在 another-project 新建终端", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "普通终端", exact: true }).click();
  await page
    .getByRole("region", { name: "项目 another-project", exact: true })
    .getByRole("tab", { name: "终端 2", exact: true })
    .waitFor();
  assert.equal(
    state.terminals[1].workspace,
    "/Users/shihao/Projects/another-project",
  );
  await page
    .getByRole("button", { name: "在 design-lab 新建终端", exact: true })
    .click();
  await page.getByRole("menuitem", { name: "普通终端", exact: true }).click();
  await page
    .getByRole("region", { name: "项目 design-lab", exact: true })
    .getByRole("tab", { name: "终端 3", exact: true })
    .waitFor();
  assert.equal(state.terminals[2].workspace, state.chosenFolder);
  assert.equal(
    state.settings.workspace,
    "/Users/shihao/Projects/another-project",
  );
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await shot("browser-minimum");
  assert.equal(
    await page.locator(".workspace-sidebar").getByRole("tab").count(),
    3,
  );
  assert.equal(await tabs().getByRole("tab").count(), 2);
  await page.getByRole("button", { name: "打开 another-project 的编辑器", exact: true }).click();
  await tabs().getByRole("tab", { name: "文件 · another-project", exact: true }).waitFor();
  await page.getByRole("button", { name: "打开 design-lab 的编辑器", exact: true }).click();
  await tabs()
    .getByRole("tab", { name: "文件 · design-lab", exact: true })
    .waitFor();
  assert.equal(state.tabs.filter((tab) => tab.kind === "editor").length, 2);
  assert.equal(state.tabs.find((tab) => tab.id === state.active)?.workspace, state.chosenFolder);
  checks.push(
    "两个项目终端各自归组、指定目录新建不受最后打开项目影响、浏览器与终端保持运行",
  );
  const deleteTab = page.getByRole("tab", { name: "终端 3", exact: true });
  const remove = page.getByRole("group", { name: "终端 3 的操作" }).getByRole("button", { name: "删除" });
  await deleteTab.hover();
  await remove.click();
  const confirmClose = page.getByRole("dialog", { name: "关闭这个终端会话？" });
  await confirmClose.getByRole("button", { name: "取消" }).click();
  assert.equal(state.terminals.length, 3);
  await deleteTab.hover();
  await remove.click();
  await confirmClose.getByRole("button", { name: "关闭终端" }).click();
  await deleteTab.waitFor({ state: "detached" });
  assert.equal(state.terminals.length, 2);
  checks.push("终端行悬停展开、键盘置顶、重命名、取消和确认删除指定终端");
  await page.getByRole("button", { name: "工作台", exact: true }).click();
  await shot("main-minimum");
  await page.getByRole("button", { name: "收起侧栏", exact: true }).click();
  await page.reload();
  await page.getByRole("button", { name: "展开侧栏", exact: true }).waitFor();
  await page.setViewportSize({ width: 375, height: 812 });
  await page.keyboard.press("Meta+t");
  await tabs().waitFor();
  await shot("compact-workspace");
  assert.equal(
    await page.evaluate(
      () => document.documentElement.scrollWidth > innerWidth,
    ),
    false,
  );
  checks.push(
    "运行中项目切换保留终端和网页、820px 最小窗口、侧栏折叠刷新、375px 收起侧栏无横向溢出",
  );
  assert.deepEqual(errors, []);
  await fs.writeFile(
    output + "smoke-result.json",
    JSON.stringify(
      {
        passed: true,
        checks,
        pageErrors: errors,
        scope:
          "Mock API/native picker/terminal socket; no real VM, network or file edit verification.",
      },
      null,
      2,
    ),
  );
  console.log(JSON.stringify({ passed: true, checks }, null, 2));
} finally {
  await browser.close();
}
