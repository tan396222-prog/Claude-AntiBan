// Started by TestMobileCLIThroughRelayAndSSH. The private one-time launch URL
// is read from stdin; browser automation uses the real production endpoints.
import process from "node:process";
import { mkdir, readFile } from "node:fs/promises";
import path from "node:path";
import assert from "node:assert/strict";
const chunks = [];
for await (const chunk of process.stdin) chunks.push(chunk);
const entry = Buffer.concat(chunks).toString("utf8").trim();
const engine = process.env.CAGE_TEST_MOBILE_BROWSER;
if (!["chromium", "webkit"].includes(engine)) throw Error("Choose chromium or webkit");
const playwright = await import(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const browser = await playwright[engine].launch({headless:true});
try {
  const page = await browser.newPage({viewport:{width:390,height:844},deviceScaleFactor:1,isMobile:true,hasTouch:true});
  page.setDefaultTimeout(18000);
  // Exercise compatibility with the original independent-session capability.
  // The shared-terminal user flow has its own real CLI/browser integration test.
  await page.route("**/api/mobile/cli/projects", async route => {
    const response = await route.fetch();
    const value = await response.json();
    delete value.shared_sessions;
    await route.fulfill({response, json:value});
  });
  await page.route("**/api/mobile/cli/sessions", async route => {
    if (route.request().method() !== "POST") return route.continue();
    // Existing headless history is seeded by Go; new production sessions use
    // the shared path covered by shared-cli-live-smoke.mjs.
    await route.fulfill({status:200,contentType:"application/json",body:process.env.CAGE_TEST_MOBILE_LEGACY_SESSION});
  });
  await page.goto(entry);
  await page.getByRole("dialog", {name:"工作区侧栏"}).getByRole("button", {name:/ 新建会话$/}).first().click();
  await page.getByRole("dialog", {name:"工作区侧栏"}).waitFor({state:"hidden"});
  const message = page.getByRole("textbox", {name:"消息"});
  await message.fill("initial mobile prompt");
  await page.getByRole("button", {name:"发送",exact:true}).click();
  const approve = page.getByRole("button",{name:"允许一次"});
  await approve.waitFor();
  assert.match(await page.getByLabel("完整工具参数").innerText(), /mobile-approved\.txt/);
  await assert.rejects(readFile(process.env.CAGE_TEST_MOBILE_MARKER), {code:"ENOENT"});
  const out = process.env.CAGE_TEST_MOBILE_CAPTURE;
  if(out) {
    await mkdir(out,{recursive:true});
    await page.screenshot({path:path.join(out,engine+"-cli-approval.png")});
  }
  // The pending tool request belongs to the host, so a page reload must
  // recover it and its original arguments.
  await page.reload();
  // Losing both the decision and its immediate status check must not leave
  // the approval disabled after the next successful read.
  let offline = false;
  const sessionRequests = "**/api/mobile/cli/sessions/**";
  await page.route(sessionRequests, async route => {
    if (route.request().method() === "POST" && route.request().url().endsWith("/approval")) offline = true;
    if (offline) await route.abort("internetdisconnected");
    else await route.continue();
  });
  await approve.click();
  await page.getByRole("alert").getByRole("button", {name:"重新读取"}).waitFor();
  await page.unroute(sessionRequests);
  await page.getByText("主机仍在等待这次审批。请核对参数后再次选择。", {exact:true}).waitFor();
  await assert.rejects(readFile(process.env.CAGE_TEST_MOBILE_MARKER), {code:"ENOENT"});
  await approve.click();
  await page.getByText("isolated tool completed", {exact:true}).waitFor();
  const tools = page.locator(".tool-history");
  await tools.locator("summary").click();
  await tools.getByText("Bash", {exact:true}).waitFor();
  await tools.getByText("已完成", {exact:true}).waitFor();
  assert.equal(await readFile(process.env.CAGE_TEST_MOBILE_MARKER,"utf8"),"approved");
  await message.fill("follow-up");
  await page.getByRole("button", {name:"发送",exact:true}).click();
  await page.getByText("isolated continuation",{exact:true}).waitFor();
  if(out) await page.screenshot({path:path.join(out,engine+"-cli-result.png")});
} catch(error) {
  throw Error(String(error).replace(/(link-bootstrap)#[A-Za-z0-9_-]+/g,"$1#[redacted]"));
} finally {
  await browser.close();
}
