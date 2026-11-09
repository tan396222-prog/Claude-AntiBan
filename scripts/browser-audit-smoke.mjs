// Run in the Linux guest with a separate X display and temporary browser profile:
// DISPLAY=:179 node scripts/browser-audit-smoke.mjs
import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { auditBrowser } from "../internal/cage/browser-audit.mjs";

assert.equal(process.platform, "linux", "Run this check in the Linux guest");
assert.ok(process.env.DISPLAY, "Provide an isolated X display");
const directory = await mkdtemp(join(tmpdir(), "cage-audit-smoke-"));
const chrome = spawn(
  process.env.CAGE_TEST_CHROMIUM || "/usr/bin/chromium",
  [
    `--user-data-dir=${directory}`,
    "--remote-debugging-pipe",
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-background-networking",
    "about:blank",
  ],
  { stdio: ["ignore", "ignore", "pipe", "pipe", "pipe"] },
);
let sequence = 0,
  buffer = Buffer.alloc(0),
  stderr = "",
  exit;
const pending = new Map();
chrome.stderr.on("data", (chunk) => {
  stderr = (stderr + chunk).slice(-64000);
});
chrome.stdio[3].on("error", () => {});
chrome.on("exit", (code, signal) => {
  exit = { code, signal };
  for (const call of pending.values()) {
    clearTimeout(call.timer);
    call.reject(new Error(`Chromium exited: ${JSON.stringify(exit)}`));
  }
  pending.clear();
});
chrome.stdio[4].on("data", (chunk) => {
  buffer = Buffer.concat([buffer, chunk]);
  let end;
  while ((end = buffer.indexOf(0)) >= 0) {
    const message = JSON.parse(buffer.subarray(0, end));
    buffer = buffer.subarray(end + 1);
    const call = pending.get(message.id);
    if (!call) continue;
    pending.delete(message.id);
    clearTimeout(call.timer);
    if (message.error) call.reject(new Error(message.error.message));
    else call.resolve(message.result);
  }
});
function cdp(method, params = {}, sessionId, timeout = 10000) {
  return new Promise((resolve, reject) => {
    if (exit)
      return reject(new Error(`Chromium exited: ${JSON.stringify(exit)}`));
    const id = ++sequence;
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error(`Timeout: ${method}`));
    }, timeout);
    pending.set(id, { resolve, reject, timer });
    chrome.stdio[3].write(
      JSON.stringify({ id, method, params, sessionId }) + "\0",
    );
  });
}
try {
  const version = await cdp("Browser.getVersion");
  console.log(JSON.stringify({ browser: version.product }));
  await cdp("Target.setDiscoverTargets", { discover: true });
  const { targetInfos } = await cdp("Target.getTargets");
  const userPage = targetInfos.find((page) => page.type === "page");
  assert.ok(userPage);
  const { sessionId } = await cdp("Target.attachToTarget", {
    targetId: userPage.targetId,
    flatten: true,
  });
  await cdp(
    "Runtime.evaluate",
    {
      expression:
        "document.title = 'Cage test page'; document.body.innerHTML = '<input value=unsaved-text>'; window.cageTestState = 42;",
    },
    sessionId,
  );
  const state = async () =>
    (
      await cdp(
        "Runtime.evaluate",
        {
          expression:
            "JSON.stringify({title:document.title,input:document.querySelector('input').value,state:window.cageTestState,focus:document.hasFocus()})",
          returnByValue: true,
        },
        sessionId,
      )
    ).result.value;
  const before = await state();
  const excluded = new Set();
  const clean = async () => {
    assert.equal(excluded.size, 0);
    assert.deepEqual(
      (await cdp("Target.getBrowserContexts")).browserContextIds,
      [],
    );
    assert.deepEqual(
      (await cdp("Target.getTargets")).targetInfos
        .filter((page) => page.type === "page")
        .map((page) => page.targetId),
      [userPage.targetId],
    );
    assert.equal(await state(), before, "The user's page or focus changed");
  };
  for (let index = 0; index < 20; index++) {
    const result = await auditBrowser(cdp, excluded, false, "UTC");
    assert.equal(result.expected_timezone, "UTC");
    assert.match(result.platform, /Linux/);
    await clean();
  }
  await assert.rejects(
    auditBrowser(cdp, excluded, false, "Invalid/Timezone"),
    /浏览器诊断未完成/,
  );
  await clean();
  // The guest's existing diagnostic endpoint exercises navigation and fetch.
  let networkStatus = "diagnostic endpoint unavailable";
  try {
    const network = await auditBrowser(cdp, excluded, true, "UTC");
    assert.equal(typeof network.network.status, "number");
    networkStatus = network.network.status;
  } catch (error) {
    // The diagnostic endpoint is only served during network verification.
    // Outside that window, verify cleanup of the failed navigation as well.
    assert.match(error.message, /诊断页面无法连接|诊断页面尚未就绪/);
  }
  await clean();
  console.log(
    JSON.stringify({
      audits: 22,
      networkStatus,
      resourcesClean: true,
      userPagePreserved: true,
    }),
  );
  // Chromium 156 waits 60 seconds before destroying a referenced context.
  // A short smoke test would pass even with the original crash.
  for (let seconds = 5; seconds <= 75; seconds += 5) {
    await delay(5000);
    await clean();
    if (seconds % 15 === 0)
      console.log(JSON.stringify({ aliveSeconds: seconds }));
  }
  assert.ok(
    !stderr.includes("rph_with_bc_reference"),
    "Chromium reported a dangling context",
  );
  console.log(
    "PASS: browser survived deferred cleanup with its page and focus intact",
  );
} finally {
  if (!exit) {
    const exited = once(chrome, "exit");
    chrome.kill("SIGTERM");
    const kill = setTimeout(() => chrome.kill("SIGKILL"), 5000);
    await exited;
    clearTimeout(kill);
  }
  await rm(directory, { recursive: true, force: true });
}
