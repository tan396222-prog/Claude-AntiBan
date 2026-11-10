// The fixed sudo helper checks the host-minted lease and drops privileges
// before attaching a phone view to the existing desktop application.
import { spawn } from "node:child_process";

export async function phoneDesktop(op, input) {
  const child = spawn("/usr/bin/sudo", ["-n", "/usr/local/lib/cage-phone-desktop/control.py"],
    { stdio: ["pipe", "pipe", "ignore"] });
  let size = 0;
  const chunks = [];
  const timeout = setTimeout(() => child.kill("SIGKILL"), 12500);
  try {
    const output = await new Promise((resolve, reject) => {
      child.on("error", reject);
      child.stdout.on("data", chunk => {
        size += chunk.length;
        if (size > 3600000) child.kill("SIGKILL");
        else chunks.push(chunk);
      });
      child.stdin.on("error", () => {});
      child.once("close", code => {
        if (code !== 0 || !size || size > 3600000) reject(new Error("手机 Desktop 服务不可用"));
        else resolve(Buffer.concat(chunks));
      });
      child.stdin.end(JSON.stringify({ ...input, op }) + "\n");
    });
    const result = JSON.parse(output);
    if (result.status) {
      const status = [400, 403, 409, 410, 412, 501, 503].includes(result.status) ? result.status : 503;
      throw Object.assign(new Error("手机 Desktop 操作未完成"), { status });
    }
    return result;
  } finally { clearTimeout(timeout); }
}
