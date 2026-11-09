import { getKeycode, getKeysym } from "../.generated/novnc/core/input/util.js";
import { writeClipboard, nativeAvailable, native } from "./api";
import {
  readClipboardContent,
  transferFiles,
  hasFiles,
} from "./image-transfer";

// A user gesture starts every host clipboard access. Fences and serialization
// in the shared RFB patch ensure that repeated pastes are processed in order.
export function installRemoteInput(
  rfb,
  container,
  { mode, onStatus, onFallback, isActive, insertText, insertComposition, caretRect, tabId: getTargetId = () => "", pasteContent, setCopyContext },
) {
  const listeners = [],
    handled = new Set(),
    physical = new Set(),
    remote = new Map();
  let lastText = "",
    copyRequested = false,
    copyContext = "",
    copyTimer,
    pending = 0,
    composing = false,
    compositionTarget = "",
    compositionSession = "",
    compositionSequence = 0,
    caretState,
    caretTimer,
    caretPending = false,
    nativeOwner = "",
    nativeTarget = "",
    nativeOwned = false,
    nativeFailed = false,
    disposed = false;
  const target = document.createElement("textarea");
  target.setAttribute("aria-label", "Cage 网页输入");
  target.setAttribute("autocomplete", "off");
  target.setAttribute("autocapitalize", "off");
  target.setAttribute("wrap", "off");
  target.spellcheck = false;
  target.className = "remote-text-input";
  target.style.cssText = "position:fixed;left:0;top:0";
  container.append(target);
  rfb.cageSetKeyboardTarget(target);
  const on = (object, type, fn, options) => {
    object.addEventListener(type, fn, options);
    listeners.push(() => object.removeEventListener(type, fn, options));
  };
  const focused = () =>
    isActive() &&
    (nativeOwned || container.contains(document.activeElement) ||
      document.activeElement === target);
  const cancelCopy = () => {
    copyContext = "";
    copyRequested = false;
    clearTimeout(copyTimer);
  };
  const prepareCopy = () => {
    if (!setCopyContext || !isActive() || disposed) return;
    copyContext = crypto.randomUUID().replaceAll("-", "");
    const context = copyContext;
    void setCopyContext(context, () => !disposed && isActive() && context === copyContext).catch(() => {
      if (copyContext === context) cancelCopy();
    });
  };
  const deactivateNative = () => {
    const owner = nativeOwner;
    nativeOwned = false;
    nativeOwner = "";
    nativeTarget = "";
    if (owner) void native("remoteInput", { operation: "deactivate", owner }).catch(() => {});
  };
  const updateNative = (rect) => {
    if (nativeFailed || !window.cageNativeIMEAvailable || !nativeAvailable() || !focused()) return;
    if (!nativeOwned && composing) return;
    const operation = nativeOwned ? "caret" : "activate";
    if (!nativeOwned) { nativeOwner = crypto.randomUUID(); nativeTarget = getTargetId(); nativeOwned = true; }
    const owner = nativeOwner;
    void native("remoteInput", { operation, owner, ...rect }).then((accepted) => {
      if (owner !== nativeOwner) return;
      if (!accepted || disposed) {
        deactivateNative();
        nativeFailed = !accepted;
      }
    }).catch(() => { if (owner === nativeOwner) { deactivateNative(); nativeFailed = true; } });
  };
  const position = (x, y) => {
    target.style.left = `${Math.max(0, Math.min(x, window.innerWidth - 1))}px`;
    target.style.top = `${Math.max(0, Math.min(y, window.innerHeight - 24))}px`;
  };
  const syncCaret = async () => {
    if (!caretRect || caretPending || disposed || !isActive()) return;
    caretPending = true;
    const id = getTargetId();
    if (nativeOwned && nativeTarget !== id) deactivateNative();
    try {
      const value = await caretRect(compositionSession);
      if (disposed || !isActive() || id !== getTargetId()) return;
      caretState = value?.available ? { ...value, target: id, time: Date.now() } : null;
      if (!value?.available) { deactivateNative(); return; }
      if (!value.rect || value.rect[3] <= 0) return;
      const canvas = container.querySelector("canvas");
      if (!canvas?.width || !canvas.height) return;
      const bounds = canvas.getBoundingClientRect();
      const height = Math.max(1, value.rect[3] / canvas.height * bounds.height);
      target.style.height = `${height}px`;
      target.style.lineHeight = `${height}px`;
      const x = bounds.left + (value.rect[0] / canvas.width) * bounds.width;
      const y = bounds.top + (value.rect[1] / canvas.height) * bounds.height;
      position(x, y);
      updateNative({ x, y, height });
    } catch {} finally { caretPending = false; }
  };
  const pollCaret = async () => {
    if (focused()) await syncCaret();
    if (!disposed) caretTimer = setTimeout(pollCaret, 250);
  };
  if (caretRect) caretTimer = setTimeout(pollCaret, 0);
  const bounds = container.getBoundingClientRect();
  position(bounds.left + 12, bounds.top + 12);
  on(
    container,
    "pointerdown",
    (event) => {
      // Cancel before noVNC queues a click into another remote field.
      if (composing) {
        if (compositionSession) queueComposition({ mode: "cancel" });
        resetComposition();
        const owner = nativeOwner;
        if (owner) void native("remoteInput", { operation: "cancel", owner }).catch(() => {});
        target.blur();
      }
      position(event.clientX, event.clientY);
      if (event.isTrusted && event.button === 0) prepareCopy();
    },
    true,
  );
  const commit = (text) => {
    if (disposed || !isActive() || !text) return;
    // X0tigervnc cannot map arbitrary Unicode to its finite X11 keycode pool.
    // The browser inserts committed text directly, without clipboard mutation.
    insertText(text).catch(() => {
      if (!disposed) onStatus("文字输入未完成，请重新连接画面后重试");
    });
  };
  const queueComposition = (value) => {
    const targetId = compositionTarget;
    if (!insertComposition || !compositionSession || !targetId || disposed ||
        (!isActive() && value.mode !== "cancel")) return;
    // Reserve the shared RFB input queue in this event handler. Deferring this
    // call to another promise would let the next Enter overtake the commit.
    insertComposition(targetId, { session: compositionSession, sequence: compositionSequence++, ...value }).catch((error) => {
        if (!disposed && value.mode !== "cancel")
          say(error.message || "输入法组合文字未能同步");
    });
  };
  const resetComposition = () => {
    composing = false;
    compositionTarget = "";
    compositionSession = "";
    target.value = "";
  };
  const startComposition = () => {
    composing = true;
    compositionTarget = getTargetId();
    if (insertComposition && caretState?.context && caretState.target === compositionTarget &&
        Date.now() - caretState.time < 1000) {
      compositionSession = crypto.randomUUID().replaceAll("-", "");
      compositionSequence = 0;
      queueComposition({ mode: "begin", context: caretState.context });
    }
    void syncCaret();
  };
  on(target, "compositionstart", startComposition);
  on(target, "compositionupdate", () => {
    void syncCaret();
  });
  const endComposition = (text) => {
    if (composing) {
      if (compositionSession) queueComposition({ mode: "commit", text, selectionStart: 0, selectionEnd: 0 });
      else if (compositionTarget === getTargetId()) commit(text);
    }
    resetComposition();
    void syncCaret();
  };
  on(target, "compositionend", (event) => endComposition(event.data || ""));
  on(target, "input", (event) => {
    if (document.activeElement !== target) {
      if (!composing) target.value = "";
      return;
    }
    if (composing || event.isComposing) {
      if (compositionSession && ["insertCompositionText", "insertFromComposition"].includes(event.inputType)) {
        queueComposition({
          mode: "update",
          text: target.value,
          selectionStart: target.selectionStart ?? target.value.length,
          selectionEnd: target.selectionEnd ?? target.value.length,
        });
      }
      return;
    }
    // WebKit may deliver the final input after compositionend. That text has
    // already been sent. Input without composition covers emoji and dictation.
    if (
      !["insertFromComposition", "insertCompositionText"].includes(
        event.inputType,
      )
    )
      commit(target.value);
    target.value = "";
  });
  on(target, "blur", () => {
    if (nativeOwned) return;
    if (composing && compositionSession) queueComposition({ mode: "cancel", text: "", selectionStart: 0, selectionEnd: 0 });
    resetComposition();
  });
  on(document, "focusin", (event) => {
    if (!container.contains(event.target)) cancelCopy();
    if (nativeOwned && !container.contains(event.target)) deactivateNative();
  });
  on(document, "pointerdown", (event) => {
    if (!container.contains(event.target)) cancelCopy();
  }, true);
  on(document, "visibilitychange", () => {
    if (document.visibilityState === "hidden") cancelCopy();
  });
  on(window, "cage-native-input", (event) => {
    const value = event.detail;
    if (disposed || !value || value.owner !== nativeOwner) return;
    if (value.kind === "deactivated") {
      nativeOwned = false;
      nativeOwner = "";
      if (composing && compositionSession) queueComposition({ mode: "cancel" });
      resetComposition();
      return;
    }
    if (!isActive() || nativeTarget !== getTargetId()) { deactivateNative(); return; }
    if (value.kind === "compositionstart") startComposition();
    else if (value.kind === "compositionupdate" && composing) {
      queueComposition({ mode: "update", text: value.text, selectionStart: value.selectionStart, selectionEnd: value.selectionEnd });
    } else if (value.kind === "compositionend") endComposition(value.text || "");
    else if (value.kind === "input") commit(value.text);
    else if (["keydown", "keyup"].includes(value.kind)) {
      const key = new KeyboardEvent(value.kind, { ...value, bubbles: true, cancelable: true });
      key.cageNativeTextKey = value.textKey;
      target.dispatchEvent(key);
    }
  });
  on(container, "pointerup", () => {
    setTimeout(() => void syncCaret(), 50);
  });
  const say = (text) => {
    if (!disposed) onStatus(text);
  };
  const chord = (key) => {
    rfb.sendKey(0xffe3, "ControlLeft", true);
    rfb.sendKey(key.charCodeAt(0), "Key" + key.toUpperCase(), true);
    rfb.sendKey(key.charCodeAt(0), "Key" + key.toUpperCase(), false);
    rfb.sendKey(0xffe3, "ControlLeft", false);
  };
  const copy = (key = "c") => {
    copyRequested = true;
    chord(key);
    say("正在复制所选文本…");
    clearTimeout(copyTimer);
    copyTimer = setTimeout(() => {
      copyRequested = false;
      onFallback(lastText, "未收到新的选中文本，可在这里复制 Cage 剪贴板。");
    }, 1600);
  };
  const paste = (text) => {
    if (disposed) return;
    pending++;
    say("正在粘贴…");
    rfb.focus();
    rfb.cagePasteText(text).then(
      () => {
        pending--;
        say(pending ? "正在粘贴…" : "已粘贴");
      },
      () => {
        pending--;
        say("粘贴连接未就绪，请重新连接画面后重试");
      },
    );
  };
  const receive = (content) => {
    if (disposed || !isActive()) return;
    pending++;
    say("正在传递剪贴板内容…");
    rfb.focus();
    return pasteContent(content).then(
      () => {
        pending--;
        say(pending ? "正在粘贴…" : "已传递到当前输入位置");
      },
      (error) => {
        pending--;
        if (error.name === "NotAllowedError" && !disposed) {
          onFallback(
            "",
            "浏览器未允许读取剪贴板。图片可直接拖入画面；文本可在这里粘贴。",
          );
          return;
        }
        say(error.message || "图片或文本未能传入，请重新连接后重试");
      },
    );
  };
  const readAndPaste = () => {
    if (disposed || !isActive()) return;
    return receive(readClipboardContent());
  };
  on(rfb, "clipboard", async (event) => {
    lastText = event.detail.text;
    if (!copyRequested || !isActive()) return;
    copyRequested = false;
    clearTimeout(copyTimer);
    try {
      await writeClipboard(lastText);
      say("已复制到本机");
    } catch {
      onFallback(lastText, "请在这里复制到本机。");
    }
  });
  const release = () => {
    cancelCopy();
    for (const [code, keysym] of remote) rfb.sendKey(keysym, code, false);
    remote.clear();
    physical.clear();
    handled.clear();
  };
  on(window, "blur", release);
  on(
    document,
    "keydown",
    (event) => {
      if (!focused()) return;
      if (event.isTrusted && (!copyContext || ["Enter", " ", "Tab"].includes(event.key) ||
          event.ctrlKey || event.metaKey || event.altKey)) prepareCopy();
      if (
        composing ||
        event.isComposing ||
        event.keyCode === 229 ||
        ["Process", "Dead"].includes(event.key)
      ) {
        // Leave composition keys to the host IME. The guest receives the
        // composition text through the authenticated browser IME endpoint.
        event.stopImmediatePropagation();
        return;
      }
      const code = getKeycode(event),
        mapping = mode() === "mac",
        malformedCommandLetter =
          event.metaKey &&
          /^Key[A-Z]$/.test(code) &&
          !/^[a-z]$/i.test(event.key),
        key = malformedCommandLetter
          ? code[3].toLowerCase()
          : event.key.toLowerCase();
      if (["Shift", "Control", "Alt"].includes(event.key)) physical.add(code);
      if (
        mapping &&
        event.metaKey &&
        ["Shift", "Control", "Alt"].includes(event.key)
      ) {
        const sym = getKeysym(event);
        rfb.sendKey(sym, code, true);
        remote.set(code, sym);
        event.preventDefault();
        event.stopImmediatePropagation();
        return;
      }
      const clipboard =
        (event.metaKey || event.ctrlKey) &&
        !event.altKey &&
        ((!event.shiftKey && ["c", "x"].includes(key)) || key === "v");
      if (clipboard) {
        event.stopImmediatePropagation();
        handled.add(code);
        if (key === "v") {
          if (nativeAvailable()) {
            event.preventDefault();
            void readAndPaste();
          } else {
            target.value = "";
            target.focus({ preventScroll: true });
          }
        } else {
          event.preventDefault();
          copy(key);
        }
        return;
      }
      if (
        mapping &&
        event.metaKey &&
        !["Meta", "Shift", "Control", "Alt", "CapsLock"].includes(event.key)
      ) {
        let sym = getKeysym(event);
        if (
          (malformedCommandLetter || sym === null || sym < 32) &&
          /^Key[A-Z]$/.test(code)
        )
          sym = (event.shiftKey ? code[3] : code[3].toLowerCase()).codePointAt(
            0,
          );
        if (sym !== null) {
          const held = (p) => [...physical].some((c) => c.startsWith(p)),
            ctrl = physical.has("ControlRight")
              ? [0xffe4, "ControlRight"]
              : [0xffe3, "ControlLeft"],
            shift = physical.has("ShiftRight")
              ? [0xffe2, "ShiftRight"]
              : [0xffe1, "ShiftLeft"];
          rfb.sendKey(...ctrl, true);
          if (event.shiftKey) rfb.sendKey(...shift, true);
          rfb.sendKey(sym, code, true);
          rfb.sendKey(sym, code, false);
          if (event.shiftKey && !held("Shift")) rfb.sendKey(...shift, false);
          if (!event.ctrlKey || !held("Control")) rfb.sendKey(...ctrl, false);
          event.preventDefault();
          event.stopImmediatePropagation();
          handled.add(code);
          return;
        }
      }
      if (mapping && event.key === "Meta") event.stopImmediatePropagation();
      if (!event.metaKey && !event.ctrlKey && [...event.key].length === 1 &&
          event.key.codePointAt(0) > 127) {
        // Fullwidth punctuation may arrive without a composition session.
        // Sending it as an X11 keysym can silently drop it in X0tigervnc.
        event.preventDefault();
        event.stopImmediatePropagation();
        handled.add(code);
        commit(event.key);
      } else if (!event.cageNativeTextKey && !event.metaKey && !event.ctrlKey && !event.altKey &&
          event.key.length === 1 && /[^a-zA-Z0-9 ]/.test(event.key)) {
        // Let the host input method choose punctuation (e.g. ? -> ？).
        // The resulting input event carries the actual character to insert.
        event.stopImmediatePropagation();
      }
    },
    true,
  );
  on(
    document,
    "keyup",
    (event) => {
      const code = getKeycode(event);
      // noVNC ignores releases for keys it never sent. Let keyup through so a
      // modifier pressed before composition can still be released normally.
      physical.delete(code);
      if (remote.has(code)) {
        rfb.sendKey(remote.get(code), code, false);
        remote.delete(code);
        event.stopImmediatePropagation();
        return;
      }
      if (
        handled.delete(code) ||
        (focused() && mode() === "mac" && event.key === "Meta")
      )
        event.stopImmediatePropagation();
      else if (focused() && !composing && caretRect)
        setTimeout(() => void syncCaret(), 35);
    },
    true,
  );
  on(
    document,
    "paste",
    (event) => {
      if (!isActive() || (!focused() && event.target !== target)) return;
      event.preventDefault();
      event.stopImmediatePropagation();
      const files = transferFiles(event.clipboardData);
      if (files.length) {
        receive({ files, kind: "paste" });
      } else if (event.clipboardData.types.includes("text/plain"))
        paste(event.clipboardData.getData("text/plain"));
      else say("剪贴板中没有可粘贴的图片或文本");
    },
    true,
  );
  on(container, "dragover", (event) => {
    if (!isActive() || !hasFiles(event.dataTransfer)) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  });
  on(container, "drop", (event) => {
    if (!isActive() || !hasFiles(event.dataTransfer)) return;
    event.preventDefault();
    event.stopPropagation();
    const canvas = container.querySelector("canvas") || container;
    const rect = canvas.getBoundingClientRect();
    receive({ files: transferFiles(event.dataTransfer), kind: "drop",
      x: Math.max(0, Math.min(1, (event.clientX - rect.left) / rect.width)),
      y: Math.max(0, Math.min(1, (event.clientY - rect.top) / rect.height)) });
  });
  return {
    copy,
    cancelCopy,
    async receiveCopy({ id, text, context }) {
      const valid = () => !disposed && Boolean(copyContext) && context === copyContext && id === getTargetId() && focused() &&
        document.visibilityState !== "hidden" && document.hasFocus();
      if (typeof text !== "string" || text.length > 65536 || !valid()) return;
      lastText = text;
      copyRequested = false;
      clearTimeout(copyTimer);
      try {
        await writeClipboard(text);
        if (valid()) say("已复制到本机");
      } catch {
        if (valid()) onFallback(text, "请在这里复制到本机。");
      }
    },
    readAndPaste,
    paste,
    text: () => lastText,
    edit(action) {
      if (composing) return false;
      if (action === "copy") copy();
      else if (action === "cut") copy("x");
      else if (action === "paste") void readAndPaste();
      else if (action === "selectAll") chord("a");
      else if (action === "undo") chord("z");
      else if (action === "redo") {
        rfb.sendKey(0xffe3, "ControlLeft", true);
        rfb.sendKey(0xffe1, "ShiftLeft", true);
        rfb.sendKey(0x5a, "KeyZ", true);
        rfb.sendKey(0x5a, "KeyZ", false);
        rfb.sendKey(0xffe1, "ShiftLeft", false);
        rfb.sendKey(0xffe3, "ControlLeft", false);
      } else return false;
      return true;
    },
    dispose() {
      if (composing && compositionSession)
        queueComposition({ mode: "cancel", text: "", selectionStart: 0, selectionEnd: 0 });
      disposed = true;
      deactivateNative();
      clearTimeout(copyTimer);
      clearTimeout(caretTimer);
      release();
      listeners.forEach((remove) => remove());
      rfb.cageSetKeyboardTarget(null);
      target.remove();
    },
  };
}
