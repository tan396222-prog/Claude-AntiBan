import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const calls = [];
const view = {
  mode: "local", desktop_gateway: true, connection_id: "test-local-view",
  connection: { state: "connected" },
};
const { page, browser, errors } = await createWorkbench({
  clientContext: view,
  nativeRequest: async (request) => { calls.push(request); return null; },
});
const quit = () => page.evaluate(() => window.cageNativeAction("quit"));
const exits = () => calls.filter(({ action }) => action === "quitNow").length;
const refused = (route) => route.abort("connectionrefused");
const stops = [];
page.on("request", (request) => {
  if (new URL(request.url()).pathname === "/api/stop") stops.push(request);
});
try {
  await page.exposeFunction("testDesktopExits", exits);
  // Lose the gateway while the workbench still shows the previous environment.
  await page.route("**/api/**", refused);
  await page.locator(".connection-banner").waitFor();
  await quit();
  const dialog = page.getByRole("dialog", { name: "退出 Cage" });
  await dialog.getByRole("button", { name: "取消" }).click();
  assert.equal(exits(), 0);
  await quit();
  await dialog.getByRole("button", { name: "仅退出桌面应用" }).click();
  await page.waitForFunction(async () => (await window.testDesktopExits()) === 1, null, { timeout: 5000 });
  assert.equal(exits(), 1);

  // Initial context cannot be loaded on reopening the page.
  await page.reload();
  await page.getByRole("alert").getByRole("button", { name: "重新连接" }).waitFor();
  await quit();
  assert.equal(exits(), 2);

  // Gateway is alive, but its selected CLI controller has exited.
  view.connection = { state: "failed", error_code: "connection_lost" };
  await page.unroute("**/api/**", refused);
  await page.reload();
  await page.getByRole("heading", { name: "重新连接本机工作台" }).waitFor();
  await quit();
  assert.equal(exits(), 3);
  assert.equal(stops.length, 0);
  assert.deepEqual(errors, []);
} finally {
  await browser.close();
}
