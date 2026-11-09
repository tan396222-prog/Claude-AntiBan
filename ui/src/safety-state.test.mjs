import test from "node:test";
import assert from "node:assert/strict";
import {
  networkState,
  reportForSharing,
  isGuestLocalURL,
  canonicalExitPolicy,
} from "./safety-state.mjs";

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
