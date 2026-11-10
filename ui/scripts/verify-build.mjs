import { createHash } from "node:crypto";
import { readFile, readdir, stat, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { spawnSync } from "node:child_process";

const defaultDirectory = fileURLToPath(new URL("../../internal/workbench/web/", import.meta.url));
export async function verifyBuild(directory = defaultDirectory) {
  const files = {};
  async function inspect(relative = "") {
    for (const entry of await readdir(join(directory, relative), { withFileTypes: true })) {
      const name = [relative, entry.name].filter(Boolean).join("/");
      if (entry.isDirectory()) { await inspect(name); continue; }
      if (name === "README" || name === "build-integrity.json") continue;
      const bytes = await readFile(join(directory, name));
      if (bytes.length === 0) throw new Error(`Empty build resource: ${name}`);
      if (/\.(?:js|css|html)$/.test(name)) {
        if (bytes.includes(0)) throw new Error(`NUL bytes in build resource: ${name}`);
        if (name.endsWith(".js")) {
          const parsed = spawnSync(process.execPath, ["--check", "--input-type=module"], { input: bytes, encoding: "utf8" });
          if (parsed.error) throw parsed.error;
          if (parsed.status !== 0) throw new Error(`Invalid JavaScript build resource: ${name}\n${parsed.stderr}`);
        }
      }
      files[name] = createHash("sha256").update(bytes).digest("hex");
    }
  }
  await inspect();
  for (const entry of ["index.html", "mobile.html"]) {
    const html = await readFile(join(directory, entry), "utf8");
    const scripts = [...html.matchAll(/<script\b[^>]*\bsrc=["']([^"']+)["']/gi)];
    if (!scripts.length) throw new Error(`Missing entry script: ${entry}`);
    for (const match of html.matchAll(/<(?:script|link)\b[^>]*\b(?:src|href)=["']([^"']+)["']/gi)) {
      const url = new URL(match[1], "http://cage.local/" + entry);
      if (url.origin !== "http://cage.local") throw new Error(`External build resource: ${match[1]}`);
      const resource = decodeURIComponent(url.pathname).slice(1);
      if (!files[resource] || !(await stat(join(directory, resource))).isFile()) throw new Error(`Missing build resource: ${resource}`);
    }
  }
  await writeFile(join(directory, "build-integrity.json"), JSON.stringify({ version: 1, files }, null, 2) + "\n");
  return Object.keys(files).length;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const count = await verifyBuild(process.argv[2] ? resolve(process.argv[2]) : defaultDirectory);
  console.log(`Verified ${count} frontend build resources.`);
}
