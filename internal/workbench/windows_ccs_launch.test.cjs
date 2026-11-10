'use strict';
const test = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const os = require('node:os');
const crypto = require('node:crypto');
const cp = require('node:child_process');
const http = require('node:http');
const {once} = require('node:events');
const bridge = require('./windows_ccs_launch.cjs');

function fixture(t) {
  const folder = fs.mkdtempSync(path.join(os.tmpdir(), 'cage-ccs-spawn-'));
  t.after(() => fs.rmSync(folder,{recursive:true,force:true}));
  return {folder,id:crypto.randomUUID(),session_id:crypto.randomUUID(),profile:'qa',binary:path.join(folder,'trusted-claude.exe'),python:process.execPath,helper:path.join(folder,'actual-helper.py')};
}
test('only the fixed native Claude launch is wrapped, with complete dynamic child environment', async t => {
  const context = fixture(t), calls = [];
  const original = (command,args,options) => { calls.push({command,args,options});return new (require('node:events').EventEmitter)(); };
  const restore = bridge.install(context,original);t.after(restore);
  const unrelated = {env:{UNCHANGED:'yes'}};
  cp.spawn('unrelated.exe',['--session-id',context.session_id],unrelated);
  assert.equal(calls[0].command,'unrelated.exe');assert.equal(calls[0].options,unrelated);
  cp.spawn(context.binary,['--version'],{env:{CCS_PROFILE_TYPE:'settings'}});
  assert.equal(calls[1].command,context.binary);
  const settings = path.join(context.folder,'source.settings.json');
  const env = {CCS_PROFILE_TYPE:'settings',CLAUDE_CONFIG_DIR:path.join(context.folder,'actual-config'),DYNAMIC_PROXY_TOKEN:'fixture-only',UNRECOGNIZED_SETTING:'keep'};
  cp.spawn(context.binary,['--settings',settings,'--session-id',context.session_id,'--model','fixture-model'],{env,stdio:'inherit'});
  const call = calls[2];assert.equal(call.command,context.python);assert.deepEqual(call.args.slice(0,5),['-X','utf8','-I',context.helper,'ccs-live']);
  assert.equal(call.options.env.DYNAMIC_PROXY_TOKEN,env.DYNAMIC_PROXY_TOKEN);assert.equal(call.options.env.CLAUDE_CONFIG_DIR,env.CLAUDE_CONFIG_DIR);assert.equal(call.options.env.UNRECOGNIZED_SETTING,'keep');
  const note = JSON.parse(fs.readFileSync(call.args[5],'utf8'));assert.equal(note.settings_source,settings);assert.equal(note.actual_args.at(-1),'fixture-model');
  assert.equal(fs.readFileSync(call.args[5],'utf8').includes('fixture-only'),false,'temporary token was copied into binding metadata');
  assert.throws(() => cp.spawn(context.binary,['--session-id',context.session_id],{env}),/duplicate_claude/);
});
test('the real parent proxy remains usable until the actual child exits', async t => {
  const context=fixture(t), token=crypto.randomUUID();let accepted=0;
  const server=http.createServer((req,res)=>{if(req.headers.authorization===token){accepted++;res.end('alive')}else{res.statusCode=403;res.end()}});
  server.listen(0,'127.0.0.1');await once(server,'listening');t.after(()=>server.close());
  const originalSpawn=cp.spawn;
  const childCode=`const fs=require('node:fs'),http=require('node:http');const note=JSON.parse(fs.readFileSync(process.argv[1],'utf8'));if(!note.actual_args.includes('--model'))process.exit(2);http.get(process.env.PROXY_URL,{headers:{authorization:process.env.DYNAMIC_PROXY_TOKEN}},r=>{let text='';r.on('data',b=>text+=b);r.on('end',()=>{if(text!=='alive')process.exit(3);setTimeout(()=>process.exit(0),100)})});`;
  const wrapped=(command,args,options)=>originalSpawn(process.execPath,['-e',childCode,args[5]],{...options,stdio:'ignore'});
  const restore=bridge.install(context,wrapped);t.after(restore);
  const child=cp.spawn(context.binary,['--session-id',context.session_id,'--model','fixture-model'],{env:{...process.env,PROXY_URL:`http://127.0.0.1:${server.address().port}`,DYNAMIC_PROXY_TOKEN:token}});
  const [code]=await once(child,'exit');assert.equal(code,0);assert.equal(accepted,1);assert.equal(fs.readdirSync(context.folder).some(name=>name.startsWith('ccs-actual-')),false);
});
