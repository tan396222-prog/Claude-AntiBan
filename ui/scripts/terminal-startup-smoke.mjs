import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const warning = "WARNING: Loading development channels\r\nChannels: server:cage\r\n1. I am using this for local development\r\nEnter to confirm";
for (const platform of ["darwin", "windows", "linux"]) {
  const fixture = await createWorkbench({
    mockWebSockets: false,
    clientContext: { mode: "local", connection_id: "local", client_platform: platform, server_platform: platform, connection: { state: "disconnected" } },
    beforeNavigate: page => page.clock.install(),
  });
  const { page, state } = fixture;
  let terminalSocket;
  const inputs = [];
  try {
    await page.route("**/api/terminals", route => {
      if (route.request().method() !== "POST") return route.fallback();
      const tab = { id: "startup-test", title: "Claude 1", kind: "claude", workspace: state.settings.workspace, cli_session_id: "fixture-cli", cli_starting: true };
      state.terminals = [tab];
      return route.fulfill({ json: tab });
    });
    await page.routeWebSocket("**/api/**", socket => {
      if (!socket.url().includes("/terminals/")) return socket.close();
      terminalSocket = socket;
      socket.onMessage(message => { const value = JSON.parse(message); if (value.type === "input") inputs.push(value.data); });
      socket.send(JSON.stringify({ type: "cli-startup", pending: true }));
      socket.send(Buffer.from(warning.slice(0, 22)));
      socket.send(Buffer.from(warning.slice(22)));
    });
    await page.reload();
    await page.getByRole("button", { name: "启动 Cage 环境", exact: true }).click();
    await page.getByRole("button", { name: "新建终端", exact: true }).first().click();
    await page.getByRole("menuitem", { name: /Claude/ }).first().click();
    await page.getByText("正在准备 Claude 会话…", { exact: true }).waitFor();
    await page.waitForFunction(() => document.querySelector(".xterm-rows")?.textContent.includes("Loading development channels"));
    assert.equal(await page.locator(".terminal-host").isVisible(), false);
    await page.evaluate(() => {
      window.startupFlashes = [];
      const observer = new MutationObserver(() => {
        const host = document.querySelector(".terminal-host");
        const text = host?.querySelector(".xterm-rows")?.textContent || "";
        if (host && getComputedStyle(host).visibility === "visible" && text.includes("Loading development channels")) window.startupFlashes.push(text.slice(0, 250));
      });
      observer.observe(document.querySelector(".terminal-view"), { childList: true, subtree: true, attributes: true });
    });
    terminalSocket.send(JSON.stringify({ type: "cli-startup", pending: false }));
    await page.clock.runFor(120);
    assert.equal(await page.locator(".terminal-host").isVisible(), false, "channel registration must not expose the previous warning screen");
    await page.clock.resume();
    terminalSocket.send(Buffer.from("\x1b[2J\x1b[HWelcome to Claude\r\n> "));
    await page.locator(".terminal-host").waitFor({ state: "visible" });
    assert.deepEqual(await page.evaluate(() => window.startupFlashes), []);
    assert.deepEqual(inputs, [], "presentation never confirms tools or other prompts");
    await page.keyboard.type("hello");
    await page.waitForFunction(() => document.querySelector(".xterm-helper-textarea") === document.activeElement);

    // A new attachment can be waiting for login. It must become interactive
    // without channel readiness, and the user can reveal it immediately.
    terminalSocket.close();
    await page.getByRole("button", { name: "重新连接", exact: true }).click();
    await page.getByText("正在准备 Claude 会话…", { exact: true }).waitFor();
    terminalSocket.send(Buffer.from("\x1b[2J\x1b[HSelect login method\r\n1. Claude account"));
    await page.clock.fastForward(8100);
    await page.clock.resume();
    await page.locator(".terminal-host").waitFor({ state: "visible" });
    assert.ok((await page.locator(".xterm-rows").textContent()).includes("Select login method"));
    terminalSocket.close();
    await page.getByRole("button", { name: "重新连接", exact: true }).click();
    await page.getByText("正在准备 Claude 会话…", { exact: true }).waitFor();
    await page.getByRole("button", { name: "显示启动过程", exact: true }).click();
    await page.locator(".terminal-host").waitFor({ state: "visible" });
    assert.deepEqual(fixture.errors, []);
    console.log(`PASS: ${platform} hides split confirmation output, waits for repaint, restores focus, and exposes stalled/login output`);
  } finally { await fixture.browser.close(); }
}
