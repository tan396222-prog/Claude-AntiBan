// Headed Chromium check; on Linux, run on an isolated DISPLAY. Browser data and
// the controller listener use a temporary directory, never the user's profile.
// CAGE_TEST_BROWSER_PATH can select a local Chromium on macOS or Windows.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { randomBytes } from "node:crypto";
import { cp, mkdtemp, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

if (process.platform === "linux") assert.ok(process.env.DISPLAY, "Provide an isolated X display");
const executable = process.env.CAGE_TEST_BROWSER_PATH || "/usr/bin/chromium";
const directory = await realpath(await mkdtemp(join(tmpdir(), "cage-devtools-smoke-")));
const token = randomBytes(32).toString("hex");
let agent, control, stderr = "";
async function waitFor(fn, label) {
  const deadline = Date.now() + 20000;
  while (Date.now() < deadline) {
    if (agent && agent.exitCode !== null) throw new Error("Controller exited: " + stderr);
    const result = await fn();
    if (result) return result;
    await delay(100);
  }
  throw new Error("Timed out: " + label + ". " + stderr);
}
async function api(path, input) {
  const response = await fetch(control + path, {
    method: input === undefined ? "GET" : "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    ...(input === undefined ? {} : { body: JSON.stringify(input) }),
  });
  const result = await response.json();
  assert.equal(response.status, 200, JSON.stringify(result));
  return result;
}
const cdp = (method, params = {}, sessionId) => api("/__devtools-test", { method, params, sessionId });
try {
  const sourceDirectory = new URL("../internal/cage/", import.meta.url);
  for (const name of await readdir(sourceDirectory))
    if ((name.startsWith("browser-") && name.endsWith(".mjs")) || name === "native-input.mjs")
      await cp(new URL(name, sourceDirectory), join(directory, name));
  await cp(new URL("split-extension", sourceDirectory), join(directory, "extension"), { recursive: true });
  const source = await readFile(new URL("browser-agent.mjs", sourceDirectory), "utf8");
  const listen = 'server.listen(18500, "127.0.0.1");';
  const input = 'const input = JSON.parse(text || "{}");';
  assert.ok(source.includes(listen) && source.includes(input), "Update the isolated controller adapter");
  // Auto-open exercises Chromium's real DevTools availability check without
  // relying on OS-specific shortcut synthesis. Keep the production mode flag.
  const adapted = source
    .replaceAll("/home/cage-browser", directory)
    .replace("/etc/claude-cage/browser-control.json", join(directory, "control.json"))
    .replace("/usr/local/lib/cage-split", join(directory, "extension"))
    .replace('spawn("/usr/bin/chromium", flags', `spawn(${JSON.stringify(executable)}, [...flags, "--auto-open-devtools-for-tabs"]`)
    .replace(listen, 'server.listen(0, "127.0.0.1", () => console.log("PORT=" + server.address().port));')
    .replace(input, `${input}
      if (req.url === "/__devtools-test") {
        res.end(JSON.stringify(await cdp(input.method, input.params || {}, input.sessionId)));
        return;
      }`);
  await writeFile(join(directory, "browser-agent.mjs"), adapted);
  await writeFile(join(directory, "control.json"), JSON.stringify({ token, workspace: directory }));
  await writeFile(join(directory, "homepage.json"), JSON.stringify({ url: "about:blank" }));
  agent = spawn(process.execPath, [join(directory, "browser-agent.mjs")], { stdio: ["ignore", "pipe", "pipe"] });
  let output = "";
  agent.stdout.on("data", chunk => {
    output += chunk;
    const match = output.match(/PORT=(\d+)/);
    if (match) control = `http://127.0.0.1:${match[1]}`;
  });
  agent.stderr.on("data", chunk => { stderr = (stderr + chunk).slice(-4000); });
  await waitFor(() => control, "control listener");
  await waitFor(async () => (await fetch(control + "/health", { headers: { Authorization: `Bearer ${token}` } })).ok, "browser ready");
  const devtools = await waitFor(async () => (await cdp("Target.getTargets")).targetInfos.find(t => t.url.startsWith("devtools://")), "real DevTools page");
  const { sessionId } = await cdp("Target.attachToTarget", { targetId: devtools.targetId, flatten: true });
  await waitFor(async () => {
    const result = await cdp("Runtime.evaluate", { expression: "document.readyState === 'complete' && document.title.includes('DevTools')", returnByValue: true }, sessionId);
    return result.result.value;
  }, "DevTools UI loaded");
  const state = await api("/tabs");
  assert.equal(state.tabs.length, 1, "DevTools must not appear as a Cage tab");
  assert.equal(state.tabs[0].url, "about:blank");
  assert.equal(state.active, state.tabs[0].id, "DevTools must not become the active Cage tab");
  await cdp("Target.closeTarget", { targetId: devtools.targetId });
  assert.equal((await api("/tabs")).active, state.active);
  console.log("PASS: production fullscreen controller opens real DevTools, keeps its page out of Cage tabs, and preserves the active website when DevTools closes");
} finally {
  if (agent?.exitCode === null) {
    const exited = once(agent, "exit");
    agent.kill("SIGTERM");
    const timer = setTimeout(() => agent.kill("SIGKILL"), 5000);
    await exited;
    clearTimeout(timer);
  }
  await rm(directory, { recursive: true, force: true });
}
