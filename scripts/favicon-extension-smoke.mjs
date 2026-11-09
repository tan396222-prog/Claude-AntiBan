import assert from "node:assert/strict";
import http from "node:http";
import { createHash } from "node:crypto";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const extension = fileURLToPath(new URL("../internal/cage/split-extension", import.meta.url));
const extensionId = createHash("sha256").update(extension).digest("hex").slice(0, 32)
  .replace(/./g, digit => String.fromCharCode(97 + parseInt(digit, 16)));
const profile = await mkdtemp(join(tmpdir(), "cage-favicon-extension-"));
const requests = [];
const icons = http.createServer((req, res) => {
  requests.push(req.url);
  res.setHeader("Content-Type", "image/svg+xml");
  const color = req.url.includes("blue") ? "blue" : "red";
  res.end(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><path fill="${color}" d="M0 0h32v32H0z"/></svg>`);
});
await new Promise(resolve => icons.listen(0, "127.0.0.1", resolve));
const iconOrigin = `http://127.0.0.1:${icons.address().port}`;
const site = http.createServer((req, res) => {
  res.setHeader("Content-Type", "text/html");
  res.setHeader("Content-Security-Policy", req.url.startsWith("/blocked")
    ? "default-src 'none'; img-src 'none'; connect-src *"
    : "default-src 'none'; img-src *; connect-src 'none'");
  res.end(`<!doctype html><link rel="icon" href="${iconOrigin}/red.svg"><title>Favicon fixture</title>`);
});
await new Promise(resolve => site.listen(0, "127.0.0.1", resolve));
const origin = `http://localhost:${site.address().port}`;
const launch = () => chromium.launchPersistentContext(profile, {
  executablePath: process.env.CAGE_TEST_BROWSER_PATH,
  channel: "chromium",
  headless: true,
  args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`, "--restore-last-session"],
});
let context;
async function waitFor(action, label) {
  const deadline = Date.now() + 10000;
  while (Date.now() < deadline) {
    const value = await action();
    if (value) return value;
    await new Promise(resolve => setTimeout(resolve, 50));
  }
  throw new Error(`Timed out waiting for ${label}`);
}
async function targetFor(page) {
  const session = await context.newCDPSession(page);
  const { targetInfo } = await session.send("Target.getTargetInfo");
  await session.detach();
  return targetInfo.targetId;
}
async function setupController() {
  const page = await context.newPage();
  await page.goto(`chrome-extension://${extensionId}/control.html`);
  await page.evaluate(() => {
    globalThis.faviconEvents = [];
    globalThis.cageFaviconChanged = target => faviconEvents.push(target);
  });
  return page;
}
async function read(page, target) {
  return page.evaluate(id => readCageFavicon(id), target);
}
try {
  context = await launch();
  let controller = await setupController();
  const blocked = await context.newPage();
  await blocked.goto(origin + "/blocked");
  const blockedTarget = await targetFor(blocked);
  assert.equal((await read(controller, blockedTarget)).favicon, "", "img-src denial must keep the fallback");
  assert.deepEqual(requests, [], "controller must never load the blocked website icon");

  const page = await context.newPage();
  await page.goto(origin + "/allowed");
  const target = await targetFor(page);
  const red = await waitFor(async () => (await read(controller, target))?.favicon, "native favicon despite connect-src denial");
  assert.match(red, /^data:image\/png;base64,/);
  assert.ok(red.length <= 8192);
  const bytes = Buffer.from(red.split(",")[1], "base64");
  assert.equal(bytes.readUInt32BE(16), 32);
  assert.equal(bytes.readUInt32BE(20), 32);
  const nativeRequestCount = requests.length;
  await read(controller, target);
  await read(controller, target);
  assert.equal(requests.length, nativeRequestCount, "cache reads must not request website resources");
  await page.evaluate(() => history.pushState({}, "", "/next"));
  await waitFor(async () => (await read(controller, target))?.favicon === red, "favicon after SPA route change");
  await controller.evaluate(() => { faviconEvents.length = 0; });
  await page.evaluate(url => { document.querySelector('link[rel="icon"]').href = url; }, iconOrigin + "/blue.svg");
  const blue = await waitFor(async () => {
    const result = await read(controller, target);
    return result?.favicon && result.favicon !== red && result.favicon;
  }, "dynamic favicon update");
  await waitFor(() => controller.evaluate(id => faviconEvents.includes(id), target), "favicon invalidation binding");
  assert.notEqual(blue, red);

  // Pause only decoding so real navigation and closure race the production reader.
  await controller.evaluate(id => {
    const decode = HTMLImageElement.prototype.decode;
    globalThis.originalFaviconDecode = decode;
    HTMLImageElement.prototype.decode = async function () {
      await decode.call(this);
      await new Promise(resolve => { globalThis.releaseFaviconDecode = resolve; });
    };
    globalThis.pendingFaviconRead = readCageFavicon(id);
  }, target);
  await controller.waitForFunction(() => typeof releaseFaviconDecode === "function");
  await page.goto(origin + "/blocked-race");
  await controller.evaluate(() => releaseFaviconDecode());
  assert.equal(await controller.evaluate(() => pendingFaviconRead), null, "navigation cannot publish the previous document's icon");
  await controller.evaluate(() => { HTMLImageElement.prototype.decode = originalFaviconDecode; });
  await page.goto(origin + "/allowed");
  await waitFor(async () => (await read(controller, target))?.favicon === red, "favicon after full navigation");
  await controller.evaluate(id => {
    globalThis.releaseFaviconDecode = null;
    HTMLImageElement.prototype.decode = async function () {
      await originalFaviconDecode.call(this);
      await new Promise(resolve => { globalThis.releaseFaviconDecode = resolve; });
    };
    globalThis.pendingFaviconRead = readCageFavicon(id);
  }, target);
  await controller.waitForFunction(() => typeof releaseFaviconDecode === "function");
  await page.close();
  await controller.evaluate(() => releaseFaviconDecode());
  assert.equal(await controller.evaluate(() => pendingFaviconRead), null, "a closed tab cannot publish a late icon");
  assert.equal(await read(controller, target), null);
  const saved = await context.newPage();
  await saved.goto(origin + "/restored");
  await context.close();

  context = await launch();
  controller = await setupController();
  const restored = await waitFor(async () => context.pages().find(tab => tab.url() === origin + "/restored"), "restored browser tab");
  const restoredTarget = await targetFor(restored);
  await waitFor(async () => (await read(controller, restoredTarget))?.favicon === red, "restored tab favicon");
  console.log("PASS: native Chromium icon cache, reciprocal CSP policies, cross-origin icons, 32px bounded PNG, SPA, dynamic updates, navigation/close races and session restoration");
} finally {
  await context?.close();
  await Promise.all([new Promise(resolve => site.close(resolve)), new Promise(resolve => icons.close(resolve))]);
  await rm(profile, { recursive: true, force: true });
}
