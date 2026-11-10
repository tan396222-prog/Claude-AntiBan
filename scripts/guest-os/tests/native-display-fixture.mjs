import http from "node:http";
import { writeFile, appendFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
const root = path.dirname(fileURLToPath(import.meta.url));
const html = `<!doctype html><title>Cage display verification</title>
<style>
body { min-height:2400px; margin:0; background:rgb(238,240,244); font:24px Arial }
textarea { position:absolute; left:40px; top:40px; width:400px; height:70px; font:24px Arial }
button { position:absolute; left:40px; top:150px; width:300px; height:60px; font:24px Arial }
#status { position:absolute; left:40px; top:230px }
#bar { position:absolute; left:40px; top:300px; width:100px; height:70px; background:rgb(220,80,40) }
#corner { position:fixed; left:auto; top:auto; right:20px; bottom:20px; width:140px; height:44px; font:18px Arial }
</style>
<textarea id="entry" aria-label="Display verification text"></textarea>
<button id="motion">Run animation</button><div id="status">Ready</div><div id="bar"></div>
<button id="corner">Check edge</button>
<script>
const field = document.getElementById('entry');
const label = document.getElementById('status');
const bar = document.getElementById('bar');
const send = (route, value) => fetch(route, {method:'POST', body:String(value)});
document.getElementById('corner').addEventListener('click', event => {
  event.currentTarget.style.background = 'rgb(90,60,210)';
  event.currentTarget.textContent = 'Edge verified';
  send('/edge', 'yes');
});
field.addEventListener('input', () => {
  document.body.style.background = 'rgb(40,160,90)';
  label.textContent = field.value;
  send('/state', field.value);
});
document.getElementById('motion').addEventListener('click', () => {
  send('/clicked', 'yes');
  const started = performance.now();
  let frames = 0;
  function step(now) {
    bar.style.left = (40 + ((now - started) / 3) % 400) + 'px';
    frames++;
    if (now - started < 4000) requestAnimationFrame(step);
    else { bar.style.left = '40px'; send('/motion', frames); }
  }
  requestAnimationFrame(step);
});
addEventListener('scroll', () => send('/scroll', scrollY));
addEventListener('pointerdown', event => send('/pointer', JSON.stringify({
  target:event.target.id, x:event.clientX, y:event.clientY,
  viewport:{width:innerWidth, height:innerHeight, outerWidth, outerHeight, scale:devicePixelRatio}
})));
addEventListener('error', event => send('/error', event.message));
</script>`;
let maxScroll = 0;
let writes = Promise.resolve();
const files = { '/state':'fixture-input.txt', '/motion':'fixture-motion.txt', '/scroll':'fixture-max-scroll.txt', '/clicked':'fixture-clicked.txt', '/edge':'fixture-edge.txt', '/pointer':'fixture-pointer.jsonl', '/error':'fixture-errors.txt' };
const server = http.createServer(async (request, response) => {
  if (request.method === 'POST') {
    let content = '';
    for await (const chunk of request) {
      content += chunk;
      if (content.length > 8192) { response.writeHead(413); response.end(); return; }
    }
    if (!files[request.url]) { response.writeHead(404); response.end(); return; }
    if (request.url === '/scroll') { maxScroll = Math.max(maxScroll, Number(content)); content = String(maxScroll); }
    const destination = path.join(root, files[request.url]);
    writes = writes.then(() => request.url === '/pointer' || request.url === '/error' ? appendFile(destination, content + '\n') : writeFile(destination, content));
    await writes;
    response.end('ok');
    return;
  }
  response.setHeader('Content-Type', 'text/html; charset=utf-8');
  response.end(html);
});
server.listen(49681, '127.0.0.1', async () => {
  await writeFile(path.join(root,'fixture-port.txt'), String(server.address().port));
  await writeFile(path.join(root,'fixture-pid.txt'), String(process.pid));
});
