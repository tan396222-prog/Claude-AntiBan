// Real lightweight client entry point and controller; no remote host connection.
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { isAbsolute, join } from "node:path";
import { setTimeout } from "node:timers/promises";
const binary = process.argv[2];
assert.ok(binary && isAbsolute(binary), "Pass an absolute cage-client binary.");
const root = await mkdtemp(join(tmpdir(), "cage-agent-client-"));
const config = join(root, "config.json");
let pid;
const invoke = (args) => {
  const result = spawnSync(binary, args, {
    encoding: "utf8",
    timeout: 30000,
    env: { ...process.env, CLAUDE_CAGE_CONFIG: config },
  });
  assert.ifError(result.error);
  assert.equal(
    result.status,
    0,
    `Client operation failed: ${result.stdout} ${result.stderr}`,
  );
  return JSON.parse(result.stdout);
};
try {
  const capabilities = invoke(["agent", "capabilities"]);
  assert.ok(
    capabilities.data.operations.some((x) => x.name === "client.hosts"),
  );
  invoke(["desktop", "--json"]);
  pid = JSON.parse(await readFile(join(root, "control.json"), "utf8")).pid;
  const context = invoke(["agent", "call", "client.context"]);
  assert.equal(context.data.client_only, true);
  const doctor = invoke(["agent", "doctor"]);
  assert.equal(doctor.data.scope, "client");
  assert.equal(doctor.data.snapshot.client_only, true);
  assert.equal(doctor.data.snapshot.connection_id, context.data.connection_id);
  const routes = invoke(["agent", "routes"]);
  assert.ok(routes.data.includes("POST /api/agent/native"));
  const hosts = invoke(["agent", "call", "client.hosts"]);
  assert.deepEqual(hosts.data.hosts, []);
  console.log(
    "PASS: lightweight client exposes the shared Agent protocol, native queue route, exact client context, scope-aware doctor and saved host collection. No remote host or VM was started.",
  );
} finally {
  if (pid) {
    try {
      process.kill(pid, "SIGTERM");
    } catch (error) {
      if (error.code !== "ESRCH") throw error;
    }
    let retired = false;
    for (let i = 0; i < 100; i++) {
      try {
        await stat(join(root, "control.json"));
      } catch (error) {
        if (error.code === "ENOENT") {
          retired = true;
          break;
        }
        throw error;
      }
      await setTimeout(100);
    }
    assert.ok(
      retired,
      "Client controller is still running; preserve its test profile.",
    );
  }
  await rm(root, { recursive: true, force: true });
}
