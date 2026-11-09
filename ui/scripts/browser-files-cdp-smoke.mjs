import assert from "node:assert/strict";
import { createRequire } from "node:module";
import http from "node:http";
import { mkdtemp, readFile, rm, mkdir, copyFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Readable } from "node:stream";
import { UploadManager } from "../../internal/cage/browser-uploads.mjs";
import { DownloadManager } from "../../internal/cage/browser-downloads.mjs";
const require = createRequire(import.meta.url);
const { chromium } = require(
  process.env.CAGE_PLAYWRIGHT_MODULE || "playwright",
);
const directory = await mkdtemp(join(tmpdir(), "cage-browser-files-"));
const bytes = Buffer.alloc(8 * 1024 * 1024);
for (let i = 0; i < bytes.length; i++) bytes[i] = i % 251;
const received = [];
const server = http.createServer(async (request, response) => {
  const url = new URL(request.url, "http://localhost");
  if (url.pathname === "/download") {
    response.writeHead(200, {
      "Content-Type": "application/octet-stream",
      "Content-Disposition": 'attachment; filename="video.mp4"',
      "Content-Length": bytes.length,
    });
    response.end(bytes);
    return;
  }
  if (url.pathname === "/received") {
    const chunks = [];
    for await (const chunk of request) chunks.push(chunk);
    received.push({
      name: url.searchParams.get("name"),
      bytes: Buffer.concat(chunks),
    });
    response.end("ok");
    return;
  }
  response.setHeader("Content-Type", "text/html;charset=utf-8");
  response.end(`<label>上传文件<input type="file" multiple></label><p id="names"></p><div id="dropzone" contenteditable="true" style="height:160px">拖入附件</div><button>发送附件</button><a href="/download">下载测试视频</a><script>
  const zone=document.querySelector('#dropzone'); zone.ondragover=e=>e.preventDefault(); zone.ondrop=async e=>{e.preventDefault(); for(const file of e.dataTransfer.files) await fetch('/received?name='+encodeURIComponent(file.name),{method:'POST',body:file}); zone.textContent='附件完成'}; const input=document.querySelector('input');input.onchange=()=>document.querySelector('#names').textContent=Array.from(input.files).map(f=>f.name).join(',');
  document.querySelector('button').onclick=async()=>{for(const file of input.files)await fetch('/received?name='+encodeURIComponent(file.name),{method:'POST',body:file});document.querySelector('button').textContent='发送完成'};
  </script>`);
});
await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
const browser = await chromium.launch({
  headless: true,
  executablePath: process.env.CAGE_TEST_BROWSER_PATH,
});
const context = await browser.newContext();
const page = await context.newPage();
const cdp = await context.newCDPSession(page);
const errors = [];
const uploads = new UploadManager({
  directory: join(directory, "uploads"),
  cdp: (method, params) => cdp.send(method, params),
});
cdp.on("Page.fileChooserOpened", (event) => {
  void uploads
    .opened("tab-1", "session-1", event)
    .catch((error) => errors.push(error.message));
});
cdp.on("Page.frameNavigated", (event) => {
  if (!event.frame.parentId) uploads.cancelTab("tab-1");
});
const waitFor = async (condition) => {
  for (let i = 0; i < 200; i++) {
    if (condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error("browser file operation timed out");
};
try {
  await cdp.send("Page.enable");
  await cdp.send("Page.setInterceptFileChooserDialog", { enabled: true });
  await page.goto(`http://127.0.0.1:${server.address().port}`);
  await page.getByLabel("上传文件").click();
  await waitFor(() => uploads.snapshot().length === 1);
  let request = uploads.snapshot()[0];
  await uploads.receive(
    request.id,
    "参考视频.mp4",
    Readable.from([bytes.subarray(0, 3000), bytes.subarray(3000)]),
  );
  await uploads.receive(
    request.id,
    "参考文档.txt",
    Readable.from([Buffer.from("多文件上传\n中文🙂")]),
  );
  await uploads.finish(request.id);
  await page
    .locator("#names")
    .filter({ hasText: "参考视频.mp4,参考文档.txt" })
    .waitFor();
  await page.getByRole("button", { name: "发送附件" }).click();
  await page.getByRole("button", { name: "发送完成" }).waitFor();
  assert.deepEqual(received[0].bytes, bytes);
  assert.equal(received[1].bytes.toString(), "多文件上传\n中文🙂");
  await page.getByLabel("上传文件").click();
  await waitFor(() => uploads.snapshot().length === 1);
  request = uploads.snapshot()[0];
  uploads.cancel(request.id);
  assert.equal(
    await page.locator("input").evaluate((input) => input.files.length),
    2,
    "cancel must retain the prior selection",
  );
  await page.getByLabel("上传文件").click();
  await waitFor(() => uploads.snapshot().length === 1);
  request = uploads.snapshot()[0];
  await page.reload();
  await assert.rejects(uploads.finish(request.id), /关闭/);

  for (const kind of ["drop", "paste"]) {
    await page.locator("#dropzone").focus();
    const box = await page.locator("#dropzone").boundingBox();
    const viewport = page.viewportSize();
    const batch = uploads.start("tab-1", "session-1", { kind, x: (box.x + box.width / 2) / viewport.width, y: (box.y + box.height / 2) / viewport.height });
    await uploads.receive(batch.id, `原视频-${kind}.mp4`, Readable.from([bytes]));
    await uploads.receive(batch.id, `中文-${kind}.txt`, Readable.from([Buffer.from("保留原文件🙂")]));
    await uploads.finish(batch.id);
    await waitFor(() => received.length === (kind === "drop" ? 4 : 6));
    assert.deepEqual(received.at(-2).bytes, bytes);
    assert.equal(received.at(-2).name, `原视频-${kind}.mp4`);
    assert.equal(received.at(-1).bytes.toString(), "保留原文件🙂");
  }

  const workspace = join(directory, "workspace");
  await mkdir(workspace);
  const downloads = new DownloadManager({
    directory,
    save: async (id, name) => {
      const path = join(workspace, name);
      await copyFile(join(directory, "Downloads", id), path);
      return { ok: true, name, path, bytes: bytes.length };
    },
  });
  const root = await browser.newBrowserCDPSession();
  const { targetInfo } = await cdp.send("Target.getTargetInfo");
  root.on("Browser.downloadWillBegin", (event) =>
    downloads.begin(event.guid, event.suggestedFilename),
  );
  root.on("Browser.downloadProgress", (event) => downloads.progress(event));
  await root.send("Browser.setDownloadBehavior", {
    behavior: "allowAndName",
    downloadPath: join(directory, "Downloads"),
    browserContextId: targetInfo.browserContextId,
    eventsEnabled: true,
  });
  await page.getByRole("link", { name: "下载测试视频" }).click();
  await waitFor(() => downloads.snapshot()[0]?.state === "saved");
  assert.deepEqual(await readFile(downloads.snapshot()[0].path), bytes);
  assert.deepEqual(errors, []);
  console.log(
    "PASS: real Chromium chooser interception, 8 MB binary + Unicode chooser/drop/paste uploads, cancel preserves selection, navigation cancels stale chooser, download progress and exact saved bytes",
  );
} finally {
  await browser.close();
  server.closeAllConnections();
  await new Promise((resolve) => server.close(resolve));
  await rm(directory, { recursive: true, force: true });
}
