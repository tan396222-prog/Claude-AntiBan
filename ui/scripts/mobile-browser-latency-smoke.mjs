// Build with node mobile/build-ui.mjs. Optional CAGE_MOBILE_BASELINE points
// at the previous mobile-ui directory. Both builds use the same real remote
// Chromium and page. Only the paired-host HTTP adapter and WAN delay are local.
import assert from "node:assert/strict";
import {once} from "node:events";
import {mkdtemp, readFile, rm} from "node:fs/promises";
import http from "node:http";
import net from "node:net";
import {tmpdir} from "node:os";
import {join} from "node:path";
import {setTimeout as delay} from "node:timers/promises";
import {fileURLToPath} from "node:url";
import {PhoneBrowser} from "../../internal/cage/browser-phone.mjs";

const playwright = await import(process.env.CAGE_PLAYWRIGHT_MODULE || "playwright");
const binary = process.env.CAGE_TEST_CHROMIUM || playwright.chromium.executablePath();
const engine = process.env.CAGE_TEST_MOBILE_BROWSER || "webkit";
const current = fileURLToPath(new URL("../../mobile/.build/resources/mobile-ui/",import.meta.url));
const site = http.createServer((req,res) => {
  res.setHeader("Content-Type","text/html; charset=utf-8");
  res.end('<meta name="viewport" content="width=device-width, initial-scale=1"><title>Scroll test</title>' +
    '<style>*{box-sizing:border-box}body{margin:0;font:18px sans-serif;scrollbar-width:none}::-webkit-scrollbar{display:none}button{display:block;width:100%;height:80px;touch-action:manipulation}p{padding:24px;margin:0;border-bottom:1px solid silver}</style>' +
    '<button onclick="document.body.style.backgroundColor=\'wheat\'">Change page colour</button>' +
    Array.from({length:100},(_,i)=>"<p>Row "+(i+1)+"</p>").join(""));
}).listen(0,"127.0.0.1");
await once(site,"listening");

async function measure(directory, baseline) {
  const profile = await mkdtemp(join(tmpdir(),"cage-phone-latency-"));
  const guest = new PhoneBrowser({directory:profile,binary,connect:async (host,port) => {
    if (host !== "scroll.example" || port !== site.address().port) throw Error("Unexpected test destination");
    const socket = net.connect({host:"127.0.0.1",port}); await once(socket,"connect"); return socket;
  }});
  const events = [];
  let pageReady = false;
  const host = http.createServer(async (req,res) => {
    const url = new URL(req.url,"http://localhost");
    const send = value => {res.setHeader("Content-Type","application/json"); res.end(JSON.stringify(value));};
    try {
      if (url.pathname === "/api/mobile/cli/projects") return send({ready:true,host:"Test",projects:[]});
      if (url.pathname === "/api/mobile/cli/sessions") return send({sessions:[]});
      if (!url.pathname.startsWith("/api/mobile/browser/")) {
        const asset = url.pathname.slice(1) || "mobile.html";
        if (asset !== "mobile.html" && !/^assets\/[\w.-]+$/.test(asset)) throw Error("Missing asset");
        res.setHeader("Content-Type",asset.endsWith(".js") ? "text/javascript" : asset.endsWith(".css") ? "text/css" : asset.endsWith(".png") ? "image/png" : "text/html");
        res.end(await readFile(join(directory,asset))); return;
      }
      const parts = url.pathname.split("/"), op = parts.at(-1);
      const chunks = []; for await (const b of req) chunks.push(b);
      const input = chunks.length ? JSON.parse(Buffer.concat(chunks)) : {};
      if (op === "open") input.profile = "a".repeat(64);
      else input.id = parts.at(-2);
      if (url.searchParams.has("after")) input.after = url.searchParams.get("after");
      const log = {op,type:input.type,at:Date.now(),after:!!input.after,delta:input.dy||0};
      events.push(log);
      await delay(80);
      const output = await guest.run(op,input);
      if (op === "open" || output.image) output.action_frame = true;
      if (output.title === "Scroll test") pageReady = true;
      log.bytes = Buffer.byteLength(JSON.stringify(output));
      log.image = !!output.image;
      await delay(80);
      send(output);
    } catch (error) { if (!res.headersSent) res.writeHead(error.status || 500); res.end(error.message); }
  }).listen(0,"127.0.0.1");
  await once(host,"listening");
  const browser = await playwright[engine].launch();
  try {
    const page = await browser.newPage({viewport:{width:390,height:844},deviceScaleFactor:1,isMobile:true,hasTouch:true,userAgent:"CageMobile/1"});
    await page.goto("http://127.0.0.1:"+host.address().port);
    await page.getByRole("button",{name:"浏览器",exact:true}).click();
    const address = page.getByRole("textbox",{name:"远程网页地址"});
    await address.fill("http://scroll.example:"+site.address().port);
    await address.press("Enter");
    for(let i=0; i<80 && !pageReady; i++) await delay(100);
    assert.ok(pageReady,"remote Chromium did not load the local page");
    await delay(550);
    const stage = page.getByRole("img",{name:"远程浏览器画面"});
    const rect = await stage.boundingBox();
    const start = async type => {
      await page.evaluate(type => {
        const stage = document.querySelector(".remote-stage");
        const img = stage.querySelector("img"), original = img.src;
        window.result = null;
        stage.addEventListener(type, () => {
          const start = performance.now();
          const observer = new MutationObserver(() => {
            if (img.src === original) return;
            requestAnimationFrame(() => requestAnimationFrame(() => {window.result = {ms:Math.round(performance.now()-start),wall:Date.now()};}));
            observer.disconnect();
          });
          observer.observe(img,{attributes:true,attributeFilter:["src"]});
        },{once:true});
      },type);
    };
    const finish = async (logStart,timeout=5000) => {
      await page.waitForFunction(() => window.result !== null,null,{timeout});
      const result = await page.evaluate(() => window.result);
      const log = events.slice(logStart).filter(event => event.at <= result.wall);
      const first = log.findIndex(e => e.op === "action");
      assert.notEqual(first,-1);
      return {ms:result.ms, requests:log.slice(first).length};
    };
    const tapStart = events.length;
    await start("pointerup");
    await page.touchscreen.tap(rect.x+rect.width/2,rect.y+40);
    const tap = await finish(tapStart);
    const x = rect.x + rect.width/2, y = rect.y + rect.height*.75;
    await page.mouse.move(x,y); await page.mouse.down();
    await start("pointermove");
    const dragStart = events.length;
    for (let i=1; i<=12; i++) {await page.mouse.move(x,y-i*12); await delay(32);}
    const beforeRelease = await page.evaluate(() => window.result !== null);
    await page.mouse.up();
    const scroll = await finish(dragStart);
    assert.equal(beforeRelease,!baseline,"scroll feedback while the finger is down");
    // Allow compositor scrolling and its temporary scrollbar to settle.
    await delay(2000);
    const sent = events.slice(dragStart).filter(e => e.type === "scroll");
    assert.equal(sent.reduce((sum,e)=>sum+e.delta,0),144,"only unsent motion may be coalesced");
    if (!baseline) assert.ok(sent.length > 1 && sent.length < 12);
    const idleStart = events.length;
    await delay(5200);
    const idle = events.slice(idleStart).filter(e=>e.op === "frame" && e.bytes);
    if (!baseline) assert.ok(idle.length && idle.every(e=>e.after && !e.image),"the idle page must not keep downloading JPEGs");
    return {tap,scroll:{...scroll,paintedWhileDragging:beforeRelease,writes:sent.length},idle:{reads:idle.length,bodyBytes:idle.reduce((sum,e)=>sum+e.bytes,0)}};
  } finally {
    await browser.close(); await guest.dispose(); host.closeAllConnections(); await new Promise(resolve => host.close(resolve));
    await rm(profile,{recursive:true,force:true});
  }
}
try {
  const before = process.env.CAGE_MOBILE_BASELINE ? await measure(process.env.CAGE_MOBILE_BASELINE,true) : null;
  const after = await measure(current,false);
  if (before) {
    assert.ok(after.tap.ms < before.tap.ms,"combined action feedback must remove one round trip");
    assert.ok(after.scroll.ms < before.scroll.ms,"continuous drag must paint sooner");
  }
  console.log(JSON.stringify({engine,roundTripMs:160,before,after},null,2));
} finally {site.closeAllConnections(); site.close();}
