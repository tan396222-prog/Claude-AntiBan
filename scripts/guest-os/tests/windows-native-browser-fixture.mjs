import http from "node:http";
import { writeFile } from "node:fs/promises";
import path from "node:path";

// This fixture runs in an ordinary GUI ConPTY. It binds a random loopback port
// and reports only data created by this test; it does not control a browser.
const output = process.argv[2];
if (!output || !path.isAbsolute(output)) throw new Error("An absolute evidence directory is required.");
const reports = [];
const copiedText = "Cage native copy 中文🙂 " + path.basename(output);
const html = `<!doctype html><meta charset="utf-8"><title>Cage native browser acceptance</title>
<style>body{margin:0;background:#eef3f8;color:#17233b;font:18px Arial}header{height:42px;padding:12px;background:#1e4363;color:white}main{padding:20px}textarea,#images{display:block;box-sizing:border-box;width:420px;max-width:90vw;height:90px;margin:12px 0;padding:8px;border:2px solid #345;border-radius:4px;background:white}button{height:42px;margin:0 8px 12px 0}#images{height:100px}#images img{max-height:64px}#drop{height:64px;border:2px dashed #678;width:400px;max-width:90vw}input{margin:10px 0}p{margin:8px}</style>
<header>Cage Windows native browser acceptance</header><button id="another" style="position:fixed;right:20px;top:18px">Open fixture tab</button><main>
<textarea id="text" aria-label="Acceptance text"></textarea>
<button id="copy">Copy fixture text</button><button id="permission">Request fixture permission</button>
<div id="images" contenteditable="true" tabindex="0" aria-label="Acceptance images">Paste image here</div>
<p>Find this unique phrase: cage-native-find-needle</p><a id="download" href="/download">Download fixture</a>
<input id="upload" type="file" multiple><div id="drop" tabindex="0">Drop fixture files here</div></main>
<script>
const report = value => fetch('/report', {method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({page:location.pathname,...value})});
text.addEventListener('input',()=>report({type:'text',value:text.value}));
document.addEventListener('pointerdown',e=>report({type:'pointer',x:e.clientX,y:e.clientY,target:e.target.id,width:innerWidth,height:innerHeight,outerWidth,outerHeight,dpr:devicePixelRatio,visualScale:visualViewport.scale}));
document.addEventListener('keydown',e=>report({type:'key',key:e.key,target:e.target.id,ctrl:e.ctrlKey,width:innerWidth,height:innerHeight}));
document.addEventListener('scroll',()=>report({type:'scroll',y:scrollY}),{passive:true});
copy.onclick=async()=>{await navigator.clipboard.writeText(${JSON.stringify(copiedText)});report({type:'copied'})};
permission.onclick=async()=>report({type:'permission',value:await Notification.requestPermission()});
another.onclick=()=>window.open('/private-two','_blank');
images.addEventListener('paste',async e=>{const files=[...e.clipboardData.files];if(!files.length)return; e.preventDefault();for(const f of files){const b=new Uint8Array(await f.arrayBuffer());const image=new Image();image.src=URL.createObjectURL(f);await image.decode();images.replaceChildren(image);report({type:'png',mime:f.type,width:image.naturalWidth,height:image.naturalHeight,bytes:b.length})}});
const selected=async(files,type)=>report({type,files:await Promise.all([...files].map(async f=>({name:f.name,bytes:[...new Uint8Array(await f.arrayBuffer())]})))});
upload.onchange=()=>selected(upload.files,'upload');drop.ondragover=e=>e.preventDefault();drop.ondrop=e=>{e.preventDefault();selected(e.dataTransfer.files,'drop')};
</script>`;
const server = http.createServer(async (request, response) => {
  if (request.url === "/state") { response.setHeader("Content-Type", "application/json"); response.end(JSON.stringify(reports)); return; }
  if (request.url === "/report" && request.method === "POST") {
    const chunks = []; let length = 0;
    for await (const chunk of request) { length += chunk.length; if (length > 1 << 20) { response.writeHead(413); response.end(); return; } chunks.push(chunk); }
    try { reports.push(JSON.parse(Buffer.concat(chunks).toString("utf8"))); await writeFile(path.join(output, "fixture-state.json"), JSON.stringify(reports, null, 2)); }
    catch { response.writeHead(400); response.end(); return; }
    response.end("ok"); return;
  }
  if (request.url === "/download") { response.setHeader("Content-Disposition", 'attachment; filename="cage-native-fixture.txt"'); response.end("Cage native download 中文🙂"); return; }
  response.setHeader("Content-Type", "text/html; charset=utf-8");
  const rows = request.url === "/perf" ? Array.from({length:60},(_,i)=>`<section style="height:180px;background:hsl(${i*23%360} 45% 85%);padding:20px">Native scroll fixture row ${i}</section>`).join("") : "";
  response.end(rows ? html.replace("</main>", "</main>"+rows) : html);
});
server.listen(0, "127.0.0.1", async () => {
  await writeFile(path.join(output, "fixture-port"), String(server.address().port));
  process.stdout.write("CAGE_NATIVE_FIXTURE_READY\n");
});
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => server.close(() => process.exit(0)));
