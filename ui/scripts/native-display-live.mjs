import assert from "node:assert/strict";
import { pathToFileURL } from "node:url";
import path from "node:path";
import { writeFile } from "node:fs/promises";
import { execFile } from "node:child_process";
import { promisify } from "node:util";

const { chromium } = await import(pathToFileURL(process.env.CAGE_NATIVE_DISPLAY_PLAYWRIGHT).href);
let input = "";
for await (const chunk of process.stdin) input += chunk;
const config = JSON.parse(input);
const browser = await chromium.launch({
  executablePath: process.env.CAGE_NATIVE_DISPLAY_CHROMIUM || path.join(process.env["ProgramFiles(x86)"], "Microsoft", "Edge", "Application", "msedge.exe"),
  headless: true,
});
const measurements = [], errors = [];
let received = 0, bytes = 0, opened = 0, closed = 0;
const context = await browser.newContext({ viewport: { width: 1440, height: 950 }, deviceScaleFactor: 1 });
await context.addCookies(config.cookies.map(cookie => ({ ...cookie, url: config.url, httpOnly: true, sameSite: "Strict" })));
const page = await context.newPage();
page.setDefaultTimeout(45000);
page.on("pageerror", error => errors.push(error.message));
const diagnostics = [];
page.on("console", message => {
  if (["error", "warning"].includes(message.type())) diagnostics.push(message.text());
});
page.on("response", response => {
  if (response.status() >= 400) diagnostics.push(`${response.status()} ${new URL(response.url()).pathname}`);
});
page.on("websocket", socket => {
  if (!new URL(socket.url()).pathname.endsWith("/vnc")) return;
  opened++;
  socket.on("close", () => { closed++; });
  socket.on("framereceived", event => { received++; bytes += Buffer.byteLength(event.payload); });
});
const canvas = page.locator(".remote-canvas canvas:visible").last();
const execute = promisify(execFile);
async function cpuSample() {
  if (!process.env.CAGE_NATIVE_DISPLAY_SAMPLE) return null;
  const result = await execute("powershell.exe", ["-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", process.env.CAGE_NATIVE_DISPLAY_SAMPLE, "-ServiceProcessId", String(process.ppid)]);
  return JSON.parse(result.stdout.replace(/^\uFEFF/, ""));
}
async function measure(name, milliseconds) {
  const before = { received, bytes, opened, closed }, start = performance.now();
  const cpuBefore = await cpuSample();
  await new Promise(resolve => setTimeout(resolve, milliseconds));
  const cpuAfter = await cpuSample();
  const sample = { name, milliseconds: performance.now() - start, messages: received - before.received, bytes: bytes - before.bytes, opened: opened - before.opened, closed: closed - before.closed };
  if (cpuBefore && cpuAfter) {
    sample.guest_cpu_seconds = cpuAfter.guest.cpu - cpuBefore.guest.cpu;
    sample.guest_process_cpu_percent_one_core = 100000 * sample.guest_cpu_seconds / (cpuAfter.guest.timestamp - cpuBefore.guest.timestamp);
    sample.guest_process_groups = Object.fromEntries(Object.entries(cpuAfter.guest.groups || {}).map(([name, value]) => [name, 100000 * (value - (cpuBefore.guest.groups?.[name] || 0)) / (cpuAfter.guest.timestamp - cpuBefore.guest.timestamp)]));
    sample.host_service_cpu_seconds = cpuAfter.hostCPU - cpuBefore.hostCPU;
    sample.host_service_cpu_percent_one_core = 100000 * sample.host_service_cpu_seconds / (cpuAfter.timestamp - cpuBefore.timestamp);
  }
  measurements.push(sample);
  return sample;
}
async function point(x, y) {
  const position = await canvas.evaluate((element, coordinate) => {
    const box = element.getBoundingClientRect();
    return { x: box.x + coordinate.x * 2 / element.width * box.width, y: box.y + coordinate.y * 2 / element.height * box.height };
  }, { x, y });
  await page.mouse.click(position.x, position.y);
}
try {
  await page.goto(config.url);
  await page.getByRole("navigation", { name: "主导航" }).getByRole("button", { name: "打开浏览器", exact: true }).click();
  const address = page.locator(".address-bar input").first();
  await address.fill(config.fixture);
  await address.press("Enter");
  await page.waitForFunction(() => [...document.querySelectorAll(".remote-canvas canvas")].some(element => {
    if (element.getBoundingClientRect().width < 100 || element.width < 320 || element.height < 740) return false;
    const pixel = element.getContext("2d").getImageData(150, 650, 1, 1).data;
    return Math.abs(pixel[0] - 220) < 20 && Math.abs(pixel[1] - 80) < 20;
  }));
  await measure("warmup", 1500);
  const idle = await measure("static", 5000);
  assert.equal(idle.closed, 0, "static frames disconnected the native bridge");
  assert.equal(idle.opened, 0, "static frames triggered reconnection");
  assert(idle.bytes < 65536, `static frames repeatedly transferred pixels (${idle.bytes} bytes)`);
  await page.screenshot({ path: path.join(config.evidence, "windows-browser-static.png") });

  await point(120, 65);
  const typed = performance.now();
  await page.keyboard.type("Cage live 123", { delay: 30 });
  await page.waitForFunction(() => [...document.querySelectorAll(".remote-canvas canvas")].some(element => {
    if (element.getBoundingClientRect().width < 100 || element.width < 320) return false;
    const pixel = element.getContext("2d").getImageData(20, 20, 1, 1).data;
    return Math.abs(pixel[0] - 40) < 20 && Math.abs(pixel[1] - 160) < 20;
  }));
  measurements.push({ name: "input_to_visible_confirmation", milliseconds: performance.now() - typed });
  await point(600, 100);
  await point(120, 175);
  await page.waitForFunction(() => [...document.querySelectorAll(".remote-canvas canvas")].some(element => {
    if (element.getBoundingClientRect().width < 100 || element.height < 740) return false;
    const pixels = element.getContext("2d").getImageData(0, 650, element.width, 1).data;
    const red = x => Math.abs(pixels[x * 4] - 220) < 20 && Math.abs(pixels[x * 4 + 1] - 80) < 20;
    return !red(90) && Array.from({ length: 80 }, (_, index) => 160 + index * 10).some(x => x < element.width && red(x));
  }), null, { timeout: 5000 });
  const motion = await measure("animation", 4500);
  assert(motion.bytes > 65536, "animation produced no changed-frame payload");
  assert.equal(motion.closed, 0);
  await page.waitForFunction(() => [...document.querySelectorAll(".remote-canvas canvas")].some(element => {
    if (element.getBoundingClientRect().width < 100 || element.height < 740) return false;
    const pixel = element.getContext("2d").getImageData(150, 650, 1, 1).data;
    return Math.abs(pixel[0] - 220) < 20 && Math.abs(pixel[1] - 80) < 20;
  }));
  await page.mouse.wheel(0, 650);
  const scrolling = await measure("scroll", 800);
  assert(scrolling.bytes > 0, "scrolling did not update the rendered page");
  assert.equal(scrolling.closed, 0);
  await page.keyboard.press("Control+Home");

  await page.keyboard.press("Control+f");
  const menu = await measure("native_find", 1200);
  assert(menu.bytes > 1000, "native find UI did not update the frame");
  await page.screenshot({ path: path.join(config.evidence, "windows-browser-native-find.png") });
  await page.keyboard.press("Escape");
  const previousWidth = await canvas.evaluate(element => element.width);
  await page.setViewportSize({ width: 1280, height: 850 });
  await page.waitForFunction(width => [...document.querySelectorAll(".remote-canvas canvas")].some(element => element.getBoundingClientRect().width > 100 && element.width >= 320 && element.width !== width), previousWidth);
  await page.screenshot({ path: path.join(config.evidence, "windows-browser-resized.png") });

  const edge = await canvas.evaluate(element => ({ x: element.width / 2 - 90, y: element.height / 2 - 42 }));
  await point(edge.x, edge.y);
  await page.waitForFunction(() => [...document.querySelectorAll(".remote-canvas canvas")].some(element => {
    if (element.getBoundingClientRect().width < 100 || element.width < 320 || element.height < 240) return false;
    const pixel = element.getContext("2d").getImageData(element.width - 100, element.height - 50, 1, 1).data;
    return Math.abs(pixel[0] - 90) < 20 && Math.abs(pixel[1] - 60) < 20 && Math.abs(pixel[2] - 210) < 20;
  }), null, { timeout: 5000 });
  measurements.push({ name: "resized_edge_click", confirmed: true });

  await page.evaluate(() => { window.cageWindowVisible = false; window.dispatchEvent(new Event("cage-window-visibility")); });
  await measure("hide_transition", 500);
  const hidden = await measure("hidden", 2000);
  assert.equal(hidden.messages, 0, "hidden workbench retained its frame stream");
  await page.evaluate(() => { window.cageWindowVisible = true; window.dispatchEvent(new Event("cage-window-visibility")); });
  await canvas.waitFor();
  const resumed = await measure("resume", 2000);
  assert(resumed.bytes > 0, "restoring the workbench did not deliver pixels");
  if (process.env.CAGE_NATIVE_DISPLAY_DESKTOP === "1") {
    await page.getByRole("navigation", { name: "主导航" }).getByRole("button", { name: "Claude Desktop", exact: true }).click();
    await page.waitForFunction(() => [...document.querySelectorAll('section[aria-label="Claude Desktop"] canvas')].some(element => {
      if (element.getBoundingClientRect().width < 100 || element.width < 320 || element.height < 240) return false;
      const pixels = element.getContext("2d").getImageData(0, 0, element.width, element.height).data;
      for (let y = 0; y < element.height; y += 8) for (let x = 0; x < element.width; x += 8) {
        const index = (y * element.width + x) * 4;
        if (pixels[index] > 180 && pixels[index + 1] > 70 && pixels[index + 1] < 160 && pixels[index + 2] < 130) return true;
      }
      return false;
    }), null, { timeout: 75000 });
    await page.screenshot({ path: path.join(config.evidence, "windows-claude-live.png") });
    const desktop = await measure("claude_visible", 5000);
    assert.equal(desktop.closed, 0, "the official Claude window disconnected while visible");
    await page.evaluate(() => { window.cageWindowVisible = false; window.dispatchEvent(new Event("cage-window-visibility")); });
    await measure("claude_hide_transition", 500);
    const desktopHidden = await measure("claude_hidden", 2000);
    assert.equal(desktopHidden.messages, 0);
    await page.evaluate(() => { window.cageWindowVisible = true; window.dispatchEvent(new Event("cage-window-visibility")); });
    const desktopResumed = await measure("claude_resume", 2000);
    assert(desktopResumed.bytes > 0);
  }
  assert.deepEqual(errors, []);
  await writeFile(path.join(config.evidence, "windows-browser-live.json"), JSON.stringify({ measurements, diagnostics, errors, opened, closed }, null, 2));
  console.log(JSON.stringify({ measurements, opened, closed }));
} catch (error) {
  await page.screenshot({ path: path.join(config.evidence, "windows-browser-failure.png") });
  await writeFile(path.join(config.evidence, "windows-browser-live-partial.json"), JSON.stringify({ measurements, diagnostics, errors, received, bytes, opened, closed }, null, 2));
  console.log(JSON.stringify({ diagnostics, measurements, errors, received, bytes, opened, closed }));
  throw error;
} finally { await browser.close(); }
