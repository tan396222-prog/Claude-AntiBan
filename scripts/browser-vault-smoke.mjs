// Real Chromium, isolated profile, intercepted HTTPS fixtures; no personal sites.
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { BrowserVault } from "../internal/cage/browser-vault.mjs";
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext();
const page = await context.newPage();
const html = `<form onsubmit="event.preventDefault()"><label>Email<input name="email" type="email" autocomplete="username"></label><label>Password<input name="pass" type="password" autocomplete="current-password"></label><button>Sign in</button></form>`;
await context.route("**/*", route => {
  const pathname = new URL(route.request().url()).pathname;
  const body = pathname === "/username-step" ? `<form action="/password-step"><label>Email<input type="email" name="email" autocomplete="username"></label><button>Next</button></form>`
    : pathname === "/password-step" ? `<form onsubmit="event.preventDefault()"><label>Password<input type="password" autocomplete="current-password"></label><button>Sign in</button></form>`
    : pathname === "/framed" ? `<iframe src="https://frame.example.test/login"></iframe>` : html;
  return route.fulfill({ contentType: "text/html", body });
});
await page.goto("https://mail.example.test/login");
const session = await context.newCDPSession(page);
const vault = new BrowserVault({ cdp: (method, args) => session.send(method, args), active: () => "tab", tab: () => ({}) });
for (const method of ["Runtime.executionContextCreated", "Runtime.executionContextDestroyed", "Runtime.executionContextsCleared", "Runtime.bindingCalled", "Page.frameNavigated"])
  session.on(method, params => vault.event({ method, params, sessionId: "session" }));
await session.send("Page.enable");
await vault.attach("tab", "session");
const lease = "a".repeat(64);
const request = value => vault.request({ lease, tab: "tab", ...value });
const status = () => request({ action: "status" });
const waitCandidate = async () => {
  await assert.doesNotReject(async () => {
    for (let i = 0; i < 30 && !vault.pending.size; i++) await new Promise(resolve => setTimeout(resolve, 20));
    assert.ok(vault.pending.size, "trusted submission produced no candidate");
  });
  return (await status()).candidates[0];
};
try {
  let state = await status();
  assert.equal(state.origin, "https://mail.example.test");
  assert.ok(state.document);
  assert.equal(await page.evaluate(() => typeof globalThis.cageVaultSubmitted), "undefined", "page gained vault binding");
  await page.getByLabel("Email").fill("alice@example.test");
  await page.getByLabel("Password").fill("first-secret");
  await page.evaluate(() => document.querySelector("form").dispatchEvent(new Event("submit", { bubbles: true })));
  assert.equal(vault.pending.size, 0, "synthetic submission was captured");
  await page.getByRole("button", { name: "Sign in" }).click();
  let candidate = await waitCandidate();
  assert.equal(candidate.password, "first-secret");
  assert.equal(candidate.username, "alice@example.test");
  await request({ action: "dismiss", candidate: candidate.id });
  await page.getByLabel("Email").fill("");
  await page.getByLabel("Password").fill("");
  await request({ action: "fill", document: state.document, origin: state.origin, username: "bob@example.test", password: "second-secret" });
  assert.equal(await page.getByLabel("Email").inputValue(), "bob@example.test");
  assert.equal(await page.getByLabel("Password").inputValue(), "second-secret");
  await assert.rejects(request({ action: "fill", document: state.document, origin: "https://evil.test", username: "bad", password: "wrong" }));
  assert.equal(await page.getByLabel("Password").inputValue(), "second-secret");
  await page.goto("https://mail.example.test/other");
  await assert.rejects(request({ action: "fill", document: state.document, origin: state.origin, username: "bad", password: "wrong" }));
  state = await status();
  await page.getByLabel("Email").fill("alice@example.test");
  await page.getByLabel("Password").fill("updated-secret");
  await page.getByRole("button", { name: "Sign in" }).click();
  candidate = await waitCandidate();
  assert.equal(candidate.password, "updated-secret");
  await request({ action: "lock" });
  assert.equal(vault.pending.size, 0);
  await assert.rejects(request({ action: "candidate", candidate: candidate.id }));
  await page.getByRole("button", { name: "Sign in" }).click();
  assert.equal(vault.pending.size, 0);
  const freshLease = "b".repeat(64);
  await vault.request({ action: "status", lease: freshLease, tab: "tab" });
  assert.equal(vault.pending.size, 0);
  await vault.request({ action: "lock", lease });
  assert.equal(vault.lease, freshLease, "late lock cleared new epoch");
  await page.goto("http://plain.example.test/login");
  state = await vault.request({ action: "status", lease: freshLease, tab: "tab" });
  assert.equal(state.origin, "");
  await page.getByLabel("Password").fill("must-not-capture");
  await page.getByRole("button", { name: "Sign in" }).click();
  assert.equal(vault.pending.size, 0);
  await page.goto("https://mail.example.test/login");
  await vault.request({ action: "status", lease: freshLease, tab: "tab", blocked: ["https://mail.example.test"] });
  await page.getByLabel("Password").fill("ignored-secret");
  await page.getByRole("button", { name: "Sign in" }).click();
  assert.equal(vault.pending.size, 0);
  await page.goto("https://mail.example.test/username-step");
  await vault.request({ action: "status", lease: freshLease, tab: "tab", blocked: [] });
  await page.getByLabel("Email").fill("steps@example.test");
  await page.getByRole("button", { name: "Next", exact: true }).click();
  await page.waitForURL("**/password-step?*");
  await vault.request({ action: "status", lease: freshLease, tab: "tab" });
  await page.getByLabel("Password").fill("step-secret");
  await page.getByRole("button", { name: "Sign in" }).click();
  for (let i = 0; i < 30 && !vault.pending.size; i++) await new Promise(resolve => setTimeout(resolve, 20));
  assert.equal([...vault.pending.values()][0]?.username, "steps@example.test", "username lost across same-origin steps");
  vault.deadline = 0; vault.prune();
  assert.equal(vault.pending.size, 0, "expired lease retained password");
  assert.equal(vault.usernames.size, 0);
  await page.goto("https://mail.example.test/framed");
  await vault.request({ action: "status", lease: freshLease, tab: "tab" });
  await page.frameLocator("iframe").getByLabel("Password").fill("frame-secret");
  await page.frameLocator("iframe").getByRole("button", { name: "Sign in" }).click();
  assert.equal(vault.pending.size, 0, "iframe credential was captured");
  console.log("PASS: real Chromium capture, isolated binding, synthetic rejection, fill, exact origin, stale navigation, lock, lease, HTTP, blocked sites, multi-step login, lease expiry and iframe exclusion");
} finally {
  clearInterval(vault.timer);
  await browser.close();
}
