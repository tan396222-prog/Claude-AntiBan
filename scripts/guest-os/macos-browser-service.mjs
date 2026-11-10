import net from "node:net";
import fs from "node:fs/promises";
import path from "node:path";
import { lines } from "./windows-browser.mjs";

async function privateDirectory(directory) {
  await fs.mkdir(directory, { recursive: true, mode: 0o700 });
  const info = await fs.lstat(directory);
  if (!info.isDirectory() || info.uid !== process.getuid() || (info.mode & 0o077))
    throw new Error("Browser service directory is unavailable");
  return path.join(directory, "browser.sock");
}

async function socketInfo(address) {
  const info = await fs.lstat(address);
  if (!info.isSocket() || info.uid !== process.getuid() || (info.mode & 0o077))
    throw new Error("Browser service socket is unavailable");
  return info;
}

function connect(address) {
  return new Promise((resolve, reject) => {
    const socket = net.createConnection({ path: address, allowHalfOpen: true });
    socket.once("error", reject);
    socket.once("connect", () => { socket.off("error", reject); resolve(socket); });
  });
}

export async function relayMacOSBrowser(directory, input = process.stdin, output = process.stdout) {
  const address = await privateDirectory(directory);
  await socketInfo(address);
  const socket = await connect(address);
  socket.on("error", () => {});
  try {
    const replies = lines(socket, 30 << 20)[Symbol.asyncIterator]();
    for await (const line of lines(input)) {
      await new Promise((resolve, reject) => socket.write(line + "\n", error => error ? reject(error) : resolve()));
      const reply = await replies.next();
      if (reply.done) throw new Error("Browser service connection closed");
      await new Promise((resolve, reject) => output.write(reply.value + "\n", error => error ? reject(error) : resolve()));
    }
    socket.end();
  } finally {
    socket.destroy();
  }
}

// The ordinary guest user owns one browser service. Each caller owns only its
// connection and cancellation, while dispatch retains a single mutation order.
export async function serveMacOSBrowser({ directory, dispatch, close, disconnect }) {
  const address = await privateDirectory(directory);
  const previous = await fs.lstat(address).catch(error => { if (error.code !== "ENOENT") throw error; });
  if (previous) {
    await socketInfo(address);
    let live;
    try { live = await connect(address); }
    catch (error) { if (error.code !== "ECONNREFUSED" && error.code !== "ENOENT") throw error; }
    if (live) { live.destroy(); throw new Error("Browser service is already running"); }
    const current = await fs.lstat(address).catch(error => { if (error.code !== "ENOENT") throw error; });
    if (current && current.ino !== previous.ino) throw new Error("Browser service owner changed");
    if (current) await fs.unlink(address);
  }
  const clients = new Set();
  let queue = Promise.resolve(), stopping;
  const request = (input, options = {}) => {
    const pending = queue.then(() => {
      if (stopping) throw new Error("Browser service is stopping");
      options.signal?.throwIfAborted();
      return dispatch(input, options);
    });
    queue = pending.catch(() => {});
    return pending;
  };
  const server = net.createServer({ allowHalfOpen: true }, socket => {
    if (stopping || clients.size >= 16) { socket.destroy(); return; }
    clients.add(socket);
    const cancellation = new AbortController();
    socket.on("error", () => {});
    socket.once("end", () => { cancellation.abort(); socket.end(); });
    socket.once("close", () => {
      cancellation.abort(); clients.delete(socket);
      if (disconnect) {
        const released = queue.then(() => disconnect(cancellation.signal));
        queue = released.catch(() => {});
      }
    });
    let pendingReply = false;
    void (async () => {
      try {
        for await (const line of lines(socket)) {
          if (pendingReply) throw new Error("Browser service caller has an unfinished request");
          pendingReply = true;
          const pending = request(JSON.parse(line), { signal: cancellation.signal });
          void (async () => {
            let result;
            try { result = await pending; }
            catch (error) { result = { error: error.safe ? error.message : "客机浏览器操作失败", code: error.safe ? error.code : "browser_operation_failed" }; }
            if (socket.destroyed) return;
            const data = JSON.stringify({ ...result, service: { version: 1, persistent: true } }) + "\n";
            if (Buffer.byteLength(data) > 30 << 20) throw new Error("Browser service response is too large");
            await new Promise((resolve, reject) => socket.write(data, error => error ? reject(error) : resolve()));
          })().catch(() => socket.destroy()).finally(() => { pendingReply = false; });
        }
      } catch { socket.destroy(); }
    })();
  });
  await new Promise((resolve, reject) => { server.once("error", reject); server.listen(address, resolve); });
  await fs.chmod(address, 0o600);
  const owned = await socketInfo(address);
  const stop = async () => {
    if (!stopping) stopping = (async () => {
      const closed = new Promise(resolve => server.close(resolve));
      for (const client of clients) client.destroy();
      try { await close(); }
      finally {
        await closed;
        await queue;
        const current = await fs.lstat(address).catch(() => null);
        if (current?.ino === owned.ino) await fs.unlink(address);
      }
    })();
    return stopping;
  };
  return { request, close: stop };
}
