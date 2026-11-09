// Read/write the host system clipboard only after an explicit user action.
import UI from './app/ui.js?cage=3';
import { getKeycode, getKeysym } from './core/input/util.js';

const bar = document.createElement('div');
bar.id = 'cage-clipboard-bar';
bar.innerHTML = `<strong>Cage 浏览器</strong><button id="cage-copy">复制到本机</button><button id="cage-paste">粘贴到 Cage</button><button id="cage-transfer">文本传递</button><span role="status" id="cage-clipboard-status">远程画面内 ⌘→Ctrl · 剪贴板仅传递文本</span>`;
document.body.append(bar);
const panel = document.createElement('div');
panel.id = 'cage-clipboard-panel';
panel.hidden = true;
panel.innerHTML = `<label for="cage-transfer-text">待传递文本（可在这里使用本机复制、粘贴）</label><textarea id="cage-transfer-text" rows="5"></textarea><button id="cage-send">粘贴到 Cage</button><button id="cage-close">关闭</button>`;
document.body.append(panel);
const style = document.createElement('style');
style.textContent = `#cage-clipboard-bar{position:fixed;z-index:10000;top:0;left:0;right:0;height:42px;display:flex;align-items:center;gap:10px;padding:0 14px;background:#20252b;color:#fff;font:13px system-ui}#cage-clipboard-bar button,#cage-clipboard-panel button{padding:5px 10px;border:1px solid #777;border-radius:5px;cursor:pointer}#cage-clipboard-status{color:#c7d2df}body{padding-top:42px;box-sizing:border-box}#cage-clipboard-panel{position:fixed;z-index:10001;top:50px;right:12px;width:min(440px,90vw);padding:16px;background:#fff;color:#222;font:14px system-ui;border:1px solid #aaa;border-radius:8px}#cage-transfer-text{display:block;box-sizing:border-box;width:100%;margin:10px 0;resize:vertical}`;
document.head.append(style);
// Browsers reliably dispatch native paste to an editable element. A canvas
// alone is not a cross-browser clipboard target.
const pasteTarget = document.createElement('textarea');
pasteTarget.id = 'cage-native-paste';
pasteTarget.setAttribute('aria-label', 'Cage 原生粘贴接收区');
pasteTarget.style.cssText = 'position:fixed;left:-10000px;top:0;width:1px;height:1px';
document.body.append(pasteTarget);
const status = document.getElementById('cage-clipboard-status');
const field = document.getElementById('cage-transfer-text');
let bound;
let copyRequested = false;
let pasteSequence = 0;
let pendingPastes = 0;
let copyTimer;
let lastRemoteText = '';
const say = text => { status.textContent = text; };
function remoteFocused() {
    return document.getElementById('noVNC_container')?.contains(document.activeElement);
}
function shortcut(key) {
    const rfb = UI.rfb;
    rfb.sendKey(0xffe3, 'ControlLeft', true);
    rfb.sendKey(key.charCodeAt(0), 'Key' + key.toUpperCase(), true);
    rfb.sendKey(key.charCodeAt(0), 'Key' + key.toUpperCase(), false);
    rfb.sendKey(0xffe3, 'ControlLeft', false);
}
function bind() {
    if (!UI.rfb || bound === UI.rfb) return;
    bound = UI.rfb;
    bound.addEventListener('clipboard', async event => {
        lastRemoteText = event.detail.text;
        if (!copyRequested) return;
        copyRequested = false;
        clearTimeout(copyTimer);
        try {
            await navigator.clipboard.writeText(lastRemoteText);
            say('已复制到本机');
        } catch {
            field.value = lastRemoteText;
            panel.hidden = false;
            field.focus();
            field.select();
            say('浏览器未允许剪贴板写入，请在文本框中复制');
        }
    });
}
new MutationObserver(bind).observe(document.documentElement, {attributes:true, attributeFilter:['class']});
bind();
function connected() {
    bind();
    if (!UI.connected || !UI.rfb) { say('请先连接 Cage 浏览器'); return false; }
    return true;
}
function copy(key = 'c') {
    if (!connected()) return;
    copyRequested = true;
    shortcut(key);
    say('正在复制 Cage 中选中的文本…');
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
        copyRequested = false;
        // Do not copy an older value when the remote selection did not change.
        field.value = lastRemoteText;
        panel.hidden = false;
        say('如未选中文本，请先选择；也可在文本框中复制现有 Cage 剪贴板');
    }, 1800);
}
function paste(text) {
    if (!connected()) return;
    console.debug("Cage paste accepted", ++pasteSequence);
    pendingPastes++;
    const done = UI.rfb.cagePasteText(text);
    UI.rfb.focus();
    panel.hidden = true;
    say('正在粘贴…');
    done.then(() => { pendingPastes--; say(pendingPastes ? '正在粘贴…' : '已粘贴到 Cage'); }, () => { pendingPastes--; say('剪贴板未就绪，请重新连接后重试'); });
}
async function readAndPaste() {
    if (!connected()) return;
    try { paste(await navigator.clipboard.readText()); }
    catch {
        field.value = '';
        panel.hidden = false;
        field.focus();
        say('请在文本框中粘贴，再点击“粘贴到 Cage”');
    }
}
document.getElementById('cage-copy').onclick = () => copy();
document.getElementById('cage-paste').onclick = readAndPaste;
document.getElementById('cage-transfer').onclick = () => {
    field.value = lastRemoteText;
    panel.hidden = !panel.hidden;
    if (!panel.hidden) field.focus();
};
document.getElementById('cage-send').onclick = () => paste(field.value);
document.getElementById('cage-close').onclick = () => { panel.hidden = true; UI.rfb?.focus(); };
// Capture shortcuts only over the remote screen. Local toolbar fields keep
// the host browser's shortcuts. Use noVNC's key conversion for letters,
// digits, punctuation and named keys rather than maintaining another map.
const handledKeys = new Set();
const remoteModifiers = new Map();
const physicalModifiers = new Set();
window.addEventListener('blur', () => {
    for (const [code, {rfb, keysym}] of remoteModifiers) rfb.sendKey(keysym, code, false);
    remoteModifiers.clear();
    physicalModifiers.clear();
    handledKeys.clear();
});
const modifierKeys = new Set(['Meta', 'Control', 'Shift', 'Alt', 'CapsLock']);
function commandShortcut(event) {
    const code = getKeycode(event);
    let keysym = getKeysym(event);
    // Some macOS Command chords expose a control character instead of the
    // letter. Recover only that malformed case from its physical letter key.
    if ((keysym === null || keysym < 0x20) && /^Key[A-Z]$/.test(code)) {
        keysym = (event.shiftKey ? code[3] : code[3].toLowerCase()).codePointAt(0);
    }
    if (keysym === null || !connected()) return false;
    // Some macOS input paths report modifier flags without separate modifier
    // keydown events. Assert the chord explicitly, then release only modifiers
    // that are not physically held according to the events we observed.
    const held = prefix => [...physicalModifiers].some(code => code.startsWith(prefix));
    const control = physicalModifiers.has('ControlRight') ? [0xffe4, 'ControlRight'] : [0xffe3, 'ControlLeft'];
    const shift = physicalModifiers.has('ShiftRight') ? [0xffe2, 'ShiftRight'] : [0xffe1, 'ShiftLeft'];
    UI.rfb.sendKey(...control, true);
    if (event.shiftKey) UI.rfb.sendKey(...shift, true);
    UI.rfb.sendKey(keysym, code, true);
    UI.rfb.sendKey(keysym, code, false);
    if (event.shiftKey && !held('Shift')) UI.rfb.sendKey(...shift, false);
    if (!event.ctrlKey || !held('Control')) UI.rfb.sendKey(...control, false);
    return true;
}
document.addEventListener('keydown', event => {
    if (!remoteFocused()) return;
    const key = event.key.toLowerCase();
    const code = getKeycode(event);
    if (['Shift', 'Control', 'Alt'].includes(event.key)) physicalModifiers.add(code);
    // noVNC normally synthesizes immediate key-up while Command is held,
    // even for Shift. Own these modifier pairs so Cmd+Shift+Z keeps Shift.
    if (event.metaKey && ['Shift', 'Control', 'Alt'].includes(event.key) && connected()) {
        const keysym = getKeysym(event);
        UI.rfb.sendKey(keysym, code, true);
        remoteModifiers.set(code, {rfb:UI.rfb, keysym});
        event.preventDefault();
        event.stopImmediatePropagation();
        return;
    }
    const plainClipboard = !event.altKey && !event.shiftKey && ['c','x'].includes(key);
    const pasteClipboard = !event.altKey && key === 'v';
    if ((event.metaKey || event.ctrlKey) && (plainClipboard || pasteClipboard)) {
        event.stopImmediatePropagation();
        handledKeys.add(code);
        if (pasteClipboard) {
            pasteTarget.value = '';
            pasteTarget.focus({preventScroll:true});
            say('等待本机粘贴事件…');
            return; // Keep the native paste event and its clipboard payload.
        }
        event.preventDefault();
        copy(key);
        return;
    }
    if (event.metaKey && !modifierKeys.has(event.key) && commandShortcut(event)) {
        event.preventDefault();
        event.stopImmediatePropagation();
        handledKeys.add(code);
        return;
    }
    // Command itself must never reach Linux as Super. Shift/Alt/Ctrl still
    // reach noVNC normally, regardless of the order modifiers are pressed.
    if (event.key === 'Meta') event.stopImmediatePropagation();
}, true);
document.addEventListener('keyup', event => {
    const code = getKeycode(event);
    physicalModifiers.delete(code);
    const modifier = remoteModifiers.get(code);
    if (modifier) {
        modifier.rfb.sendKey(modifier.keysym, code, false);
        remoteModifiers.delete(code);
        event.stopImmediatePropagation();
        return;
    }
    // Match by physical key even if Command was released first, or focus
    // moved to the native paste receiver after keydown.
    if (handledKeys.delete(getKeycode(event)) || (remoteFocused() && event.key === 'Meta')) {
        event.stopImmediatePropagation();
    }
}, true);
document.addEventListener('paste', event => {
    if ((!remoteFocused() && event.target !== pasteTarget) || !connected()) return;
    event.preventDefault();
    event.stopImmediatePropagation();
    if (!event.clipboardData.types.includes('text/plain')) { say('当前仅支持文本粘贴'); return; }
    paste(event.clipboardData.getData('text/plain'));
}, true);
