import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = fileURLToPath(new URL("../../docs/test-reports/2026-11-07-paired-phone-model/", import.meta.url));
await fs.mkdir(output, { recursive: true });
let paired = true, device = null, rejectRemove = false, removals = 0;
let power = {active:true};
let holdNextStatus = false, releaseStatus;
let signalStatus, signalDelivered;
const statusHeld = new Promise(resolve => { signalStatus = resolve; });
const statusDelivered = new Promise(resolve => { signalDelivered = resolve; });
const { browser, page, errors } = await createWorkbench({
  beforeNavigate: async page => page.route("**/api/mobile-pair", async route => {
    if (route.request().method() === "GET" && holdNextStatus) {
      holdNextStatus = false;
      const snapshot = { paired, status: "", device };
      await new Promise(resolve => { releaseStatus = resolve; signalStatus(); });
      await route.fulfill({ json: snapshot });
      signalDelivered();
      return;
    }
    if (route.request().method() === "DELETE") {
      if (rejectRemove) return route.fulfill({ status: 500, contentType:"text/plain; charset=utf-8", body: "移除失败，请重试" });
      paired = false; device = null; removals++;
    }
    return route.fulfill({ json: { paired, status: "", device, keep_awake:power } });
  }),
});
async function open() {
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("navigation", { name: "设置分组" }).getByRole("button", { name: "远程连接", exact: true }).click();
  await page.getByRole("heading", { name: "手机已配对", exact: true }).waitFor();
}
async function confirmRemoval() {
  await page.getByRole("button", { name: "移除配对手机", exact: true }).click();
  await page.getByRole("heading", { name: "移除配对手机？", exact: true }).waitFor();
}
try {
  await open();
  await page.getByText("远程待机已请求主机保持运行，屏幕可正常熄灭。移除配对或结束后台服务后恢复原系统休眠策略。",{exact:true}).waitFor();
  power = {active:false,error:"测试：系统未允许防休眠"};
  await page.getByRole("alert").filter({hasText:"主机防休眠未生效：测试：系统未允许防休眠"}).waitFor();
  power = {active:true};
  await page.getByRole("alert").filter({hasText:"主机防休眠未生效"}).waitFor({state:"hidden"});
  await page.getByText("型号待获取，请更新手机端 Cage 后重新连接。", { exact: true }).waitFor();
  device = { platform: "android", model: "Google Pixel 9 Pro" };
  await page.getByLabel("已配对手机", { exact: true }).getByText(device.model, { exact: true }).waitFor();
  assert.equal(await page.getByLabel("已配对手机", { exact: true }).innerText(), "Google Pixel 9 Pro\nAndroid");
  await page.screenshot({ path: output + "android-model.png", fullPage: true });
  await page.reload(); await open();
  await page.getByLabel("已配对手机", { exact: true }).getByText(device.model, { exact: true }).waitFor();
  await confirmRemoval();
  await page.getByText("移除 Google Pixel 9 Pro 后，这部手机需要重新扫码才能访问工作台。", { exact: true }).waitFor();
  await page.getByRole("button", { name: "取消", exact: true }).click();
  assert.equal(removals, 0);

  device = { platform: "ios", model: "iPhone 16 Pro" };
  await page.getByLabel("已配对手机", { exact: true }).getByText(device.model, { exact: true }).waitFor();
  await page.getByLabel("已配对手机", { exact: true }).getByText("iOS", { exact: true }).waitFor();
  await page.screenshot({ path: output + "iphone-model.png", fullPage: true });
  rejectRemove = true;
  await confirmRemoval();
  await page.getByRole("button", { name: "移除手机", exact: true }).click();
  await page.getByText("移除失败，请重试", { exact: true }).waitFor();
  await page.getByLabel("已配对手机", { exact: true }).getByText(device.model, { exact: true }).waitFor();
  rejectRemove = false;
  holdNextStatus = true;
  await statusHeld;
  await confirmRemoval();
  await page.getByRole("button", { name: "移除手机", exact: true }).click();
  await page.getByRole("heading", { name: "用手机连接 Cage", exact: true }).waitFor();
  releaseStatus();
  await statusDelivered;
  await page.getByRole("heading", { name: "用手机连接 Cage", exact: true }).waitFor();
  assert.equal(await page.getByLabel("已配对手机", { exact: true }).count(), 0);
  assert.equal(removals, 1);
  assert.deepEqual(errors, []);
  console.log("Paired phone model: legacy fallback, live Android/iOS metadata, reload, cancel, failed removal, successful removal and stale polling response passed.");
} finally { await browser.close(); }
