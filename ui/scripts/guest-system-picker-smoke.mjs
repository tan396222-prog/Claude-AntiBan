import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

for (const [platform, id] of [["darwin","macos"], ["windows","windows-11"], ["linux",null]]) {
  for (const fresh of [true, false]) {
    const fixture = await createWorkbench({
      clientContext: {mode:"local", connection_id:"local", client_platform:platform, server_platform:platform},
      async beforeNavigate(page, state) {
        state.status.local_vm_storage = true;
        await page.route("**/api/environment/guest-systems", route => route.fulfill({
          json:{fresh, available:false, system:fresh ? id : null, reason:"此构建缺少原生客户机安装组件"},
        }));
      },
    });
    const { browser, page, errors } = fixture;
    try {
      const region = page.locator(".guest-system-picker");
      if (fresh) {
        await region.waitFor();
        if (id) {
          const picker = region.getByRole("combobox");
          const storage = page.getByRole("button",{name:"选择环境存储位置"});
          const initialY = (await storage.boundingBox()).y;
          await picker.click();
          const option = page.getByRole("option").last();
          assert.equal(await option.getAttribute("aria-disabled"),"true");
          const position = await option.boundingBox();
          assert.ok(Math.abs((await storage.boundingBox()).y - initialY) < 1 && position.height > 0);
          await picker.press("Escape");
          assert.ok(Math.abs((await storage.boundingBox()).y - initialY) <= 1, "collapsed picker restores the following control");
        }
      } else await region.waitFor({state:"hidden"});
      const submitted = page.waitForRequest(req => req.url().endsWith("/api/start") && req.method()==="POST");
      await page.getByRole("button",{name:"启动 Cage 环境",exact:true}).click();
      assert.deepEqual((await submitted).postDataJSON(), fresh ? {guest_system:"debian-13"} : {});
      assert.deepEqual(errors,[]);
    } finally { await browser.close(); }
  }
}
console.log("Debian fallback and existing environments verified on all three host types.");
