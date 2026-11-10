import { createHash } from "node:crypto";
import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";

const [executable, icon, sevenZip] = process.argv.slice(2);
if (!executable || !icon || !sevenZip) {
  throw new Error("Usage: verify-windows-installer-icon.mjs INSTALLER.exe ICON.ico SEVEN_ZIP");
}

function frames(bytes) {
  if (bytes.length < 6 || bytes.readUInt16LE(0) !== 0 || bytes.readUInt16LE(2) !== 1) {
    throw new Error("Invalid Windows icon file.");
  }
  const count = bytes.readUInt16LE(4);
  if (!count || bytes.length < 6 + count * 16) throw new Error("Missing Windows icon images.");
  const result = new Map();
  for (let index = 0; index < count; index++) {
    const entry = 6 + index * 16;
    const size = bytes.readUInt32LE(entry + 8);
    const offset = bytes.readUInt32LE(entry + 12);
    const key = `${bytes[entry] || 256}x${bytes[entry + 1] || 256}`;
    if (offset + size > bytes.length) throw new Error("Truncated Windows icon image.");
    result.set(key, createHash("sha256").update(bytes.subarray(offset, offset + size)).digest("hex"));
  }
  return result;
}

function resource(...names) {
  return execFileSync(sevenZip, ["e", "-tPE", "-so", executable, ...names], {
    maxBuffer: 2 * 1024 * 1024,
    stdio: ["ignore", "pipe", "pipe"],
  });
}

const expected = frames(readFileSync(icon));
// NSIS uses icon group 103 for the executable's primary shell icon.
const group = resource(".rsrc/GROUP_ICON/103");
if (group.length !== 6 + expected.size * 14 || group.readUInt32LE(0) !== 0x00010000 || group.readUInt16LE(4) !== expected.size) {
  throw new Error("Installer icon sizes differ from the Cage icon.");
}
const seen = new Set();
for (let index = 0; index < expected.size; index++) {
  const entry = 6 + index * 14;
  const key = `${group[entry] || 256}x${group[entry + 1] || 256}`;
  const id = group.readUInt16LE(entry + 12);
  // 7-Zip returns PNG frames directly and wraps bitmap frames in an ICO header.
  const bytes = resource(`.rsrc/ICON/${id}`, `.rsrc/ICON/${id}.ico`);
  const actual = bytes.length >= 6 && bytes.readUInt32LE(0) === 0x00010000
    ? frames(bytes).get(key)
    : createHash("sha256").update(bytes).digest("hex");
  if (seen.has(key) || !expected.has(key) || actual !== expected.get(key)) {
    throw new Error(`Installer icon ${key} differs from the Cage logo.`);
  }
  seen.add(key);
}
console.log(`Verified ${expected.size} Cage installer icon sizes.`);
