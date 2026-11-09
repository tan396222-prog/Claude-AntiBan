import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { fileURLToPath } from "node:url";
import { createServer } from "vite";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const vite = await createServer({ root: fileURLToPath(new URL("..", import.meta.url)), configFile: fileURLToPath(new URL("../vite.config.js", import.meta.url)), server: { host: "127.0.0.1", port: 0 } });
await vite.listen();
const origin = `http://127.0.0.1:${vite.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, args: ["--autoplay-policy=user-gesture-required"] });
const page = await browser.newPage();
let connected = 0, open = 0;
const timers = new Set(), errors = [];
page.on("pageerror", e => errors.push(e.message));
await page.addInitScript(() => {
  const Original = window.AudioContext;
  window.AudioContext = class extends Original {
    constructor(...args) { super(...args); window.testAudioContext = this; }
    createGain() { const node = super.createGain(); window.testAudioGain = node; return node; }
  };
});
await page.route("**/audio-test", async route => route.fulfill({ contentType: "text/html", body: await vite.transformIndexHtml("/audio-test", '<div id="root"></div><script type="module" src="/scripts/audio-fixture.jsx"></script>') }));
const format = { format: "s16le", sample_rate: 48000, channels: 2 };
await page.route("**/api/audio", route => route.fulfill({ json: format }));
const packet = Buffer.alloc(3840);
for (let i = 0; i < 960; i++) for (let channel = 0; channel < 2; channel++) packet.writeInt16LE(Math.round(Math.sin(i * 2 * Math.PI * 1000 / 48000) * 10000), i * 4 + channel * 2);
await page.routeWebSocket("**/api/audio", socket => {
  connected++; open++;
  socket.send(JSON.stringify(format));
  const timer = setInterval(() => socket.send(packet), 20);
  timers.add(timer);
  socket.onClose(() => { clearInterval(timer); timers.delete(timer); open--; });
});
const waitFor = async predicate => {
  for (let i = 0; i < 100; i++) { if (predicate()) return; await new Promise(r => setTimeout(r, 30)); }
  throw new Error("Audio lifecycle did not settle");
};
const rms = () => page.evaluate(() => {
  const analyser = window.testAudioAnalyser;
  const samples = new Float32Array(analyser.fftSize);
  analyser.getFloatTimeDomainData(samples);
  return Math.sqrt(samples.reduce((sum, v) => sum + v * v, 0) / samples.length);
});
try {
  await page.goto(origin + "/audio-test");
  await page.waitForFunction(() => !!window.testAudioContext);
  await page.locator('input[aria-label="环境音量"]').click();
  await page.locator("output").filter({ hasText: "playing" }).waitFor();
  await page.evaluate(() => {
    window.testAudioAnalyser = window.testAudioContext.createAnalyser();
    window.testAudioAnalyser.fftSize = 2048;
    window.testAudioGain.connect(window.testAudioAnalyser);
  });
  await page.locator('input[aria-label="环境音量"]').press("End");
  await page.waitForFunction(() => window.testAudioGain.gain.value === 1);
  let full;
  for (let i = 0; i < 100; i++) { full = await rms(); if (full > .19) break; await new Promise(r => setTimeout(r, 20)); }
  assert.ok(full > .19 && full < .24, `real audio RMS ${full}`);
  for (let i = 0; i < 50; i++) await page.locator('input[aria-label="环境音量"]').press("ArrowLeft");
  await page.waitForFunction(() => window.testAudioGain.gain.value === .5);
  let half;
  for (let i = 0; i < 100; i++) { half = await rms(); if (half > .09 && half < .13) break; await new Promise(r => setTimeout(r, 20)); }
  assert.ok(half > .09 && half < .13, `half-volume RMS ${half}`);
  await page.getByRole("button", { name: "静音", exact: true }).click();
  await waitFor(() => open === 0);
  assert.equal(await page.evaluate(() => window.testAudioContext.state), "suspended");
  await page.getByRole("button", { name: "取消静音", exact: true }).click();
  await waitFor(() => connected === 2 && open === 1);
  await page.locator("output").filter({ hasText: "playing" }).waitFor();
  await page.getByRole("button", { name: "停止测试环境" }).click();
  await waitFor(() => open === 0);
  assert.equal(await page.evaluate(() => JSON.parse(localStorage.getItem("cage.audio")).volume), .5);
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ result: "PASS", fullVolumeRMS: full, halfVolumeRMS: half, checks: ["real AudioWorklet PCM output", "volume", "mute closes stream", "unmute reconnects", "environment stop", "preference persistence"] }));
} catch (error) {
  console.error({ pageErrors: errors, body: await page.locator("body").innerText() });
  throw error;
} finally {
  for (const timer of timers) clearInterval(timer);
  await browser.close(); await vite.close();
}
