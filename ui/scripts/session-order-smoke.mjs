import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const engine = process.env.CAGE_TEST_BROWSER || "chromium";
const output = process.env.CAGE_TEST_CAPTURE || "/tmp/cage-session-order";
let state, opens = 0;
const sessions = [
  { id:"order-idle", title:"已打开的空闲会话", updated_at:"2026-11-07T10:00:00Z" },
  { id:"order-active", title:"正在执行的会话", active:true, updated_at:"2026-11-07T09:00:00Z" },
  { id:"order-saved", title:"保存的会话", updated_at:"2026-11-07T08:00:00Z" },
];
const fixture = await createWorkbench({ url: process.env.CAGE_UI_URL || "http://127.0.0.1:5217",
  cliRequest: async (route, path) => {
    if (path === "/cli/sessions") return route.fulfill({json:{sessions}});
    if (path.endsWith("/account")) return route.fulfill({json:{session:{},supported:false,candidates:[]}});
    const session = sessions.find(s => path.includes(s.id));
    if (path.endsWith("/open")) {
      opens++;
      const terminal = {id:"opened-history", workspace:state.settings.workspace, title:session.title, cli_session_id:session.id};
      state.terminals.push(terminal);
      return route.fulfill({json:{...session,desktop_terminal_id:terminal.id}});
    }
    return route.fulfill({json:session});
  },
});
state = fixture.state;
const {page,browser,errors} = fixture;
try {
  sessions.forEach(session => {session.workspace = state.settings.workspace;});
  state.status.ready = state.status.vm_running = true;
  state.terminals = [{id:"idle", workspace:state.settings.workspace, title:sessions[0].title, cli_session_id:sessions[0].id}];
  const project = page.getByRole("region",{name:"项目 默认工作区",exact:true});
  const order = () => project.locator(".sidebar-tab").evaluateAll(rows => rows.map(row => row.querySelector(":scope > button").textContent));
  const saved = project.getByRole("button",{name:"保存的会话",exact:true});
  await saved.waitFor();
  assert.deepEqual(await order(),[sessions[1].title,sessions[0].title,sessions[2].title]);
  await project.getByRole("img",{name:"会话正在运行"}).waitFor();
  await mkdir(output,{recursive:true});
  await page.screenshot({path:`${output}/${engine}-active.png`});
  const phoneContext = await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true,userAgent:"CageMobile/1"});
  const phone = await phoneContext.newPage();
  await phone.route("**/api/**", async route => {
    const pathname = new URL(route.request().url()).pathname;
    return route.fulfill({json:pathname.endsWith("/projects")
      ? {host:"隔离测试",ready:true,shared_sessions:true,projects:[{path:state.settings.workspace,name:"默认工作区"}]}
      : {sessions}});
  });
  await phone.goto((process.env.CAGE_UI_URL || "http://127.0.0.1:5217")+"/mobile.html");
  const drawer = phone.getByRole("dialog",{name:"工作区侧栏"});
  await drawer.getByRole("img",{name:"会话正在运行"}).waitFor();
  assert.equal(await drawer.locator(".session-select").first().getAttribute("aria-label"),sessions[1].title);
  await phoneContext.close();
  const target = project.getByRole("button",{name:sessions[1].title,exact:true});
  const from = await saved.boundingBox(), to = await target.boundingBox();
  await page.mouse.move(from.x+35,from.y+from.height/2); await page.mouse.down();
  await page.mouse.move(to.x+35,to.y+to.height/2,{steps:12}); await page.mouse.up();
  assert.equal(opens,0,"drag opened a conversation");
  assert.deepEqual(await order(),[sessions[2].title,sessions[1].title,sessions[0].title]);
  await saved.click();
  await project.getByRole("tab",{name:sessions[2].title,exact:true}).waitFor();
  assert.deepEqual(await order(),[sessions[2].title,sessions[1].title,sessions[0].title],"opening changed order");
  await project.getByRole("tab",{name:sessions[0].title,exact:true}).click();
  assert.deepEqual(await order(),[sessions[2].title,sessions[1].title,sessions[0].title],"click changed order");
  sessions[1].active = false;
  await page.evaluate(() => window.dispatchEvent(new Event("cage-cli-sessions-changed")));
  await project.getByRole("img",{name:"会话正在运行"}).waitFor({state:"hidden"});
  assert.deepEqual(await order(),[sessions[2].title,sessions[1].title,sessions[0].title],"activity changed manual order");
  await page.reload();
  await project.getByRole("tab",{name:sessions[2].title,exact:true}).waitFor();
  assert.deepEqual(await order(),[sessions[2].title,sessions[1].title,sessions[0].title],"reload lost manual order");
  const selected = project.getByRole("tab",{name:sessions[2].title,exact:true});
  await selected.focus(); await selected.press("Alt+ArrowDown");
  assert.deepEqual(await order(),[sessions[1].title,sessions[2].title,sessions[0].title],"keyboard reorder failed");
  const before = await order();
  const a = await selected.boundingBox(), b = await project.getByRole("tab",{name:sessions[0].title,exact:true}).boundingBox();
  await page.mouse.move(a.x+30,a.y+15); await page.mouse.down();
  await page.mouse.move(b.x+30,b.y+15,{steps:8}); await page.keyboard.press("Escape"); await page.mouse.up();
  assert.deepEqual(await order(),before,"Escape committed a drag");
  assert.deepEqual(errors,[]);
  await mkdir(output,{recursive:true});
  await page.screenshot({path:`${output}/${engine}.png`});
  await writeFile(`${output}/${engine}.json`,JSON.stringify({order:await order(),opens,errors}));
  console.log(`${engine}: active priority/spinner, mixed live/history drag, no click reorder, manual priority, reload, keyboard and cancellation passed`);
} finally {await browser.close();}
