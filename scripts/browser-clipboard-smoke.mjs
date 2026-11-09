// Uses Playwright's isolated Chromium headless shell and its process-local
// clipboard. Do not substitute a headed browser or a running user profile.
import assert from "node:assert/strict";
import http from "node:http";
import { createRequire } from "node:module";
import { randomUUID } from "node:crypto";
import { BrowserClipboard } from "../internal/cage/browser-clipboard.mjs";

const { chromium } = createRequire(import.meta.url)(
  process.env.CAGE_PLAYWRIGHT_MODULE || "playwright",
);
const site = http.createServer((request, response) => {
  response.setHeader("Content-Type", "text/html; charset=utf-8");
  response.end(`<!doctype html><button id="copy">Copy</button><script>
    const originalWrite = navigator.clipboard.writeText.bind(navigator.clipboard);
    window.action = () => navigator.clipboard.writeText("中文🙂\\nexact text");
    document.querySelector("button").onclick = () => {
      window.finished = false;
      Promise.resolve().then(() => action()).then(() => { finished = true; }, () => { finished = true; });
    };
  </script>`);
});
await new Promise((resolve) => site.listen(0, "127.0.0.1", resolve));
const origin = `http://127.0.0.1:${site.address().port}`;
const browser = await chromium.launch({ headless: true });
const context = await browser.newContext({
  permissions: ["clipboard-read", "clipboard-write"],
});
const copies = [];
const sessions = new Map();
const pageIds = new WeakMap();
const observer = new BrowserClipboard(
  (method, params, id) => sessions.get(id).send(method, params),
  (copy) => copies.push(copy),
);
async function attach(page, id) {
  pageIds.set(page, id);
  const session = await context.newCDPSession(page);
  sessions.set(id, session);
  for (const method of [
    "Runtime.executionContextCreated",
    "Runtime.executionContextDestroyed",
    "Runtime.executionContextsCleared",
    "Runtime.bindingCalled",
    "Page.frameNavigated",
    "Page.frameDetached",
  ])
    session.on(method, (params) =>
      observer.event({ method, params, sessionId: id }),
    );
  await session.send("Page.enable");
  return observer.attach(id, id);
}
async function click(page, expected) {
  const count = copies.length;
  await observer.setContext(pageIds.get(page), randomUUID());
  await page.click("#copy");
  await page.waitForFunction(() => window.finished);
  await page.waitForTimeout(50);
  assert.deepEqual(
    copies.slice(count).map((copy) => copy.text),
    expected,
  );
}
try {
  const page = await context.newPage();
  await page.goto(origin);
  assert.deepEqual(await attach(page, "existing"), { skipped: [] });
  await observer.setContext("existing", randomUUID());
  await sessions.get("existing").send("Runtime.evaluate", {
    expression: 'navigator.clipboard.writeText("background write")',
    awaitPromise: true,
    userGesture: false,
  });
  await page.waitForTimeout(50);
  assert.deepEqual(copies, []);
  await click(page, ["中文🙂\nexact text"]);
  await page.reload();
  await click(page, ["中文🙂\nexact text"]);

  await page.evaluate(() => {
    window.action = () =>
      navigator.clipboard.write([
        new ClipboardItem({
          "text/plain": new Blob(["ClipboardItem🙂"], { type: "text/plain" }),
        }),
      ]);
  });
  await click(page, ["ClipboardItem🙂"]);
  await page.evaluate(() => {
    document.addEventListener("copy", (event) => {
      event.clipboardData.setData("text/plain", "first value");
      event.clipboardData.clearData("text/plain");
      event.clipboardData.setData("text", "execCommand 精确文本");
      event.preventDefault();
    });
    window.action = () => document.execCommand("copy");
  });
  await click(page, ["execCommand 精确文本"]);

  for (const mode of ["textarea", "input", "shadow", "password", "collapsed"]) {
    await page.reload();
    const previousClipboard = await page.evaluate(() =>
      navigator.clipboard.readText(),
    );
    await page.evaluate((mode) => {
      window.action = () => {
        const field = document.createElement(
          mode === "input" || mode === "password" ? "input" : "textarea",
        );
        if (mode === "password") field.type = "password";
        field.value = "默认复制🙂\n保留\u00a0空格";
        let parent = document.body;
        if (mode === "shadow") {
          const host = document.createElement("div");
          document.body.append(host);
          parent = host.attachShadow({ mode: "open" });
        }
        parent.append(field);
        field.select();
        if (mode === "collapsed") field.setSelectionRange(0, 0);
        document.execCommand("copy");
        field.remove();
      };
    }, mode);
    const expected =
      mode === "input"
        ? "默认复制🙂保留\u00a0空格"
        : "默认复制🙂\n保留\u00a0空格";
    const exported = mode !== "password" && mode !== "collapsed";
    await click(page, exported ? [expected] : []);
    assert.equal(
      await page.evaluate(() => navigator.clipboard.readText()),
      exported ? expected : previousClipboard,
      mode,
    );
  }

  await page.reload();
  await page.evaluate(() => {
    window.action = () => {
      const field = document.createElement("textarea");
      field.value = "original selection";
      const content = document.createElement("div");
      content.innerHTML = 'a<img alt="image alternative">b';
      document.body.append(field, content);
      field.select();
      document.addEventListener(
        "copy",
        () => getSelection().selectAllChildren(content),
        { once: true },
      );
      document.execCommand("copy");
      field.remove();
    };
  });
  await click(page, []);
  assert.equal(
    await page.evaluate(() => navigator.clipboard.readText()),
    "aimage alternativeb",
  );

  await page.reload();
  await page.evaluate(() => {
    window.action = () => {};
  });
  await click(page, []);
  await page.evaluate(() => {
    window.action = () =>
      document.dispatchEvent(
        new ClipboardEvent("copy", {
          clipboardData: new DataTransfer(),
          cancelable: true,
        }),
      );
  });
  await click(page, []);
  await page.evaluate(() => {
    window.action = () =>
      navigator.clipboard.write([
        new ClipboardItem({
          "text/plain": new Blob(["must not escape"], { type: "text/plain" }),
          "application/x-cage-invalid": new Blob(["unsupported"], {
            type: "application/x-cage-invalid",
          }),
        }),
      ]);
  });
  await click(page, []);

  // The observer must keep captured primitives when a site later patches them.
  await page.reload();
  await page.evaluate(() => {
    window.action = () =>
      navigator.clipboard.writeText("captured native success");
    Function.prototype.call = () => {
      throw new Error("site patched call");
    };
    Document.prototype.hasFocus = () => false;
    JSON.stringify = () => '{"text":"forged"}';
    Object.prototype.toJSON = () => ({ text: "forged" });
  });
  await click(page, ["captured native success"]);

  await page.reload();
  const beforeBlur = copies.length;
  await page.evaluate(() => {
    window.action = () =>
      navigator.clipboard.write([
        new ClipboardItem({
          "text/plain": new Promise((resolve) => {
            window.finishBlob = resolve;
          }),
        }),
      ]);
  });
  await observer.setContext("existing", randomUUID());
  await page.click("#copy");
  await page.waitForFunction(() => typeof window.finishBlob === "function");
  // Dispatching blur cancels authorization just as the native focus event does.
  // The workbench smoke separately exercises host focus and tab cancellation.
  await page.evaluate(() => {
    window.dispatchEvent(new Event("blur"));
    finishBlob(new Blob(["late clipboard"], { type: "text/plain" }));
  });
  await page.waitForFunction(() => window.finished);
  await page.waitForTimeout(50);
  assert.equal(copies.length, beforeBlur);

  await page.reload();
  await page.evaluate(() => {
    window.action = () =>
      navigator.clipboard.write([
        new ClipboardItem({
          "text/plain": new Promise((resolve) => {
            window.finishBlob = resolve;
          }),
        }),
      ]);
  });
  const contextA = randomUUID(),
    contextB = randomUUID();
  await observer.setContext("existing", contextA);
  const beforeContextChange = copies.length;
  await page.click("#copy");
  await page.waitForFunction(() => typeof window.finishBlob === "function");
  await observer.setContext("existing", contextB);
  await page.evaluate(() =>
    finishBlob(new Blob(["copy began in A"], { type: "text/plain" })),
  );
  await page.waitForFunction(() => window.finished);
  await page.waitForTimeout(50);
  assert.deepEqual(copies.slice(beforeContextChange), [
    { id: "existing", text: "copy began in A", context: contextA },
  ]);

  const modified = await context.newPage();
  await modified.goto(origin);
  await modified.evaluate(() => {
    Clipboard.prototype.writeText = () => Promise.resolve();
  });
  const result = await attach(modified, "modified");
  assert.match(result.skipped[0]?.reason, /writeText/);
  await click(modified, []);
  await modified.reload();
  await click(modified, ["中文🙂\nexact text"]);

  const poisoned = await context.newPage();
  await poisoned.goto(origin);
  await poisoned.evaluate(() => {
    const originalDefine = Object.defineProperty;
    const originalWrite = Clipboard.prototype.writeText;
    Clipboard.prototype.writeText = () => Promise.resolve();
    window.leakedReferenceTable = false;
    Object.defineProperty = (object, key, descriptor) => {
      if (object && Object.hasOwn(object, "apply")) {
        window.leakedReferenceTable = true;
        object.defineProperty = originalDefine;
        object.writeText = originalWrite;
        return object;
      }
      return originalDefine(object, key, descriptor);
    };
  });
  const poisonedResult = await attach(poisoned, "poisoned");
  assert.match(poisonedResult.skipped[0]?.reason, /defineProperty/);
  assert.equal(
    await poisoned.evaluate(() => window.leakedReferenceTable),
    false,
  );
  await click(poisoned, []);
  await poisoned.close();

  await modified.evaluate(() => {
    const frame = document.createElement("iframe");
    frame.src = "/frame";
    document.body.append(frame);
  });
  const frameCount = copies.length;
  const frameContext = randomUUID();
  await modified.frameLocator("iframe").locator("#copy").waitFor();
  await observer.setContext("modified", frameContext);
  await modified.frameLocator("iframe").locator("#copy").click();
  await modified.waitForTimeout(100);
  assert.deepEqual(copies.slice(frameCount), [
    { id: "modified", text: "中文🙂\nexact text", context: frameContext },
  ]);

  await modified.evaluate(() => {
    window.action = () => navigator.clipboard.writeText("x".repeat(65537));
  });
  await click(modified, []);
  console.log(
    "PASS: existing and new documents, exact writeText/write/execCommand content, rejected and late writes, synthetic events, captured native functions, modified existing APIs, same-process frames and size bound",
  );
} finally {
  await browser.close();
  await new Promise((resolve) => site.close(resolve));
}
