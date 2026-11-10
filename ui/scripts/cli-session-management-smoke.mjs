import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const base = process.env.CAGE_UI_URL || "http://127.0.0.1:5209";
const output = process.env.CAGE_TEST_CAPTURE || "../docs/test-reports/2026-11-07-cli-session-sync";
const workspace = "/Users/shihao/.claude-cage-workspace";
const id = "12345678-1234-4321-aaaa-123456789abc";
const otherID = "12345678-1234-4321-aaaa-123456789abd";
const otherProject = "/Users/shihao/Projects/mobile";
const sessions = [
  {id, workspace, title:"电脑保留的历史", terminal_available:true, revision:1, updated_at:"2026-11-07T08:00:00Z",
    turns:[{id:"turn-1", prompt:"原来的问题", text:"原来的回答", status:"completed"}]},
  {id:otherID, workspace, title:"另一段会话", revision:1, updated_at:"2026-11-07T07:00:00Z", turns:[]},
  {id:"12345678-1234-4321-aaaa-123456789abe", workspace, title:"准备发布说明", archived:true, pinned:true, updated_at:"2026-11-06T09:00:00Z", turns:[]},
  {id:"12345678-1234-4321-aaaa-123456789abf", workspace, title:"核对接口", archived:true, updated_at:"2026-11-05T08:00:00Z", turns:[]},
  {id:"12345678-1234-4321-aaaa-123456789ac0", workspace:otherProject, title:"移动端布局检查", archived:true, updated_at:"2026-11-04T08:00:00Z", turns:[]},
];
let state, failPurge = true, failOpen = true, opens = 0, releaseOpen;
const handle = async (route, path, body) => {
  const archived = new URL(route.request().url()).searchParams.get("archived") === "true";
  if (path === "/cli/sessions") return route.fulfill({json:{sessions:sessions.filter(s => Boolean(s.archived) === archived),notice:""}});
  const session = sessions.find(s => path.includes(s.id));
  if (!session) return route.fulfill({status:404});
  if (path.endsWith("/open")) {
    opens++;
    if (failOpen) return route.fulfill({status:409,contentType:"text/plain; charset=utf-8",body:"测试：原终端仍在运行，请先退出其中的 Claude Code"});
    await new Promise(resolve => { releaseOpen = resolve; });
    const terminal = {id:"restored-terminal", kind:"claude", workspace, title:session.title, cli_session_id:session.id};
    state.terminals = [terminal];
    return route.fulfill({json:{...session,desktop_terminal_id:terminal.id}});
  }
  if (path.endsWith("/archive")) {
    session.archived = true; session.revision++;
    state.terminals = state.terminals.filter(t => t.cli_session_id !== session.id);
  } else if (path.endsWith("/restore")) {
    session.archived = false; session.revision++;
  } else if (path.endsWith("/purge")) {
    assert.equal(body.confirm,true);
    if (failPurge) return route.fulfill({status:503,contentType:"text/plain; charset=utf-8",body:"删除连接中断，请重试"});
    sessions.splice(sessions.indexOf(session),1);
  } else if (path.endsWith("/metadata")) Object.assign(session,body);
  return route.fulfill({json:session});
};
const desktop = await createWorkbench({url:base, cliRequest:handle});
state = desktop.state;
const {page,browser,errors} = desktop;
try {
  state.status.ready = state.status.vm_running = true;
  state.status.projects = [workspace, otherProject];
  const history = page.getByRole("button",{name:"电脑保留的历史",exact:true});
  await history.waitFor();
  await history.click();
  await page.getByRole("status").filter({hasText:"测试：原终端仍在运行"}).waitFor();
  assert.equal(await page.getByRole("dialog",{name:"电脑保留的历史"}).count(),0,"session click opened an intermediate preview");
  assert.equal(await history.isEnabled(),true,"failed session open could not be retried");
  failOpen = false;
  await history.click();
  await page.waitForFunction(() => document.querySelector('.sidebar-session[aria-busy="true"]')?.disabled);
  assert.equal(await page.getByRole("button",{name:"另一段会话",exact:true}).isDisabled(),true,"another open was allowed during terminal restore");
  releaseOpen();
  await page.getByRole("tab",{name:"电脑保留的历史",exact:true}).waitFor();
  assert.equal(await history.count(),0,"history duplicated its live terminal");
  assert.equal(opens,2,"clicking one session sent duplicate open requests");
  assert.equal(state.terminalConnections,1,"restored terminal did not connect");
  assert.equal(state.requests.filter(r => r.path === "/cli/sessions/" + id).length,0,"desktop fetched a history preview");
  await page.getByRole("tab",{name:"电脑保留的历史",exact:true}).hover();
  await page.getByRole("group",{name:"电脑保留的历史 的操作"}).getByRole("button",{name:"归档",exact:true}).click();
  await page.getByRole("dialog",{name:"归档这段会话？"}).getByRole("button",{name:"归档会话",exact:true}).click();
  await page.getByRole("tab",{name:"电脑保留的历史",exact:true}).waitFor({state:"hidden"});
  assert.deepEqual(errors,[],"desktop archive");

  const context = await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,userAgent:"CageMobile/1"});
  const phone = await context.newPage();
  const phoneErrors = [];
  phone.on("pageerror",error => phoneErrors.push(error.message));
  await phone.route("**/api/**",async route => {
    const path = new URL(route.request().url()).pathname.replace("/api/mobile/cli","/cli");
    if (path.endsWith("/projects")) return route.fulfill({json:{host:"测试电脑",ready:true,shared_sessions:true,projects:[{path:workspace,name:"默认工作区"},{path:otherProject,name:"mobile"}]}});
    return handle(route,path,route.request().postDataJSON());
  });
  await phone.goto(base+"/mobile.html");
  const drawer = phone.getByRole("dialog",{name:"工作区侧栏"});
  await drawer.getByRole("button",{name:"设置",exact:true}).click();
  await phone.getByRole("button",{name:"已归档会话",exact:true}).click();
  let archived = phone.getByRole("article").filter({has:phone.getByRole("heading",{name:"电脑保留的历史",exact:true})});
  await archived.getByRole("button",{name:"取消归档"}).click();
  await history.waitFor();
  await phone.getByRole("button",{name:"返回设置"}).click();
  await phone.getByRole("button",{name:"返回会话"}).click();
  const row = drawer.locator(".swipe-session").filter({has:phone.getByRole("button",{name:"电脑保留的历史",exact:true})});
  await row.getByRole("button",{name:"电脑保留的历史的更多操作"}).click();
  await row.getByRole("button",{name:"归档",exact:true}).click();
  await phone.getByRole("dialog",{name:"归档会话",exact:true}).getByRole("button",{name:"归档会话",exact:true}).click();
  await history.waitFor({state:"hidden"});
  assert.deepEqual(errors,[],"phone archive");

  await page.getByRole("button",{name:"设置",exact:true}).click();
  await page.getByRole("navigation",{name:"设置分组"}).getByRole("button",{name:"已归档会话",exact:true}).click();
  const panel = page.getByRole("region",{name:"已归档会话",exact:true});
  const projectFilter = panel.getByRole("combobox",{name:"项目筛选"});
  const group = panel.locator(".archive-project").first();
  await group.waitFor();
  const before = await group.boundingBox();
  await projectFilter.click();
  const options = page.getByRole("listbox",{name:"项目筛选选项"});
  const opened = await options.boundingBox();
  const shifted = await group.boundingBox();
  assert.ok(Math.abs(shifted.y-before.y)<1 && opened.height>0,"floating project filter moved content");
  await projectFilter.press("Escape");
  assert.ok(Math.abs((await group.boundingBox()).y - before.y) < 2,"filter did not restore layout");
  await projectFilter.press("End"); await projectFilter.press("Enter");
  await panel.getByRole("heading",{name:"移动端布局检查",exact:true}).waitFor();
  assert.equal(await panel.getByRole("heading",{name:"电脑保留的历史",exact:true}).count(),0);
  await projectFilter.press("Home"); await projectFilter.press("Enter");
  const filter = panel.getByRole("combobox",{name:"会话筛选"});
  await filter.click(); await page.getByRole("option",{name:"已置顶",exact:true}).click();
  assert.equal(await panel.getByRole("article").count(),1);
  await filter.press("Home"); await filter.press("Enter");
  const search = panel.getByRole("searchbox",{name:"搜索已归档的会话"});
  await search.fill("核对");
  assert.equal(await panel.getByRole("article").count(),1);
  await search.fill("");
  await panel.getByRole("heading",{name:".claude-cage-workspace",exact:true}).click();
  await mkdir(output,{recursive:true});
  await page.screenshot({path:output+"/"+(process.env.CAGE_TEST_BROWSER||"chromium")+"-archive-layout.png"});
  const target = panel.getByRole("article").filter({has:page.getByRole("heading",{name:"电脑保留的历史",exact:true})});
  await target.getByRole("button",{name:"删除会话",exact:true}).click();
  await target.getByRole("button",{name:"永久删除",exact:true}).click();
  await panel.getByRole("alert").filter({hasText:"删除连接中断"}).waitFor();
  assert.equal(sessions.length,5,"failed purge removed history");
  await mkdir(output,{recursive:true});
  await page.screenshot({path:output+"/"+(process.env.CAGE_TEST_BROWSER||"chromium")+"-archived-desktop.png"});
  failPurge = false;
  await target.getByRole("button",{name:"永久删除",exact:true}).click();
  await target.waitFor({state:"hidden"});
  await phone.evaluate(() => window.dispatchEvent(new Event("cage-open-archives")));
  await drawer.waitFor({state:"hidden"});
  await phone.getByRole("heading",{name:"准备发布说明",exact:true}).waitFor();
  assert.equal(await phone.getByRole("heading",{name:"电脑保留的历史",exact:true}).count(),0);
  const phoneGroup = phone.locator(".archive-project").first();
  const phoneBefore = await phoneGroup.boundingBox();
  const phoneFilter = phone.getByRole("combobox",{name:"项目筛选"});
  await phoneFilter.click();
  const phoneOptions = await phone.getByRole("listbox",{name:"项目筛选选项"}).boundingBox();
  assert.ok(Math.abs((await phoneGroup.boundingBox()).y-phoneBefore.y)<1 && phoneOptions.height>0,"floating phone filter moved content");
  await phoneFilter.press("Escape");
  assert.ok(Math.abs((await phoneGroup.boundingBox()).y-phoneBefore.y)<2);
  await phone.getByRole("heading",{name:"已归档会话",exact:true}).click();
  await phone.screenshot({path:output+"/"+(process.env.CAGE_TEST_BROWSER||"chromium")+"-archived-mobile.png"});
  assert.deepEqual(errors,[],"before reload");
  await page.waitForLoadState("networkidle");
  await page.reload();
  await page.getByRole("button",{name:"另一段会话",exact:true}).waitFor();
  assert.equal(await history.count(),0);
  assert.equal(sessions.length,4);
  assert.equal(sessions[0].id,otherID);
  assert.deepEqual(errors,[]);
  assert.deepEqual(phoneErrors,[]);
  console.log("Persistent desktop history, terminal resume, bidirectional archive/restore, purge confirmation/failure/retry, reload passed");
} finally { await browser.close(); }
