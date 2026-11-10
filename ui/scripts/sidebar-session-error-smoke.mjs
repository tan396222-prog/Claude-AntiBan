import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = process.env.CAGE_TEST_CAPTURE || "/tmp/cage-sidebar-session-error";
await mkdir(output, { recursive: true });
const original = "Windows Claude Code 尚未就绪，请检查原生组件、图形登录和官方账号登录";
const current = "未能连接 Windows 会话服务，请确认 Windows 环境已启动；持续失败时在设置中导出诊断包";
const longError = (current + "。").repeat(4);
const evidence = [];
for (const [platform, guestSystem] of [["windows", "windows-11"], ["darwin", "macos"], ["linux", "debian-13"]]) {
  let message = original, httpFailure = false, pending = false, release, requests = 0;
  const fixture = await createWorkbench({
    url: process.env.CAGE_UI_URL || "http://127.0.0.1:5217",
    clientContext: { mode: "local", connection_id: "local", client_platform: platform, server_platform: platform, connection: { state: "disconnected" } },
    beforeNavigate: async (_page, state) => {
      state.account.guest_system = state.status.guest_system = guestSystem;
      state.status.ready = state.status.vm_running = true;
    },
    cliRequest: async route => {
      requests++;
      if (pending) await new Promise(resolve => { release = resolve; });
      if (httpFailure) return route.fulfill({ status: 503, contentType: "text/plain; charset=utf-8", body: message });
      return route.fulfill({ json: { sessions: [{ id: "error-history", title: "保留的历史会话", workspace: "/Users/shihao/.claude-cage-workspace" }], notice: message } });
    },
  });
  const { page, browser, errors } = fixture;
  try {
    const card = page.locator(".sidebar-session-error");
    const alert = card.getByRole("alert");
    await alert.getByText(original, { exact: true }).waitFor();
    const retry = card.getByRole("button", { name: "重新读取会话", exact: true });
    await page.getByRole("button", { name: "保留的历史会话", exact: true }).waitFor();
    await retry.waitFor();
    await page.locator(".sidebar-scroll").screenshot({ path: `${output}/${platform}-legacy-message.png` });

    for (const [theme, sidebarWidth, fontSize, error] of [["light", 248, 14, current], ["dark", 220, 14, longError], ["light", 248, 28, current]]) {
      message = error;
      await page.emulateMedia({ colorScheme: theme });
      await page.evaluate(({sidebarWidth, fontSize}) => {
        document.documentElement.style.setProperty("--sidebar-width", `${sidebarWidth}px`);
        document.documentElement.style.setProperty("font-size", `${fontSize}px`);
      }, {sidebarWidth, fontSize});
      await retry.click();
      await alert.getByText(error, { exact: true }).waitFor();
      await page.waitForFunction(() => document.querySelector(".sidebar-session-error > button")?.disabled === false);
      const layout = await card.evaluate(element => {
        const box = node => { const r = node.getBoundingClientRect(); return { top:r.top, bottom:r.bottom, left:r.left, right:r.right }; };
        const button = element.querySelector("button"), paragraph = element.querySelector("p");
        return { card:box(element), paragraph:box(paragraph), button:box(button),
          project:box(element.nextElementSibling), width:element.clientWidth, scrollWidth:element.scrollWidth,
          buttonHeight:button.clientHeight, buttonScrollHeight:button.scrollHeight,
          text:paragraph.textContent };
      });
      assert.ok(layout.button.top >= layout.paragraph.bottom + 8, "retry overlaps the explanation");
      assert.ok(layout.project.top >= layout.card.bottom, "error covers project controls");
      assert.ok(layout.scrollWidth <= layout.width + 1, "error overflows narrow sidebar");
      assert.ok(layout.buttonScrollHeight <= layout.buttonHeight + 1, "retry label is clipped");
      assert.equal(layout.text, error, "long error was truncated");
      // A large-font card can be taller than the scroll viewport. Increase only
      // the capture height so the artifact contains the entire card.
      if (fontSize === 28) await page.setViewportSize({ width:1280, height:1400 });
      await card.screenshot({ path: `${output}/${platform}-${theme}-${fontSize}.png` });
      if (fontSize === 14 && theme === "light") await page.locator(".sidebar-scroll").screenshot({ path: `${output}/${platform}-after.png` });
      if (fontSize === 28) await page.setViewportSize({ width:1280, height:850 });
      evidence.push({ platform, theme, sidebarWidth, fontSize, layout });
    }

    await page.evaluate(() => document.documentElement.style.setProperty("font-size", "14px"));
    message = "会话服务连接中断，请重试";
    httpFailure = true;
    await retry.click();
    await alert.getByText(message, { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector(".sidebar-session-error > button")?.disabled === false);
    assert.equal(await page.getByRole("button", { name: "保留的历史会话", exact: true }).count(), 1, "HTTP failure discarded saved history");

    pending = true;
    await retry.focus();
    await page.keyboard.press("Enter");
    await card.getByRole("button", { name: "正在读取…", exact: true }).waitFor();
    assert.equal(await card.getByRole("button").isDisabled(), true);
    assert.equal(await card.getByRole("button").getAttribute("aria-busy"), "true");
    await alert.getByText(message, { exact: true }).waitFor();
    const inFlight = requests;
    await page.keyboard.press("Enter");
    assert.equal(requests, inFlight, "keyboard submitted a duplicate retry");
    await card.screenshot({ path: `${output}/${platform}-retry.png` });
    httpFailure = pending = false;
    message = "";
    release();
    await card.waitFor({ state: "hidden" });
    await page.getByRole("button", { name: "保留的历史会话", exact: true }).waitFor();
    await page.reload();
    await page.getByRole("button", { name: "保留的历史会话", exact: true }).waitFor();
    assert.equal(await card.count(), 0, "successful reload restored a stale error");
    assert.deepEqual(errors, []);
    console.log(`${platform}: notice/HTTP failure, long text, narrow sidebar, dark theme, 200% text, keyboard retry and recovery passed`);
  } finally { release?.(); await browser.close(); }
}
await writeFile(`${output}/result.json`, JSON.stringify(evidence, null, 2));
