import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const parts = [];
for await (const chunk of process.stdin) parts.push(chunk);
const input = JSON.parse(Buffer.concat(parts).toString());
const fixture = await createWorkbench({
  url: input.origin,
  mockWebSockets: false,
  cliRequest: route => route.continue(),
  beforeNavigate: page => page.context().addCookies([{ name: input.cookie, value: input.session, url: input.origin }]),
});
try {
  const { page, state } = fixture;
  await page.route("**/api/terminals**", route => route.continue());
  state.status.ready = state.status.vm_running = true;
  state.status.projects = [input.project];
  state.status.environment_directory = state.settings.workspace = input.project;
  let output = "";
  page.on("websocket", socket => {
    if (!socket.url().includes("/terminals/")) return;
    socket.on("framereceived", ({ payload }) => { output = (output + payload.toString()).slice(-65536); });
  });
  await page.reload();
  await page.evaluate(() => {
    window.visibleStartupWarnings = 0;
    const check = () => {
      for (const host of document.querySelectorAll(".terminal-host")) {
        const text = host.querySelector(".xterm-rows")?.textContent.replace(/\s/g, "") || "";
        if (getComputedStyle(host).visibility === "visible" && text.includes("Loadingdevelopmentchannels")) window.visibleStartupWarnings++;
      }
    };
    new MutationObserver(check).observe(document.body, { attributes: true, childList: true, subtree: true });
  });
  await page.getByRole("button", { name: "新建终端", exact: true }).first().click();
  await page.getByRole("menuitem", { name: "Claude 终端", exact: true }).click();
  await page.getByText("正在准备 Claude 会话…", { exact: true }).waitFor();
  await page.locator(".terminal-host").waitFor({ state: "visible" });
  await page.waitForFunction(() => document.querySelector(".xterm-rows")?.textContent.includes("Claude Code"));
  const text = output.replace(/\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)/g, "").replace(/\s/g, "");
  assert.ok(text.includes("Loadingdevelopmentchannels"), "the genuine CLI confirmation must still exist in the raw output");
  assert.equal(await page.evaluate(() => window.visibleStartupWarnings), 0, "the genuine startup confirmation flashed on screen");
  assert.deepEqual(fixture.errors, []);
  console.log("PASS: genuine Claude Code, isolated SSH/PTY and product WebSocket reach the desktop session without displaying the channel confirmation");
} finally { await fixture.browser.close(); }
