import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const failure = "联网检查未通过：浏览器语言未通过。语言与已保存的设置不一致。请核对语言设置后重新检测。外网保持关闭。";
const output = process.env.CAGE_SAFETY_SCREENSHOTS || "/tmp/cage-safety-checklist";
await mkdir(output, { recursive: true });
const platforms = process.env.CAGE_TEST_BROWSER === "webkit" ? ["darwin"] : ["darwin", "windows", "linux"];

for (const platform of platforms) {
  const { browser, page, state, errors } = await createWorkbench({
    clientContext: { mode: "local", connection_id: "local", client_platform: platform, server_platform: platform, connection: { state: "disconnected" } },
    // Session history is unrelated to this check. Resolve its poll immediately
    // so WebKit does not report a cancelled poll when this test reloads pages.
    beforeNavigate: page => page.addInitScript(() => {
      const fetch = window.fetch;
      window.fetch = (input, ...args) => String(input) === "/api/cli/sessions"
        ? Promise.resolve(new Response('{"sessions":[]}', { headers: { "Content-Type": "application/json" } }))
        : fetch(input, ...args);
    }),
  });
  const checklist = page.getByRole("dialog", { name: "网络与隔离检查", exact: true });
  const link = parent => parent.getByRole("button", { name: "查看检查清单", exact: true });
  const showChecklist = async (button, keyboard = false) => {
    if (keyboard) { await button.focus(); await button.press("Enter"); }
    else await button.click();
    await checklist.waitFor();
    assert.equal(await page.getByRole("dialog").evaluateAll(nodes => nodes.at(-1).getAttribute("aria-label")), "网络与隔离检查");
    await checklist.getByRole("heading", { name: "关键检查", exact: true }).waitFor();
    await checklist.getByText("1 / 2 通过", { exact: true }).waitFor();
    const failed = checklist.locator("details").filter({ hasText: "浏览器语言" });
    await failed.locator("summary").click();
    await failed.getByText("语言与已保存的设置不一致", { exact: true }).waitFor();
    await failed.getByText("请核对语言设置后重新检测", { exact: true }).waitFor();
  };
  const closeChecklist = async () => {
    await page.keyboard.press("Escape");
    await checklist.waitFor({ state: "hidden" });
  };
  try {
    Object.assign(state.status, {
      ready: true, vm_running: true,
      job: { id: "failed-check", kind: "safety", running: false, error: failure, message: "环境检查未完成", log: "fixture check log" },
      safety: {
        gate: { mode: "closed", reason: failure }, error: failure,
        report: { created_at: new Date().toISOString(), checks: [
          { id: "dns", title: "加密 DNS", critical: true, state: "passed", evidence: "DNS 通过指定代理" },
          { id: "browser", title: "浏览器语言", critical: true, state: "failed", evidence: "语言与已保存的设置不一致", remedy: "请核对语言设置后重新检测" },
        ] },
      },
    });
    await page.reload();
    const homeError = page.getByRole("alert").filter({ hasText: failure });
    await link(homeError).waitFor();
    await page.screenshot({ path: `${output}/${platform}-failure.png` });
    const postCount = () => state.requests.filter(r => r.method === "POST").length;
    const beforeOpening = postCount();
    await showChecklist(link(homeError), true);
    await page.screenshot({ path: `${output}/${platform}-checklist.png` });
    await closeChecklist();
    assert.equal(postCount(), beforeOpening, "viewing a checklist must not start checks or open networking");
    assert.equal(await link(homeError).evaluate(node => node === document.activeElement), true);

    const feedback = page.locator(".status-feedback");
    state.status.job = { ...state.status.job, id: "failed-check-notification" };
    await link(feedback).waitFor();
    await showChecklist(link(feedback));
    await closeChecklist();

    await page.getByRole("button", { name: "查看操作详情", exact: true }).click();
    const details = page.getByRole("dialog", { name: "操作详情", exact: true });
    await showChecklist(link(details));
    await closeChecklist();
    assert.equal(await details.isVisible(), true, "closing the checklist must preserve operation details");
    await details.getByRole("button", { name: "关闭面板", exact: true }).click();

    state.status.job.setup_progress = { phase: "services", label: "环境服务" };
    await page.reload();
    const setup = page.getByRole("region", { name: "环境安装进度", exact: true });
    await showChecklist(link(setup));
    await closeChecklist();
    delete state.status.job.setup_progress;
    await page.reload();

    await page.route("**/api/settings/probe", route => route.fulfill({ status: 409, contentType: "text/plain; charset=utf-8", body: failure }));
    await page.getByRole("button", { name: "设置", exact: true }).click();
    await page.getByRole("navigation", { name: "设置分组" }).getByRole("button", { name: "网络与代理", exact: true }).click();
    const settings = page.getByRole("region", { name: "设置", exact: true });
    const dns = settings.getByLabel("加密 DNS", { exact: true });
    await dns.fill("https://example.com/dns-query");
    await settings.getByRole("button", { name: "验证出口", exact: true }).click();
    const settingsError = settings.getByRole("alert").filter({ hasText: failure });
    await showChecklist(link(settingsError), true);
    await closeChecklist();
    assert.equal(await dns.inputValue(), "https://example.com/dns-query", "viewing checks must preserve unsaved settings");
    assert.equal(await settings.isVisible(), true);
    await dns.fill(state.settings.dns_url);

    await page.route("**/api/proxy/route", route => route.fulfill({ status: 409, contentType: "text/plain; charset=utf-8", body: "关键检测证据不完整，外网保持关闭" }));
    await settings.getByRole("button", { name: "重新应用", exact: true }).click();
    const proxyError = settings.getByRole("alert").filter({ hasText: "关键检测证据不完整" });
    await showChecklist(link(proxyError));
    await closeChecklist();
    await settings.getByRole("button", { name: "返回工作台", exact: true }).first().click();

    await page.route("**/api/projects/open", route => route.fulfill({ status: 409, contentType: "text/plain; charset=utf-8", body: failure }));
    await page.getByRole("button", { name: "打开项目", exact: true }).click();
    const projects = page.getByRole("dialog", { name: "打开项目", exact: true });
    await projects.getByRole("button", { name: "打开项目", exact: true }).click();
    await showChecklist(link(projects));
    await closeChecklist();
    assert.equal(await projects.isVisible(), true);
    await projects.getByRole("button", { name: "取消", exact: true }).click();

    for (const viewport of [{ width: 820, height: 558 }, { width: 560, height: 812 }]) {
      await page.setViewportSize(viewport);
      if (viewport.width === 560) await page.getByRole("button", { name: "收起侧栏", exact: true }).click();
      await showChecklist(link(homeError));
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false);
      await closeChecklist();
      await page.screenshot({ path: `${output}/${platform}-${viewport.width}.png` });
    }

    state.status.job = { ...state.status.job, id: "unrelated-error", error: "项目文件夹连接失败" };
    await page.reload();
    const unrelated = page.getByRole("alert").filter({ hasText: "项目文件夹连接失败" });
    await unrelated.waitFor();
    assert.equal(await link(unrelated).count(), 0);
    assert.equal(state.status.safety.gate.mode, "closed");
    assert.deepEqual(errors, []);
    console.log(`PASS ${platform}: home, notification, details, setup, settings, proxy, project, keyboard, return focus, compact layout and no mutations`);
  } finally { await browser.close(); }
}
