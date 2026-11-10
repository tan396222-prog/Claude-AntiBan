// Chromium's own loopback bypass is disabled. Pin each connection to the
// checked IPv4 from the guest resolver, including redirects and subresources.
// The guest's existing Cage network gate still controls these sockets.
import { resolve4 } from "node:dns/promises";
import { once } from "node:events";
import net from "node:net";
import { networkInterfaces } from "node:os";

const reserved = new net.BlockList();
for (const [address, prefix] of [
  ["0.0.0.0", 8], ["10.0.0.0", 8], ["100.64.0.0", 10],
  ["127.0.0.0", 8], ["169.254.0.0", 16], ["172.16.0.0", 12],
  ["192.0.0.0", 24], ["192.0.2.0", 24], ["192.88.99.0", 24],
  ["192.168.0.0", 16], ["198.18.0.0", 15], ["198.51.100.0", 24],
  ["203.0.113.0", 24], ["224.0.0.0", 3],
]) reserved.addSubnet(address, prefix);

function publicIPv4(ip) {
  return net.isIPv4(ip) && !reserved.check(ip);
}

function publicName(name) {
  if (net.isIP(name)) return publicIPv4(name);
  return name.length <= 253 && name.includes(".") &&
    !/(^|\.)(localhost|local|internal|home\.arpa)$/.test(name) &&
    name.split(".").every(label => /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/i.test(label));
}

export function phoneError(status, message) {
  return Object.assign(new Error(message), { status });
}

export function webAddress(value) {
  if (typeof value !== "string" || value.length > 16384) throw new Error("请输入有效的网址");
  let url;
  try { url = new URL(value); } catch { throw new Error("请输入有效的网址"); }
  if (!["http:", "https:"].includes(url.protocol) || url.username || url.password ||
      !publicName(url.hostname.toLowerCase().replace(/\.$/, ""))) {
    throw phoneError(403, "手机浏览器仅支持公网 HTTP 或 HTTPS 网址");
  }
  return url.href;
}

async function internetSocket(name, port) {
  if (!publicName(name)) throw new Error("非公网地址");
  const ips = net.isIPv4(name) ? [name] : await resolve4(name);
  const local = new Set(Object.values(networkInterfaces()).flat().map(info => info.address));
  if (!ips.length || !ips.every(ip => publicIPv4(ip) && !local.has(ip))) throw new Error("非公网地址");
  const socket = net.createConnection({ host: ips[0], port, family: 4 });
  socket.setTimeout(12000, () => socket.destroy(new Error("连接超时")));
  await once(socket, "connect");
  socket.setTimeout(0);
  socket.removeAllListeners("timeout");
  return socket;
}

// Only SOCKS5 CONNECT is implemented. UDP, BIND, and IPv6 are refused.
export async function startPhoneProxy(connect = internetSocket) {
  const sockets = new Set();
  const server = net.createServer({ pauseOnConnect: true }, client => {
    sockets.add(client);
    client.once("close", () => sockets.delete(client));
    client.on("error", () => client.destroy());
    client.setTimeout(15000, () => client.destroy());
    async function handshake() {
      const reader = client.iterator({ destroyOnReturn: false });
      let buffer = Buffer.alloc(0);
      async function take(length) {
        while (buffer.length < length) {
          const next = await reader.next();
          if (next.done) throw new Error("连接中断");
          buffer = Buffer.concat([buffer, next.value]);
        }
        const head = buffer.subarray(0, length);
        buffer = buffer.subarray(length);
        return head;
      }
      let remote;
      try {
        const [version, methodCount] = await take(2);
        if (version !== 5 || !(await take(methodCount)).includes(0)) throw new Error("协议不支持");
        client.write(Buffer.from([5, 0]));
        const [nextVersion, command, zero, family] = await take(4);
        if (nextVersion !== 5 || command !== 1 || zero !== 0 || ![1, 3].includes(family)) throw new Error("协议不支持");
        const name = family === 1 ? [...await take(4)].join(".") :
          (await take((await take(1))[0])).toString("latin1").toLowerCase().replace(/\.$/, "");
        const port = (await take(2)).readUInt16BE();
        if (!port || !publicName(name)) throw new Error("目标不可用");
        remote = await connect(name, port);
        remote.on("error", () => client.destroy());
        if (client.destroyed) throw new Error("连接已关闭");
        sockets.add(remote);
        remote.once("close", () => { sockets.delete(remote); client.destroy(); });
        client.once("close", () => remote.destroy());
        await reader.return();
        client.setTimeout(0);
        client.write(Buffer.from([5, 0, 0, 1, 0, 0, 0, 0, 0, 0]));
        if (buffer.length) remote.write(buffer);
        client.pipe(remote);
        remote.pipe(client);
      } catch {
        remote?.destroy();
        client.destroy();
      }
    }
    void handshake();
  });
  server.maxConnections = 128;
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  return {
    port: server.address().port,
    close() { server.close(); for (const socket of sockets) socket.destroy(); },
  };
}
