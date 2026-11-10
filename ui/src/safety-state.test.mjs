import test from "node:test";
import assert from "node:assert/strict";
import {
  networkState,
  reportForSharing,
  isGuestLocalURL,
  canonicalExitPolicy,
  isSafetyCheckMessage,
} from "./safety-state.mjs";

test("check failures and incomplete evidence link to the checklist without tagging unrelated errors", () => {
  for (const message of [
    "关键检查尚未通过，外网保持关闭",
    "关键检测证据不完整，外网保持关闭",
    "Cage 外网未放行，请在工作台完成关键检查",
    "代理节点尚未应用或配置不一致，请重新应用节点；外网保持关闭",
    "浏览器配置检查未通过：语言不一致",
  ]) assert.equal(isSafetyCheckMessage(message), true, message);
  for (const message of [undefined, "", "关键检查已通过", "关键检查进行中", "项目文件夹连接失败", "Git 设置已写入，但检查尚未通过。", "资源限制未通过核验"])
    assert.equal(isSafetyCheckMessage(message), false, message);
});

test("an unchanged exit policy does not restart the VM because of empty optional fields", () => {
  assert.deepEqual(
    canonicalExitPolicy({
      mode: "dynamic",
      scope: "country",
      country_code: "US",
    }),
    canonicalExitPolicy({
      mode: "dynamic",
      scope: "country",
      country_code: "US",
      ip: "",
      region: "",
      city: "",
    }),
  );
});

test("only a live gate lease displays networking as allowed", () => {
  const now = Date.now();
  const status = {
    vm_running: true,
    safety: {
      gate: { mode: "open", until: new Date(now + 1000).toISOString() },
      report: { checks: [{ state: "passed" }] },
    },
  };
  assert.equal(networkState(status, now).mode, "open");
  assert.equal(networkState(status, now + 2000).mode, "closed");
  status.safety.gate.mode = "closed";
  assert.equal(networkState(status, now).mode, "closed");
  status.safety.gate = null;
  assert.equal(networkState(status, now).mode, "unknown");
  status.vm_running = false;
  assert.equal(networkState(status, now).mode, "stopped");
});

test("shared reports omit identifiers and secret-derived configuration hashes", () => {
  const report = {
    policy: "v1",
    created_at: "today",
    checks: [],
    run_id: "private-run",
    check_id: "private-check",
    configuration: "secret-derived-hash",
    ip: "private-ip",
  };
  const text = JSON.stringify(reportForSharing(report));
  assert.ok(!text.includes("private") && !text.includes("secret-derived"));
});

test("offline browser previews allow guest loopback only", () => {
  assert.equal(isGuestLocalURL("http://localhost:3000"), true);
  assert.equal(isGuestLocalURL("http://127.0.0.1:18080"), true);
  assert.equal(isGuestLocalURL("http://[::1]:3000"), true);
  assert.equal(isGuestLocalURL("about:blank"), true);
  assert.equal(isGuestLocalURL("https://localhost.attacker.example"), false);
  assert.equal(isGuestLocalURL("http://192.168.5.2:17777"), false);
  assert.equal(isGuestLocalURL("https://example.com"), false);
});
