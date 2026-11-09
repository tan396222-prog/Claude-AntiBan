import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";
const id = "34e399c1-78fe-4cf1-93b0-d7fef431b211";
const chooser = "61c716bb-5e8f-4a32-b872-cfa099a695ef";
const nativeCalls = [],
  uploads = [];
let saved = [],
  selections = [],
  cancelSave = true;
const download = {
  id,
  name: "video.mp4",
  state: "downloading",
  receivedBytes: 3,
  totalBytes: 100,
};
const fixture = await createWorkbench({
  nativeRequest: async (body) => {
    if (body.action === "chooseUploadFiles") { nativeCalls.push(body); return { fileSelection: "fixture-selection", count: 2 }; }
    if (body.action === "uploadHostFiles") { nativeCalls.push(body); assert.equal(body.target, "browser"); assert.equal(body.id, chooser); assert.equal(body.selection, "fixture-selection"); return {ok:true}; }
    if (body.action === "saveBrowserDownload") {
      nativeCalls.push(body);
      if (cancelSave) return { canceled: true };
      saved = [
        { id, name: "视频.mp4", path: "/Users/test/Downloads/视频.mp4" },
      ];
      return saved[0];
    }
    if (body.action === "browserDownloadAction") {
      nativeCalls.push(body);
      return { ok: true };
    }
    return null;
  },
});
const { browser, page, state, errors } = fixture;
await page.route("**/api/browser/tabs", (route) =>
  route.fulfill({
    json: {
      tabs: state.tabs,
      active: state.active,
      downloads: [download],
      uploads: selections,
    },
  }),
);
await page.route("**/api/browser/files/saved", (route) =>
  route.fulfill({ json: saved }),
);
await page.route("**/api/browser/files/uploads/**", async (route) => {
  uploads.push({
    name: new URL(route.request().url()).searchParams.get("name"),
    bytes: route.request().postDataBuffer(),
  });
  await route.fulfill({ json: { ok: true } });
});
await page.route("**/api/browser/upload-finish", (route) => {
  assert.equal(route.request().postDataJSON().id, chooser);
  selections = [];
  return route.fulfill({ json: { ok: true } });
});
await page.route("**/api/browser/upload-cancel", (route) => {
  selections = [];
  return route.fulfill({ json: { ok: true } });
});
const poll = async (condition) => {
  for (let i = 0; i < 120; i++) {
    if (condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 50));
  }
  throw new Error("UI state timeout");
};
try {
  state.status.ready = true;
  state.status.vm_running = true;
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await page.getByRole("button", { name: "浏览器下载", exact: true }).click();
  await page.getByText("正在下载", { exact: false }).waitFor();
  await page
    .getByRole("dialog", { name: "下载文件" })
    .getByRole("button", { name: "关闭面板" })
    .click();
  selections = [{ id: chooser, tabId: "tab-1", multiple: true }];
  await page.getByRole("dialog", { name: "上传本机文件" }).waitFor();
  Object.assign(download, {
    state: "saved",
    path: "/Users/test/Cage/Downloads/video.mp4",
  });
  await page.getByRole("button", { name: "选择本机文件", exact: true }).click();
  await page.getByRole("dialog", { name: "上传本机文件" }).waitFor({ state: "hidden" });
  assert.ok(nativeCalls.some(call => call.action === "uploadHostFiles"));
  await poll(() =>
    nativeCalls.some((call) => call.action === "saveBrowserDownload"),
  );
  assert.equal(saved.length, 0, "cancel must not invent a save destination");
  await page.getByRole("button", { name: "浏览器下载", exact: true }).click();
  await page
    .getByText("/Users/test/Cage/Downloads/video.mp4", { exact: true })
    .waitFor();
  cancelSave = false;
  await page.getByRole("button", { name: "另存为…", exact: true }).click();
  await page
    .getByText("/Users/test/Downloads/视频.mp4", { exact: true })
    .waitFor();
  await page
    .getByRole("dialog", { name: "下载文件" })
    .getByRole("button", { name: "打开文件", exact: true })
    .click();
  await page
    .getByRole("button", { name: "在文件夹中显示", exact: true })
    .click();
  await poll(
    () =>
      nativeCalls.filter((call) => call.action === "browserDownloadAction")
        .length === 2,
  );
  assert.deepEqual(
    nativeCalls
      .filter((call) => call.action === "browserDownloadAction")
      .map((call) => [call.operation, call.id]),
    [
      ["open", id],
      ["reveal", id],
    ],
  );
  assert.ok(
    nativeCalls.every((call) => !("path" in call)),
    "web UI must not send arbitrary host paths",
  );
  await page
    .getByRole("dialog", { name: "下载文件" })
    .getByRole("button", { name: "关闭面板" })
    .click();
  selections = [{ id: "a8af5851-5837-45ca-a820-1a2565bc7ec6", tabId: "tab-1", multiple: false }];
  await page
    .getByRole("dialog", { name: "上传本机文件" })
    .getByRole("button", { name: "取消上传" })
    .click();
  await page
    .getByRole("dialog", { name: "上传本机文件" })
    .waitFor({ state: "hidden" });
  state.status.ready = false;
  state.status.vm_running = false;
  await page.waitForFunction(
    () =>
      document.querySelector('[aria-label="打开浏览器"]')?.disabled === true,
  );
  await page.getByRole("button", { name: "下载文件", exact: true }).click();
  await page
    .getByText("/Users/test/Downloads/视频.mp4", { exact: true })
    .waitFor();
  await page
    .getByRole("dialog", { name: "下载文件" })
    .getByRole("button", { name: "打开文件", exact: true })
    .click();
  await poll(
    () =>
      nativeCalls.filter((call) => call.action === "browserDownloadAction")
        .length === 3,
  );
  assert.deepEqual(errors, []);
  console.log(
    "PASS: workbench native picker with opaque selection handle, multiple files, cancel, deferred Save As, canceled Save As keeps fallback, saved path, Open/Reveal IDs",
  );
} finally {
  await browser.close();
}
