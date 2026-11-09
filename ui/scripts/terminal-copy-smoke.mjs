import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

for (const platform of ["MacIntel", "Win32", "Linux x86_64"]) {
  const writes = [], inputs = [];
  const { browser, page, errors } = await createWorkbench({
    mockWebSockets: false,
    nativeRequest({ action, text }) {
      if (action === "clipboardWrite") writes.push(text);
    },
  });
  try {
    let terminalSocket;
    const interrupted = Promise.withResolvers();
    await page.addInitScript((platform) => {
      Object.defineProperty(navigator, "platform", { value: platform });
    }, platform);
    await page.routeWebSocket("**/api/**", (socket) => {
      if (!socket.url().includes("/terminals/")) return socket.close();
      terminalSocket = socket;
      socket.onMessage((message) => {
        const { type, data } = JSON.parse(message);
        if (type === "input") {
          inputs.push(data);
          if (data === "\x03") interrupted.resolve();
        }
      });
      socket.send(Buffer.from("CopyMe1234\r\n$ "));
    });
    await page.reload();
    await page.getByRole("button", { name: "启动 Cage 环境", exact: true }).click();
    await page.getByRole("button", { name: "新建终端", exact: true }).first().click();
    await page.getByRole("menuitem", { name: "普通终端", exact: true }).click();
    const line = await page.locator(".xterm-rows").getByText("CopyMe1234", { exact: true }).first().boundingBox();
    const drag = async () => {
      await page.mouse.move(line.x + 1, line.y + line.height / 2);
      await page.mouse.down();
      await page.mouse.move(line.x + line.width * 0.6, line.y + line.height / 2, { steps: 5 });
      await page.mouse.up();
    };
    const mac = platform === "MacIntel";
    const copy = () => page.keyboard.press(mac ? "Meta+c" : "Control+Shift+C");
    await drag();
    await copy();
    await page.getByText("已复制终端选中文本", { exact: true }).waitFor();
    assert.deepEqual(writes, ["CopyMe"]);

    terminalSocket.send(Buffer.from("\x1b[?1002h\x1b[?1006h"));
    await page.locator(".xterm.enable-mouse-events").waitFor();
    await drag();
    await copy();
    await page.getByText(new RegExp(`请按住 ${mac ? "Option" : "Shift"} 拖选文字`)).waitFor();
    assert.deepEqual(writes, ["CopyMe"]);
    assert.ok(inputs.some(data => data.startsWith("\x1b[<")));

    const previousInputs = inputs.length;
    const modifier = mac ? "Alt" : "Shift";
    await page.keyboard.down(modifier);
    await drag();
    await page.keyboard.up(modifier);
    await copy();
    await page.getByText("已复制终端选中文本", { exact: true }).waitFor();
    assert.deepEqual(writes, ["CopyMe", "CopyMe"]);
    assert.equal(inputs.length, previousInputs);
    await page.keyboard.press("Control+c");
    await Promise.race([
      interrupted.promise,
      new Promise((_, reject) => setTimeout(() => reject(new Error("Ctrl+C 未传给终端")), 3000).unref()),
    ]);
    assert.deepEqual(errors, []);
    console.log(`${platform}: drag selection, mouse tracking, clipboard shortcut and Ctrl+C passed`);
  } finally {
    await browser.close();
  }
}
