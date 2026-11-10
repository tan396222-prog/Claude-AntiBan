import assert from "node:assert/strict";
import { createWorkbench } from "./workbench-fixture.mjs";
for (const [platform, system, version, edition] of [
  ["darwin", "macos", "26.5", ""],
  ["windows", "windows-11", "25H2", "Professional"],
  ["linux", "ubuntu-22.04", "22.04", ""],
  ["linux", "ubuntu-24.04", "24.04", ""],
]) {
  let planRequests = 0;
  const fixture = await createWorkbench({
    clientContext: {
      mode: "local",
      connection_id: "local",
      client_platform: platform,
      server_platform: platform,
    },
    beforeNavigate: async (page, state) => {
      state.status.local_vm_storage = true;
      await page.route("**/api/environment/guest-systems", (route) =>
        route.fulfill({ json: { fresh: true, available: true, system } }),
      );
      await page.route("**/api/environment/guest-systems/clone", (route) => {
        planRequests++;
        return route.fulfill({
          json: {
            key: "source-key",
            enough_space: true,
            minimum_free_gib: 64,
            free_gib: 100,
            download_bytes: 5000000000,
            plan: {
              host: {
                system,
                version,
                build: "host-build",
                edition,
                language: "zh-CN",
              },
              version,
              build: "image-build",
              format: system === "macos" ? "ipsw" : "esd",
            },
          },
        });
      });
    },
  });
  try {
    const { page } = fixture;
    const region = page.locator(".guest-system-picker");
    const picker = region.getByRole("combobox");
    await picker.waitFor();
    const initialHeight = (await region.boundingBox()).height;
    await picker.focus();
    await picker.press("ArrowDown");
    const option = page.getByRole("option", {
      name: "自动匹配本机系统版本",
      exact: true,
    });
    await option.waitFor();
    assert.ok(
      Math.abs((await region.boundingBox()).height - initialHeight) < 1,
      "floating options must not change layout height",
    );
    await picker.press("Home");
    await picker.press("Enter");
    assert.equal(await picker.getAttribute("aria-expanded"), "false");
    await region
      .getByText("本机构建号 host-build · 镜像构建号 image-build")
      .waitFor();
    if (system === "windows-11") await region.getByRole("checkbox").check();
    const submit = page.waitForRequest(
      (request) =>
        request.url().endsWith("/api/start") && request.method() === "POST",
    );
    await page
      .getByRole("button", {
        name: "安装客户机",
        exact: true,
      })
      .click();
    const body = (await submit).postDataJSON();
    assert.equal(body.clone_key, "source-key");
    assert.equal(body.guest_system, system);
    assert.equal(planRequests, 1);
    assert.deepEqual(fixture.errors, []);
    console.log(platform, "host clone selection and submission passed");
  } finally {
    await fixture.browser.close();
  }
}
