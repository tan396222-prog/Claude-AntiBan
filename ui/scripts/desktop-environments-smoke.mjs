// Real workbench UI with isolated, controllable SSH and local lifecycle responses.
// Use CAGE_UI_URL for a built workbench served on loopback.
import assert from "node:assert/strict";
import { mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";
import { createWorkbench } from "./workbench-fixture.mjs";

const reports = resolve(
  process.env.CAGE_REMOTE_QA_DIR || "ui/_output/desktop-environments",
);
await mkdir(reports, { recursive: true });
const host = {
  id: "fixture-host",
  name: "开发云服务器",
  host: "dev.example",
  user: "cage",
  port: 22,
  auth: { kind: "agent" },
};
const second = {
  ...host,
  id: "fixture-second",
  name: "第二台开发机",
  host: "test.example",
};
let generation = 0;
const view = {
  desktop_gateway: true,
  mode: "none",
  connection_id: "view-0",
  client_platform: "darwin",
  server_platform: "darwin",
  connection: { state: "disconnected" },
};
const local = {
  available: true,
  known: true,
  service_running: false,
  running: false,
  busy: false,
  live_terminals: 0,
  controller_id: "",
};
let failSSH = true,
  holdSSH = true,
  staleStop = false;
const actions = [];
function switchTo(mode, hostID) {
  const target = hostID === second.id ? second : host;
  Object.assign(view, {
    mode,
    connection_id: "view-" + ++generation,
    candidate: null,
    host_id: mode === "remote" ? target.id : "",
    name: mode === "remote" ? target.name : "",
    server_platform: mode === "remote" ? "linux" : "darwin",
    instance_id: mode === "remote" ? "1".repeat(64) : "",
    connection: { state: mode === "none" ? "disconnected" : "connected" },
  });
}
function stopLocal() {
  Object.assign(local, {
    service_running: false,
    running: false,
    live_terminals: 0,
    controller_id: "",
  });
}
const { browser, page, state, errors } = await createWorkbench({
  clientContext: view,
  clientRequest: async (route, path, body) => {
    const reply = (value) => route.fulfill({ json: value });
    if (path === "/client/hosts") return reply({ hosts: [host, second] });
    if (path === "/client/local-status") return reply(local);
    if (path === "/client/context") {
      if (!holdSSH && view.candidate?.connection.state === "connecting") {
        Object.assign(
          view.candidate.connection,
          failSSH
            ? { state: "failed", message: "无法连接云端服务" }
            : { state: "connected" },
        );
      }
      return reply(view);
    }
    assert.equal(body.connection_id, view.connection_id, "stale client intent");
    actions.push([path, body]);
    if (path === "/client/candidate") {
      view.candidate = {
        id: "candidate-" + ++generation,
        mode: body.mode,
        connection: {
          host_id: body.host_id,
          state: body.mode === "local" ? "connected" : "connecting",
        },
      };
      return reply(view.candidate);
    }
    if (path === "/client/cancel") {
      assert.equal(body.candidate_id, view.candidate.id);
      view.candidate = null;
      return reply(view);
    }
    if (path === "/client/commit") {
      assert.equal(body.candidate_id, view.candidate.id);
      assert.equal(view.candidate.connection.state, "connected");
      if (body.local_action === "stop") {
        assert.equal(body.local_controller_id, local.controller_id);
        if (staleStop)
          return route.fulfill({ status: 423, body: "请切回本机解锁" });
        stopLocal();
      }
      switchTo(view.candidate.mode, view.candidate.connection.host_id);
      return reply(view);
    }
    if (path === "/client/local-stop") {
      assert.equal(body.local_controller_id, local.controller_id);
      if (staleStop)
        return route.fulfill({ status: 423, body: "请切回本机解锁" });
      stopLocal();
      return reply(view);
    }
    if (path === "/client/disconnect") {
      switchTo("none");
      return reply(view);
    }
    throw new Error("Unexpected client API " + path);
  },
});
page.setDefaultTimeout(7000);
const click = (name) => page.getByRole("button", { name, exact: true }).click();
const visible = (name) =>
  page.getByText(name, { exact: true }).first().waitFor();
try {
  await page.getByRole("heading", { name: "先启动 Cage 环境" }).waitFor();
  assert.equal(view.mode, "local");
  assert.equal(state.requests.some(r => r.path === "/start"), false);
  await click("SSH 主机");
  // The working surface survives opening the sidebar and a failed candidate.
  await page.getByRole("heading", { name: "先启动 Cage 环境" }).waitFor();
  await click("返回工作台侧栏");
  await page.getByRole("navigation", { name: "主导航" }).waitFor();
  await click("SSH 主机");
  await click("连接此主机");
  await page.getByText("正在验证…", { exact: true }).waitFor();
  assert.equal(await page.getByRole("dialog").count(), 0);
  holdSSH = false;
  await visible("无法连接云端服务");
  assert.equal(view.mode, "local");
  failSSH = false;
  Object.assign(local, {
    service_running: true,
    running: true,
    live_terminals: 2,
    controller_id: "local-identity",
  });
  await click("连接此主机");
  const decision = page.getByRole("dialog", { name: "切换到 SSH 主机" });
  await decision.waitFor();
  await page.keyboard.press("Escape");
  await decision.waitFor({ state: "hidden" });
  assert.equal(view.mode, "local");
  assert.equal(local.running, true);
  await click("连接此主机");
  await decision.waitFor();
  await page.screenshot({ path: join(reports, "ssh-switch-confirmation.png") });
  await click("保留本机运行");
  await visible("本机也在运行");
  await visible("SSH · 云端运行");
  assert.equal(view.mode, "remote");
  assert.equal(local.running, true);
  await click("停止本机环境");
  staleStop = true;
  await click("确认停止本机");
  await visible("本机环境已锁定。请先返回本机解锁，再停止本机环境。");
  assert.equal(view.mode, "remote");
  assert.equal(state.account.unlocked, true);
  await click("返回本机解锁");
  await page.locator(".client-toolbar").waitFor({state: "hidden"});
  await page.getByRole("navigation", {name: "主导航"}).waitFor();
  assert.equal(view.mode, "local");
  staleStop = false;
  await click("SSH 主机");
  await click("连接此主机");
  await decision.waitFor();
  await click("保留本机运行");
  await visible("SSH · 云端运行");
  await click("SSH 主机");
  await page.screenshot({ path: join(reports, "ssh-sidebar-background.png") });
  await page.getByRole("button", { name: /第二台开发机/ }).click();
  await click("连接此主机");
  await decision.waitFor();
  assert.equal(view.host_id, host.id, "A remains selected before B's commit");
  staleStop = true;
  await click("停止本机并连接");
  await decision.getByRole("button", { name: "返回本机解锁" }).waitFor();
  assert.equal(view.host_id, host.id);
  const cancelled = view.candidate.id;
  await click("返回本机解锁");
  await page.locator(".client-toolbar").waitFor({state: "hidden"});
  await page.getByRole("navigation", {name: "主导航"}).waitFor();
  assert.ok(
    actions.some(
      ([path, body]) =>
        path === "/client/cancel" && body.candidate_id === cancelled,
    ),
  );
  staleStop = false;
  await click("SSH 主机");
  await click("连接此主机");
  await decision.waitFor();
  await click("保留本机运行");
  await visible("SSH · 云端运行");
  await click("SSH 主机");
  await page.getByRole("button", { name: /第二台开发机/ }).click();
  await click("连接此主机");
  await decision.waitFor();
  await click("停止本机并连接");
  await visible("SSH · 云端运行");
  assert.equal(view.host_id, second.id);
  assert.equal(local.running, false);
  Object.assign(local, {
    running: true,
    live_terminals: 1,
    controller_id: "new-local-identity",
  });
  await click("停止本机环境");
  await click("确认停止本机");
  await visible("SSH · 云端运行");
  assert.equal(view.mode, "remote");
  assert.equal(local.running, false);
  await click("SSH 主机");
  await click("断开连接");
  await page.getByRole("complementary", { name: "SSH 主机" }).waitFor();
  assert.equal(view.mode, "none");
  assert.equal(
    actions.some(
      ([path, body]) =>
        path === "/client/local-stop" &&
        body.local_controller_id === "new-local-identity",
    ),
    true,
  );
  assert.deepEqual(errors, []);
  console.log(
    "PASS: on-demand workbench, SSH sidebar round trip, candidate failure, cancel/keep/stop, locked local stop, confirmed background stop and disconnect.",
    reports,
  );
} catch (error) {
  await page.screenshot({ path: join(reports, "failure.png") }).catch(() => {});
  throw error;
} finally {
  await browser.close();
}
