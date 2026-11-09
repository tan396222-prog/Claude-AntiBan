// Production controller and private CDP pipe, with a disposable local HTTPS site.
// Only deployment paths, test certificate handling and the test CDP route differ.
import assert from "node:assert/strict";
import { spawn, execFileSync } from "node:child_process";
import { once } from "node:events";
import { randomBytes } from "node:crypto";
import { cp, mkdtemp, readdir, readFile, realpath, rm, writeFile } from "node:fs/promises";
import https from "node:https";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
assert.ok(process.env.CAGE_TEST_BROWSER_PATH, "Set CAGE_TEST_BROWSER_PATH to Chromium");
const dir = await realpath(await mkdtemp(join(tmpdir(), "cage-vault-controller-")));
const token = randomBytes(32).toString("hex"), lease = randomBytes(32).toString("hex");
execFileSync("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-keyout", join(dir, "key.pem"), "-out", join(dir, "cert.pem"), "-days", "1", "-subj", "/CN=localhost"], { stdio: "ignore" });
const site = https.createServer({ key: await readFile(join(dir,"key.pem")), cert: await readFile(join(dir,"cert.pem")) }, (req,res) => {
  res.setHeader("Content-Type","text/html");
  res.end('<title>Vault fixture</title><form onsubmit="event.preventDefault()"><label>Email<input id="user" autocomplete="username"></label><label>Password<input id="pass" type="password" autocomplete="current-password"></label><button>Sign in</button></form>');
});
await new Promise(resolve => site.listen(0, "127.0.0.1", resolve));
const origin = `https://127.0.0.1:${site.address().port}`;
let agent, control, stderr = "";
async function waitFor(fn, label) {
  for (let i=0;i<150;i++) { if (agent?.exitCode !== null) throw new Error("Controller exited: " + stderr.slice(-1000)); const result=await fn(); if(result) return result; await delay(50); }
  throw new Error("Timed out: " + label);
}
async function api(path, input) {
  const res=await fetch(control+path,{method:input===undefined?"GET":"POST",headers:{Authorization:`Bearer ${token}`,"Content-Type":"application/json"},body:input===undefined?undefined:JSON.stringify(input)});
  assert.equal(res.status,200);
  return res.json();
}
const vault = input => api("/vault",{lease,...input});
async function evaluate(id, expression) {
  const result=await api("/__vault-test",{id,method:"Runtime.evaluate",params:{expression,returnByValue:true}});
  assert.equal(result.exceptionDetails, undefined);
  return result.result.value;
}
try {
  const sourceDir=new URL("../internal/cage/",import.meta.url);
  for(const name of await readdir(sourceDir)) if ((name.startsWith("browser-")&&name.endsWith(".mjs"))||name==="native-input.mjs") await cp(new URL(name,sourceDir),join(dir,name));
  await cp(new URL("split-extension",sourceDir),join(dir,"extension"),{recursive:true});
  const original=await readFile(new URL("browser-agent.mjs",sourceDir),"utf8");
  const marker='const input = JSON.parse(text || "{}");';
  const adapted=original.replaceAll("/home/cage-browser",dir).replace("/etc/claude-cage/browser-control.json",join(dir,"control.json"))
    .replace("/usr/local/lib/cage-split",join(dir,"extension"))
    .replace('spawn("/usr/bin/chromium", flags',`spawn(${JSON.stringify(process.env.CAGE_TEST_BROWSER_PATH)}, flags`)
    .replace('"--start-fullscreen",','"--headless=new", "--ignore-certificate-errors", "--disable-background-networking",')
    .replace('server.listen(18500, "127.0.0.1");','server.listen(0,"127.0.0.1",()=>console.log("PORT="+server.address().port));')
    .replace(marker,`${marker}\nif (req.url === "/__vault-test") { res.end(JSON.stringify(await cdp(input.method,input.params||{},await session(input.id)))); return; }`);
  assert.notEqual(adapted, original);
  await writeFile(join(dir,"browser-agent.mjs"),adapted);
  await writeFile(join(dir,"control.json"),JSON.stringify({token,workspace:dir}));
  await writeFile(join(dir,"homepage.json"),JSON.stringify({url:origin+"/login"}));
  agent=spawn(process.execPath,[join(dir,"browser-agent.mjs")],{stdio:["ignore","pipe","pipe"]});
  agent.stdout.on("data",chunk=>{const match=chunk.toString().match(/PORT=(\d+)/);if(match)control="http://127.0.0.1:"+match[1];});
  agent.stderr.on("data",chunk=>{stderr=(stderr+chunk).slice(-3000);});
  await waitFor(()=>control,"listener");
  await waitFor(async()=> (await fetch(control+"/health",{headers:{Authorization:`Bearer ${token}`}})).ok,"ready");
  const tab=(await api("/tabs")).active;
  await waitFor(()=>evaluate(tab,"document.readyState === 'complete'"),"fixture");
  const state=await vault({action:"status",tab});
  assert.equal(state.origin,origin);assert.ok(state.document);
  await evaluate(tab,"document.getElementById('user').value='controller@example.test';document.getElementById('pass').value='controller-secret'");
  const point=await evaluate(tab,"(()=>{const r=document.querySelector('button').getBoundingClientRect();return {x:r.x+r.width/2,y:r.y+r.height/2}})()");
  for(const type of ["mousePressed","mouseReleased"]) await api("/__vault-test",{id:tab,method:"Input.dispatchMouseEvent",params:{type,...point,button:"left",clickCount:1}});
  const pending=await waitFor(async()=> (await vault({action:"status",tab})).candidates[0],"captured password");
  assert.equal(pending.password,"controller-secret");assert.equal(pending.username,"controller@example.test");
  assert.ok(!JSON.stringify(await api("/tabs")).includes("controller-secret"));
  assert.equal((await fetch(control+"/vault",{method:"POST",headers:{"Content-Type":"application/json"},body:"{}"})).status,401);
  assert.equal((await fetch(control+"/vault",{method:"POST",headers:{Authorization:`Bearer ${token}`,Origin:origin,"Content-Type":"application/json"},body:"{}"})).status,403);
  await evaluate(tab,"document.getElementById('user').value='';document.getElementById('pass').value=''");
  assert.equal((await vault({action:"fill",tab,origin,document:state.document,username:"selected@example.test",password:"selected-secret"})).ok,true);
  assert.equal(await evaluate(tab,"document.getElementById('pass').value"),"selected-secret");
  await api("/navigate",{id:tab,url:origin+"/next"});
  await waitFor(()=>evaluate(tab,"location.pathname === '/next' && document.readyState === 'complete'"),"navigation");
  assert.ok((await vault({action:"fill",tab,origin,document:state.document,username:"stale",password:"stale"})).vaultError);
  await vault({action:"lock"});
  assert.ok((await vault({action:"candidate",candidate:pending.id})).vaultError);
  console.log("PASS: production controller HTTPS capture/fill, private authentication, public-state redaction, navigation and lock");
} finally {
  if(agent?.exitCode===null){const exit=once(agent,"exit");agent.kill("SIGTERM");const timer=setTimeout(()=>agent.kill("SIGKILL"),5000);await exit;clearTimeout(timer);}
  await new Promise(resolve=>site.close(resolve));
  await rm(dir,{recursive:true,force:true});
}
