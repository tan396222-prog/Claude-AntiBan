# Patch noVNC's keyboard target and serialize clipboard transfers.
# The resulting rfb.js retains noVNC's MPL-2.0 license header.
from pathlib import Path
import sys

root = Path(sys.argv[1]) if len(sys.argv) > 1 else Path('/usr/share/novnc')
path = root / 'core/rfb.js'
source = path.read_text()
original = path.with_name('rfb.cage-original.js')
if 'cagePasteText(text)' in source:
    if not original.exists():
        raise SystemExit('Restore the package rfb.js before applying a new clipboard patch')
    source = original.read_text()
else:
    original.write_text(source)
ui = root / 'app/ui.js'
u = ui.read_text() if ui.exists() else ''
u = u.replace('cage=2', 'cage=3')
u = u.replace('"../core/rfb.js"', '"../core/rfb.js?cage=3"')
page = root / 'vnc.html'
h = (page.read_text() if page.exists() else '').replace('cage=2', 'cage=3').replace('"./app/ui.js"', '"./app/ui.js?cage=3"')
h = h.replace('src="cage-clipboard.js"', 'src="cage-clipboard.js?cage=3"')

def replace_once(old, new):
    global source
    if source.count(old) != 1:
        raise SystemExit('Unsupported noVNC version: Cage input/clipboard patch did not apply')
    source = source.replace(old, new)

# Keep noVNC's key tracking and focus lifecycle when the workbench supplies an
# editable host target for IME. Legacy viewers continue to use the canvas.
replace_once('''    focus(options) {
        this._canvas.focus(options);
    }

    blur() {
        this._canvas.blur();
    }''', '''    cageSetKeyboardTarget(target) {
        this._keyboard.ungrab();
        this._cageKeyboardTarget = target || this._canvas;
        this._keyboard = new Keyboard(this._cageKeyboardTarget);
        this._keyboard.onkeyevent = this._handleKeyEvent.bind(this);
        if (this._rfbConnectionState === 'connected' && !this._viewOnly) {
            this._keyboard.grab();
        }
    }

    focus(options) {
        (this._cageKeyboardTarget || this._canvas).focus(options);
    }

    blur() {
        (this._cageKeyboardTarget || this._canvas).blur();
    }''')

replace_once('    clipboardPasteFrom(text) {', '''    // Cage: serialize the clipboard notification, data transfer and paste.
    // A server fence confirms it has processed the provided clipboard data.
    _cageQueueInput(action) {
        const result = (this._cageInputChain || Promise.resolve()).then(action);
        this._cageInputChain = result;
        const clear = () => {
            if (this._cageInputChain === result) this._cageInputChain = null;
        };
        // A failed text transfer also cancels keys/clicks already queued behind
        // it, so an Enter cannot submit a form with the committed text missing.
        result.then(clear, clear);
        return result;
    }

    cageRunInput(action) {
        return this._cageQueueInput(() => this._cageRunInput(action));
    }

    async _cageRunInput(action) {
        if (this._rfbConnectionState !== 'connected' || this._viewOnly || !this._supportsFence)
            throw new Error('Input transport is not ready');
        // Flush preceding VNC clicks/keys before a browser text insertion.
        await new Promise((resolve, reject) => {
            const id = 'cage-input-' + (this._cageInputSequence = (this._cageInputSequence || 0) + 1);
            const timer = setTimeout(() => {
                this._cageInputFence = null;
                reject(new Error('Input fence timed out'));
            }, 4000);
            this._cageInputFence = { id, timer, resolve };
            RFB.messages.clientFence(this._sock, (1 << 31) | 1, id);
        });
        if (this._rfbConnectionState !== 'connected') throw new Error('Input disconnected');
        return action((...key) => this._cageSendKey(...key));
    }

    cagePasteContent(content, pasteImage, valid, pasteText) {
        // Reserve the queue before asynchronous host clipboard/image decoding.
        // Keep a rejection handler attached even if an earlier input fails.
        const pending = Promise.resolve(content).then(value => ({value}), error => ({error}));
        return this._cageQueueInput(async () => {
            const {value, error} = await pending;
            if (error) throw error;
            if (!valid()) throw new Error('Input target changed');
            if (value.png || value.files || value.fileSelection) return this._cageRunInput(() => pasteImage(value));
            if (pasteText) return this._cageRunInput(() => pasteText(value.text || ''));
            return this._cagePasteText(value.text || '');
        });
    }

    cagePasteText(text) {
        return this._cageQueueInput(() => this._cagePasteText(text));
    }

    _cagePasteText(text) {
        return new Promise((resolve, reject) => {
            if (this._rfbConnectionState !== 'connected' || this._viewOnly ||
                !this._supportsFence ||
                !this._clipboardServerCapabilitiesActions[extendedClipboardActionNotify] ||
                !this._clipboardServerCapabilitiesActions[extendedClipboardActionProvide]) {
                reject(new Error('Clipboard transport is not ready'));
                return;
            }
            const id = 'cage-paste-' + (this._cagePasteSequence = (this._cagePasteSequence || 0) + 1);
            const timer = setTimeout(() => {
                if (this._cagePasteJob?.id === id) this._cagePasteJob = null;
                reject(new Error('Remote application did not accept the paste'));
            }, 4000);
            this._cagePasteJob = {id, timer, resolve, phase: 'data'};
            this.clipboardPasteFrom(text);
        });
    }

    clipboardPasteFrom(text) {''')

replace_once('''    sendKey(keysym, code, down) {
        if (this._rfbConnectionState''', '''    sendKey(keysym, code, down) {
        if (this._cageInputChain) {
            this._cageQueueInput(() => this._cageSendKey(keysym, code, down));
            return;
        }
        this._cageSendKey(keysym, code, down);
    }

    _cageSendKey(keysym, code, down) {
        if (this._rfbConnectionState''')
replace_once('''            this.sendKey(keysym, code, true);
            this.sendKey(keysym, code, false);''', '''            this._cageSendKey(keysym, code, true);
            this._cageSendKey(keysym, code, false);''')
replace_once('''        const scancode = XtScancode[code];''', '''        // Track modifiers when they actually reach the wire, including keys
        // waiting behind text transfers. Host key state may already be newer.
        if (keysym >= 0xffe1 && keysym <= 0xffee) {
            this._cageModifiers ||= new Map();
            if (down) this._cageModifiers.set(code, keysym);
            else this._cageModifiers.delete(code);
        }
        const scancode = XtScancode[code];''')
replace_once('''    _sendMouse(x, y, mask) {''', '''    _sendMouse(x, y, mask) {
        if (this._cageInputChain) {
            this._cageQueueInput(() => this._cageSendMouse(x, y, mask));
            return;
        }
        this._cageSendMouse(x, y, mask);
    }

    _cageSendMouse(x, y, mask) {''')
provide = 'RFB.messages.extendedClipboardProvide(this._sock, [extendedClipboardFormatText], [this._clipboardText]);'
replace_once(provide, provide + '''
                        if (this._cagePasteJob?.phase === 'data') {
                            this._cagePasteJob.phase = 'fence';
                            RFB.messages.clientFence(this._sock, (1 << 31) | 1, this._cagePasteJob.id);
                        }''')
replace_once('''        if (!(flags & (1<<31))) {
            return this._fail("Unexpected fence response");
        }''', '''        if (!(flags & (1<<31))) {
            const input = this._cageInputFence;
            if (input && payload === input.id) {
                clearTimeout(input.timer);
                this._cageInputFence = null;
                input.resolve();
                return true;
            }
            if (payload.startsWith('cage-input-')) return true;
            const job = this._cagePasteJob;
            if (job && job.phase === 'fence' && payload === job.id) {
                // Punctuation can commit while Shift is still physically held.
                // Paste with a plain Ctrl+V, then restore the wire modifiers;
                // queued host key releases will follow in their normal order.
                const modifiers = [...(this._cageModifiers || [])];
                for (const [code, sym] of modifiers) this._cageSendKey(sym, code, false);
                this._cageSendKey(0xffe3, 'ControlLeft', true);
                this._cageSendKey(0x76, 'KeyV', true);
                this._cageSendKey(0x76, 'KeyV', false);
                this._cageSendKey(0xffe3, 'ControlLeft', false);
                for (const [code, sym] of modifiers) this._cageSendKey(sym, code, true);
                job.phase = 'keys';
                RFB.messages.clientFence(this._sock, (1 << 31) | 1, job.id + '-keys');
                return true;
            }
            if (job && job.phase === 'keys' && payload === job.id + '-keys') {
                clearTimeout(job.timer);
                this._cagePasteJob = null;
                job.resolve();
                return true;
            }
            // A late acknowledgement after a timeout must not disconnect.
            if (payload.startsWith('cage-paste-')) return true;
            return this._fail("Unexpected fence response");
        }''')
path.write_text(source)
if ui.exists(): ui.write_text(u)
if page.exists(): page.write_text(h)
