const {test} = require('node:test');
const assert = require('node:assert/strict');
const {readFileSync} = require('node:fs');
const vm = require('node:vm');

function page() {
  const elements = new Map();
  const handlers = new Map();
  const remoteEvents = new Map();
  const calls = [];
  const element = id => {
    if (!elements.has(id)) elements.set(id, {id, value:'', hidden:false, textContent:'',
      contains: node => node?.remote === true, append(){}, focus(){document.activeElement=this;}, select(){}});
    return elements.get(id);
  };
  const canvas = {remote:true};
  const document = {activeElement:canvas, documentElement:{}, body:{append(){}}, head:{append(){}},
    createElement:()=>({style:{},setAttribute(){},focus(){document.activeElement=this;}}), getElementById:element,
    addEventListener:(name,fn)=>handlers.set(name,fn)};
  const UI = {connected:true, rfb:{addEventListener:(name,fn)=>remoteEvents.set(name,fn),
    sendKey:(...args)=>calls.push(['key',...args]),
    cagePasteText:text=>{calls.push(['paste',text]);return Promise.resolve();},focus(){document.activeElement=canvas;}}};
  const clipboard = {writeText:async text=>calls.push(['write-host',text]),readText:async()=>{calls.push(['read-host']);return '本机文本';}};
  const source = readFileSync(__dirname+'/clipboard.js','utf8').replace(/^import .*;$/gm,'');
  vm.runInNewContext(source,{getKeycode:e=>e.code || ('Key'+e.key.toUpperCase()),getKeysym:e=>e.key.length===1 ? e.key.codePointAt(0) : ({Shift:0xffe1,Control:0xffe3,Alt:0xffe9}[e.key] ?? null),window:{addEventListener:(name,fn)=>handlers.set('window:'+name,fn)},UI,document,navigator:{clipboard},MutationObserver:class{observe(){}},setTimeout:()=>1,clearTimeout(){}});
  const event = values => ({key:'',metaKey:false,ctrlKey:false,preventDefault(){this.prevented=true;},stopImmediatePropagation(){this.stopped=true;},...values});
  return {UI,document,element,handlers,remoteEvents,calls,clipboard,event,canvas};
}

test('receiving remote clipboard never writes to host without a user copy',async()=>{
  const p=page();
  await p.remoteEvents.get('clipboard')({detail:{text:'private guest text'}});
  assert.equal(p.calls.length,0);
});
test('Mac copy maps to Linux Ctrl+C and exports the subsequent remote clipboard',async()=>{
  const p=page();
  const e=p.event({key:'c',metaKey:true}); p.handlers.get('keydown')(e);
  assert.equal(e.prevented,true); assert.equal(e.stopped,true);
  assert.deepEqual(p.calls.map(c=>c.slice(0,2)),[['key',0xffe3],['key',99],['key',99],['key',0xffe3]]);
  await p.remoteEvents.get('clipboard')({detail:{text:'中文\n✅'}});
  assert.deepEqual(p.calls.at(-1),['write-host','中文\n✅']);
});
test('native paste transfers Unicode only while the remote canvas is focused',()=>{
  const p=page();
  const e=p.event({clipboardData:{types:['text/plain'],getData:()=> '中文\n✅'}});
  p.handlers.get('paste')(e);
  assert.equal(e.prevented,true); assert.deepEqual(p.calls[0],['paste','中文\n✅']);
  p.calls.length=0; p.document.activeElement=p.element('cage-transfer-text');
  p.handlers.get('paste')(p.event({clipboardData:{types:['text/plain'],getData:()=> 'local field'}}));
  assert.equal(p.calls.length,0);
});
test('paste shortcut preserves the native paste event; Meta is not sent to Linux',()=>{
  const p=page();
  const e=p.event({key:'v',metaKey:true});p.handlers.get('keydown')(e);
  assert.equal(e.stopped,true);assert.equal(e.prevented,undefined);assert.equal(p.calls.length,0);
  assert.equal(p.document.activeElement.id,'cage-native-paste');
  p.document.activeElement=p.canvas;
  const meta=p.event({key:'Meta',metaKey:true});p.handlers.get('keydown')(meta);assert.equal(meta.stopped,true);
});
test('denied host clipboard access exposes a manual transfer field',async()=>{
  const p=page();p.clipboard.readText=async()=>{throw Error('denied');};
  await p.element('cage-paste').onclick();
  assert.equal(p.document.activeElement.id,'cage-transfer-text');
  assert.equal(p.calls.length,0);
});

test('Command+F and Command+Z become guest Ctrl shortcuts',()=>{
  for (const key of ['f','z']) {
    const p=page(), e=p.event({key,code:'Key'+key.toUpperCase(),metaKey:true});
    p.handlers.get('keydown')(e);
    assert.equal(e.prevented,true);
    assert.deepEqual(p.calls,[['key',0xffe3,'ControlLeft',true],['key',key.charCodeAt(0),e.code,true],['key',key.charCodeAt(0),e.code,false],['key',0xffe3,'ControlLeft',false]]);
    const up=p.event({key,code:e.code,metaKey:false});
    p.handlers.get('keyup')(up);assert.equal(up.stopped,true);
  }
});
test('Command+Shift+Z preserves Shift and does not release held modifiers',()=>{
  const p=page();p.handlers.get('keydown')(p.event({key:'Z',code:'KeyZ',metaKey:true,shiftKey:true}));
  assert.ok(p.calls.some(c=>c[1]===90 && c[3]===true));
  assert.ok(p.calls.some(c=>c[2]==='ShiftLeft' && c[3]===true));
  assert.ok(p.calls.some(c=>c[2]==='ShiftLeft' && c[3]===false));
  p.calls.length=0;
  p.handlers.get('keydown')(p.event({key:'Control',code:'ControlLeft',ctrlKey:true}));
  p.handlers.get('keydown')(p.event({key:'f',code:'KeyF',metaKey:true,ctrlKey:true}));
  assert.deepEqual(p.calls,[['key',0xffe3,'ControlLeft',true],['key',102,'KeyF',true],['key',102,'KeyF',false]]);
});
test('local fields and normal Ctrl shortcuts retain their own handlers',()=>{
  const p=page();p.document.activeElement=p.element('cage-transfer-text');
  const local=p.event({key:'f',metaKey:true});p.handlers.get('keydown')(local);
  assert.equal(local.prevented,undefined);assert.equal(p.calls.length,0);
  p.document.activeElement=p.canvas;
  const ctrl=p.event({key:'f',ctrlKey:true});p.handlers.get('keydown')(ctrl);
  assert.equal(ctrl.prevented,undefined);assert.equal(p.calls.length,0);
});
test('Command+Shift+C remains a guest shortcut rather than clipboard export',async()=>{
  const p=page();p.handlers.get('keydown')(p.event({key:'C',code:'KeyC',metaKey:true,shiftKey:true}));
  await p.remoteEvents.get('clipboard')({detail:{text:'not requested'}});
  assert.ok(!p.calls.some(c=>c[0]==='write-host'));
});

test('modifier pressed after Command stays down until release or focus loss',()=>{
  const p=page();
  const shift=p.event({key:'Shift',code:'ShiftLeft',metaKey:true,shiftKey:true});
  p.handlers.get('keydown')(shift);
  assert.equal(shift.stopped,true);
  assert.deepEqual(p.calls,[['key',0xffe1,'ShiftLeft',true]]);
  p.handlers.get('keyup')(p.event({key:'Shift',code:'ShiftLeft',metaKey:false}));
  assert.deepEqual(p.calls.at(-1),['key',0xffe1,'ShiftLeft',false]);
  p.handlers.get('keydown')(shift);
  p.handlers.get('window:blur')();
  assert.deepEqual(p.calls.at(-1),['key',0xffe1,'ShiftLeft',false]);
});

test('right-side held modifiers do not leave synthetic left modifiers pressed',()=>{
  const p=page();
  p.handlers.get('keydown')(p.event({key:'Control',code:'ControlRight',ctrlKey:true}));
  p.handlers.get('keydown')(p.event({key:'Shift',code:'ShiftRight',shiftKey:true,ctrlKey:true}));
  p.handlers.get('keydown')(p.event({key:'Z',code:'KeyZ',metaKey:true,ctrlKey:true,shiftKey:true}));
  assert.deepEqual(p.calls.slice(0,2),[['key',0xffe4,'ControlRight',true],['key',0xffe2,'ShiftRight',true]]);
  assert.ok(!p.calls.some(c=>c[2]==='ControlLeft'||c[2]==='ShiftLeft'));
});

test('malformed macOS Command letter events recover the letter without losing Shift',()=>{
  const p=page();p.handlers.get('keydown')(p.event({key:'\u0000',code:'KeyZ',metaKey:true,shiftKey:true}));
  assert.ok(p.calls.some(c=>c[1]===90 && c[2]==='KeyZ' && c[3]===true));
  assert.ok(!p.calls.some(c=>c[1]===0));
});
