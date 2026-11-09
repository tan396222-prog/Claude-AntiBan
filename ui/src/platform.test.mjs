import test from "node:test";
import assert from "node:assert/strict";
import { primaryModifier, terminalModifier, shortcut } from "./platform.mjs";

test("Linux defaults to Ctrl and macOS keeps Command", async () => {
  const descriptor = Object.getOwnPropertyDescriptor(globalThis, "navigator");
  try {
    for (const platform of ["Linux x86_64", "Win32", "MacIntel"]) {
      Object.defineProperty(globalThis, "navigator", { configurable: true, value: { platform } });
      const api = await import(`./platform.mjs?platform=${encodeURIComponent(platform)}`);
      const control = platform !== "MacIntel";
      assert.equal(api.usesControl, control);
      assert.equal(api.isWindows, platform === "Win32");
      assert.equal(api.shortcut("⌘O"), control ? "Ctrl+O" : "⌘O");
      const event = { ctrlKey: true, metaKey: false, altKey: false, shiftKey: false };
      assert.equal(api.primaryModifier(event), control);
      assert.equal(api.terminalModifier(event), false);
      assert.equal(api.terminalModifier({ ...event, shiftKey: true }), control);
    }
  } finally {
    if (descriptor) Object.defineProperty(globalThis, "navigator", descriptor);
    else delete globalThis.navigator;
  }
});

test("Windows shortcuts reserve Ctrl+C for terminal interruption and ignore AltGr", () => {
  const ctrl = { ctrlKey: true, altKey: false, metaKey: false, shiftKey: false };
  assert.equal(primaryModifier(ctrl, true), true);
  assert.equal(terminalModifier(ctrl, true), false);
  assert.equal(terminalModifier({ ...ctrl, shiftKey: true }, true), true);
  assert.equal(primaryModifier({ ...ctrl, altKey: true }, true), false);
  assert.equal(primaryModifier({ ...ctrl, metaKey: true }, false), true);
  assert.equal(shortcut("⌘⇧↵", true), "Ctrl+Shift+Enter");
  assert.equal(shortcut("⌘W", false), "⌘W");
});
