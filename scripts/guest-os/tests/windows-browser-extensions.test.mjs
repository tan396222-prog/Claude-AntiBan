import assert from "node:assert/strict";
import { test } from "node:test";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import path from "node:path";
import { tmpdir } from "node:os";
import { Readable } from "node:stream";
import { pathToFileURL } from "node:url";
import { extractWindowsExtensionArchive, nativeExtensionID } from "../windows-browser-extensions.mjs";
import { fixtureZip } from "./windows-browser-fixtures.mjs";
const moduleURL = process.env.CAGE_BROWSER_TEST_MODULE_DIRECTORY ? pathToFileURL(path.join(process.env.CAGE_BROWSER_TEST_MODULE_DIRECTORY, "browser-extensions.mjs")) : new URL("../../../internal/cage/browser-extensions.mjs", import.meta.url);
const { BrowserExtensions } = await import(moduleURL);

const manifest = JSON.stringify({ manifest_version: 3, name: "Fixture extension", version: "1.0", permissions: ["storage"], content_scripts: [{ matches: ["https://example.test/*"], js: ["content.js"] }] });
async function fixture(t) { const root = await mkdtemp(path.join(tmpdir(), "cage-windows-extension-")); t.after(() => rm(root, { recursive: true, force: true })); return root; }

test("extension IDs use native Windows UTF16LE and normalize only drive case", () => {
  const windows = "C:\\Users\\Guest\\Cage\\extensions\\example";
  assert.equal(nativeExtensionID(windows, "win32"), "knnkgdbljifnnoaknlcaeepfhjhcnebo");
  assert.equal(nativeExtensionID("c:" + windows.slice(2), "win32"), "knnkgdbljifnnoaknlcaeepfhjhcnebo");
  assert.equal(nativeExtensionID(windows, "linux"), "fnbldeeooamfcppikgofhpodlhnnkleh");
  assert.notEqual(nativeExtensionID(windows.toLowerCase(), "win32"), nativeExtensionID(windows, "win32"));
});

test("Windows ZIP preview extracts only declared safe files and includes script permissions", async t => {
  const root = await fixture(t), archive = path.join(root, "fixture.zip"), output = path.join(root, "unpacked");
  await writeFile(archive, fixtureZip([{ name: "package/manifest.json", data: manifest }, { name: "package/content.js", data: "void 0" }]));
  const preview = await extractWindowsExtensionArchive(archive, output);
  assert.equal(preview.relative, "package"); assert.deepEqual(preview.hostPermissions, ["https://example.test/*"]);
  assert.equal(await readFile(path.join(output, "package", "content.js"), "utf8"), "void 0");
});

test("ZIP traversal, links, ADS, reserved names, duplicate Windows paths and declared bombs fail before extraction", async t => {
  const root = await fixture(t);
  const bad = [
    { name: "../outside" }, { name: "/outside" }, { name: "C:/outside" }, { name: "a\\b" },
    { name: "normal:stream" }, { name: "CON.txt" }, { name: "file." }, { name: "file " },
    { name: "link", attributes: 0o120777 << 16 }, { name: "encrypted", flags: 1 }, { name: "bomb", size: (32 << 20) + 1 },
  ];
  let index = 0;
  for (const entry of bad) {
    const archive = path.join(root, String(index) + ".zip"), output = path.join(root, String(index++));
    await writeFile(archive, fixtureZip([{ name: "manifest.json", data: manifest }, entry]));
    await assert.rejects(extractWindowsExtensionArchive(archive, output));
    await assert.rejects(readFile(path.join(output, "manifest.json"))); // nothing was written before validation
  }
  const duplicate = path.join(root, "duplicate.zip"); await writeFile(duplicate, fixtureZip([{ name: "manifest.json", data: manifest }, { name: "A.js" }, { name: "a.js" }]));
  await assert.rejects(extractWindowsExtensionArchive(duplicate, path.join(root, "duplicates")), /重复/);
  const oversized = path.join(root, "oversized.zip"); await writeFile(oversized, fixtureZip(Array.from({ length: 5 }, (_, n) => ({ name: "file" + n, size: 32 << 20 }))));
  await assert.rejects(extractWindowsExtensionArchive(oversized, path.join(root, "oversized")), /128 MB/);
  const damaged = path.join(root, "damaged.zip"); await writeFile(damaged, fixtureZip([{ name: "manifest.json", data: manifest, crc: 0 }]));
  await assert.rejects(extractWindowsExtensionArchive(damaged, path.join(root, "damaged")), /校验/);
});

test("disabled extension import computes the Windows ID without loading or executing it", async t => {
  const root = await fixture(t), calls = [];
  const extensions = new BrowserExtensions({ directory: root, protectedID: "a".repeat(32), control: async () => "private-control", idForPath: p => nativeExtensionID(p, "win32"), extractArchive: extractWindowsExtensionArchive,
    cdp: async (method, params) => { calls.push(method); if (method === "Runtime.evaluate") return { result: { value: { items: [] } } }; throw new Error("an extension must not load during disabled import"); } });
  const preview = await extensions.upload(Readable.from([fixtureZip([{ name: "manifest.json", data: manifest }, { name: "content.js", data: "globalThis.executed=true" }])]));
  const result = await extensions.request({ mode: "install", session: preview.session, enabled: false });
  assert.equal(result.items.length, 1); assert.equal(result.items[0].enabled, false);
  assert.equal(result.items[0].id, nativeExtensionID(path.join(root, "extensions", "packages", preview.session), "win32"));
  assert(!calls.includes("Extensions.loadUnpacked"));
});
