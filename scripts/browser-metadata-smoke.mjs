// Headless adapter for the production controller. Set CAGE_TEST_BROWSER_PATH
// to a Chromium executable that supports unpacked extensions. All browser data
// and the authenticated, private-pipe test controller live in a temporary folder.
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { cp, mkdtemp, readdir, readFile, realpath, rm, writeFile } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

assert.ok(process.env.CAGE_TEST_BROWSER_PATH, "Set CAGE_TEST_BROWSER_PATH to a local Chromium executable");
const executable = resolve(process.env.CAGE_TEST_BROWSER_PATH);
const directory = await realpath(await mkdtemp(join(tmpdir(), "cage-browser-metadata-")));
const token = randomBytes(32).toString("hex");
const iconRequests = [];
const icons = http.createServer((req, res) => {
  iconRequests.push(req.url);
  if (req.url === "/missing.svg") { res.writeHead(404); res.end(); return; }
  res.setHeader("Content-Type", "image/svg+xml");
  const color = req.url === "/blue.svg" ? "blue" : "red";
  res.end(`<svg xmlns="http://www.w3.org/2000/svg" width="32" height="32"><path fill="${color}" d="M0 0h32v32H0z"/></svg>`);
});
await new Promise(resolve => icons.listen(0, "127.0.0.1", resolve));
const iconOrigin = `http://127.0.0.1:${icons.address().port}`;
const site = http.createServer((req, res) => {
  if (req.url === "/favicon.ico") { res.writeHead(404); res.end(); return; }
  res.setHeader("Content-Type", "text/html");
  res.setHeader("Content-Security-Policy", req.url.startsWith("/blocked")
    ? "default-src 'none'; img-src 'none'; connect-src *"
    : "default-src 'none'; img-src *; connect-src 'none'; script-src 'unsafe-inline'; frame-src http://127.0.0.1:*");
  const icon = req.url === "/missing" ? "missing" : req.url === "/blue" ? "blue" : "red";
  const buttons = req.url === "/copy" ? `<button id="text">Copy text</button><button id="rich">Copy item</button><button id="legacy">Legacy copy</button><button id="plain">Ordinary click</button><textarea id="selection">selected clipboard fixture</textarea><button id="selected">Copy selection</button>
    <script>
      const text = 'production clipboard fixture';
      document.getElementById('text').onclick = () => navigator.clipboard.writeText(text);
      document.getElementById('rich').onclick = () => navigator.clipboard.write([new ClipboardItem({'text/plain': new Blob(['clipboard item fixture'], {type:'text/plain'})})]);
      let customCopy = false;
      document.addEventListener('copy', event => {
        if (customCopy) { event.clipboardData.setData('text/plain', 'legacy clipboard fixture'); event.preventDefault(); }
      });
      document.getElementById('legacy').onclick = () => {
        customCopy = true;
        try { document.execCommand('copy'); } finally { customCopy = false; }
      };
      document.getElementById('selected').onclick = () => {
        document.getElementById('selection').select();
        document.execCommand('copy');
      };
    </script>` : "";
  const frame = req.url === "/frame" ? `<iframe id="child" width="1000" height="300" allow="clipboard-write" src="http://127.0.0.1:${site.address().port}/copy"></iframe>` : "";
  res.end(`<!doctype html><title>${req.url}</title><link rel="icon" href="${iconOrigin}/${icon}.svg"><p>Isolated browser metadata fixture</p>${buttons}${frame}`);
});
await new Promise(resolve => site.listen(0, "127.0.0.1", resolve));
const origin = `http://localhost:${site.address().port}`;
let agent, control, stderr = "", snapshot, streamTask, streamError;
const snapshots = [], clipboard = [];
const streamController = new AbortController();

async function waitFor(read, label) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    if (streamError) throw streamError;
    if (agent && agent.exitCode !== null) throw new Error(`Controller exited during ${label}: ${stderr}`);
    const result = await read();
    if (result) return result;
    await delay(50);
  }
  throw new Error(`Timed out waiting for ${label}. Last snapshot: ${JSON.stringify(snapshot)}. Browser stderr: ${stderr}`);
}
async function api(path, body) {
  const response = await fetch(control + path, {
    method: body === undefined ? "GET" : "POST",
    headers: { Authorization: `Bearer ${token}`, "Content-Type": "application/json" },
    ...(body === undefined ? {} : { body: JSON.stringify(body) }),
  });
  const result = await response.json();
  assert.equal(response.status, 200, JSON.stringify(result));
  return result;
}
async function evaluate(id, expression) {
  const result = await api("/__metadata-test/cdp", {
    id, method: "Runtime.evaluate", params: { expression, awaitPromise: true, returnByValue: true },
  });
  assert.equal(result.exceptionDetails, undefined, JSON.stringify(result.exceptionDetails));
  return result.result.value;
}
async function click(id, selector) {
  const context = randomBytes(16).toString("hex");
  await api("/copy-context", { id, context });
  const point = await evaluate(id, `(() => {
    const rect = document.querySelector(${JSON.stringify(selector)}).getBoundingClientRect();
    return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
  })()`);
  for (const type of ["mousePressed", "mouseReleased"])
    await api("/__metadata-test/cdp", { id, method: "Input.dispatchMouseEvent", params: { type, ...point, button: "left", clickCount: 1 } });
  return context;
}
async function listenEvents() {
  const response = await fetch(control + "/events", {
    headers: { Authorization: `Bearer ${token}` }, signal: streamController.signal,
  });
  assert.equal(response.status, 200);
  streamTask = (async () => {
    const decoder = new TextDecoder();
    let buffer = "";
    for await (const chunk of response.body) {
      buffer += decoder.decode(chunk, { stream: true });
      let end;
      while ((end = buffer.indexOf("\n\n")) !== -1) {
        const frame = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        const lines = frame.split("\n");
        const type = lines.find(line => line.startsWith("event:"))?.slice(6).trim() || "message";
        const data = lines.filter(line => line.startsWith("data:")).map(line => line.slice(5).trimStart()).join("\n");
        if (!data) continue;
        if (type === "clipboard") clipboard.push(JSON.parse(data));
        else if (type === "message") { snapshot = JSON.parse(data); snapshots.push(snapshot); }
      }
    }
  })().catch(error => { if (!streamController.signal.aborted) streamError = error; });
}
function tab(id) { return snapshot?.tabs.find(item => item.id === id); }
function checkPNG(icon) {
  assert.match(icon, /^data:image\/png;base64,[A-Za-z0-9+/=]+$/);
  assert.ok(icon.length <= 8192);
  const bytes = Buffer.from(icon.split(",")[1], "base64");
  assert.equal(bytes.readUInt32BE(16), 32);
  assert.equal(bytes.readUInt32BE(20), 32);
}

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
  // The extra authenticated route only drives the real page through the existing
  // private CDP pipe. Production state, extension bindings and SSE are untouched.
  const testRoute = `${input}
    if (req.url === "/__metadata-test/cdp") {
      const sid = input.id ? await session(input.id) : input.sessionId;
      res.end(JSON.stringify(await cdp(input.method, input.params || {}, sid)));
      return;
    }`;
  const isolated = source
    .replaceAll("/home/cage-browser", directory)
    .replace("/etc/claude-cage/browser-control.json", join(directory, "control.json"))
    .replace("/usr/local/lib/cage-split", join(directory, "extension"))
    .replace('spawn("/usr/bin/chromium", flags', `spawn(${JSON.stringify(executable)}, flags`)
    .replace('"--start-fullscreen",', '"--headless=new",')
    .replace(listen, 'server.listen(0, "127.0.0.1", () => console.log("CONTROL_PORT=" + server.address().port));')
    .replace(input, testRoute);
  await writeFile(join(directory, "browser-agent.mjs"), isolated);
  await writeFile(join(directory, "control.json"), JSON.stringify({ token, workspace: directory }));
  await writeFile(join(directory, "homepage.json"), JSON.stringify({ url: origin + "/red" }));
  agent = spawn(process.execPath, [join(directory, "browser-agent.mjs")], { stdio: ["ignore", "pipe", "pipe"] });
  let output = "";
  agent.stdout.on("data", chunk => {
    output += chunk;
    const match = output.match(/CONTROL_PORT=(\d+)/);
    if (match) control = `http://127.0.0.1:${match[1]}`;
  });
  agent.stderr.on("data", chunk => { stderr = (stderr + chunk).slice(-4000); });
  await waitFor(() => control, "control listener");
  await waitFor(async () => (await fetch(control + "/health", { headers: { Authorization: `Bearer ${token}` } })).ok, "browser readiness");
  await listenEvents();
  const initial = await waitFor(() => snapshot?.tabs.find(item => item.url === origin + "/red" && item.favicon), "initial favicon via production SSE");
  const id = initial.id, red = initial.favicon;
  checkPNG(red);
  assert.equal(await evaluate(id, "navigator.webdriver"), false);
  assert.equal((await api("/tabs")).tabs.find(item => item.id === id).favicon, red);

  await api("/navigate", { id, url: origin + "/blue" });
  const blue = await waitFor(() => tab(id)?.url === origin + "/blue" && tab(id)?.favicon && tab(id).favicon !== red && tab(id).favicon, "full navigation favicon via SSE");
  checkPNG(blue);
  await evaluate(id, "history.pushState({}, '', '/spa')");
  await waitFor(() => tab(id)?.url === origin + "/spa" && tab(id)?.favicon === blue, "SPA route favicon via SSE");
  await evaluate(id, `document.querySelector('link[rel="icon"]').href = ${JSON.stringify(iconOrigin + "/red.svg")}`);
  await waitFor(() => tab(id)?.favicon === red, "dynamic favicon via SSE");

  const beforeBlocked = iconRequests.length;
  // Chromium may reuse a known same-site icon even after navigation. A new
  // origin separates that native cache behavior from the CSP network check.
  const uncachedOrigin = `http://127.0.0.1:${site.address().port}`;
  const blocked = await api("/new", { url: uncachedOrigin + "/blocked" });
  await waitFor(() => tab(blocked.id)?.url === uncachedOrigin + "/blocked" && !tab(blocked.id)?.favicon, "CSP-blocked favicon fallback");
  await waitFor(() => evaluate(blocked.id, "document.readyState === 'complete'"), "blocked page load");
  await delay(300);
  assert.equal(iconRequests.length, beforeBlocked, "extension must not bypass img-src");
  assert.equal(tab(blocked.id)?.favicon, "");

  await api("/navigate", { id: blocked.id, url: uncachedOrigin + "/missing" });
  await waitFor(() => iconRequests.includes("/missing.svg"), "failed favicon request");
  await waitFor(() => tab(blocked.id)?.url === uncachedOrigin + "/missing" && !tab(blocked.id)?.favicon, "failed favicon fallback");
  await api("/navigate", { id: blocked.id, url: uncachedOrigin + "/red" });
  await waitFor(() => tab(blocked.id)?.url === uncachedOrigin + "/red" && tab(blocked.id)?.favicon === red, "favicon recovery after failed load");

  const { targetInfos } = await api("/__metadata-test/cdp", { method: "Target.getTargets" });
  assert.ok(targetInfos.some(item => item.url.startsWith("chrome-extension://") && item.url.endsWith("/control.html")), "real extension controller must exist");
  assert.ok(snapshots.every(state => state.tabs.every(item => !item.url.startsWith("chrome-extension://"))), "hidden controller must never enter public snapshots");
  assert.ok((await api("/tabs")).tabs.every(item => !item.url.startsWith("chrome-extension://")));
  const copyTab = await api("/new", { url: origin + "/copy" });
  await waitFor(() => evaluate(copyTab.id, "document.readyState === 'complete'"), "clipboard fixture load");
  for (const [selector, expected] of [["#text", "production clipboard fixture"], ["#text", "production clipboard fixture"],
    ["#rich", "clipboard item fixture"], ["#legacy", "legacy clipboard fixture"], ["#selected", "selected clipboard fixture"]]) {
    const before = clipboard.length;
    const context = await click(copyTab.id, selector);
    await waitFor(() => clipboard.length > before, `${selector} copy through production SSE`);
    assert.deepEqual(clipboard.slice(before), [{ id: copyTab.id, text: expected, context }]);
  }
  const copied = clipboard.length;
  await click(copyTab.id, "#plain");
  await delay(200);
  assert.equal(clipboard.length, copied, "ordinary clicks must not export clipboard text");
  await api("/activate", { id });
  await evaluate(copyTab.id, "navigator.clipboard.writeText('background copy').catch(() => {})");
  await delay(200);
  assert.equal(clipboard.length, copied, "background tabs must not export clipboard text");
  const frameTab = await api("/new", { url: origin + "/frame" });
  const childURL = `http://127.0.0.1:${site.address().port}/copy`;
  const childTarget = await waitFor(async () => {
    const { targetInfos } = await api("/__metadata-test/cdp", { method: "Target.getTargets" });
    return targetInfos.find(target => target.type === "iframe" && target.url === childURL);
  }, "real cross-site OOPIF target");
  const { sessionId } = await api("/__metadata-test/cdp", {
    method: "Target.attachToTarget", params: { targetId: childTarget.targetId, flatten: true },
  });
  try {
    await waitFor(async () => {
      const result = await api("/__metadata-test/cdp", { sessionId, method: "Runtime.evaluate", params: {
        expression: "document.readyState === 'complete'", returnByValue: true,
      } });
      return result.result.value;
    }, "OOPIF fixture load");
    const { result } = await api("/__metadata-test/cdp", { sessionId, method: "Runtime.evaluate", params: {
      expression: `(() => {
        const rect = document.getElementById('text').getBoundingClientRect();
        return { x: rect.x + rect.width / 2, y: rect.y + rect.height / 2 };
      })()`, returnByValue: true,
    } });
    const button = result.value;
    const frame = await evaluate(frameTab.id, `(() => {
      const element = document.getElementById('child'), rect = element.getBoundingClientRect();
      return { x: rect.x + element.clientLeft, y: rect.y + element.clientTop };
    })()`);
    const before = clipboard.length;
    const context = randomBytes(16).toString("hex");
    await api("/copy-context", { id: frameTab.id, context });
    for (const type of ["mousePressed", "mouseReleased"])
      await api("/__metadata-test/cdp", { id: frameTab.id, method: "Input.dispatchMouseEvent", params: {
        type, x: frame.x + button.x, y: frame.y + button.y, button: "left", clickCount: 1,
      } });
    await waitFor(() => clipboard.length > before, "OOPIF copy through production SSE");
    assert.deepEqual(clipboard.slice(before), [{ id: frameTab.id, text: "production clipboard fixture", context }]);
    assert.ok(!snapshot.tabs.some(item => item.id === childTarget.targetId), "OOPIF must not become a workbench tab");
  } finally {
    await api("/__metadata-test/cdp", { method: "Target.detachFromTarget", params: { sessionId } });
  }
  assert.ok(!("clipboard" in await api("/tabs")), "clipboard events must not enter replayable snapshots");
  const reconnectController = new AbortController();
  const reconnect = await fetch(control + "/events", {
    headers: { Authorization: `Bearer ${token}` }, signal: reconnectController.signal,
  });
  const reader = reconnect.body.getReader();
  try {
    const { value } = await reader.read();
    const firstFrame = new TextDecoder().decode(value);
    assert.match(firstFrame, /^data: /, "reconnect should start with the current browser snapshot");
    assert.ok(!firstFrame.includes("event: clipboard"), "reconnecting must not replay an earlier copy");
    assert.ok(!firstFrame.includes("legacy clipboard fixture"));
  } finally {
    reconnectController.abort();
    await reader.cancel().catch(() => {});
  }
  console.log("PASS: production controller headless adapter, initial/REST/SSE favicons, navigation, SPA, dynamic icons, native CSP, failed-load recovery, hidden extension target, copy APIs, selected-text copy and real cross-site OOPIF clipboard SSE");
} finally {
  streamController.abort();
  await streamTask;
  if (agent && agent.exitCode === null) {
    const exited = once(agent, "exit");
    agent.kill("SIGTERM");
    const force = setTimeout(() => agent.kill("SIGKILL"), 5000);
    await exited;
    clearTimeout(force);
  }
  await Promise.all([new Promise(resolve => site.close(resolve)), new Promise(resolve => icons.close(resolve))]);
  await rm(directory, { recursive: true, force: true });
}
