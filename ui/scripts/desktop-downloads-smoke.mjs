import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const id = "ef88f909-6ac3-4839-b45d-a9569f34d4ab";
let downloads = [], saved = [], calls = [];
const fixture = await createWorkbench({ nativeRequest: async (body) => {
  if (["saveBrowserDownload", "browserDownloadAction"].includes(body.action)) calls.push(body);
  if (body.action === "saveBrowserDownload") return { canceled: true };
  return { ok: true };
} });
const { browser, page, state, errors } = fixture;
await page.route("**/api/input/files/desktop/requests", (route) => route.fulfill({ json: { uploads: [], downloads } }));
await page.route("**/api/browser/files/saved", (route) => route.fulfill({ json: saved }));
let retries = 0;
await page.route("**/api/input/files/download-retry", async (route) => {
  assert.deepEqual(route.request().postDataJSON(), { id, target: "desktop" });
  retries++;
  await route.fulfill({ json: { ok: true } });
});
const waitFor = async (predicate) => {
  for (let count = 0; count < 120; count++) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("Desktop download UI did not reach expected state");
};
try {
  state.status.ready = true;
  state.status.vm_running = true;
  await page.reload();
  await page.getByRole("button", { name: "下载文件", exact: true }).waitFor();
  // A tiny export can finish between polls; it must still offer Save As.
  const item = { id, name: "桌面视频.mp4", state: "saved", target: "desktop",
    startedAt: new Date(Date.now() + 5).toISOString(), path: "/Users/test/Cage/Downloads/桌面视频.mp4" };
  saved = [{ id, name: item.name, path: item.path }];
  downloads = [item];
  await waitFor(() => calls.some((call) => call.action === "saveBrowserDownload"));
  await page.getByRole("button", { name: "下载文件", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "下载文件" });
  await dialog.getByText(item.path, { exact: true }).waitFor();
  await dialog.getByRole("button", { name: "在文件夹中显示", exact: true }).click();
  await waitFor(() => calls.some((call) => call.action === "browserDownloadAction" && call.id === id));
  await page.reload();
  await page.getByRole("button", { name: "下载文件", exact: true }).click();
  await dialog.getByText(item.path, { exact: true }).waitFor();
  assert.equal(calls.filter((call) => call.action === "saveBrowserDownload").length, 1, "history reopened a save dialog");
  saved = [];
  downloads = [{ ...item, state: "failed", retryable: true, path: "", error: "共享目录暂时不可写" }];
  await page.reload();
  await page.getByRole("button", { name: "下载文件", exact: true }).click();
  await dialog.getByRole("button", { name: "重试保存", exact: true }).click();
  await waitFor(() => retries === 1);
  assert.equal(await dialog.getByRole("button", { name: "另存为…", exact: true }).count(), 0);
  assert.deepEqual(errors, []);
  console.log("PASS: Desktop instant export, native Save As cancel retains fallback, reveal, history does not reprompt, failed export uses Desktop retry");
} finally { await browser.close(); }
