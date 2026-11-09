#!/usr/bin/env node
import { cpSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { resolve, join } from "node:path";

// Collect the licenses of the packages actually shipped in the workbench.
const target = process.argv[2];
if (!target) throw new Error("Usage: node scripts/collect-app-licenses.mjs <output-directory>");
const root = resolve(import.meta.dirname, "..");
mkdirSync(target, { recursive: true });
for (const file of ["LICENSE", "NOTICE", "THIRD_PARTY_LICENSES"]) {
  cpSync(join(root, file), join(target, file));
}
const lock = JSON.parse(readFileSync(join(root, "ui/package-lock.json"), "utf8"));
const notices = [];
for (const [path, entry] of Object.entries(lock.packages)) {
  if (!path || entry.dev) continue;
  const source = join(root, "ui", path);
  const name = JSON.parse(readFileSync(join(source, "package.json"), "utf8")).name;
  const dest = join(target, "npm", name);
  const files = readdirSync(source).filter((file) => /^(licen[sc]e|copying|notice)(\.|$)/i.test(file) && statSync(join(source, file)).isFile());
  if (!files.length) throw new Error(`Missing license for ${name}@${entry.version}`);
  mkdirSync(dest, { recursive: true });
  for (const file of files) cpSync(join(source, file), join(dest, file));
  notices.push(`${name}@${entry.version}: ${entry.license || "see license files"}`);
}
writeFileSync(join(target, "npm", "NOTICE.txt"), `${notices.join("\n")}\n`);
const novnc = join(root, "ui/vendor/novnc");
for (const file of ["LICENSE.txt", "AUTHORS", "README.cage.md", "docs", "vendor/pako/LICENSE"]) {
  mkdirSync(join(target, "novnc", file.includes("/") ? file.slice(0, file.lastIndexOf("/")) : ""), { recursive: true });
  cpSync(join(novnc, file), join(target, "novnc", file), { recursive: true });
}
// MPL-covered source and local modifications travel with the executable.
cpSync(join(root, "ui/.generated/novnc"), join(target, "novnc-source"), { recursive: true });
