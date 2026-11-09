// Run only with disposable builds. Every service uses a newly created profile.
// Usage: node scripts/control-upgrade-smoke.mjs <legacy-cage> <current-cage> <different-build-cage>
import assert from "node:assert/strict";
import { spawn, execFile } from "node:child_process";
import { promisify } from "node:util";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";

const runFile = promisify(execFile);
const [legacy, current, next] = process.argv.slice(2).map((file) => path.resolve(file));
assert(legacy && current && next, "Pass three cage executables");
const profile = await mkdtemp(path.join(tmpdir(), "cage-control-upgrade-"));
const env = { ...process.env, CLAUDE_CAGE_CONFIG: path.join(profile, "config.json") };
const call = async (binary, ...args) => {
  const { stdout } = await runFile(binary, [...args, "--json"], { env, timeout: 150_000, maxBuffer: 512 * 1024 });
  return JSON.parse(stdout);
};
const record = async (entry = false) => JSON.parse(await readFile(path.join(profile, entry ? "desktop-entry" : ".", "control.json"), "utf8"));
const wait = async (test) => {
  for (let i = 0; i < 100; i++) {
    try { if (await test()) return; } catch {}
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("Timed out waiting for the disposable controller");
};
const old = spawn(legacy, ["serve"], { cwd: profile, env, stdio: "ignore" });
try {
  await wait(async () => (await record()).pid === old.pid);
  assert.equal((await call(current, "desktop")).status, "ready", "Chooser can open without taking over the older workload");
  assert.equal((await record()).pid, old.pid);
  assert.equal((await call(current, "desktop", "--existing")).status, "existing");
  assert.equal((await call(current, "prepare-update")).status, "stop_required");
  assert.equal((await call(current, "prepare-update", "--stop")).status, "prepared");
  await wait(() => old.exitCode !== null || old.signalCode !== null);
  assert.equal((await call(current, "desktop")).status, "ready");
  const first = await record(true);
  assert.equal((await call(current, "desktop")).status, "ready");
  assert.equal((await record(true)).pid, first.pid, "Same build must reuse its controller");
  assert.equal((await call(next, "desktop")).status, "ready");
  const replacement = await record(true);
  assert.notEqual(replacement.pid, first.pid, "Different build must replace an idle controller");
  assert.notEqual(replacement.controller.build, first.controller.build);
  assert.equal(replacement.controller.version, first.controller.version, "Exercise same-version distinct builds");
  assert.equal((await call(next, "prepare-update")).status, "prepared");
  console.log("PASS: legacy requires explicit stop, verified legacy migration, same-build reuse, same-version build replacement, and graceful shutdown");
} finally {
  // Only this fresh profile is addressed. Never target the user's normal profile.
  await call(current, "prepare-update", "--stop").catch(() => {});
  if (old.exitCode === null && old.signalCode === null) old.kill("SIGTERM");
  await rm(profile, { recursive: true, force: true });
  await rm(profile + "-workspace", { recursive: true, force: true });
}
