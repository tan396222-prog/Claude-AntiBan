import http from "node:http";
import path from "node:path";
import { writeFile } from "node:fs/promises";

// Isolated GUI-session QA only. No request opens an upstream connection.
const directory = process.argv[2];
if (!directory || !path.isAbsolute(directory)) throw new Error("An absolute QA directory is required.");
const privateHost = process.argv[3];
const counts = { http: 0, connect: 0, public_probe: 0, private_probe: 0, connect_probe: 0 };
let recording = Promise.resolve();
const record = (kind, address) => {
  counts[kind]++;
  try {
    const host = new URL(kind === "connect" ? "https://" + address : address).hostname;
    if (kind === "connect" && host === "example.com") counts.connect_probe++;
    else if (host === "example.com") counts.public_probe++;
    else if (privateHost && host === privateHost) counts.private_probe++;
  } catch {}
  recording = recording.then(() => writeFile(path.join(directory, "deny-proxy-counts.json"), JSON.stringify(counts)));
};
const server = http.createServer((request, response) => {
  record("http", request.url);
  response.writeHead(503, { "Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store" });
  response.end("Cage local browser QA: upstream HTTP requests are blocked.");
});
server.on("connect", (request, socket) => {
  record("connect", request.url);
  socket.end("HTTP/1.1 503 Service Unavailable\r\nConnection: close\r\nContent-Length: 0\r\n\r\n");
});
server.listen(0, "127.0.0.1", async () => {
  await writeFile(path.join(directory, "deny-proxy-port"), String(server.address().port));
  process.stdout.write("CAGE_QA_DENY_PROXY_READY\n");
});
for (const signal of ["SIGTERM", "SIGINT"]) process.on(signal, () => server.close(() => process.exit(0)));
