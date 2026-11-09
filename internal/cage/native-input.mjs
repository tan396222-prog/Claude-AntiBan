import { createConnection } from "node:net";
import { spawn } from "node:child_process";

// Each display owns a private IBus socket. The browser can access Desktop's
// bridge only through this fixed, sudo-authorized helper, with data on stdin.
export function nativeInput(value) {
  return new Promise((resolve, reject) => {
    let data = "";
    const finish = () => {
      try {
        const result = JSON.parse(data);
        if (result.error) throw new Error(result.error);
        resolve(result);
      } catch (error) { reject(error); }
    };
    const unavailable = (error) => {
      if (value.mode === "status") resolve({ available: false });
      else reject(error);
    };
    const collect = (chunk) => {
      data += chunk;
      if (data.length > 32768) {
        channel.destroy();
        reject(new Error("Input response too large"));
      }
    };
    let channel;
    if (value.target === "desktop") {
      const child = spawn("/usr/bin/sudo", ["-n", "-H", "-u", "cage", "/usr/local/bin/cage-native-input", "--request"], {
        stdio: ["pipe", "pipe", "ignore"], timeout: 3500,
      });
      channel = child.stdout;
      channel.on("data", collect);
      child.on("error", unavailable);
      child.stdin.on("error", unavailable);
      child.on("close", (code) => {
        if (code === 0) finish();
        else unavailable(new Error("Desktop input bridge unavailable"));
      });
      child.stdin.end(JSON.stringify(value) + "\n");
    } else {
      channel = createConnection("/home/cage-browser/.cache/cage-input/input.sock");
      channel.setTimeout(3000, () => channel.destroy(new Error("Input bridge timed out")));
      channel.on("error", unavailable);
      channel.on("connect", () => channel.end(JSON.stringify(value) + "\n"));
      channel.on("data", collect);
      channel.on("end", finish);
    }
  });
}
