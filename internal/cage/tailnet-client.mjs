// Private stdio control of the Cage-owned guest node. No browser page can
// select an executable, destination command, data directory or auth key.
import { spawn } from "node:child_process";
import { access } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { createInterface } from "node:readline";

export function createTailnetClient({ executable, launch, start = spawn } = {}) {
  executable ||= process.platform === "linux" ? "/usr/local/bin/cage-tailnet"
    : fileURLToPath(new URL(process.platform === "win32" ? "./cage-tailnet.exe" : "./cage-tailnet", import.meta.url));
  let child, lines, pending, queue = Promise.resolve();
  async function open() {
    if (child) return true;
    try { await access(executable); } catch { return false; }
    const command = launch || (process.platform === "linux" ? { file: "/usr/bin/sudo", args: ["-n", "-H", "-u", "cage", executable] } : { file: executable, args: [] });
    const current = start(command.file, command.args, { stdio: ["pipe", "pipe", "ignore"], windowsHide: true });
    child = current;
    const reset = () => {
      if (child !== current) return;
      pending?.reject(new Error("Tailscale 组件连接中断，请重新打开面板")); pending = undefined;
      lines?.close(); child = undefined;
    };
    lines = createInterface({ input: current.stdout });
    lines.on("line", line => {
      const active = pending; pending = undefined;
      if (!active) return;
      try { const value = JSON.parse(line); if (value.error) { value.tailnet_error = value.error; delete value.error; } active.resolve(value); }
      catch (error) { active.reject(error); }
    });
    current.once("error", reset); current.once("exit", reset);
    current.stdin.on("error", reset);
    return true;
  }
  async function run(input) {
    if (!input || !["status", "connect", "login", "disconnect", "logout", "add-key", "remove-key", "open-service"].includes(input.action) || typeof input.key !== "undefined" && (typeof input.key !== "string" || input.key.length > 8192)) throw new Error("Tailscale 操作无效");
    if (input.action === "open-service" && (typeof input.host !== "string" || !/^[a-zA-Z0-9.-]{1,253}$/.test(input.host) || !Number.isInteger(input.port) || input.port < 1 || input.port > 65535)) throw new Error("请选择私网设备并填写有效端口");
    if (!await open()) return { installed: false, enabled: false, backend: "NotInstalled", ips: [], peers: [], keys: [] };
    return new Promise((resolve, reject) => {
      const current = child;
      const timer = setTimeout(() => { current.kill(); reject(new Error("Tailscale 暂未响应，请稍后重试")); }, 12000);
      pending = { resolve: value => { clearTimeout(timer); resolve(value); }, reject: error => { clearTimeout(timer); reject(error); } };
      current.stdin.write(JSON.stringify({ action: input.action, ...(input.key === undefined ? {} : { key: input.key }), ...(input.action === "open-service" ? { host: input.host, port: input.port } : {}) }) + "\n");
    });
  }
  return {
    request(input) { const result = queue.then(() => run(input)); queue = result.catch(() => {}); return result; },
    close() { child?.stdin.end(); },
    restore() { return this.request({ action: "status" }); },
  };
}
