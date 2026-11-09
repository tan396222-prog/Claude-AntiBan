// Explicitly target an unlocked local test installation and its existing VM.
// This emits a short test tone; it does not read browser content or credentials.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { spawn, execFileSync } from "node:child_process";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const bundle = process.argv[2];
for (const key of ["CAGE_CONFIG", "CAGE_SSH_CONFIG", "CAGE_SSH_TARGET"]) assert.ok(process.env[key], `set ${key}`);
assert.ok(bundle?.startsWith("/") && bundle.endsWith(".app"));
const ssh = ["-q", "-F", process.env.CAGE_SSH_CONFIG, "-o", "BatchMode=yes", "-o", "ClearAllForwardings=yes", "-o", "ControlPath=none", "-o", "ConnectTimeout=3", process.env.CAGE_SSH_TARGET, "--"];
const capturePIDs = () => execFileSync("ssh", [...ssh, "pgrep -x parec || true"], { encoding: "utf8" }).trim().split(/\s+/).filter(Boolean);
const before = new Set(capturePIDs());
const entry = JSON.parse(execFileSync(bundle + "/Contents/Resources/cage", ["desktop", "--json", "--existing"], { encoding: "utf8", env: { ...process.env, CLAUDE_CAGE_CONFIG: process.env.CAGE_CONFIG } }));
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1280, height: 850 } });
let play;
const errors = [];
page.on("pageerror", e => errors.push(e.message));
await page.addInitScript(() => {
  const Original = window.AudioContext;
  window.AudioContext = class extends Original {
    constructor(...args) { super(...args); window.testAudioContext = this; }
    createGain() { const gain = super.createGain(); window.testAudioGain = gain; return gain; }
  };
});
try {
  await page.goto(entry.url);
  const volume = page.getByRole("slider", { name: "环境音量" });
  await volume.press("End");
  await page.waitForFunction(() => window.testAudioGain && window.testAudioContext.state === "running");
  await page.evaluate(() => {
    window.testAudioAnalyser = window.testAudioContext.createAnalyser();
    window.testAudioAnalyser.fftSize = 2048;
    window.testAudioGain.connect(window.testAudioAnalyser);
  });
  const packet = Buffer.alloc(48000 * 4 * 3);
  for (let i = 0; i < 48000 * 3; i++) {
    const value = Math.round(Math.sin(i * 2 * Math.PI * 880 / 48000) * 8000);
    packet.writeInt16LE(value, i * 4); packet.writeInt16LE(value, i * 4 + 2);
  }
  play = spawn("ssh", [...ssh, "sudo -u cage-browser /usr/bin/pacat --server=unix:/run/cage-audio/native --playback --raw --format=s16le --rate=48000 --channels=2 --latency-msec=20"]);
  const completed = new Promise((resolve, reject) => { play.on("error", reject); play.on("close", code => code === 0 ? resolve() : reject(new Error(`guest playback exited ${code}`))); });
  play.stdin.end(packet);
  const measured = await page.waitForFunction(() => {
    const samples = new Float32Array(2048);
    window.testAudioAnalyser.getFloatTimeDomainData(samples);
    const rms = Math.sqrt(samples.reduce((sum, v) => sum + v * v, 0) / samples.length);
    return rms > .14 && rms < .20 ? rms : false;
  }, null, { timeout: 10000 });
  console.log(JSON.stringify({ installedAudio: "PASS", RMS: await measured.jsonValue(), chain: "guest application → PulseAudio → SSH → authenticated WebSocket → production AudioWorklet" }));
  await completed;
  assert.deepEqual(errors, []);
} finally {
  play?.kill();
  await browser.close();
}
let remaining = [];
for (let i = 0; i < 40; i++) {
  remaining = capturePIDs().filter(pid => !before.has(pid));
  if (!remaining.length) break;
  await new Promise(r => setTimeout(r, 100));
}
assert.deepEqual(remaining, [], "closing the player left a capture process");
console.log("PASS: closing the production viewer stops guest capture");
