import assert from "node:assert/strict";
import test from "node:test";
import { needsProxySetup } from "./onboarding-state.mjs";

test("only an applied Cage node completes setup", () => {
  assert.equal(needsProxySetup({}, {}, {}), true);
  assert.equal(
    needsProxySetup({}, {}, { active: false, selected: "node" }),
    true,
  );
  assert.equal(
    needsProxySetup({}, {}, { active: true, selected: "node" }),
    false,
  );
});
test("running VM, old endpoint and restore file cannot bypass node setup", () => {
  for (const status of [{ vm_running: true }, { can_restore: true }]) {
    assert.equal(
      needsProxySetup({ proxy_url: "socks5://127.0.0.1:17896" }, status, {}),
      true,
    );
  }
});
