import process from "node:process";
import assert from "node:assert/strict";
import {mkdir, readFile} from "node:fs/promises";
import path from "node:path";

const parts = [];
for await (const chunk of process.stdin) parts.push(chunk);
const entry = Buffer.concat(parts).toString("utf8").trim();
const engine = process.env.CAGE_TEST_CHANNEL_BROWSER;
const playwright = await import(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const browser = await playwright[engine].launch({headless:true});
let page;
const snapshots = [];
try {
  page = await browser.newPage({viewport:{width:390,height:844},isMobile:true,hasTouch:true});
  await page.addInitScript(() => {
    window.fixtureInputEvents = [];
    for (const kind of ["input","change"]) document.addEventListener(kind,event=>{
      if (event.target.tagName === "TEXTAREA") window.fixtureInputEvents.push({kind,value:event.target.value});
    },true);
  });
  page.on("response", async response => {
    if (!/\/api\/mobile\/cli\/sessions\/[^/]+(\/open)?(?:\?|$)/.test(response.url())) return;
    try {
      const value = await response.json();
      snapshots.push({path:new URL(response.url()).pathname,status:response.status(),id:value.id,revision:value.revision,can_send:value.can_send,terminal_available:value.terminal_available,connection_notice:value.connection_notice});
    } catch { /* Incremental reads may have no response body. */ }
  });
  page.setDefaultTimeout(25000);
  let sentRequests = 0, interruptedReads = 0;
  let persistentReadFailure = false, persistentReadAttempts = 0;
  const recovering = process.env.CAGE_TEST_SESSION_RECOVERY === "1";
  if (recovering) {
    await page.route("**/api/mobile/cli/sessions/*/messages", async route => {
      sentRequests++;
      if (sentRequests === 1) {
        await route.fetch();
        // The guest accepted the message, but its HTTP receipt is lost.
        await route.fulfill({status:503,contentType:"text/plain",body:"电脑暂时无法连接运行环境，聊天记录已保留。"});
      } else await route.continue();
    });
    await page.route(/\/api\/mobile\/cli\/sessions\/[^/?]+(?:\?.*)?$/, async route => {
      if (route.request().method() === "GET" && persistentReadFailure) {
        persistentReadAttempts++;
        await route.fulfill({status:503,contentType:"text/plain",body:"电脑暂时无法连接运行环境，聊天记录已保留。"});
      } else if (route.request().method() === "GET" && sentRequests && interruptedReads++ < 2) {
        await route.fulfill({status:503,contentType:"text/plain",body:"电脑暂时无法连接运行环境，聊天记录已保留。"});
      } else await route.continue();
    });
  }
  await page.goto(entry);
  await page.getByRole("dialog",{name:"工作区侧栏"}).getByRole("button",{name:/ 新建会话$/}).first().click();
  await page.getByRole("dialog",{name:"工作区侧栏"}).waitFor({state:"hidden"});
  const message = page.getByRole("textbox",{name:"消息"});
  await message.fill("initial mobile prompt");
  await page.getByRole("button",{name:"发送",exact:true}).click();
  const approve = page.getByRole("button",{name:"允许一次"});
  await approve.waitFor();
  if (recovering) {
    assert.equal(sentRequests,1,"a lost receipt replayed an accepted message");
    assert.ok(interruptedReads >= 2);
    assert.equal(await page.getByText(/尚未收到主机确认|同一编号|已自动重连5次/).count(),0);
    assert.equal(await page.getByRole("alert").count(),0,"a recovered interruption left a failure card");
  }
  assert.equal(await page.getByRole("button",{name:"在电脑打开会话",exact:true}).count(),0);
  assert.equal(await page.getByRole("alert").count(),0,"normal startup displayed an extra notice");
  assert.match(await page.getByLabel("工具参数预览").innerText(), /mobile-approved\.txt/);
  await assert.rejects(readFile(process.env.CAGE_TEST_MOBILE_MARKER),{code:"ENOENT"});
  await page.reload();
  await approve.waitFor();
  const capture = process.env.CAGE_TEST_MOBILE_CAPTURE;
  if (capture) {
    await mkdir(capture,{recursive:true});
    await page.screenshot({path:path.join(capture,engine+"-shared-approval.png")});
  }
  await approve.click();
  await page.getByText("isolated tool completed",{exact:true}).waitFor();
  await page.getByText("follow-up",{exact:true}).waitFor();
  await page.getByText("isolated continuation",{exact:true}).waitFor();
  assert.equal(await readFile(process.env.CAGE_TEST_MOBILE_MARKER,"utf8"),"approved");
  await page.reload();
  await page.getByText("isolated continuation",{exact:true}).waitFor();
  assert.equal(await page.getByLabel("会话消息").getByText("initial mobile prompt",{exact:true}).count(),1);
  if (capture) await page.screenshot({path:path.join(capture,engine+"-shared-history.png")});
  const title = await page.locator(".chat-header h1").innerText();
  // Prepare an ended history through the real archive/restore API. The user
  // then opens it from the actual phone list, with no desktop helper.
  await page.evaluate(async () => {
    const selected = history.state.cageCLI;
    const root = "/api/mobile/cli/sessions/" + selected.session;
    for (const operation of ["archive", "restore"]) {
      const result = await fetch(root+"/"+operation,{method:"POST",headers:{"Content-Type":"application/json"},body:"{}"});
      if (!result.ok) throw Error("history setup failed: "+result.status);
    }
    history.replaceState({...history.state,cageCLI:{tool:"code",project:selected.project,session:""}},"");
  });
  await page.reload();
  await page.getByRole("button",{name:"展开侧栏",exact:true}).click();
  await page.getByRole("dialog",{name:"工作区侧栏"}).getByRole("button",{name:title,exact:true}).click();
  await page.getByRole("dialog",{name:"工作区侧栏"}).waitFor({state:"hidden"});
  await message.fill("after reopen");
  await page.getByRole("button",{name:"发送",exact:true}).click();
  await page.getByLabel("会话消息").getByText("after reopen",{exact:true}).waitFor();
  await page.waitForFunction(() => document.querySelectorAll('.message:not(.user)').length === 3 &&
    document.querySelectorAll('.message:not(.user)')[2].textContent.includes("isolated continuation"));
  assert.equal(await page.getByRole("alert").count(),0,"history restore displayed an extra notice");
  if (capture) await page.screenshot({path:path.join(capture,engine+"-seamless-resume.png")});
  await page.getByRole("button",{name:"展开侧栏",exact:true}).click();
  await page.getByRole("dialog",{name:"工作区侧栏"}).getByRole("button",{name:/ 新建会话$/}).first().click();
  await page.getByRole("dialog",{name:"工作区侧栏"}).waitFor({state:"hidden"});
  await message.fill("draft for second");
  const secondTitle = await page.locator(".chat-header h1").innerText();
  const select = async name => {
    await page.getByRole("button",{name:"展开侧栏",exact:true}).click();
    await page.getByRole("dialog",{name:"工作区侧栏"}).getByRole("button",{name,exact:true}).click();
    await page.getByRole("dialog",{name:"工作区侧栏"}).waitFor({state:"hidden"});
  };
  await select(title);
  assert.equal(await message.inputValue(),"");
  await message.fill("draft for first");
  await select(secondTitle);
  assert.equal(await message.inputValue(),"draft for second");
  await select(title);
  assert.equal(await message.inputValue(),"draft for first");
  assert.equal(await page.getByRole("alert").count(),0,"session switching displayed an extra notice");
  if (recovering) {
    persistentReadFailure = true;
    await page.getByRole("alert").filter({hasText:"已自动重连5次"}).waitFor({timeout:40000});
    assert.equal(persistentReadAttempts,6,"initial failure must be followed by five retries");
    assert.equal(await message.inputValue(),"draft for first");
    if (capture) await page.screenshot({path:path.join(capture,engine+"-reconnect-exhausted.png")});
    persistentReadFailure = false;
    await page.getByRole("button",{name:"再次连接",exact:true}).click();
    await page.getByRole("alert").waitFor({state:"hidden"});
    assert.equal(await message.inputValue(),"draft for first");
    assert.equal(sentRequests,2,"reconnection replayed a user message");
  }
} catch (error) {
  console.error(JSON.stringify(snapshots.slice(-12)));
  console.error(await page?.locator("body").innerText());
  console.error(await page?.getByRole("textbox",{name:"消息"}).evaluate(el=>({value:el.value,disabled:el.disabled})));
  console.error(await page?.evaluate(()=>window.fixtureInputEvents));
  throw Error(String(error).replace(/(link-bootstrap)#[A-Za-z0-9_-]+/g,"$1#[redacted]"));
} finally {
  await browser.close();
}
