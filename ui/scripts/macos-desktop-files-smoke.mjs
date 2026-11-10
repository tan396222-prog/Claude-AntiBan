import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";

const require = createRequire(import.meta.url);
const engine = process.env.CAGE_TEST_BROWSER || "chromium";
const playwright = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const vite = await createServer({ root: fileURLToPath(new URL("..", import.meta.url)), configFile: fileURLToPath(new URL("../vite.config.js", import.meta.url)), server: { host: "127.0.0.1", port: 0 } });
await vite.listen();
const origin = `http://127.0.0.1:${vite.httpServer.address().port}`;
const browser = await playwright[engine].launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1280, height: 850 } });
const png = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVQIHWP4z8DwHwAFgAI/ScLbtAAAAABJRU5ErkJggg==";
const operations = [], errors = [];
let sequence = 0, failUpload = false, chooser = null;
page.on("pageerror", error => errors.push(error.message));
await page.route("**/mac-files-test", async route => route.fulfill({ contentType: "text/html", body: await vite.transformIndexHtml("/mac-files-test", '<div id="root"></div><script type="module" src="/scripts/macos-desktop-files-fixture.jsx"></script>') }));
await page.route("**/api/**", async route => {
  const request = route.request(), url = new URL(request.url());
  if (url.pathname === "/api/macos/claude/window") {
    const value = request.postDataJSON();
    if (value.action === "status") return route.fulfill({ json: { installed: true, running: true, input: true, screen: true, persistent: false, version: "fixture" } });
    if (value.action === "frame") return route.fulfill({ json: { image: png, pid: 100, window: 7, geometry: "stable-window", contentWidth: 640, contentHeight: 480 } });
    return route.fulfill({ json: { ok: true } });
  }
  if (url.pathname === "/api/input/files/desktop/requests") return route.fulfill({ json: { available: true, uploads: chooser ? [chooser] : [], downloads: [], opens: [] } });
  if (url.pathname === "/api/input/files/start") {
    const body = request.postDataJSON();
    operations.push({ action: "start", body });
    return route.fulfill({ json: { ok: true, id: `11111111-2222-4333-8444-${String(++sequence).padStart(12, "0")}` } });
  }
  if (url.pathname.startsWith("/api/input/files/desktop/")) {
    operations.push({ action: "upload", name: url.searchParams.get("name"), bytes: request.postDataBuffer()?.length });
    if (failUpload) { failUpload = false; return route.fulfill({ status: 500, contentType: "text/plain", body: "测试附件传递中断，请重新选择文件" }); }
    return route.fulfill({ json: { ok: true } });
  }
  if (["/api/input/files/finish", "/api/input/files/cancel"].includes(url.pathname)) {
    operations.push({ action: url.pathname.split("/").at(-1), body: request.postDataJSON() });
    chooser = null;
    return route.fulfill({ json: { ok: true } });
  }
  if (url.pathname === "/api/input/image") {
    operations.push({ action: "image", body: request.postDataJSON() });
    return route.fulfill({ json: { ok: true } });
  }
  if (url.pathname === "/api/input/paste/start") return route.fulfill({ json: { available: true, transfer: "22222222-3333-4444-8555-666666666666" } });
  return route.fulfill({ json: { running: true, ready: true, available: true } });
});
const waitForCount = async (action, count) => {
  for (let attempt = 0; attempt < 150; attempt++) {
    if (operations.filter(item => item.action === action).length >= count) return;
    await new Promise(resolve => setTimeout(resolve, 40));
  }
  throw new Error(`Did not observe ${count} ${action} operations`);
};
const choose = async name => {
  const selected = page.waitForEvent("filechooser");
  await page.getByRole("button", { name: "添加附件", exact: true }).click();
  await (await selected).setFiles({ name, mimeType: "text/plain", buffer: Buffer.from("file bytes 中文") });
};
try {
  await page.goto(origin + "/mac-files-test");
  await page.getByAltText("Claude Desktop 客机窗口").waitFor();
  await choose("paper.txt");
  await waitForCount("finish", 1);
  assert.deepEqual(operations.slice(0, 3).map(item => item.action), ["start", "upload", "finish"]);
  assert.equal(operations[0].body.target, "desktop");
  assert.equal(operations[0].body.pid, 100);
  assert.equal(operations[0].body.window, 7);
  assert.equal(operations[1].name, "paper.txt");
  await page.locator(".mac-claude-frame").evaluate(element => {
    const data = new DataTransfer();
    data.items.add(new File(["drop data"], "dropped.txt", { type: "text/plain" }));
    const box = element.getBoundingClientRect();
    element.dispatchEvent(new DragEvent("drop", { bubbles: true, cancelable: true, dataTransfer: data, clientX: box.x + box.width / 2, clientY: box.y + box.height / 2 }));
  });
  await waitForCount("finish", 2);
  assert.equal(operations.filter(item => item.action === "start").at(-1).body.kind, "drop");
  await page.getByLabel("向 Claude Desktop 输入").evaluate((element, png) => {
    const data = new DataTransfer();
    const bytes = Uint8Array.from(atob(png), value => value.charCodeAt(0));
    data.items.add(new File([bytes], "image.png", { type: "image/png" }));
    element.dispatchEvent(new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: data }));
  }, png);
  await waitForCount("image", 1);
  assert.equal(operations.find(item => item.action === "image").body.target, "desktop");
  assert.equal(operations.find(item => item.action === "image").body.pid, 100);
  assert.equal(operations.find(item => item.action === "image").body.window, 7);
  failUpload = true;
  await choose("failed.txt");
  await waitForCount("cancel", 1);
  await page.getByRole("alert").filter({ hasText: "测试附件传递中断" }).waitFor();
  await choose("retry.txt");
  await waitForCount("finish", 3);
  chooser = { id: "99999999-2222-4333-8444-111111111111", kind: "chooser", multiple: true, target: "desktop" };
  await page.getByText("上传本机文件", { exact: true }).waitFor();
  await page.locator('.browser-upload-body input[type="file"]').setInputFiles({ name: "native-dialog.txt", mimeType: "text/plain", buffer: Buffer.from("chooser bytes") });
  await waitForCount("finish", 4);
  assert.equal(operations.filter(item => item.action === "finish").at(-1).body.id, "99999999-2222-4333-8444-111111111111");
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ result: "PASS", engine, checks: ["attachment button", "file drop", "PNG paste", "failed upload cancels", "retry after failure", "native chooser request"], operations: operations.map(({ body, ...item }) => ({ ...item, ...(body ? { target: body.target, kind: body.kind } : {}) })) }));
} finally { await browser.close(); await vite.close(); }
