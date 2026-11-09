import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { webcrypto } from "node:crypto";
const source = readFileSync(
  new URL("./remote-input.js", import.meta.url),
  "utf8",
)
  .replace(/^import[\s\S]*?;\n/gm, "")
  .replace("export function installRemoteInput", "function installRemoteInput");
function fixture(native = false, browserComposition = false) {
  const doc = new EventTarget(),
    win = new EventTarget(),
    canvas = {},
    local = {},
    keys = [],
    pastes = [],
    texts = [],
    compositions = [],
    copyContexts = [],
    writes = [],
    statuses = [];
  let reads = 0,
    mode = "mac",
    currentTabId = "tab-1";
  doc.activeElement = canvas;
  doc.hasFocus = () => true;
  doc.body = { append() {} };
  const target = Object.assign(new EventTarget(), {
    value: "",
    style: {},
    classList: { add() {}, remove() {} },
    setAttribute() {},
    focus() {
      doc.activeElement = this;
    },
    remove() {},
  });
  doc.createElement = () => target;
  win.innerWidth = 1280;
  win.innerHeight = 800;
  const rfb = new EventTarget();
  rfb.cageSetKeyboardTarget = (element) => {
    rfb.target = element;
  };
  rfb.sendKey = (...args) => keys.push(args);
  rfb.focus = () => {
    doc.activeElement = rfb.target || canvas;
  };
  rfb.cagePasteText = (text) => {
    pastes.push(text);
    return Promise.resolve();
  };
  const context = {
    document: doc,
    window: win,
    setTimeout,
    clearTimeout,
    Promise,
    Map,
    Set,
    console,
    crypto: webcrypto,
    readClipboardContent: async () => {
      reads++;
      return { text: "中文🙂" };
    },
    transferFiles: (data) => Array.from(data?.files || []),
    hasFiles: (data) => Array.from(data?.types || []).includes("Files"),
    imagePayload: async () => ({ png: "fixture-image" }),
    writeClipboard: async (text) => writes.push(text),
    nativeAvailable: () => native,
    getKeycode: (e) => e.code,
    getKeysym: (e) =>
      ({ Shift: 0xffe1, Control: 0xffe3, Alt: 0xffe9, Meta: 0xffeb })[e.key] ??
      (e.key.length === 1 ? e.key.codePointAt(0) : null),
    Keysyms: {
      lookup: (codepoint) =>
        codepoint <= 255 ? codepoint : 0x01000000 | codepoint,
    },
  };
  const install = vm.runInNewContext(source + "\ninstallRemoteInput;", context);
  const container = Object.assign(new EventTarget(), {
      querySelector: () => null,
      contains: (element) => element === canvas || element === target,
      append() {},
      getBoundingClientRect: () => ({ left: 0, top: 0 }),
    });
  const controller = install(
    rfb,
    container,
    {
      mode: () => mode,
      onStatus: (t) => statuses.push(t),
      onFallback() {},
      isActive: () => true,
      setCopyContext: async (context, valid) => { if (valid()) copyContexts.push(context); },
      insertText: async (text) => {
        texts.push(text);
      },
      insertComposition: browserComposition ? async (id, value) => {
        if (id !== currentTabId) throw new Error("Input target changed");
        compositions.push({ id, ...value });
      } : null,
      tabId: () => currentTabId,
      caretRect: browserComposition ? async () => ({
        available: true, context: "a".repeat(32), rect: [240, 120, 0, 24],
      }) : null,
      pasteContent: async (content) => {
        const value = await content;
        if (rfb.target) pastes.push(value.png || value.text);
      },
    },
  );
  const key = (type, key, code, extra = {}) => {
    const event = new Event(type, { cancelable: true });
    Object.assign(event, {
      key,
      code,
      metaKey: false,
      ctrlKey: false,
      shiftKey: false,
      altKey: false,
      ...extra,
    });
    doc.dispatchEvent(event);
    return event;
  };
  return {
    doc,
    win,
    rfb,
    keys,
    pastes,
    texts,
    compositions,
    writes,
    statuses,
    armCopy: () => {
      const event = new Event("pointerdown");
      Object.defineProperty(event, "isTrusted", { value: true });
      Object.assign(event, { button: 0, clientX: 20, clientY: 20 });
      container.dispatchEvent(event);
      return copyContexts.at(-1);
    },
    controller,
    key,
    local,
    target,
    setMode: (value) => {
      mode = value;
    },
    setTabId: (value) => {
      currentTabId = value;
    },
    reads: () => reads,
  };
}
test("Mac find and redo preserve the Linux Ctrl/Shift chord", () => {
  const f = fixture();
  try {
    assert.equal(
      f.key("keydown", "f", "KeyF", { metaKey: true }).defaultPrevented,
      true,
    );
    assert.deepEqual(
      f.keys.map((k) => [k[0], k[2]]),
      [
        [0xffe3, true],
        [102, true],
        [102, false],
        [0xffe3, false],
      ],
    );
    f.keys.length = 0;
    f.key("keydown", "\0", "KeyZ", { metaKey: true, shiftKey: true });
    assert.deepEqual(
      f.keys.map((k) => [k[0], k[2]]),
      [
        [0xffe3, true],
        [0xffe1, true],
        [90, true],
        [90, false],
        [0xffe1, false],
        [0xffe3, false],
      ],
    );
  } finally {
    f.controller.dispose();
  }
});

function inputEvent(f, type, properties = {}) {
  const event = new Event(type, { cancelable: true });
  Object.assign(event, properties);
  f.target.dispatchEvent(event);
  return event;
}

test("host IME receives composition keys and sends only committed Unicode in order", () => {
  const f = fixture();
  try {
    f.rfb.focus();
    assert.equal(f.doc.activeElement, f.target);
    assert.equal(
      f.key("keydown", "Process", "KeyN", { keyCode: 229 }).defaultPrevented,
      false,
    );
    inputEvent(f, "compositionstart");
    f.target.value = "nihao";
    inputEvent(f, "input", {
      data: "nihao",
      isComposing: true,
      inputType: "insertCompositionText",
    });
    assert.equal(
      f.key("keydown", " ", "Space", { isComposing: true }).defaultPrevented,
      false,
    );
    assert.deepEqual(f.keys, []);
    inputEvent(f, "compositionend", { data: "你好🙂" });
    // Safari sends a final input after compositionend; Chromium sends it before.
    f.target.value = "你好🙂";
    inputEvent(f, "input", {
      data: "你好🙂",
      inputType: "insertFromComposition",
    });
    assert.deepEqual(f.keys, []);
    assert.equal(f.target.value, "");
    assert.deepEqual(f.texts, ["你好🙂"]);
    assert.equal(f.reads(), 0);
    inputEvent(f, "compositionstart");
    inputEvent(f, "compositionend", { data: "你好" });
    assert.equal(
      f.texts.length,
      2,
      "identical consecutive commits are not deduplicated",
    );
  } finally {
    f.controller.dispose();
  }
  assert.equal(f.rfb.target, null);
});

test("browser composition is forwarded as preedit, then committed separately", async () => {
  const f = fixture(false, true);
  await new Promise((resolve) => setTimeout(resolve, 5));
  try {
    f.rfb.focus();
    inputEvent(f, "compositionstart");
    f.target.value = "nihao";
    inputEvent(f, "compositionupdate", { data: "nihao" });
    inputEvent(f, "input", { data: "nihao", inputType: "insertCompositionText", isComposing: true });
    await Promise.resolve();
    inputEvent(f, "compositionend", { data: "你好" });
    await new Promise((resolve) => setImmediate(resolve));
    assert.deepEqual(f.texts, []);
    assert.equal(f.compositions[0].mode, "begin");
    assert.equal(new Set(f.compositions.map(value => value.session)).size, 1);
    assert.deepEqual(f.compositions.map(value => value.sequence), [0, 1, 2]);
    assert.deepEqual(JSON.parse(JSON.stringify(f.compositions.slice(1).map(({session, sequence, ...value}) => value))), [
      { id: "tab-1", mode: "update", text: "nihao", selectionStart: 5, selectionEnd: 5 },
      { id: "tab-1", mode: "commit", text: "你好", selectionStart: 0, selectionEnd: 0 },
    ]);
  } finally {
    f.controller.dispose();
  }
});

test("queued composition stays bound to its original browser tab", async () => {
  const f = fixture(false, true);
  await new Promise((resolve) => setTimeout(resolve, 5));
  f.rfb.focus();
  inputEvent(f, "compositionstart");
  f.target.value = "nihao";
  inputEvent(f, "input", { data: "nihao", inputType: "insertCompositionText", isComposing: true });
  f.setTabId("tab-2");
  inputEvent(f, "compositionend", { data: "你好" });
  await new Promise((resolve) => setImmediate(resolve));
  assert.deepEqual(f.compositions.map(value => value.mode), ["begin", "update"]);
  assert.ok(f.statuses.includes("Input target changed"));
  f.controller.dispose();
});

test("cancelled and disconnected compositions do not insert text", () => {
  const f = fixture();
  f.rfb.focus();
  inputEvent(f, "compositionstart");
  inputEvent(f, "compositionend", { data: "" });
  assert.deepEqual(f.keys, []);
  inputEvent(f, "compositionstart");
  f.controller.dispose();
  inputEvent(f, "compositionend", { data: "迟到的文本" });
  assert.deepEqual(f.keys, []);
  assert.deepEqual(f.texts, []);
});

test("text input without a composition supports emoji and clears the host buffer", () => {
  const f = fixture();
  try {
    f.rfb.focus();
    f.target.value = "🙂";
    inputEvent(f, "input", { data: "🙂", inputType: "insertText" });
    assert.deepEqual(f.keys, []);
    assert.deepEqual(f.texts, ["🙂"]);
    assert.equal(f.target.value, "");
  } finally {
    f.controller.dispose();
  }
});

test("fullwidth punctuation without composition uses text, while host punctuation keeps its default action", () => {
  const f = fixture();
  try {
    f.rfb.focus();
    for (const character of ["？", "？", "，", "。", "！", "🙂"]) {
      assert.equal(f.key("keydown", character, "Slash", { shiftKey: true }).defaultPrevented, true);
      f.key("keyup", character, "Slash");
    }
    assert.deepEqual(f.texts, ["？", "？", "，", "。", "！", "🙂"]);
    assert.deepEqual(f.keys, []);
    assert.equal(f.key("keydown", "?", "Slash", { shiftKey: true }).defaultPrevented, false);
    f.target.value = "？";
    inputEvent(f, "input", { data: "？", inputType: "insertText" });
    assert.equal(f.texts.at(-1), "？");
    assert.equal(f.key("keydown", "?", "Slash", { ctrlKey: true }).defaultPrevented, false);
  } finally { f.controller.dispose(); }
});
test("local fields and native keyboard mode do not acquire mapped shortcuts", () => {
  const f = fixture();
  try {
    f.doc.activeElement = f.local;
    assert.equal(
      f.key("keydown", "f", "KeyF", { metaKey: true }).defaultPrevented,
      false,
    );
    assert.equal(f.keys.length, 0);
    f.rfb.focus();
    f.setMode("native");
    f.key("keydown", "f", "KeyF", { metaKey: true });
    assert.equal(f.keys.length, 0);
  } finally {
    f.controller.dispose();
  }
});
test("background clipboard notifications never write to the host", async () => {
  const f = fixture();
  try {
    const clipboard = new Event("clipboard");
    clipboard.detail = { text: "guest text" };
    f.rfb.dispatchEvent(clipboard);
    await Promise.resolve();
    assert.equal(f.reads(), 0);
    assert.deepEqual(f.writes, []);
    f.key("keydown", "c", "KeyC", { metaKey: true });
    f.rfb.dispatchEvent(clipboard);
    await Promise.resolve();
    assert.deepEqual(f.writes, ["guest text"]);
  } finally {
    f.controller.dispose();
  }
});
test("native menu paste accepts every rapid gesture, without debouncing", async () => {
  const f = fixture(true);
  try {
    for (let i = 0; i < 10; i++)
      f.key("keydown", "v", "KeyV", { metaKey: true });
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(f.reads(), 10);
    assert.equal(f.pastes.length, 10);
    assert.ok(f.pastes.every((t) => t === "中文🙂"));
  } finally {
    f.controller.dispose();
  }
});
test("mapped modifier is released even after focus is lost", () => {
  const f = fixture();
  try {
    f.key("keydown", "Shift", "ShiftLeft", { metaKey: true, shiftKey: true });
    f.doc.activeElement = f.local;
    f.win.dispatchEvent(new Event("blur"));
    assert.deepEqual(f.keys, [
      [0xffe1, "ShiftLeft", true],
      [0xffe1, "ShiftLeft", false],
    ]);
  } finally {
    f.controller.dispose();
  }
});

test("a clipboard read finishing after disconnect is not pasted into a stale connection", async () => {
  const f = fixture(true);
  f.controller.readAndPaste();
  f.controller.dispose();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(f.pastes.length, 0);
});

test("verified page copies only write to the host from the focused live tab", async () => {
  const f = fixture();
  const context = f.armCopy();
  const copy = (id = "tab-1", text = "实际复制的文字🙂") => f.controller.receiveCopy({ id, text, context });
  try {
    await copy();
    assert.deepEqual(f.writes, ["实际复制的文字🙂"]);
    await copy("other-tab");
    f.doc.hasFocus = () => false;
    await copy();
    f.doc.hasFocus = () => true;
    f.doc.visibilityState = "hidden";
    await copy();
    f.doc.visibilityState = "visible";
    f.doc.activeElement = f.local;
    await copy();
    f.doc.activeElement = f.target;
    await copy("tab-1", "x".repeat(65537));
    assert.equal(f.writes.length, 1);
    f.controller.dispose();
    await copy();
    assert.equal(f.writes.length, 1);
  } finally { f.controller.dispose(); }
});

test("copy results from before a focus or tab round-trip cannot write after returning", async () => {
  const f = fixture();
  try {
    const context = f.armCopy();
    const oldCopy = { id: "tab-1", text: "old pending copy", context };
    f.win.dispatchEvent(new Event("blur"));
    const next = f.armCopy();
    assert.notEqual(context, next);
    await f.controller.receiveCopy(oldCopy);
    f.setTabId("tab-2");
    f.controller.cancelCopy();
    f.setTabId("tab-1");
    const latest = f.armCopy();
    await f.controller.receiveCopy({ ...oldCopy, context: next });
    assert.equal(f.writes.length, 0);
    await f.controller.receiveCopy({ ...oldCopy, text: "new copy", context: latest });
    assert.deepEqual(f.writes, ["new copy"]);
  } finally { f.controller.dispose(); }
});

test("clicking between split panes rejects earlier copies after returning", async () => {
  const f = fixture();
  try {
    const leftContext = f.armCopy();
    f.setTabId("tab-2");
    const rightContext = f.armCopy();
    f.setTabId("tab-1");
    const currentContext = f.armCopy();

    // Both panes share one input surface, so these clicks do not cause blur.
    await f.controller.receiveCopy({ id: "tab-1", text: "old left copy", context: leftContext });
    await f.controller.receiveCopy({ id: "tab-2", text: "old right copy", context: rightContext });
    assert.deepEqual(f.writes, []);
    await f.controller.receiveCopy({ id: "tab-1", text: "current left copy", context: currentContext });
    assert.deepEqual(f.writes, ["current left copy"]);
  } finally { f.controller.dispose(); }
});

test("an unrelated RFB clipboard update never authorizes a host write", async () => {
  const f = fixture();
  try {
    const event = new Event("clipboard");
    event.detail = { text: "unrelated guest application" };
    f.rfb.dispatchEvent(event);
    await new Promise(resolve => setImmediate(resolve));
    assert.equal(f.writes.length, 0);
  } finally { f.controller.dispose(); }
});

test("malformed native Command letters retain clipboard and redo semantics", async () => {
  const f = fixture(true);
  try {
    f.key("keydown", "\u0016", "KeyV", { metaKey: true });
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(f.reads(), 1);
    assert.equal(f.pastes.length, 1);
    assert.equal(f.keys.length, 0);
    for (const letter of ["\ufffd", "\uf700", "Ω"]) {
      f.keys.length = 0;
      f.key("keydown", letter, "KeyZ", { metaKey: true, shiftKey: true });
      assert.deepEqual(
        f.keys.map((k) => [k[0], k[2]]),
        [
          [0xffe3, true],
          [0xffe1, true],
          [90, true],
          [90, false],
          [0xffe1, false],
          [0xffe3, false],
        ],
      );
    }
  } finally {
    f.controller.dispose();
  }
});
