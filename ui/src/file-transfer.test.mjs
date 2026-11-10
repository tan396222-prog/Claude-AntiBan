import test from "node:test";
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const source = readFileSync(new URL("./file-transfer.js", import.meta.url), "utf8")
  .replace(/^import .*;\n/, "").replaceAll("export ", "");
function fixture({ fail, switchAfterUpload = false } = {}) {
  const calls = []; let active = true;
  const functions = vm.runInNewContext(source + "\n({sendFiles,shellFilePaths})", {
    URLSearchParams,
    api: async (path, body) => { calls.push([path, body]); return { id: "upload-id" }; },
    uploadFile: async (path, file) => {
      calls.push([path, file]); if (fail === file.name) throw Error("connection lost");
      if (switchAfterUpload) active = false;
      return { ok: true, path: "/home/cage/attachments/" + file.name };
    },
    native: async (action, body) => { calls.push([action, body]); return { ok: true, paths: ["/home/cage/attachments/video.mp4"] }; },
  });
  return { ...functions, calls, valid: () => active };
}

test("video and document files remain one ordered batch and bypass image conversion", async () => {
  const f = fixture();
  const files = [{ name: "原视频.mp4", size: 8 << 20 }, { name: "中文.txt", size: 13 }];
  const result = await f.sendFiles({ files, kind: "drop", x: 0.2, y: 0.8 }, "browser", "original-tab");
  assert.equal(f.calls[0][1].id, "original-tab");
  assert.equal(f.calls[1][1], files[0]); assert.equal(f.calls[2][1], files[1]);
  assert.equal(f.calls.at(-1)[0], "/input/files/finish");
  assert.equal(result.paths.length, 2);
});

test("a failed file or switched target cancels a batch without delivering partial attachments", async () => {
  for (const options of [{ fail: "second.mp4" }, { switchAfterUpload: true }]) {
    const f = fixture(options);
    await assert.rejects(f.sendFiles({ files: [{ name: "first.txt", size: 1 }, { name: "second.mp4", size: 2 }] }, "browser", "tab", { valid: f.valid }));
    assert.equal(f.calls.at(-1)[0], "/input/files/cancel");
    assert.equal(f.calls.some(([path]) => path === "/input/files/finish"), false);
  }
});

test("native copied files consume an opaque selection handle for the original destination", async () => {
  const f = fixture();
  const result = await f.sendFiles({ fileSelection: "selected-files" }, "terminal", "original-terminal");
  assert.equal(f.calls[1][0], "uploadHostFiles");
  assert.equal(f.calls[1][1].selection, "selected-files");
  assert.equal(f.calls[1][1].target, "terminal");
  assert.equal(f.calls[1][1].id, "upload-id");
  assert.equal(result.paths[0], "/home/cage/attachments/video.mp4");
});

test("attachment paths preserve apostrophes in both PowerShell and POSIX shells", () => {
  const f = fixture();
  assert.equal(f.shellFilePaths(["C:\\Users\\Cage\\O'Brien.txt"]), "'C:\\Users\\Cage\\O''Brien.txt' ");
  assert.equal(f.shellFilePaths(["/home/cage/O'Brien.txt"]), "'/home/cage/O'\\''Brien.txt' ");
});
