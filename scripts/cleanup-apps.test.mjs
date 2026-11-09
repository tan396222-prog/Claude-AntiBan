import assert from "node:assert/strict";
import {
  mkdtemp,
  mkdir,
  writeFile,
  symlink,
  access,
  rm,
  copyFile,
} from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { spawn } from "node:child_process";
import { cleanupApps } from "./cleanup-apps.mjs";

const cageID = "io.github.lee-xeo.claude-cage";
test("cleanup preserves a Cage executable still in use", { skip: process.platform !== "darwin" }, async () => {
  const directory = await mkdtemp(path.join(os.tmpdir(), "cage-cleanup-running-"));
  let child;
  try {
    const latest = await app(path.join(directory, "new.app"));
    const running = await app(path.join(directory, "running.app"));
    await mkdir(path.join(running, "Contents/MacOS"));
    const executable = path.join(running, "Contents/MacOS/Cage");
    await copyFile("/bin/sleep", executable);
    child = spawn(executable, ["30"], { stdio: "ignore" });
    await new Promise((resolve, reject) => { child.once("spawn", resolve); child.once("error", reject); });
    const skipped = [];
    assert.deepEqual(await cleanupApps({ keep: [latest], roots: [directory], onInUse: (bundle) => skipped.push(bundle) }), []);
    assert.deepEqual(skipped, [running]);
    await access(executable);
    await new Promise((resolve) => { child.once("exit", resolve); child.kill("SIGTERM"); });
    child = null;
    assert.deepEqual(await cleanupApps({ keep: [latest], roots: [directory] }), [running]);
  } finally {
    if (child && child.exitCode === null) await new Promise((resolve) => { child.once("exit", resolve); child.kill("SIGTERM"); });
    await rm(directory, { recursive: true, force: true });
  }
});
async function app(directory, id = cageID) {
  await mkdir(path.join(directory, "Contents"), { recursive: true });
  await writeFile(
    path.join(directory, "Contents/Info.plist"),
    `<?xml version="1.0"?><plist version="1.0"><dict><key>CFBundleIdentifier</key><string>${id}</string></dict></plist>`,
  );
  return directory;
}

test(
  "cleanup removes old Cage copies while retaining replacement, foreign apps and linked directories",
  { skip: process.platform !== "darwin" },
  async (t) => {
    const directory = await mkdtemp(path.join(os.tmpdir(), "cage-cleanup-"));
    t.after(() => rm(directory, { recursive: true, force: true }));
    const root = path.join(directory, "output"),
      outside = path.join(directory, "outside");
    const latest = await app(path.join(root, "Cage.app"));
    const old = await app(path.join(root, "qa backup", "previous-Cage.app"));
    const foreign = await app(path.join(root, "Other.app"), "example.other");
    const external = await app(path.join(outside, "Cage.app"));
    await symlink(outside, path.join(root, "linked-backup"));
    assert.deepEqual(
      await cleanupApps({
        keep: [latest],
        roots: [root, path.join(directory, "missing")],
      }),
      [old],
    );
    await assert.rejects(access(old), { code: "ENOENT" });
    for (const preserved of [latest, foreign, external])
      await access(preserved);
  },
);

test(
  "cleanup refuses to delete anything without a valid Cage replacement",
  { skip: process.platform !== "darwin" },
  async (t) => {
    const directory = await mkdtemp(path.join(os.tmpdir(), "cage-cleanup-"));
    t.after(() => rm(directory, { recursive: true, force: true }));
    const old = await app(path.join(directory, "old.app"));
    const foreign = await app(
      path.join(directory, "Other.app"),
      "example.other",
    );
    await assert.rejects(cleanupApps({ keep: [], roots: [directory] }));
    await assert.rejects(cleanupApps({ keep: [foreign], roots: [directory] }));
    await access(old);
  },
);
