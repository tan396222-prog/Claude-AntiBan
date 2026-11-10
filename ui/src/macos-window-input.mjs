const identity = frame => frame && `${frame.pid}:${frame.window}:${frame.geometry || ""}`;
const modifiers = keys => ({
  shift: keys.has("ShiftLeft") || keys.has("ShiftRight"),
  control: keys.has("ControlLeft") || keys.has("ControlRight"),
  alt: keys.has("AltLeft") || keys.has("AltRight"),
  command: keys.has("MetaLeft") || keys.has("MetaRight"),
});

export function createMacWindowInput({ element, frame, active, request, onError, eventWindow = window }) {
  let keyboard, disposed = false, queued, pending = 0, move, drag, lastClick;
  const keys = new Set(), listeners = [];
  const run = action => {
    if (disposed) return Promise.reject(new Error("窗口输入连接已关闭"));
    if (pending >= 128) return Promise.reject(new Error("窗口输入等待过多，请重新连接"));
    pending++;
    const result = (queued || Promise.resolve()).then(async () => {
      if (disposed) throw new Error("窗口输入连接已关闭");
      try { return await action(); }
      catch (error) {
        keys.clear();
        await request("input-reset").catch(() => {});
        throw error;
      }
    });
    queued = result;
    const finish = () => {
      pending--;
      if (queued === result) queued = undefined;
    };
    result.then(finish, finish);
    return result;
  };
  const valid = target => !disposed && active() && identity(target) === identity(frame());
  const send = (action, fields, target = frame()) => run(() => {
    if (!target || !valid(target)) throw new Error("输入窗口已变化，请重新操作");
    return request(action, { ...target, ...fields });
  });
  const report = promise => { void promise.catch(error => { if (!disposed) onError(error.message); }); };
  const on = (target, name, callback, options) => {
    target.addEventListener(name, callback, options);
    listeners.push(() => target.removeEventListener(name, callback, options));
  };
  const point = event => {
    const box = element.getBoundingClientRect();
    return {
      x: Math.max(0, Math.min(1 - Number.EPSILON, (event.clientX - box.left) / box.width)),
      y: Math.max(0, Math.min(1 - Number.EPSILON, (event.clientY - box.top) / box.height)),
    };
  };
  const focus = () => keyboard?.focus({ preventScroll: true });
  const key = (symbol, code, down, target) => {
    if (!target || !valid(target)) throw new Error("输入窗口已变化，请重新操作");
    if (down) keys.add(code); else keys.delete(code);
    const character = symbol >= 0x01000000 ? symbol & 0x00ffffff : symbol;
    const name = character >= 32 && character < 0xff00 ? String.fromCodePoint(character) : code;
    return request("key", { ...target, code, key: name, down, ...modifiers(keys) });
  };
  on(element, "pointerdown", event => {
    if (!active() || !frame() || ![0, 1, 2].includes(event.button)) return;
    if (drag && drag.pointer !== event.pointerId) return;
    event.preventDefault(); focus(); move = undefined;
    const now = event.timeStamp;
    const nearby = lastClick && lastClick.button === event.button && now - lastClick.time < 500 &&
      Math.abs(lastClick.x - event.clientX) < 4 && Math.abs(lastClick.y - event.clientY) < 4;
    const clicks = Math.min(3, event.detail || (nearby ? lastClick.count + 1 : 1));
    lastClick = { button: event.button, x: event.clientX, y: event.clientY, count: clicks, time: now };
    drag ||= { pointer: event.pointerId, target: { ...frame() }, buttons: 0 };
    drag.buttons = event.buttons;
    element.setPointerCapture(event.pointerId);
    report(send("pointer", { phase: "down", button: event.button, buttons: event.buttons, clicks, ...point(event),
      shift: event.shiftKey, control: event.ctrlKey, alt: event.altKey, command: event.metaKey }, drag.target));
  });
  on(element, "pointermove", event => {
    if (!active() || !frame() || (drag && drag.pointer !== event.pointerId)) return;
    const target = drag?.target || { ...frame() };
    const fields = { phase: "move", buttons: event.buttons, ...point(event),
      shift: event.shiftKey, control: event.ctrlKey, alt: event.altKey, command: event.metaKey };
    if (move) { move.fields = fields; return; }
    const entry = { fields };
    move = entry;
    report(run(() => {
      if (move === entry) move = undefined;
      if (!valid(target)) throw new Error("输入窗口已变化，请重新操作");
      return request("pointer", { ...target, ...entry.fields });
    }));
  });
  on(element, "pointerup", event => {
    if (!drag || drag.pointer !== event.pointerId) return;
    event.preventDefault(); move = undefined;
    const target = drag.target;
    drag.buttons = event.buttons;
    if (!event.buttons) {
      drag = undefined;
      if (element.hasPointerCapture(event.pointerId)) element.releasePointerCapture(event.pointerId);
    }
    report(send("pointer", { phase: "up", button: event.button, buttons: event.buttons, clicks: lastClick?.count || 1,
      ...point(event), shift: event.shiftKey, control: event.ctrlKey, alt: event.altKey, command: event.metaKey }, target));
  });
  const reset = () => {
    if (disposed) return;
    move = undefined; keys.clear();
    const hadInput = Boolean(drag || keyboard);
    const pointer = drag?.pointer;
    drag = undefined;
    if (pointer !== undefined && element.hasPointerCapture(pointer)) element.releasePointerCapture(pointer);
    if (hadInput) report(run(() => { keys.clear(); return request("input-reset"); }));
  };
  on(element, "pointercancel", reset);
  on(element, "lostpointercapture", () => { if (drag) reset(); });
  on(element, "contextmenu", event => event.preventDefault());
  on(element, "wheel", event => {
    if (!active()) return;
    event.preventDefault();
    report(send("scroll", { ...point(event), delta: Math.max(-1200, Math.min(1200, event.deltaY)),
      deltaX: Math.max(-1200, Math.min(1200, event.deltaX)) }));
  }, { passive: false });
  on(eventWindow, "blur", reset);

  return {
    events: new EventTarget(), run, send, valid,
    target: () => frame() && { ...frame() },
    setKeyboardTarget(target) { keyboard = target; keyboard?.classList.add("mac-claude-typing"); },
    focus,
    pasteText: text => send("text", { text }),
    sendKey(symbol, code, down) {
      const target = frame() && { ...frame() };
      report(run(() => key(symbol, code, down, target)));
    },
    runKeyAction(action) {
      const target = frame() && { ...frame() };
      return run(() => action((...fields) => key(...fields, target)));
    },
    dispose() {
      if (disposed) return;
      disposed = true; move = undefined; keys.clear(); drag = undefined;
      void request("input-reset").catch(() => {});
      listeners.forEach(remove => remove());
      keyboard = undefined;
    },
  };
}
