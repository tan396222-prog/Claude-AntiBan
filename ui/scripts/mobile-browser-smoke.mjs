// Exercises UI recovery and touch mapping with a synthetic, paired-host API.
// Run after node mobile/build-ui.mjs with CAGE_PLAYWRIGHT_MODULE as needed.
import assert from "node:assert/strict";
import http from "node:http";
import {mkdir, readFile} from "node:fs/promises";
import {join} from "node:path";
import {fileURLToPath} from "node:url";
const playwright = await import(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const engine = process.env.CAGE_TEST_MOBILE_BROWSER || "chromium";
const resources = new URL("../../mobile/.build/resources/mobile-ui/", import.meta.url);
const httpd = http.createServer(async (req, res) => {
  const path = new URL(req.url, "http://localhost").pathname;
  try {
    if (path !== "/" && path !== "/mobile.html" && !/^\/assets\/[\w.-]+$/.test(path)) throw Error();
    const file = new URL(path.slice(1) || "mobile.html", resources);
    const mime = {js:"text/javascript", html:"text/html", png:"image/png", css:"text/css"}[file.pathname.split(".").pop()];
    res.setHeader("Content-Type", mime);
    res.end(await readFile(fileURLToPath(file)));
  } catch {res.writeHead(404).end();}
});
await new Promise(resolve => httpd.listen(0, "127.0.0.1", resolve));
let browser;
try {
  browser = await playwright[engine].launch();
  const context = await browser.newContext({viewport:{width:390, height:844}, hasTouch:true, isMobile:true, deviceScaleFactor:1, userAgent:"CageMobile/1"});
  await context.addInitScript(() => {
    window.__cageDevices = {current:"a", devices:[{id:"a",name:"Cage 工作电脑"},{id:"b",name:"另一台电脑"}]};
    window.deviceCommands = [];
    window.CageDevices = {postMessage(raw) {
      const message = JSON.parse(raw); window.deviceCommands.push(message);
      if (message.action === "select") {
        window.__cageDevices = {...window.__cageDevices,current:message.id};
        window.dispatchEvent(new CustomEvent("cage-devices",{detail:window.__cageDevices}));
      }
    }};
  });
  const page = await context.newPage();
  page.setDefaultTimeout(8000);
  const image = await page.evaluate(() => {
    const canvas = document.createElement("canvas");
    canvas.width = 12; canvas.height = 16;
    const c = canvas.getContext("2d"); c.fillStyle = "#555"; c.fillRect(0,0,12,16);
    return canvas.toDataURL("image/jpeg").split(",")[1];
  });
  const homeNotice = "电脑的新标签页无法在手机浏览器打开，请输入其他网址。";
  let enabled = false, frame, opens = 0, closes = 0, reads = 0;
  let dropNextText = false, resizeHold, reloadHold;
  let modern = false, readGate, writeGate, failedScroll, disconnectNavigation, idOnly;
  let tag = 0;
  const frameTag = () => tag.toString(16).padStart(32,"0");
  const responseFrame = () => ({...frame, frame_tag:frameTag(), ...(modern && {action_frame:true})});
  const gate = () => {
    let enter, release;
    const entered = new Promise(resolve => {enter = resolve;});
    const hold = new Promise(resolve => {release = resolve;});
    return {entered,release,wait:() => {enter(); return hold;}};
  };
  let desktopEnabled = false, desktopStartupReads = 0;
  let favoritesAvailable = false, favoritesGate;
  const favoriteWrites = [];
  const favorites = {folders:[{id:"ref",title:"参考资料"}],items:[
    {id:"one",title:"设计指引",url:"https://design.example/",folder_id:"ref"},
    {id:"another",title:"日历",url:"https://calendar.example/",folder_id:""},
  ]};
  const sessionList = [{id:"70d7194d-33c0-428d-867e-f9e4cb9f902e",workspace:"/work/cage",
    title:"移动端导航",generation:"qa",revision:1,turns:[],updated_at:new Date().toISOString()}];
  const graphicCalls = [];
  const holdResize = () => {
    let release;
    resizeHold = new Promise(resolve => {release = resolve;});
    return release;
  };
  const actions = [];
  const pairedHost = async route => {
    const req = route.request();
    const path = new URL(req.url()).pathname, body = req.postDataJSON();
    const reply = (value) => route.fulfill({json: value});
    if (path === "/api/mobile/cli/projects") return reply({host:"Cage 工作电脑", ready:true,
      projects:[{path:"/work/cage",name:"Cage"},{path:"/work/web",name:"个人网站"}]});
    if (path === "/api/mobile/cli/sessions" && body) {
      const session = {id:body.id, workspace:body.workspace, title:"测试会话", generation:"qa", revision:1, turns:[], updated_at:new Date().toISOString()};
      sessionList.push(session);
      return reply(session);
    }
    if (path === "/api/mobile/cli/sessions") return reply({sessions:sessionList});
    if (path.startsWith("/api/mobile/cli/sessions/")) return reply(sessionList.find(s => path.endsWith(s.id)));
    if (path.startsWith("/api/mobile/desktop/")) {
      if (!desktopEnabled) return route.fulfill({status:403,contentType:"text/plain",body:"请在电脑上允许 Claude Desktop。"});
      const op = path.split("/").at(-1);
      if (op !== "frame") graphicCalls.push({tool:"desktop",op,body});
      if (op === "open") {frame = {...body, document:"desktop-doc", focus:"", window:"desktop", image}; return reply({id:frame.id});}
      assert.ok(path.includes("/" + frame.id + "/"));
      if (op === "frame" && !desktopStartupReads++) return route.fulfill({status:425});
      if (op === "frame") return reply(frame);
      if (op === "action") {
        assert.equal(body.document, frame.document);
        if (body.type === "resize") Object.assign(frame,{width:body.width,height:body.height,
          ...(body.pixel_ratio && {requested_pixel_ratio:body.pixel_ratio,pixel_ratio:body.pixel_ratio})});
        if (body.type === "tap") frame.focus = "compose";
        if (body.type === "login") Object.assign(frame,{window:"login",document:"login-doc",focus:"credential",password:true});
        if (body.type === "key" || body.type === "text") assert.equal(frame.focus,body.focus);
        if (body.type === "key") await new Promise(resolve => setTimeout(resolve,65));
      }
      return reply({id:frame.id});
    }
    if (!path.startsWith("/api/mobile/browser/")) return route.fulfill({status:404});
    if (path === "/api/mobile/browser/favorites") {
      if (!favoritesAvailable) return route.fulfill({status:404});
      if (body) {
        favoriteWrites.push(body);
        if (body.action === "save") favorites.items.push({id:"new",...body});
        else if (body.action === "delete") favorites.items = favorites.items.filter(item => item.id !== body.id);
        else throw Error("unexpected favorite mutation");
      }
      const result = structuredClone(favorites);
      const pending = favoritesGate; favoritesGate = null;
      if (pending) await pending.wait();
      return reply(result);
    }
    if (!enabled) return route.fulfill({status:403, contentType:"text/plain", body:"请先在电脑上允许远程浏览器。"});
    if (path.endsWith("/open")) {
      opens++;
      graphicCalls.push({tool:"browser",op:"open",body});
      frame = {...body, document:"doc-1", focus:"", password:false, url:"", image};
      tag++;
      return reply({id:body.id, ...(modern && {action_frame:true,home_notice:homeNotice})});
    }
    assert.ok(path.includes("/" + frame.id + "/"));
    if (path.endsWith("/close")) {closes++; return reply({id:frame.id});}
    if (path.endsWith("/frame")) {
      reads++;
      const after = new URL(req.url()).searchParams.get("after");
      if (!modern) assert.equal(after,null,"a tag from an older host must not enable conditional requests");
      const snapshot = responseFrame();
      const pending = readGate; readGate = null;
      if (pending) {
        await pending.wait();
        return reply({...snapshot,url:"https://stale.example/",document:"obsolete-doc"});
      }
      return reply(after === frameTag() ? {id:frame.id,unchanged:true,frame_tag:after} : snapshot);
    }
    assert.ok(path.endsWith("/action"));
    actions.push(body);
    assert.equal(body.return_frame,modern || undefined,"only a capable host accepts the new action field");
    assert.equal(body.document, frame.document);
    if (body.type === "resize") {
      const hold = resizeHold;
      resizeHold = null;
      if (hold) await hold;
      Object.assign(frame, {width:body.width, height:body.height, document:"viewport-" + actions.length});
    }
    if (body.type === "reload" && reloadHold) await reloadHold;
    if (body.type === "navigate") Object.assign(frame, {url:body.url,document:"doc-2"});
    if (body.type === "tap") frame.focus = "email-field";
    if (body.type === "text" || body.type === "key") assert.equal(body.focus,frame.focus);
    if (body.type === "text" && dropNextText) {
      dropNextText = false;
      return route.abort("internetdisconnected");
    }
    tag++;
    if (modern) {
      const snapshot = responseFrame();
      const pending = writeGate; writeGate = null;
      if (pending) await pending.wait();
      if (failedScroll && body.type === "scroll") {failedScroll = false; return route.abort("internetdisconnected");}
      if (disconnectNavigation && body.type === "navigate") {
        disconnectNavigation = false;
        frame.url = "https://recovered.example/";
        return reply({...snapshot,url:"https://obsolete-write.example/"});
      }
      if (idOnly) {idOnly = false; return reply({id:frame.id});}
      return reply(snapshot);
    }
    return reply({id:frame.id});
  };
  const origin = "http://127.0.0.1:" + httpd.address().port;
  await page.route("**/api/mobile/**",pairedHost);
  await page.goto(origin);
  const drawer = page.getByRole("dialog",{name:"工作区侧栏"});
  await drawer.waitFor();
  for (const name of ["浏览器","Claude.ai","Claude Desktop"]) assert.equal(await drawer.getByRole("button",{name,exact:true}).count(),1);
  await drawer.getByRole("heading",{name:"Claude Code",exact:true}).waitFor();
  assert.equal(await drawer.getByRole("button",{name:"Claude Code",exact:true}).count(),0);
  assert.equal(await drawer.getByRole("combobox").count(),0);
  const menu = page.getByRole("button",{name:"展开侧栏",exact:true});
  const firstFolder = drawer.getByRole("button",{name:"项目 Cage",exact:true});
  const secondFolder = drawer.getByRole("button",{name:"项目 个人网站",exact:true});
  const exit = drawer.getByRole("link",{name:"退出",exact:true});
  await drawer.getByRole("button",{name:"移动端导航",exact:true}).waitFor();
  if (process.env.CAGE_TEST_MOBILE_CAPTURE) {
    await mkdir(process.env.CAGE_TEST_MOBILE_CAPTURE,{recursive:true});
    await page.screenshot({path:join(process.env.CAGE_TEST_MOBILE_CAPTURE,engine+"-workspace-drawer.png")});
  }
  await page.setViewportSize({width:390,height:300});
  const scroll = drawer.locator(".drawer-content");
  await page.waitForFunction(() => {const el = document.querySelector(".drawer-content"); return el.scrollHeight > el.clientHeight;});
  const footerTop = (await exit.boundingBox()).y;
  await scroll.evaluate(el => {el.scrollTop = el.scrollHeight;});
  assert.ok(await scroll.evaluate(el => el.scrollTop > 0));
  assert.equal((await exit.boundingBox()).y,footerTop);
  assert.ok(footerTop > 200 && footerTop < 300);
  await page.setViewportSize({width:390,height:844});
  assert.equal(await drawer.getByRole("button",{name:"收起侧栏"}).count(),0);
  await page.touchscreen.tap(380, 400);
  await drawer.waitFor({state:"hidden"});
  assert.equal(await page.locator(".topbar button").count(),1);
  assert.ok(await menu.getByRole("img",{name:"Cage"}).count());
  await page.getByRole("img",{name:"Claude Code",exact:true}).waitFor();
  await menu.click();
  await page.waitForFunction(() => document.querySelector(".workspace-drawer")?.getAnimations().some(animation => animation.transitionProperty === "transform"));
  await page.keyboard.press("Escape");
  await drawer.waitFor({state:"hidden"});
  await menu.click();
  await drawer.getByRole("button", {name:"浏览器",exact:true}).click();
  await drawer.waitFor({state:"hidden"});
  await page.getByRole("img",{name:"浏览器",exact:true}).waitFor();
  await page.getByText("请先在电脑上允许远程浏览器。").waitFor();
  enabled = true;
  await page.getByRole("button",{name:"重新打开",exact:true}).click();
  const imageView = page.getByRole("img",{name:"远程浏览器画面"});
  const ready = async () => {
    await imageView.waitFor();
    await page.waitForFunction(() => !document.querySelector('button[aria-label="重新加载网页"]')?.disabled);
  };
  await ready();
  assert.equal(await page.locator(".topbar input").count(),1);
  assert.equal(Math.round((await imageView.boundingBox()).y), Math.round((await page.locator(".topbar").boundingBox()).height));
  // Browser reload must resume by GET, even when the remote browser is idle.
  await page.reload();
  await ready();
  assert.equal(opens,1);
  const address = page.getByRole("textbox",{name:"远程网页地址"});
  // The soft keyboard can start resize before the user presses Go. Hold the
  // resize response until the address is submitted and confirm a single write.
  const duringResize = async (height, url) => {
    const release = holdResize();
    const resize = page.waitForRequest(req => req.url().endsWith("/action") && req.postDataJSON()?.type === "resize");
    await address.focus();
    await page.setViewportSize({width:390, height});
    await resize;
    await address.fill(url);
    await address.press("Enter");
    await page.waitForFunction(() => document.querySelector('[aria-label="前往网址"]').getAttribute("aria-busy") === "true");
    assert.equal(actions.filter(action => action.type === "navigate" && action.url === url).length,0);
    return release;
  };
  const target = "https://example.org/";
  const navigation = page.waitForResponse(res => res.url().endsWith("/action") && res.request().postDataJSON()?.type === "navigate");
  const release = await duringResize(682, target);
  release();
  await navigation;
  await ready();
  assert.equal(actions.filter(action => action.type === "navigate" && action.url === target).length,1);
  // A different user action (reload) must never silently queue navigation.
  let releaseReload;
  reloadHold = new Promise(resolve => {releaseReload = resolve;});
  await page.getByRole("button",{name:"重新加载网页"}).click();
  await address.fill("https://busy.example.org/");
  await address.press("Enter");
  await page.getByText("上一项操作尚未完成，请稍后再次前往。").waitFor();
  releaseReload();
  await ready();
  assert.equal(actions.filter(action => action.url === "https://busy.example.org/").length, 0);
  const matchingFrame = page.waitForResponse(async response => {
    if (!response.url().endsWith("/frame") || !response.ok()) return false;
    const captured = await response.json(), rect = await imageView.boundingBox();
    return rect && Math.round(rect.width) === captured.width && Math.round(rect.height) === captured.height;
  });
  await address.press("Escape");
  await matchingFrame;
  await ready();
  const box = await imageView.boundingBox();
  await page.touchscreen.tap(box.x + box.width * .25,box.y + box.height * .45);
  const keyboard = page.getByRole("button",{name:"输入文字",exact:true});
  await keyboard.click();
  const tap = actions.find(a => a.type === "tap");
  assert.ok(Math.abs(tap.x / Math.round(box.width) - .25) < .01 && Math.abs(tap.y / Math.round(box.height) - .45) < .01);
  const field = page.getByLabel("待插入文字");
  await field.fill("手机中文输入");
  const initialField = await field.elementHandle();
  // Shrinking for a soft keyboard and rotating both invalidate the frame's
  // coordinates. Neither changes the identity of this remote editable field.
  for (const size of [{width:390,height:575}, {width:844,height:390}, {width:390,height:844}]) {
    const before = frame.document;
    const fresh = page.waitForResponse(async response => {
      if (!response.url().endsWith("/frame") || !response.ok()) return false;
      const value = await response.json();
      const stage = await imageView.boundingBox();
      return stage && value.document !== before && value.focus === "email-field" &&
        value.width === Math.round(stage.width) && value.height === Math.round(stage.height);
    });
    await page.setViewportSize(size);
    await fresh;
    await ready();
    assert.equal(await field.evaluate((el, original) => el === original && document.activeElement === el, initialField), true);
    assert.equal(await field.inputValue(), "手机中文输入");
    assert.equal(frame.width,size.width);
    assert.equal(await page.getByRole("navigation",{name:"远程网页操作"}).count(),0);
  }
  await initialField.dispose();
  await page.getByRole("button",{name:"插入文字",exact:true}).click();
  await page.getByRole("button",{name:"发送回车",exact:true}).click();
  assert.deepEqual(actions.filter(a => a.type === "text").map(a => a.text),["手机中文输入"]);
  assert.deepEqual(actions.filter(a => a.type === "key").map(a => a.key),["Enter"]);
  // An ambiguous text write may already have reached the host. Refresh must
  // only read status, and no password/text draft should survive the disconnect.
  await field.fill("uncertain");
  dropNextText = true;
  await page.getByRole("button",{name:"插入文字",exact:true}).click();
  await page.getByText("输入结果待确认", {exact:false}).waitFor();
  assert.equal(await imageView.count(),0);
  await page.getByRole("button",{name:"重新读取画面",exact:true}).click();
  await ready();
  assert.equal(actions.filter(a => a.text === "uncertain").length,1);
  assert.equal(opens,1);
  if (await field.count()) assert.equal(await field.inputValue(),"");
  // A new focused element must not receive the draft for its predecessor.
  if (!await field.count()) await keyboard.click();
  await field.fill("draft for email only");
  frame.focus = "password-field";
  frame.password = true;
  await field.waitFor({state:"detached"});
  await keyboard.click();
  assert.equal(await field.getAttribute("type"), "password");
  assert.equal(await field.inputValue(), "");
  await field.fill("synthetic password");
  await page.evaluate(() => window.dispatchEvent(new Event("pagehide")));
  await imageView.waitFor({state:"detached"});
  assert.equal(await field.count(), 0);
  const suspended = reads;
  await page.waitForTimeout(900);
  assert.equal(reads, suspended);
  await page.evaluate(() => window.dispatchEvent(new Event("pageshow")));
  await ready();
  assert.equal(await field.count(), 0);
  assert.equal(actions.filter(a => a.text === "synthetic password").length, 0);
  await page.setViewportSize({width:844,height:390});
  await page.waitForFunction(() => !document.querySelector('button[aria-label="重新加载网页"]').disabled);
  for (let i=0; i<40 && !actions.some(a => a.type === "resize" && a.width > 600); i++) await page.waitForTimeout(100);
  assert.ok(actions.some(a => a.type === "resize" && a.width > 600));
  await ready();
  const before = await imageView.boundingBox();
  const xy = {x:before.x+before.width/2, y:before.y+before.height*.7};
  await page.mouse.move(xy.x,xy.y);
  await page.mouse.down();
  await page.mouse.move(xy.x,xy.y-45);
  await page.mouse.up();
  for (let i=0; i<30 && !actions.some(a => a.type === "scroll"); i++) await page.waitForTimeout(100);
  assert.ok(actions.some(a => a.type === "scroll" && a.dy > 0));
  const abandoned = "https://abandoned.example.org/";
  const afterClose = await duringResize(777, abandoned);
  await page.getByRole("button",{name:"关闭远程浏览器",exact:true}).click();
  await page.getByRole("button",{name:"浏览器",exact:true}).waitFor();
  afterClose();
  const count = reads;
  await page.waitForTimeout(900);
  assert.equal(reads,count);
  assert.equal(closes,1);
  assert.equal(actions.filter(action => action.type === "navigate" && action.url === abandoned).length,0);
  await drawer.waitFor();
  assert.equal(await firstFolder.getAttribute("aria-expanded"),"true");
  assert.equal(await secondFolder.getAttribute("aria-expanded"),"false");
  await secondFolder.click();
  assert.equal(await firstFolder.getAttribute("aria-expanded"),"true");
  assert.equal(await secondFolder.getAttribute("aria-expanded"),"true");
  await firstFolder.focus(); await firstFolder.press("Enter");
  assert.equal(await firstFolder.evaluate(el => el === document.activeElement),true);
  assert.equal(await drawer.getByRole("button",{name:"移动端导航",exact:true}).count(),0);
  assert.equal(await secondFolder.getAttribute("aria-expanded"),"true");
  await drawer.getByRole("button",{name:"在 个人网站 新建会话",exact:true}).click();
  await drawer.waitFor({state:"hidden"});
  const composer = page.getByRole("textbox",{name:"消息"});
  await composer.fill("尚未发送的草稿");
  assert.equal(sessionList.at(-1).workspace,"/work/web");
  await menu.click(); await drawer.getByRole("button",{name:"Claude.ai",exact:true}).click();
  await ready();
  await page.getByRole("img",{name:"Claude.ai",exact:true}).waitFor();
  assert.equal(await address.count(),0);
  const claudeBox = await imageView.boundingBox();
  assert.equal(claudeBox.width,390);
  assert.ok(claudeBox.height > 667);
  await page.getByRole("button",{name:"网页地址",exact:true}).click();
  assert.equal(await address.count(),1);
  assert.equal((await imageView.boundingBox()).height,claudeBox.height);
  assert.equal(graphicCalls.filter(call => call.tool === "browser" && call.op === "open").at(-1).body.url, "https://claude.ai/");
  await menu.click();
  await firstFolder.click();
  assert.equal(await firstFolder.getAttribute("aria-expanded"),"true");
  assert.equal(await secondFolder.getAttribute("aria-expanded"),"true");
  await page.getByRole("img",{name:"Claude.ai",exact:true}).waitFor();
  await drawer.getByRole("button",{name:"测试会话",exact:true}).click();
  await composer.waitFor();
  assert.equal(await composer.inputValue(), "尚未发送的草稿");
  await menu.click(); await drawer.getByRole("button",{name:"Claude Desktop",exact:true}).click();
  await page.getByRole("img",{name:"Claude Desktop",exact:true}).waitFor();
  await page.getByText("请在电脑上允许 Claude Desktop。").waitFor();
  desktopEnabled = true;
  await page.getByRole("button",{name:"重新打开",exact:true}).click();
  await page.getByText("正在启动远程 Claude Desktop…").waitFor();
  const desktop = page.getByRole("img",{name:"远程 Desktop 画面"});
  await desktop.waitFor();
  const logicalWidth = frame.width;
  const fit = await desktop.boundingBox();
  const desktopResizes = () => graphicCalls.filter(call => call.tool === "desktop" && call.body?.type === "resize").length;
  const priorResizes = desktopResizes();
  await page.getByRole("button",{name:"放大阅读",exact:true}).click();
  assert.ok((await desktop.boundingBox()).width > fit.width * 1.5);
  const stage = page.locator(".remote-stage");
  const priorLeft = await stage.evaluate(el => el.scrollLeft);
  await page.mouse.move(fit.x + fit.width/2, fit.y + fit.height/2); await page.mouse.down();
  await page.mouse.move(fit.x + fit.width/2 - 50, fit.y + fit.height/2); await page.mouse.up();
  assert.ok(await stage.evaluate(el => el.scrollLeft) > priorLeft);
  await page.getByRole("button",{name:"全幅",exact:true}).click();
  assert.equal(frame.width,logicalWidth); assert.equal(desktopResizes(),priorResizes);
  await desktop.tap();
  await page.getByRole("button",{name:"输入文字",exact:true}).click();
  await field.fill("Desktop 中文");
  await page.getByRole("button",{name:"插入文字",exact:true}).click();
  await page.getByRole("button",{name:"收起输入框",exact:true}).click();
  await page.getByRole("button",{name:"登录窗口",exact:true}).click();
  await page.getByRole("button",{name:"返回 Desktop",exact:true}).waitFor();
  await page.getByRole("button",{name:"输入文字",exact:true}).click();
  const loginField = page.getByLabel("逐键登录输入");
  await loginField.pressSequentially("Ab2");
  assert.equal(await loginField.inputValue(),"");
  for (let i=0; i<40 && graphicCalls.filter(call => call.body?.type === "key").length < 3; i++) await page.waitForTimeout(100);
  assert.deepEqual(graphicCalls.filter(call => call.body?.type === "key").map(call=>call.body.key),["A","b","2"]);
  assert.equal(graphicCalls.filter(call => call.body?.type === "text").length,1);
  await page.getByRole("button",{name:"收起输入框",exact:true}).click();
  Object.assign(frame, {shared:true});
  await page.getByRole("button",{name:"返回 Desktop",exact:true}).waitFor({state:"hidden"});
  assert.equal(await page.getByRole("button",{name:"登录窗口",exact:true}).count(),0);
  await page.getByRole("button",{name:"断开远程桌面",exact:true}).waitFor();
  assert.equal(graphicCalls.filter(call => call.body?.pixel_ratio).length,0);
  Object.assign(frame,{pixel_ratio_supported:true,requested_pixel_ratio:2,pixel_ratio:2});
  for (const ratio of [3.5,2.625]) {
    await page.evaluate(value => Object.defineProperty(window,"devicePixelRatio",{configurable:true,value}),ratio);
    for (let i=0; i<60 && frame.requested_pixel_ratio!==ratio; i++) await page.waitForTimeout(100);
    assert.equal(frame.requested_pixel_ratio,ratio);
    assert.equal(graphicCalls.filter(call=>call.body?.pixel_ratio===ratio).length,1);
  }
  await menu.click(); await exit.click();
  await page.waitForFunction(() => window.deviceCommands.some(m => m.action === "home"));
  await desktop.waitFor({state:"detached"});
  for (let i=0; i<20 && !graphicCalls.some(c => c.tool === "desktop" && c.op === "close"); i++) await page.waitForTimeout(100);
  assert.equal(graphicCalls.filter(c => c.tool === "desktop" && c.op === "close").length,1);
  assert.ok(await page.evaluate(() => window.deviceCommands.every(m => m.action !== "disconnect" && m.action !== "select")));
  modern = true;
  await drawer.getByRole("button",{name:"浏览器",exact:true}).click();
  await drawer.waitFor({state:"hidden"});
  await ready();
  await page.getByText(homeNotice).waitFor();
  assert.equal(await address.isEnabled(),true);
  assert.equal(await page.getByRole("button",{name:"收藏夹",exact:true}).isEnabled(),true);
  // A previous conditional read may finish well after an accepted action.
  // Its old URL/document must never replace the action's newer frame.
  const oldRead = readGate = gate();
  await oldRead.entered;
  const latestAction = page.waitForResponse(res => res.url().endsWith("/action") && res.request().postDataJSON()?.url === "https://latest.example/");
  await address.fill("https://latest.example/"); await address.press("Enter");
  await latestAction;
  await page.waitForFunction(() => document.activeElement !== document.querySelector('[aria-label="远程网页地址"]'));
  oldRead.release();
  await page.waitForTimeout(160);
  assert.equal(await address.inputValue(),"https://latest.example/");
  assert.equal(await page.getByText(homeNotice).count(),0);
  const pausedWrite = writeGate = gate();
  disconnectNavigation = true;
  await address.fill("https://background.example/"); await address.press("Enter");
  await pausedWrite.entered;
  await page.evaluate(() => window.dispatchEvent(new Event("pagehide")));
  await imageView.waitFor({state:"detached"});
  await page.evaluate(() => window.dispatchEvent(new Event("pageshow")));
  pausedWrite.release();
  await ready();
  assert.equal(await address.inputValue(),"https://recovered.example/");
  const motion = await imageView.boundingBox();
  const firstWrite = writeGate = gate();
  failedScroll = true;
  const startActions = actions.length;
  const x = motion.x + motion.width/2, y = motion.y + motion.height*.7;
  await page.mouse.move(x,y); await page.mouse.down(); await page.mouse.move(x,y-24);
  await firstWrite.entered;
  await page.mouse.move(x,y-56); await page.mouse.move(x,y-84); await page.mouse.up();
  firstWrite.release();
  await page.getByText("输入结果待确认",{exact:false}).waitFor();
  assert.deepEqual(actions.slice(startActions).map(a=>a.dy),[24]);
  await page.getByRole("button",{name:"重新读取画面"}).click(); await ready();
  await page.waitForTimeout(330);
  assert.deepEqual(actions.slice(startActions).map(a=>a.dy),[24],"unsent movement must be discarded after uncertain input");
  // A fresh touch cancels the tail of the prior drag. Do not send that tail
  // to a different scrollable panel under the next finger.
  const interrupted = writeGate = gate();
  const firstDrag = actions.length;
  await page.mouse.move(x,y); await page.mouse.down(); await page.mouse.move(x,y-20);
  await interrupted.entered;
  await page.mouse.move(x,y-55); await page.mouse.up();
  await page.mouse.move(x+64,y); await page.mouse.down(); await page.mouse.move(x+64,y-18); await page.mouse.up();
  interrupted.release();
  for (let i=0; i<25 && actions.length < firstDrag+2; i++) await page.waitForTimeout(50);
  assert.deepEqual(actions.slice(firstDrag).map(a=>[a.x,a.dy]),[[Math.floor(x-motion.x),20],[Math.floor(x+64-motion.x),18]]);
  // An upgraded host may bridge an older guest which only acknowledges the
  // write. Fetch the result once with no tag and never resend that action.
  idOnly = true;
  const once = actions.length;
  const fallback = page.waitForResponse(res => res.url().endsWith("/frame") && res.ok());
  const longAddress = "https://ack-only.example/" + "a".repeat(5000);
  await address.fill(longAddress); await address.press("Enter");
  await fallback;
  await ready();
  assert.deepEqual(actions.slice(once).map(a=>a.type),["navigate"]);
  assert.equal(await address.inputValue(),longAddress);
  const bookmarks = page.getByRole("button",{name:"收藏夹",exact:true});
  const panel = page.getByRole("region",{name:"电脑收藏夹",exact:true});
  await bookmarks.click();
  await panel.getByText("这台电脑尚不支持共享收藏夹，请更新电脑端 Cage。").waitFor();
  favoritesAvailable = true;
  await panel.getByRole("button",{name:"刷新收藏夹"}).click();
  const folder = panel.getByRole("button",{name:"参考资料",exact:true});
  const rootFavorite = panel.getByRole("button",{name:/^日历/});
  const rootTop = (await rootFavorite.boundingBox()).y;
  await folder.focus(); await folder.press("Enter");
  assert.ok((await rootFavorite.boundingBox()).y >= rootTop + 40);
  await folder.press("Space");
  assert.equal((await rootFavorite.boundingBox()).y,rootTop);
  await panel.getByRole("button",{name:"收藏此页"}).click();
  const destination = panel.getByRole("combobox",{name:"保存到"});
  const saveBookmark = panel.getByRole("button",{name:"保存收藏"});
  const saveTop = (await saveBookmark.boundingBox()).y;
  await destination.focus(); await destination.press("Enter");
  assert.ok((await saveBookmark.boundingBox()).y >= saveTop + 72);
  await destination.press("ArrowDown"); await destination.press("Enter");
  assert.equal(await destination.evaluate(el => document.activeElement === el),true);
  assert.equal((await saveBookmark.boundingBox()).y,saveTop);
  await saveBookmark.click();
  await panel.getByRole("button",{name:"取消此页收藏",exact:true}).waitFor();
  assert.deepEqual(favoriteWrites[0],{action:"save",title:"ack-only.example",url:longAddress,folder_id:"ref"});
  assert.equal(await folder.getAttribute("aria-expanded"),"true");
  favorites.items.push({id:"from-computer",title:"电脑新加入",url:"https://added.example/",folder_id:"ref"});
  await panel.getByRole("button",{name:"刷新收藏夹"}).click();
  const added = panel.getByRole("button",{name:/^电脑新加入/});
  await added.waitFor();
  if (process.env.CAGE_TEST_MOBILE_CAPTURE) await page.screenshot({path:join(process.env.CAGE_TEST_MOBILE_CAPTURE,engine+"-phone-favorites.png")});
  const opensBefore = actions.filter(a=>a.type === "navigate").length;
  await added.click();
  await panel.waitFor({state:"detached"});
  assert.equal(frame.url,"https://added.example/");
  assert.equal(actions.filter(a=>a.type === "navigate").length,opensBefore+1);
  assert.equal(new URL(page.url()).origin,origin);
  await bookmarks.click();
  await panel.getByRole("button",{name:"取消此页收藏",exact:true}).click();
  await panel.getByRole("button",{name:"收藏此页",exact:true}).waitFor();
  assert.deepEqual(favoriteWrites.at(-1),{action:"delete",id:"from-computer"});
  const oldFavorites = favoritesGate = gate();
  await panel.getByRole("button",{name:"刷新收藏夹"}).click(); await oldFavorites.entered;
  await page.evaluate(() => window.dispatchEvent(new Event("offline")));
  await panel.waitFor({state:"detached"});
  oldFavorites.release();
  await page.getByRole("button",{name:"重新读取画面"}).click(); await ready();
  assert.equal(await panel.count(),0);
  favorites.items = [];
  await bookmarks.click(); await folder.waitFor();
  assert.equal(await rootFavorite.count(),0,"favorites from a previous connection must not reappear");
  await panel.getByRole("button",{name:"返回网页"}).click();
  await panel.waitFor({state:"detached"});
  await page.getByRole("button",{name:"关闭远程浏览器"}).click();
  const legacy = await browser.newPage({viewport:{width:850,height:720}});
  await legacy.route("**/api/mobile/**",pairedHost);
  await legacy.goto(origin + "/mobile.html");
  const project = legacy.getByRole("combobox",{name:"选择项目"});
  const newSession = legacy.getByRole("button",{name:"新建会话",exact:true});
  await project.waitFor();
  assert.equal(await legacy.getByRole("dialog").count(),0);
  const headingY = (await newSession.boundingBox()).y;
  await project.focus(); await project.press("Enter");
  assert.ok((await newSession.boundingBox()).y > headingY + 50);
  await project.press("ArrowDown"); await project.press("Enter");
  assert.equal(await project.getAttribute("aria-expanded"),"false");
  assert.equal(await project.evaluate(el => el === document.activeElement),true);
  assert.ok(Math.abs((await newSession.boundingBox()).y - headingY) <= 1);
  await legacy.getByRole("button",{name:"测试会话",exact:true}).click();
  await legacy.getByRole("textbox",{name:"消息"}).waitFor();
  console.log(engine + ": logo, project tree, drafts, fixed exit, shared favorites, viewport, Desktop pan and login keyboard passed");
} finally {
  await browser?.close();
  await new Promise(resolve => httpd.close(resolve));
}
