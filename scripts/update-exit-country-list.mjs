#!/usr/bin/env node
import { createHash } from "node:crypto";
import { mkdir, writeFile } from "node:fs/promises";
import { isIPv4 } from "node:net";
import { resolve } from "node:path";

// Maintainer-only update: pin the reviewed upstream commit; applications never
// download executable policy or replace this bundled snapshot at runtime.
const commit = process.argv[2];
if (!/^[a-f0-9]{40}$/.test(commit || "")) {
  throw new Error("Usage: node scripts/update-exit-country-list.mjs <upstream-commit-sha>");
}
const upstream = "gaoyifan/china-operator-ip";
const source = `https://raw.githubusercontent.com/${upstream}/${commit}`;
async function download(name) {
  const response = await fetch(`${source}/${name}`, { signal: AbortSignal.timeout(30000) });
  if (!response.ok) throw new Error(`${name}: HTTP ${response.status}`);
  return response.text();
}
const [data, license] = await Promise.all([download("china.txt"), download("LICENSE")]);
const prefixes = data.trim().split(/\r?\n/);
if (prefixes.length < 1000 || prefixes.length > 50000) throw new Error("Unexpected mainland prefix count");
for (const prefix of prefixes) {
  const [address, bits, extra] = prefix.split("/");
  if (!isIPv4(address) || !/^[0-9]+$/.test(bits || "") || +bits < 8 || +bits > 32 || extra !== undefined) {
    throw new Error(`Invalid IPv4 prefix: ${prefix}`);
  }
}
if (!license.includes("MIT License") || !license.includes("Yifan Gao")) throw new Error("Review upstream license before updating");
const target = resolve(import.meta.dirname, "../internal/exitpolicy/data");
await mkdir(target, { recursive: true });
await writeFile(`${target}/mainland-ipv4.txt`, data);
await writeFile(`${target}/LICENSE`, license);
await writeFile(`${target}/source.json`, JSON.stringify({
  sha256: createHash("sha256").update(data).digest("hex"),
  ipv4_prefixes: prefixes.length, license: "MIT",
}, null, 2) + "\n");
console.log(`Updated ${prefixes.length} mainland IPv4 prefixes from ${commit}`);
