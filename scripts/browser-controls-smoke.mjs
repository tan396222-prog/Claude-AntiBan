// Run in the Linux guest on a separate X display:
// DISPLAY=:183 node scripts/browser-controls-smoke.mjs
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdtemp, readFile, writeFile, copyFile, cp, rm } from "node:fs/promises";
import http from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";

assert.equal(process.platform, "linux", "Run in the Linux guest");
assert.ok(process.env.DISPLAY, "Provide an isolated X display");
const directory = await mkdtemp(join(tmpdir(), "cage-controls-smoke-"));
const reports = new Map();
const site = http.createServer(async (req, res) => {
  const url = new URL(req.url, "http://localhost");
  if (url.pathname === "/report") {
    let body = "";
    for await (const chunk of req) body += chunk;
    const path = url.searchParams.get("path");
    const previous = reports.get(path);
    reports.set(path, { ...JSON.parse(body), visits: (previous?.visits || 0) + 1 });
    res.end("ok");
    return;
  }
  res.setHeader("Content-Type", "text/html");
  if (url.pathname === "/first")
    res.setHeader("Set-Cookie", "cage_smoke=1; SameSite=Lax");
  res.end(`<!doctype html><title>${url.pathname}</title><p>Browser smoke test</p><a href="#section">Section</a><button onclick="history.pushState({}, '', '#pushed')">Push history</button>
    <script>fetch('/report?path='+encodeURIComponent(location.pathname), {
      method:'POST',body:JSON.stringify({webdriver:navigator.webdriver,
        cookie:document.cookie.includes('cage_smoke=1')})
    });${url.pathname === "/spa" ? "setTimeout(() => history.pushState({}, '', '#pushed'), 500);" : ""}</script>`);
});
await new Promise((resolve) => site.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${site.address().port}`;
const token = randomBytes(32).toString("hex");
let agent, control, stderr = "";
let eventState, eventsDone;
const eventController = new AbortController();
async function listenEvents() {
  const response = await fetch(control + "/events", {
    headers: { Authorization: `Bearer ${token}` }, signal: eventController.signal,
  });
  assert.equal(response.status, 200);
  eventsDone = (async () => {
    const decoder = new TextDecoder();
    let buffer = "";
    for await (const chunk of response.body) {
      buffer += decoder.decode(chunk, { stream: true });
      let end;
      while ((end = buffer.indexOf("\n\n")) !== -1) {
        const frame = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        if (frame.startsWith("data: ")) eventState = JSON.parse(frame.slice(6));
      }
    }
  })().catch(error => { if (!eventController.signal.aborted) stderr += error.message; });
}
async function waitFor(read, label) {
  const deadline = Date.now() + 15000;
  while (Date.now() < deadline) {
    const value = await read();
    if (value) return value;
    if (agent?.exitCode !== null)
      throw new Error(`Browser agent exited during ${label}: ${stderr.slice(-2000)}`);
    await delay(100);
  }
  throw new Error(`Timeout: ${label}`);
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
async function start() {
  control = undefined;
  let output = "";
  agent = spawn(process.execPath, [join(directory, "agent.mjs")], {
    stdio: ["ignore", "pipe", "pipe"],
  });
  agent.stdout.on("data", (chunk) => {
    output += chunk;
    const match = output.match(/CONTROL_PORT=(\d+)/);
    if (match) control = `http://127.0.0.1:${match[1]}`;
  });
  agent.stderr.on("data", (chunk) => { stderr = (stderr + chunk).slice(-8000); });
  await waitFor(() => control, "control listener");
  await waitFor(async () => {
    const response = await fetch(control + "/health", {
      headers: { Authorization: `Bearer ${token}` },
    });
    return response.ok;
  }, "browser readiness");
}
async function stop() {
  if (!agent || agent.exitCode !== null) return;
  const exited = once(agent, "exit");
  agent.kill("SIGTERM");
  const force = setTimeout(() => agent.kill("SIGKILL"), 5000);
  await exited;
  clearTimeout(force);
}
try {
  // Redirect deployment paths and the control listener only. The Chromium
  // flags and all browser-control handlers come from the production agent.
  const source = await readFile(new URL("../internal/cage/browser-agent.mjs", import.meta.url), "utf8");
  const listen = 'server.listen(18500, "127.0.0.1");';
  assert.ok(source.includes(listen), "Update the isolated control-listener adapter");
  await writeFile(join(directory, "agent.mjs"), source
    .replaceAll("/home/cage-browser", directory)
    .replaceAll("/usr/local/lib/cage-split", join(directory, "split-extension"))
    .replace("/etc/claude-cage/browser-control.json", join(directory, "control.json"))
    .replace(listen, 'server.listen(0, "127.0.0.1", () => console.log("CONTROL_PORT=" + server.address().port));'));
  for (const name of ["browser-audit", "browser-downloads", "browser-uploads", "browser-login", "browser-vault", "browser-clipboard", "native-input"])
    await copyFile(new URL(`../internal/cage/${name}.mjs`, import.meta.url), join(directory, `${name}.mjs`));
  await cp(new URL("../internal/cage/split-extension", import.meta.url), join(directory, "split-extension"), { recursive: true });
  await writeFile(join(directory, "control.json"), JSON.stringify({ token, workspace: directory }));
  await writeFile(join(directory, "homepage.json"), JSON.stringify({ url: origin + "/first" }));
  await start();
  await waitFor(() => reports.get("/first"), "initial page");
  assert.equal(reports.get("/first").webdriver, false, "Human browsing exposes the automation marker");
  assert.equal((await fetch(control + "/tabs")).status, 401);
  assert.equal((await fetch(control + "/tabs", {
    headers: { Authorization: `Bearer ${token}`, Origin: "https://example.com" },
  })).status, 403);
  const original = (await api("/tabs")).active;
  await listenEvents();
  await waitFor(() => eventState?.active === original, "initial browser events");
  assert.equal(eventState.back, false);
  assert.equal(eventState.forward, false);
  const second = await api("/new", { url: origin + "/second" });
  await waitFor(() => reports.get("/second"), "new tab");
  assert.equal(reports.get("/second").webdriver, false);
  assert.equal((await api("/tabs")).active, second.id);
  await api("/activate", { id: original });
  assert.equal((await api("/tabs")).active, original);
  await api("/navigate", { id: original, url: origin + "/third" });
  await waitFor(() => reports.get("/third"), "navigation");
  await waitFor(() => eventState?.active === original && eventState.back === true && eventState.forward === false, "back availability via events only");
  assert.equal((await api("/tabs")).back, true);
  await api("/back", { id: original });
  await waitFor(() => eventState?.active === original && eventState.back === false && eventState.forward === true, "forward availability via events only");
  await waitFor(async () => (await api("/tabs")).tabs.some(t => t.id === original && t.url === origin + "/first"), "back");
  assert.equal((await api("/tabs")).forward, true);
  await api("/forward", { id: original });
  await waitFor(() => eventState?.active === original && eventState.back === true && eventState.forward === false, "forward navigation via events only");
  await waitFor(async () => (await api("/tabs")).tabs.some(t => t.id === original && t.url === origin + "/third"), "forward");
  const spa = await api("/new", { url: origin + "/spa" });
  await waitFor(() => eventState?.active === spa.id && eventState.back === true && eventState.forward === false, "pushState history via events only");
  await api("/back", { id: spa.id });
  await waitFor(() => eventState?.active === spa.id && eventState.back === false && eventState.forward === true, "same-document back via events only");
  await api("/activate", { id: second.id });
  await waitFor(() => eventState?.active === second.id && eventState.back === false && eventState.forward === false, "fresh tab history isolation");
  await api("/activate", { id: original });
  await waitFor(() => eventState?.active === original && eventState.back === true && eventState.forward === false, "restored tab history");
  await api("/close", { id: spa.id });
  const visits = reports.get("/third").visits;
  await api("/reload", { id: original });
  await waitFor(() => reports.get("/third").visits > visits, "reload");
  await api("/close", { id: second.id });
  assert.equal((await api("/tabs")).tabs.length, 1);
  await api("/audit", { timezone: "UTC", network: false });
  const beforeRestart = reports.get("/third").visits;
  await stop();
  await start();
  await waitFor(() => reports.get("/third").visits > beforeRestart, "session restoration");
  assert.equal(reports.get("/third").cookie, true, "Browser cookies did not persist");
  assert.equal(reports.get("/third").webdriver, false);
  const restored = await api("/tabs");
  assert.equal(restored.tabs.length, 1);
  assert.equal(restored.tabs[0].url, origin + "/third");
  await api("/close", { id: restored.active });
  assert.equal((await api("/tabs")).tabs.length, 1, "Closing the last tab must create a homepage tab");
  console.log(JSON.stringify({
    webdriver: false, newTab: true, activate: true, navigate: true,
    back: true, forward: true, reload: true, close: true,
    sessionRestored: true, cookiesPreserved: true, lastTabReplaced: true,
    unauthenticatedRejected: true, crossOriginRejected: true,
  }));
} finally {
  eventController.abort();
  await eventsDone;
  await stop();
  await new Promise(resolve => site.close(resolve));
  await rm(directory, { recursive: true, force: true });
}
