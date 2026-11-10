import assert from "node:assert/strict";
import { mkdir, writeFile } from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const output = process.env.CAGE_ERROR_QA_OUTPUT || "/tmp/cage-readable-errors";
await mkdir(output, { recursive: true });
for (const platform of ["darwin", "windows", "linux"]) {
  const fixture = await createWorkbench({
    clientContext: { mode: "local", connection_id: "local", client_platform: platform, server_platform: platform, connection: { state: "disconnected" } },
  });
  const { page, browser, state, errors } = fixture;
  try {
    state.status.job = { id: "readable-failure", kind: "start", running: false, message: "启动环境失败", error: "下载 Linux 镜像失败：等待下载服务器响应超时。请检查所选节点，再继续安装。", log: "fixture download timeout" };
    await page.reload();
    await page.getByText("启动环境失败", { exact: true }).first().waitFor();
    await page.getByRole("alert").filter({ hasText: "等待下载服务器响应超时" }).first().waitFor();
    await page.getByRole("button", { name: "查看操作详情", exact: true }).first().click();
    await page.getByRole("dialog", { name: "操作详情", exact: true }).getByText("fixture download timeout", { exact: true }).waitFor();
    await page.keyboard.press("Escape");
    await page.screenshot({ path: `${output}/${platform}-startup.png` });

    await page.route("**/api/environment/resources", route => route.fulfill({ status: 503, contentType: "text/html", body: "<html>upstream failed token=private-test-secret</html>" }));
    const failure = await page.evaluate(async () => {
      const { api } = await import("/src/api.js");
      try { await api("/environment/resources"); return "unexpected success"; }
      catch (error) { return { message: error.message, status: error.status }; }
    });
    assert.match(failure.message, /资源配置.*HTTP 503.*尚未就绪/);
    assert.equal(failure.status, 503);
    assert.ok(!failure.message.includes("private-test-secret") && !failure.message.includes("<html>"));
    await page.route("**/api/environment/resources", route => route.fulfill({ status: 200, contentType: "application/json", body: "not-json token=private-test-secret" }));
    const malformed = await page.evaluate(async () => {
      const { api } = await import("/src/api.js");
      try { await api("/environment/resources"); } catch (error) { return error.message; }
    });
    assert.match(malformed, /资源配置.*格式无效/);
    assert.ok(!malformed.includes("private-test-secret"));
    assert.deepEqual(errors, []);
    await writeFile(`${output}/${platform}.json`, JSON.stringify({ platform, failure, malformed }, null, 2));
    console.log(`PASS: ${platform} readable failures`);
  } finally { await browser.close(); }
}
