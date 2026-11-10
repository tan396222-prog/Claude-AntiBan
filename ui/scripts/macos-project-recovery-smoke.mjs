import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { createWorkbench } from "./workbench-fixture.mjs";

const project = "/Users/fixture/Cage Projects/macOS 示例";
const calls = [];
let loggedIn = false, maintenanceClosed = false, geometry = "maintenance-frame-a", images;
const { page, browser, state, errors } = await createWorkbench({
  viewport: { width: 1280, height: 900 },
  async beforeNavigate(page, state) {
    state.status.vm_running = state.status.ready = true;
    state.account.guest_system = state.status.guest_system = "macos";
    state.status.projects = [project];
    state.status.workspace = state.settings.workspace = project;
    state.status.project_error = "部分项目文件夹尚未连接，环境和浏览器可继续使用。";
    state.status.project_errors = [{ path: project, recovery: "guest-login", message: "请先登录虚拟 Mac 的客机账户。登录完成后，Cage 会自动连接项目。" }];
    images = await page.evaluate(() => {
      const make = (width, height, color) => {
        const canvas = document.createElement("canvas");
        canvas.width = width; canvas.height = height;
        const context = canvas.getContext("2d");
        context.fillStyle = color; context.fillRect(0, 0, width, height);
        context.fillStyle = "#fff"; context.font = "22px sans-serif";
        context.fillText("Synthetic Apple authorization window", 20, 45);
        return canvas.toDataURL("image/jpeg").split(",")[1];
      };
      return { login: make(1920, 1200, "#485570"), a: make(600, 400, "#334252"), b: make(480, 360, "#465835") };
    });
    await page.route("**/api/macos/login", route => {
      const request = route.request().postDataJSON();
      calls.push({ mode: "login", ...request });
      if (request.action === "open") return route.fulfill({ json: { session: "login-fixture" } });
      if (request.action === "close") return route.fulfill({ json: { closed: true } });
      if (request.action === "frame") {
        if (loggedIn) {
          state.status.project_errors = []; state.status.project_error = "";
          return route.fulfill({ json: { closed: true } });
        }
        return route.fulfill({ json: { image: images.login, mime: "image/jpeg" } });
      }
      return route.fulfill({ json: {} });
    });
    await page.route("**/api/projects/open", route => {
      if (!loggedIn) return route.fulfill({ status: 400, contentType: "text/plain; charset=utf-8", headers: { "X-Cage-Recovery": "guest-login" }, body: "请先登录虚拟 Mac 的客机账户。" });
      return route.fulfill({ json: { workspace: project } });
    });
    await page.route("**/api/macos/maintenance", route => {
      const request = route.request().postDataJSON();
      calls.push({ mode: "maintenance", ...request });
      assert(["projects", "screen", "input"].includes(request.maintenance));
      assert.equal(request.initial_setup, undefined);
      assert.equal(request.password, undefined);
      if (request.action === "open") return route.fulfill({ json: { session: "maintenance-fixture", maintenance_version: 1, administrator: "cageprovision" } });
      if (request.action === "close") return route.fulfill({ json: { closed: true, maintenance_version: 1 } });
      if (request.action === "frame") return route.fulfill({ json: maintenanceClosed
        ? { closed: true, maintenance_version: 1, error: "客机前台应用已变化" }
        : { image: geometry.endsWith("a") ? images.a : images.b, mime: "image/jpeg", geometry,
          region: geometry.endsWith("a") ? [120, 90, 600, 400] : [240, 150, 480, 360], maintenance_version: 1 } });
      return route.fulfill({ json: { maintenance_version: 1 } });
    });
  },
});
page.setDefaultTimeout(12000);
async function until(predicate) {
  for (let attempt = 0; attempt < 150; attempt++) {
    if (predicate()) return;
    await page.waitForTimeout(20);
  }
  assert(predicate(), "expected recovery operation did not arrive");
}
try {
  const failures = page.locator(".project-connection-errors");
  await failures.getByRole("button", { name: "登录客机账户", exact: true }).click();
  const login = page.getByRole("dialog", { name: "登录虚拟 Mac", exact: true });
  await login.getByAltText("Apple 客机登录屏幕").waitFor();
  await login.getByLabel(/^客机登录键盘/).press("Enter");
  await until(() => calls.some(call => call.mode === "login" && call.action === "key" && call.code === 36));
  assert(await login.isVisible(), "Return closed the view before macOS reported a completed login");
  loggedIn = true;
  await login.waitFor({ state: "detached" });
  await failures.waitFor({ state: "detached" });

  state.status.project_error = "部分项目文件夹尚未连接，环境和浏览器可继续使用。";
  state.status.project_errors = [{ path: project, recovery: "filesystem-permission", message: "虚拟 Mac 的文件系统扩展尚未启用。" }];
  await page.reload();
  await failures.getByRole("button", { name: "打开客机授权设置", exact: true }).click();
  const maintenance = page.getByRole("dialog", { name: "恢复客机系统权限", exact: true });
  const image = maintenance.getByAltText("客机系统设置或授权窗口");
  await page.waitForFunction(() => document.querySelector('[data-mode="maintenance"] img')?.naturalWidth === 600);
  const first = await image.boundingBox();
  await image.click({ position: { x: first.width / 2, y: first.height / 2 } });
  await until(() => calls.some(call => call.mode === "maintenance" && call.action === "click"));
  const click = calls.find(call => call.mode === "maintenance" && call.action === "click");
  assert.equal(click.geometry, "maintenance-frame-a");
  assert(Math.abs(click.x - 420) <= 1 && Math.abs(click.y - 290) <= 1);
  await maintenance.getByLabel(/^客机登录键盘/).press("Meta+a");
  await maintenance.getByRole("button", { name: "填写管理员密码", exact: true }).click();
  await until(() => calls.some(call => call.action === "select-all") && calls.some(call => call.action === "fill-password"));
  assert(calls.filter(call => call.mode === "maintenance" && ["select-all", "fill-password"].includes(call.action))
    .every(call => call.geometry === "maintenance-frame-a" && call.session === "maintenance-fixture"));
  assert(!calls.some(call => call.mode === "maintenance" && call.action === "key" && call.code === 36), "password fill submitted authorization without the user");
  await page.screenshot({ path: fileURLToPath(new URL("../../docs/test-reports/2026-11-09-architecture-fixes/evidence/mac-maintenance-ui.png", import.meta.url)) });

  geometry = "maintenance-frame-b";
  await page.waitForFunction(() => document.querySelector('[data-mode="maintenance"] img')?.naturalWidth === 480);
  const next = await image.boundingBox();
  await image.click({ position: { x: next.width / 2, y: next.height / 2 } });
  await until(() => calls.some(call => call.action === "click" && call.geometry === geometry));
  const moved = calls.find(call => call.action === "click" && call.geometry === geometry);
  assert(Math.abs(moved.x - 480) <= 1 && Math.abs(moved.y - 330) <= 1);
  maintenanceClosed = true;
  await maintenance.waitFor({ state: "detached" });
  assert.equal(await page.locator('[data-mode="maintenance"] img').count(), 0);

  maintenanceClosed = false;
  state.status.project_error = "";
  state.status.project_errors = [];
  await page.reload();
  await page.getByRole("button", { name: "设置", exact: true }).click();
  await page.getByRole("navigation", { name: "设置分组" }).getByRole("button", { name: "环境与恢复", exact: true }).click();
  const permissions = page.getByRole("region", { name: "客机系统权限", exact: true });
  for (const [label, purpose] of [["文件共享权限", "projects"], ["屏幕录制权限", "screen"], ["辅助功能权限", "input"]]) {
    const start = calls.length;
    await permissions.getByRole("button", { name: label, exact: true }).click();
    await image.waitFor();
    assert(calls.slice(start).some(call => call.action === "open" && call.maintenance === purpose));
    await maintenance.getByRole("button", { name: "返回工作台", exact: true }).click();
    await maintenance.waitFor({ state: "detached" });
    await until(() => calls.slice(start).some(call => call.action === "close" && call.maintenance === purpose));
  }
  assert.deepEqual(errors, []);
  console.log(JSON.stringify({ result: "PASS", browser: browser.version(), checks: ["actionable project login", "Return waits for macOS", "restored project removes warning", "separate maintenance purpose", "cropped window input coordinates", "published geometry binding", "explicit administrator fill intent without password data or submission", "geometry change rebinds crop", "foreground change removes maintenance image", "three permission recovery entries remain available without a project error"] }));
} finally { await browser.close(); }
