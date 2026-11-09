import { cpSync, mkdirSync } from "node:fs";
import { execFileSync } from "node:child_process";
mkdirSync(".generated", { recursive: true });
cpSync("vendor/novnc", ".generated/novnc", { recursive: true, force: true });
execFileSync(
  "uv",
  ["run", "python", "../internal/cage/novnc-patch.py", ".generated/novnc"],
  { stdio: "inherit" },
);
