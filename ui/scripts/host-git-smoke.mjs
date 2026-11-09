import assert from "node:assert/strict";
import fs from "node:fs/promises";
import { createWorkbench } from "./workbench-fixture.mjs";

const { browser, page, state, errors } = await createWorkbench();
const missing = {
  state: "unset",
  ready: false,
  can_enable: true,
  message: "启动前需要启用本机 Git 安全保护。",
};
const starts = () => state.requests.filter((r) => r.path === "/start").length;
const writes = () =>
  state.requests.filter((r) => r.path === "/host/git" && r.method === "POST")
    .length;
try {
  state.hostGit = { ...missing };
  await page
    .getByRole("button", { name: "启动 Cage 环境", exact: true })
    .click();
  await page
    .getByRole("button", { name: "启用保护并继续", exact: true })
    .waitFor();
  assert.equal(starts(), 0);
  assert.equal(writes(), 0);
  await page.getByRole("button", { name: "暂不启动", exact: true }).click();
  await page.getByRole("dialog").waitFor({ state: "hidden" });
  assert.equal(starts(), 0);
  await page
    .getByRole("button", { name: "启动 Cage 环境", exact: true })
    .click();
  state.failHostGit = true;
  await page
    .getByRole("button", { name: "启用保护并继续", exact: true })
    .click();
  await page
    .getByRole("alert")
    .filter({ hasText: "未能保存 Git 安全设置" })
    .waitFor();
  assert.equal(starts(), 0);
  assert.equal(writes(), 1);
  state.failHostGit = false;
  await page
    .getByRole("button", { name: "启用保护并继续", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Cage 环境已就绪", exact: true })
    .waitFor();
  assert.equal(starts(), 1);
  assert.equal(writes(), 2);
  state.status.ready = false;
  state.status.vm_running = false;
  await page.reload();
  await page
    .getByRole("button", { name: "启动 Cage 环境", exact: true })
    .click();
  await page
    .getByRole("heading", { name: "Cage 环境已就绪", exact: true })
    .waitFor();
  assert.equal(starts(), 2);
  assert.equal(writes(), 2);
  state.status.ready = false;
  state.status.vm_running = false;
  state.hostGit = {
    state: "unavailable",
    ready: false,
    can_enable: false,
    message: "请检查 Git 安装后重试；不会自动打开安装器。",
  };
  await page.reload();
  await page
    .getByRole("button", { name: "启动 Cage 环境", exact: true })
    .click();
  await page.getByText(state.hostGit.message, { exact: true }).waitFor();
  assert.equal(
    await page
      .getByRole("button", { name: "启用保护并继续", exact: true })
      .count(),
    0,
  );
  state.hostGit = { ...missing };
  await page.getByRole("button", { name: "重新检查", exact: true }).click();
  await page
    .getByRole("button", { name: "启用保护并继续", exact: true })
    .waitFor();
  await fs.mkdir("_output/onboarding-qa", { recursive: true });
  await page.screenshot({
    path: "_output/onboarding-qa/git-protection.png",
    animations: "disabled",
  });
  await page.setViewportSize({ width: 820, height: 580 });
  await page.screenshot({
    path: "_output/onboarding-qa/git-protection-small.png",
    animations: "disabled",
  });
  assert.deepEqual(errors, []);
  console.log(
    "PASS: no write/start before consent; cancellation; failed write stays blocked; explicit enable starts once; reload rechecks without rewriting; unavailable Git retries safely.",
  );
} finally {
  await browser.close();
}
