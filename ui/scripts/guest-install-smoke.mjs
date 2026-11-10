import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";

for (const [id, name, platform] of [["macos", "macOS", "darwin"], ["windows-11", "Windows 11", "windows"]]) {
  let submitted;
  const { page, browser } = await createWorkbench({
    clientContext: { mode: "local", connection_id: "local", client_platform: platform, server_platform: platform },
    nativeRequest: ({ action }) => action === "chooseGuestMedia" ? "C:\\media\\Windows11.iso" : null,
    async beforeNavigate(page, state) {
      state.status.local_vm_storage = true;
      await page.route("**/api/environment/guest-systems", r => r.fulfill({ json: { fresh: true, available: true, system: id } }));
      await page.route("**/api/start", r => {
        submitted = r.request().postDataJSON();
        return r.fulfill({ json: { id: "native", running: true, kind: "guest-install" } });
      });
    },
  });
  try {
    const picker = page.getByRole("combobox", { name: "客户机系统" });
    const storage = page.getByRole("button", { name: "选择环境存储位置" });
    const storageBefore = await storage.boundingBox();
    await picker.click();
    const option = page.getByRole("option", { name, exact: true });
    await option.waitFor();
    const row = await option.boundingBox(), below = await storage.boundingBox();
    assert.ok(Math.abs(below.y - storageBefore.y) < 1 && row.height > 0);
    await picker.press("Escape");
    assert.equal((await storage.boundingBox()).y, storageBefore.y);
    await picker.press("Enter"); await picker.press("End"); await picker.press("Enter");
    const button = page.getByRole("button", { name: "安装客户机" });
    if (platform === "darwin") {
      assert.ok(await button.isEnabled());
      assert.equal(await page.getByLabel("客机账户密码",{exact:true}).count(),0);
    } else {
      assert.ok(await button.isDisabled());
      await page.getByRole("button", { name: "选择本机 ISO" }).click();
      await page.getByText("C:\\media\\Windows11.iso", { exact: true }).waitFor();
      await page.getByLabel("ISO 的 SHA-256").fill("a".repeat(64));
      assert.ok(await button.isDisabled());
      await page.getByRole("checkbox").check();
    }
    await Promise.all([
      page.waitForResponse(response => response.url().endsWith("/api/start")),
      button.click(),
    ]);
    assert.equal(submitted.guest_system, id);
    if (platform === "windows") assert.ok(submitted.editor_terms && submitted.sha256.length === 64 && submitted.media.endsWith(".iso"));
    else assert.equal(submitted.password, undefined);
    console.log(platform, "inline picker, keyboard, validation and start request passed");
  } catch (error) {
    console.error((await page.locator("body").innerText()).slice(-2300));
    throw error;
  } finally { await browser.close(); }
}

{
  const calls=[];
  const {page,browser,errors}=await createWorkbench({
    nativeRequest: message=>{ calls.push(message.action); return null; },
    async beforeNavigate(page,state) {
      state.status.local_vm_storage=true;
      await page.route("**/api/environment/guest-systems",r=>r.fulfill({json:{fresh:false,available:true,system:"macos",pending:{system:"macos",id:"activation-fixture"},activate:true}}));
      await page.route("**/api/environment/guest-systems/activate",r=>{ calls.push("activate"); return r.fulfill({json:{stopping:true}}); });
    }
  });
  try {
    for(let i=0; !calls.includes("reconnect") && i<150;i++) await new Promise(resolve=>setTimeout(resolve,20));
    assert.equal(calls.filter(action=>action==="activate").length,1);
    assert.equal(calls.filter(action=>action==="reconnect").length,1);
    assert.deepEqual(errors,[]);
    console.log("installed guest automatically activated and reconnected");
  } finally { await browser.close(); }
}
