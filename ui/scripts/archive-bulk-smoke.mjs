import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const base = process.env.CAGE_UI_URL || "http://127.0.0.1:5209";
const engine = process.env.CAGE_TEST_BROWSER || "chromium";
const output = process.env.CAGE_TEST_CAPTURE || "../output/archive-bulk-20261108";
const alpha = "/fixture/alpha", beta = "/fixture/beta";
const seed = [
  {id:"a1",workspace:alpha,title:"项目方案",pinned:true},
  {id:"a2",workspace:alpha,title:"接口检查"},
  {id:"b1",workspace:beta,title:"移动端检查"},
  {id:"b2",workspace:beta,title:"待完成删除",deleting:true},
].map(s => ({...s,archived:true,updated_at:"2026-11-08T00:00:00Z"}));
let sessions = structuredClone(seed), failID = "", holdID = "", release;
const operations = [];
const handle = async (route, path, body) => {
  if (path === "/cli/sessions") return route.fulfill({json:{sessions:sessions.filter(s => s.archived === (new URL(route.request().url()).searchParams.get("archived") === "true"))}});
  const [, id, operation] = path.match(/\/sessions\/([^/]+)\/(restore|purge)$/) || [];
  if (!id) return route.fulfill({json:{}});
  operations.push({id,operation});
  if (operation === "purge") assert.equal(body.confirm,true);
  if (id === holdID) await new Promise(resolve => { release = resolve; });
  if (id === failID) return route.fulfill({status:503,contentType:"text/plain; charset=utf-8",body:"测试连接中断"});
  const session = sessions.find(s => s.id === id);
  if (operation === "restore") session.archived = false;
  else sessions = sessions.filter(s => s.id !== id);
  return route.fulfill({json:{ok:true}});
};
const desktop = await createWorkbench({url:base,cliRequest:handle});
const {page,browser,state,errors} = desktop;
const checkbox = (scope,name) => scope.getByRole("checkbox",{name,exact:true});
const allName = "全选当前筛选结果";
const projectName = path => `全选项目 ${path} 的当前筛选结果`;
try {
  state.status.ready = state.status.vm_running = true;
  await page.getByRole("button",{name:"设置",exact:true}).click();
  await page.getByRole("navigation",{name:"设置分组"}).getByRole("button",{name:"已归档会话",exact:true}).click();
  const panel = page.getByRole("region",{name:"已归档会话",exact:true});
  await checkbox(panel,allName).waitFor();
  const inputBox = await checkbox(panel,allName).boundingBox();
  const labelBox = await panel.locator(".archive-selection").first().locator("span").boundingBox();
  assert.ok(labelBox.x > inputBox.x && Math.abs(labelBox.y + labelBox.height / 2 - inputBox.y - inputBox.height / 2) < 2,"select-all label did not align with its checkbox");
  // Keyboard selection and mixed states must match the visible project and total.
  await checkbox(panel,"选择会话 项目方案").focus(); await page.keyboard.press("Space");
  assert.equal(await checkbox(panel,projectName(alpha)).evaluate(el => el.indeterminate),true);
  assert.equal(await checkbox(panel,allName).evaluate(el => el.indeterminate),true);
  await checkbox(panel,projectName(alpha)).check();
  assert.equal(await checkbox(panel,"选择会话 接口检查").isChecked(),true);
  assert.equal(await checkbox(panel,"选择会话 移动端检查").isChecked(),false);
  await checkbox(panel,allName).check();
  assert.equal(await panel.getByRole("button",{name:"批量取消归档",exact:true}).isDisabled(),true);
  await panel.getByRole("button",{name:"清空选择",exact:true}).click();
  assert.equal(await checkbox(panel,allName).isChecked(),false);
  const search = panel.getByRole("searchbox");
  await checkbox(panel,allName).check();
  await search.fill("接口");
  assert.equal(await checkbox(panel,allName).isChecked(),false,"filter kept hidden selections");
  await checkbox(panel,allName).check();
  await panel.getByRole("button",{name:"批量取消归档",exact:true}).click();
  await panel.getByRole("status").filter({hasText:"已取消归档 1 个会话"}).waitFor();
  assert.deepEqual(operations.splice(0),[{id:"a2",operation:"restore"}],"filtered select all affected hidden sessions");
  await search.fill("");
  // Changing filters clears selection; its floating list keeps content in place.
  await checkbox(panel,allName).check();
  const filter = panel.getByRole("combobox",{name:"项目筛选"});
  const toolbar = panel.getByRole("group",{name:"批量处理会话"});
  const before = await toolbar.boundingBox();
  await filter.click();
  const list = await page.getByRole("listbox",{name:"项目筛选选项"}).boundingBox();
  assert.ok(Math.abs((await toolbar.boundingBox()).y-before.y)<1 && list.height>0);
  await filter.press("Escape");
  assert.ok(Math.abs((await toolbar.boundingBox()).y-before.y)<2);
  await filter.press("End"); await filter.press("Enter");
  assert.equal(await checkbox(panel,allName).isChecked(),false);
  await filter.press("Home"); await filter.press("Enter");
  // Partial restore keeps only the failed selection and allows retry.
  sessions = structuredClone(seed);
  // A polling refresh brings the externally restored session back into the list.
  await panel.getByRole("heading",{name:"接口检查",exact:true}).waitFor();
  failID = "a2"; holdID = "a1";
  await checkbox(panel,projectName(alpha)).check();
  await panel.getByRole("button",{name:"批量取消归档",exact:true}).click();
  await panel.getByRole("status").filter({hasText:"正在处理"}).waitFor();
  assert.equal(await search.isDisabled(),true);
  assert.equal(await checkbox(panel,allName).isDisabled(),true);
  assert.equal(await panel.getByRole("button",{name:"批量删除",exact:true}).isDisabled(),true);
  release(); holdID = "";
  await panel.getByRole("alert").filter({hasText:"1 个会话处理失败"}).waitFor();
  assert.equal(await panel.getByRole("heading",{name:"项目方案",exact:true}).count(),0);
  assert.equal(await checkbox(panel,"选择会话 接口检查").isChecked(),true);
  assert.equal(await checkbox(panel,"选择会话 移动端检查").isChecked(),false);
  failID = "";
  await panel.getByRole("button",{name:"批量取消归档",exact:true}).click();
  await panel.getByRole("heading",{name:"接口检查",exact:true}).waitFor({state:"hidden"});
  // Batch purge requires confirmation. Partial purge retains failures for a fresh confirmation.
  await checkbox(panel,projectName(beta)).check();
  await panel.getByRole("button",{name:"批量删除",exact:true}).click();
  let confirm = panel.getByRole("group",{name:"确认批量永久删除会话",exact:true});
  await confirm.getByRole("button",{name:"取消",exact:true}).click();
  assert.equal(operations.filter(o => o.operation === "purge").length,0);
  failID = "b2";
  await panel.getByRole("button",{name:"批量删除",exact:true}).click();
  await confirm.getByRole("button",{name:"永久删除所选会话",exact:true}).click();
  await panel.getByRole("alert").filter({hasText:"待完成删除"}).waitFor();
  assert.equal(await checkbox(panel,"选择会话 待完成删除").isChecked(),true);
  assert.equal(await panel.getByRole("heading",{name:"移动端检查",exact:true}).count(),0);
  failID = "";
  await panel.getByRole("button",{name:"批量删除",exact:true}).click();
  await confirm.getByText(/所选的 1 个会话/).waitFor();
  await confirm.getByRole("button",{name:"永久删除所选会话",exact:true}).click();
  await panel.getByText("暂无已归档会话。",{exact:true}).waitFor();
  assert.deepEqual(operations.filter(o => o.operation === "purge").map(o => o.id),["b1","b2","b2"]);
  // Phone uses the same component and host API, including offline restore and deletion restrictions.
  sessions = structuredClone(seed);
  const context = await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,userAgent:"CageMobile/1"});
  const phone = await context.newPage();
  const phoneErrors = []; phone.on("pageerror",e => phoneErrors.push(e.message));
  let ready = false;
  await phone.route("**/api/**", async route => {
    const path = new URL(route.request().url()).pathname.replace("/api/mobile/cli","/cli");
    if (path.endsWith("/projects")) return route.fulfill({json:{host:"隔离测试主机",ready,shared_sessions:true,projects:[{path:alpha,name:"alpha"},{path:beta,name:"beta"}]}});
    return handle(route,path,route.request().postDataJSON());
  });
  await phone.goto(base+"/mobile.html");
  await phone.getByRole("dialog",{name:"工作区侧栏"}).getByRole("button",{name:"设置",exact:true}).click();
  await phone.getByRole("button",{name:"已归档会话",exact:true}).click();
  await checkbox(phone,projectName(alpha)).check();
  assert.equal(await phone.getByRole("button",{name:"批量删除",exact:true}).isDisabled(),true);
  assert.equal(await phone.getByRole("button",{name:"批量取消归档",exact:true}).isEnabled(),true);
  await phone.getByRole("button",{name:"批量取消归档",exact:true}).click();
  await phone.getByRole("heading",{name:"接口检查",exact:true}).waitFor({state:"hidden"});
  ready = true;
  await phone.getByRole("button",{name:"返回设置",exact:true}).click();
  await phone.getByRole("button",{name:"已归档会话",exact:true}).click();
  await checkbox(phone,allName).check();
  await phone.getByRole("button",{name:"批量删除",exact:true}).click();
  await phone.getByRole("group",{name:"确认批量永久删除会话"}).waitFor();
  await mkdir(output,{recursive:true});
  await phone.screenshot({path:`${output}/${engine}-mobile.png`,fullPage:true});
  assert.equal(await phone.evaluate(() => document.documentElement.scrollWidth <= innerWidth),true,"mobile layout overflowed");
  await phone.getByRole("button",{name:"永久删除所选会话",exact:true}).click();
  await phone.getByText("暂无已归档会话。",{exact:true}).waitFor();
  await phone.reload();
  await phone.getByRole("heading",{name:"已归档会话",exact:true}).waitFor();
  await phone.getByText("暂无已归档会话。",{exact:true}).waitFor();
  assert.equal(sessions.filter(s => s.archived).length,0);
  // Capture the desktop controls with selections for visual review.
  sessions = structuredClone(seed);
  await checkbox(panel,projectName(alpha)).check();
  await page.screenshot({path:`${output}/${engine}-desktop.png`});
  assert.deepEqual(errors,[]); assert.deepEqual(phoneErrors,[]);
  console.log(`${engine}: item/project/all selection, mixed state, keyboard, filter scope, progress/locking, restore/purge partial failure and retry, delete cancellation, mobile/offline/reload passed`);
} finally { await browser.close(); }
