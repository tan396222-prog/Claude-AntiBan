// Carries only Claude login links between processes owned by the ordinary guest user.
import net from "node:net";
import fs from "node:fs/promises";
import path from "node:path";

export async function installMacOSCLIBrowser(data, dispatch) {
  const directory = path.join(data, "Login");
  await fs.mkdir(directory, { mode: 0o700, recursive: true });
  const dir = await fs.lstat(directory);
  if (!dir.isDirectory() || dir.uid !== process.getuid() || (dir.mode & 0o077)) throw new Error("Invalid login directory");
  const address = path.join(directory, "open.sock");
  const serve = net.createServer({ allowHalfOpen: true }, socket => {
    let input = "", size = 0;
    socket.setTimeout(15000, () => socket.destroy());
    socket.on("error", () => {});
    socket.setEncoding("utf8");
    socket.on("data", chunk => {
      size += Buffer.byteLength(chunk);
      if (size > 12288) socket.destroy();
      else input += chunk;
    });
    socket.on("end", async () => {
      try {
        const { action, login, url } = JSON.parse(input);
        if (!["login-open", "login-finish"].includes(action) || !/^[0-9a-f]{24}$/.test(login)) throw new Error();
        const result = await dispatch({ method: "POST", path: "/" + action, input: { login, url } });
        socket.end(result?.error ? "ERR" : "OK");
      } catch { socket.end("ERR"); }
    });
  });
  // Publish a fresh socket after its permissions are set. A replaced process
  // retains its own inode and cannot remove its successor's socket on exit.
  const temporary = path.join(directory, ".open-" + process.pid);
  await new Promise((resolve, reject) => { serve.once("error", reject); serve.listen(temporary, resolve); });
  try {
    await fs.chmod(temporary, 0o600);
    const old = await fs.lstat(address).catch(e => { if (e.code !== "ENOENT") throw e; });
    if (old && (!old.isSocket() || old.uid !== dir.uid)) throw new Error("Invalid login socket");
    await fs.rename(temporary, address);
  } catch (error) { serve.close(); throw error; }
  const { ino } = await fs.lstat(address);
  return async () => {
    await new Promise(resolve => serve.close(resolve));
    const current = await fs.lstat(address).catch(() => null);
    if (current?.ino === ino) await fs.unlink(address);
  };
}
