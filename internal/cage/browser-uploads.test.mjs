import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, stat } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { Readable } from "node:stream";
import { UploadManager } from "./browser-uploads.mjs";

async function fixture(t, multiple = true) {
  const directory = await mkdtemp(join(tmpdir(), "cage-uploads-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const calls = [];
  const manager = new UploadManager({
    directory,
    cdp: async (...args) => calls.push(args),
  });
  await manager.opened("tab-1", "session-1", {
    backendNodeId: 42,
    mode: multiple ? "selectMultiple" : "selectSingle",
  });
  return { manager, directory, calls, id: manager.snapshot()[0].id };
}

test("binary files retain their names and bytes, and only the original input receives them", async (t) => {
  const f = await fixture(t);
  const bytes = Buffer.from([0, 255, 27, 99, 0]);
  await f.manager.receive(
    f.id,
    "参考视频.mp4",
    Readable.from([bytes.subarray(0, 2), bytes.subarray(2)]),
  );
  await f.manager.receive(f.id, "empty.txt", Readable.from([]));
  await f.manager.finish(f.id);
  assert.equal(f.calls[0][0], "DOM.setFileInputFiles");
  assert.equal(f.calls[0][1].backendNodeId, 42);
  assert.equal(f.calls[0][2], "session-1");
  const [file, empty] = f.calls[0][1].files;
  assert.deepEqual(await readFile(file), bytes);
  assert.equal((await stat(file)).mode & 0o777, 0o600);
  assert.equal((await readFile(empty)).length, 0);
  assert.deepEqual(f.manager.snapshot(), []);
  assert.equal(f.manager.retained.get("tab-1").length, 1);
});

test("cancel during a stream refuses the rest and never assigns partial files", async (t) => {
  const f = await fixture(t);
  async function* chunks() {
    yield Buffer.from("partial");
    f.manager.cancel(f.id);
    yield Buffer.from("discarded");
  }
  await assert.rejects(f.manager.receive(f.id, "video.mp4", chunks()), /取消/);
  await assert.rejects(f.manager.finish(f.id), /关闭/);
  assert.deepEqual(f.calls, []);
});

test("a stale chooser and traversal names cannot select arbitrary files", async (t) => {
  const f = await fixture(t, false);
  for (const name of ["../secret", "a/b", "a\\b", "..", "a\u0000b"]) {
    await assert.rejects(
      f.manager.receive(f.id, name, Readable.from([Buffer.from("data")])),
      /文件名/,
    );
  }
  await f.manager.receive(f.id, "one.txt", Readable.from([Buffer.from("one")]));
  await assert.rejects(
    f.manager.receive(f.id, "two.txt", Readable.from([])),
    /数量/,
  );
  f.manager.cancelTab("tab-1");
  await assert.rejects(f.manager.finish(f.id), /关闭/);
  assert.deepEqual(f.calls, []);
});

test("a removed DOM input produces a recoverable cancellation", async (t) => {
  const f = await fixture(t);
  await f.manager.receive(f.id, "one.txt", Readable.from([Buffer.from("one")]));
  f.manager.cdp = async () => {
    throw new Error("Node no longer exists");
  };
  await assert.rejects(f.manager.finish(f.id), /Node/);
  assert.deepEqual(f.manager.snapshot(), []);
});
