import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

const { browser, page, state, errors } = await createWorkbench();
await page.addInitScript(() => {
  window.EventSource = class extends EventTarget {
    constructor() {
      super();
      window.navigationEvents = this;
      queueMicrotask(() => this.onopen?.());
    }
    close() {}
  };
});
const back = page.getByRole("button", { name: "后退", exact: true });
const forward = page.getByRole("button", { name: "前进", exact: true });
const send = (snapshot) => page.evaluate(snapshot => {
  window.navigationEvents.onmessage({ data: JSON.stringify(snapshot) });
}, { tabs: state.tabs, active: state.active, ...snapshot });
async function navigate(button, path, id) {
  const request = page.waitForRequest(`**/api/browser/${path}`);
  await button.click();
  assert.deepEqual((await request).postDataJSON(), { id });
}
try {
  state.status.ready = state.status.vm_running = true;
  state.tabs = [
    { id: "first", title: "First tab", kind: "web", url: "https://example.test/first" },
    { id: "second", title: "Second tab", kind: "web", url: "https://example.test/second" },
  ];
  state.active = "first";
  await page.reload();
  await page.getByRole("button", { name: "打开浏览器", exact: true }).click();
  await send({ back: false, forward: false });
  assert.equal(await back.isDisabled(), true);
  assert.equal(await forward.isDisabled(), true);
  const reads = () => state.requests.filter(item => item.path === "/browser/tabs").length;
  const before = reads();

  await send({ back: true, forward: false });
  await navigate(back, "back", "first");
  await send({ back: false, forward: true });
  await navigate(forward, "forward", "first");

  // While a new tab is activating, the previous tab's history cannot enable its buttons.
  await send({ back: true, forward: true });
  await page.route("**/api/browser/activate", async route => {
    await new Promise(resolve => setTimeout(resolve, 150));
    state.active = route.request().postDataJSON().id;
    await route.fulfill({ json: { ok: true } });
  });
  await page.getByRole("tab", { name: "Second tab", exact: true }).click();
  assert.equal(await back.isDisabled(), true);
  assert.equal(await forward.isDisabled(), true);
  await send({ active: "second", back: false, forward: false });
  assert.equal(await back.isDisabled(), true);
  await send({ active: "second", back: true, forward: false });
  await Promise.all([
    page.waitForResponse("**/api/status"),
    page.waitForResponse("**/api/terminals"),
  ]);
  await navigate(back, "back", "second");
  assert.equal(reads(), before, "live history updates must work without REST polling");
  assert.deepEqual(errors, []);
  console.log("PASS: back/forward state via events, correct action targets, no stale history on tab switch and no REST polling");
} finally {
  await browser.close();
}
