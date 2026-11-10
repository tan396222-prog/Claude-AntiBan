import test from "node:test";
import assert from "node:assert/strict";
import { createMacWindowInput } from "./macos-window-input.mjs";

function fixture() {
  let current = { pid: 23, window: 70, geometry: "geometry-1" }, visible = true;
  const calls = [], errors = [], captured = new Set(), eventWindow = new EventTarget();
  const element = Object.assign(new EventTarget(), {
    getBoundingClientRect: () => ({ left: 20, top: 30, width: 400, height: 300 }),
    setPointerCapture: id => captured.add(id),
    hasPointerCapture: id => captured.has(id),
    releasePointerCapture(id) {
      captured.delete(id);
      element.dispatchEvent(new Event("lostpointercapture"));
    },
  });
  const input = createMacWindowInput({
    element, frame: () => current, active: () => visible,
    request: async (action, fields) => { calls.push({ action, ...fields }); return { ok: true }; },
    onError: error => errors.push(error), eventWindow,
  });
  input.setKeyboardTarget({ focus() {}, classList: { add() {} } });
  const pointer = (type, fields = {}) => {
    const event = new Event(type, { cancelable: true });
    Object.assign(event, {
      pointerId: 1, button: 0, buttons: type === "pointerup" ? 0 : 1,
      clientX: 120, clientY: 105, detail: 1,
      shiftKey: false, ctrlKey: false, altKey: false, metaKey: false, ...fields,
    });
    element.dispatchEvent(event);
    return event;
  };
  return { input, calls, errors, captured, eventWindow, element, pointer,
    changeTarget: () => { current = { ...current, geometry: "geometry-2" }; },
    hide: () => { visible = false; },
  };
}

test("native window drag preserves down/move/up and coalesces only adjacent moves", async () => {
  const f = fixture();
  let release;
  const gate = f.input.run(() => new Promise(resolve => { release = resolve; }));
  await Promise.resolve();
  try {
    assert.equal(f.pointer("pointerdown").defaultPrevented, true);
    f.pointer("pointermove", { clientX: 180 });
    f.pointer("pointermove", { clientX: 300 });
    f.pointer("pointerdown", { pointerId: 2 });
    f.pointer("pointerup", { clientX: 900, clientY: -40 });
    release();
    await gate;
    await f.input.run(() => {});
    assert.deepEqual(f.calls.map(call => call.phase), ["down", "move", "up"]);
    assert.deepEqual(f.calls.map(call => call.x), [0.25, 0.7, 1 - Number.EPSILON]);
    assert.equal(f.calls[2].y, 0);
    assert(f.calls.every(call => call.pid === 23 && call.window === 70 && call.geometry === "geometry-1"));
    assert.equal(f.captured.size, 0);
    assert.deepEqual(f.errors, []);
  } finally { f.input.dispose(); }
});

test("native window forwards right button, double click and horizontal scroll", async () => {
  const f = fixture();
  try {
    f.pointer("pointerdown", { button: 2, buttons: 2 });
    f.pointer("pointerup", { button: 2 });
    f.pointer("pointerdown", { detail: 2 });
    f.pointer("pointerup", { detail: 2 });
    f.pointer("wheel", { deltaX: 54, deltaY: -2000 });
    await f.input.run(() => {});
    assert.deepEqual(f.calls.slice(0, 4).map(call => [call.phase, call.button, call.clicks]), [
      ["down", 2, 1], ["up", 2, 1], ["down", 0, 2], ["up", 0, 2],
    ]);
    assert.equal(f.calls[4].deltaX, 54);
    assert.equal(f.calls[4].delta, -1200);
    assert.equal(f.pointer("contextmenu").defaultPrevented, true);
  } finally { f.input.dispose(); }
});

test("cancelled pointer capture resets held input before new keys", async () => {
  const f = fixture();
  try {
    f.input.sendKey(0xffeb, "MetaLeft", true);
    f.pointer("pointerdown");
    f.pointer("pointercancel");
    f.input.sendKey(97, "KeyA", true);
    await f.input.run(() => {});
    assert.equal(f.captured.size, 0);
    assert.deepEqual(f.calls.map(call => call.action), ["key", "pointer", "input-reset", "key"]);
    assert.equal(f.calls.at(-1).command, false);
  } finally { f.input.dispose(); }
});

test("queued native paste sends its chord before a following Enter", async () => {
  const f = fixture();
  let release;
  const clipboard = new Promise(resolve => { release = resolve; });
  try {
    const paste = f.input.runKeyAction(async sendKey => {
      await clipboard;
      for (const key of [[0xffeb, "MetaLeft", true], [118, "KeyV", true], [118, "KeyV", false], [0xffeb, "MetaLeft", false]])
        await sendKey(...key);
    });
    f.input.sendKey(0xff0d, "Enter", true);
    f.input.sendKey(0xff0d, "Enter", false);
    await Promise.resolve();
    assert.deepEqual(f.calls, []);
    release();
    await paste;
    await f.input.run(() => {});
    assert.deepEqual(f.calls.map(call => [call.code, call.down]), [
      ["MetaLeft", true], ["KeyV", true], ["KeyV", false], ["MetaLeft", false], ["Enter", true], ["Enter", false],
    ]);
  } finally { f.input.dispose(); }
});

test("a failed paste cancels already queued Enter and recovers for new input", async () => {
  const f = fixture();
  let fail;
  const clipboard = new Promise((_, reject) => { fail = reject; });
  try {
    const paste = f.input.runKeyAction(async () => { await clipboard; });
    f.input.sendKey(0xff0d, "Enter", true);
    const drained = f.input.run(() => {});
    fail(new Error("fixture clipboard failed"));
    await assert.rejects(paste, /fixture clipboard failed/);
    await assert.rejects(drained, /fixture clipboard failed/);
    assert.deepEqual(f.calls.map(call => call.action), ["input-reset"]);
    f.input.sendKey(97, "KeyA", true);
    await f.input.run(() => {});
    assert.equal(f.calls.at(-1).code, "KeyA");
  } finally { f.input.dispose(); }
});

test("a frame change rejects queued input and disposal releases only its connection", async () => {
  const f = fixture();
  let release;
  const gate = f.input.run(() => new Promise(resolve => { release = resolve; }));
  await Promise.resolve();
  f.input.sendKey(97, "KeyA", true);
  const drained = f.input.run(() => {});
  f.changeTarget();
  release();
  await gate;
  await assert.rejects(drained, /输入窗口已变化/);
  assert.deepEqual(f.calls.map(call => call.action), ["input-reset"]);
  f.input.dispose();
  const count = f.calls.length;
  f.pointer("pointerdown");
  f.eventWindow.dispatchEvent(new Event("blur"));
  await assert.rejects(f.input.run(() => {}), /连接已关闭/);
  assert.equal(f.calls.length, count);
});
