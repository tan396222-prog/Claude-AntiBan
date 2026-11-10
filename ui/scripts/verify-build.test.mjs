import assert from "node:assert/strict";
import { mkdtemp, mkdir, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { verifyBuild } from "./verify-build.mjs";

test("build verification rejects corrupted or missing frontend resources", async (t) => {
  for (const problem of ["none", "zero JS", "invalid JS", "zero CSS", "missing script", "missing entry"]) {
    await t.test(problem, async () => {
      const directory = await mkdtemp(join(tmpdir(), "cage-web-integrity-"));
      try {
        await mkdir(join(directory, "assets"));
        const html = '<div id="root"></div><script type="module" src="/assets/main.js"></script><link rel="stylesheet" href="/assets/main.css">';
        await writeFile(join(directory, "index.html"), html);
        await writeFile(join(directory, "mobile.html"), problem === "missing entry" ? '<div id="root"></div>' : html);
        if (problem !== "missing script") await writeFile(join(directory, "assets/main.js"), problem === "zero JS" ? Buffer.alloc(100) : problem === "invalid JS" ? "export const =;" : "export const valid = true;");
        await writeFile(join(directory, "assets/main.css"), problem === "zero CSS" ? Buffer.alloc(10) : "body { color: black }");
        if (problem !== "none") {
          await assert.rejects(verifyBuild(directory), /(?:NUL bytes|Invalid JavaScript|Missing build resource|Missing entry script)/);
        } else {
          assert.equal(await verifyBuild(directory), 4);
          const manifest = JSON.parse(await readFile(join(directory, "build-integrity.json"), "utf8"));
          assert.match(manifest.files["assets/main.js"], /^[a-f0-9]{64}$/);
        }
      } finally { await rm(directory, { recursive: true, force: true }); }
    });
  }
});
