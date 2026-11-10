import http from "node:http";
import { mkdir, rm } from "node:fs/promises";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { createNativeBrowserService, lines } from "../windows-browser.mjs";

const [root, directory] = process.argv.slice(2);
if (!root || !directory || !path.isAbsolute(root) || !path.isAbsolute(directory)) throw new Error("Absolute test directories are required.");
const source = name => pathToFileURL(path.join(root, "internal", "cage", name));
const { BrowserClipboard } = await import(source("browser-clipboard.mjs"));
const { UploadManager } = await import(source("browser-uploads.mjs"));
const { DownloadManager } = await import(source("browser-downloads.mjs"));
const { browserCookies } = await import(source("browser-cookies.mjs"));
const { BrowserVault } = await import(source("browser-vault.mjs"));
const { chromium } = await import(pathToFileURL(process.env.CAGE_BROWSER_TEST_PLAYWRIGHT));
const payload = Buffer.alloc(131089);
for (let index = 0; index < payload.length; index++) payload[index] = index % 251;
const server = http.createServer((request, response) => {
  if (request.url === "/download") {
    response.setHeader("Content-Disposition", 'attachment; filename="native-download.bin"');
    response.end(payload);
    return;
  }
  response.setHeader("Content-Type", "text/html; charset=utf-8");
  response.end('<!doctype html><a href="/download">Download test file</a>');
});
await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
await mkdir(directory, { recursive: true });
let browser;
const service = createNativeBrowserService({
  platform: "darwin", testing: true, headless: true,
  directory: path.join(directory, "controller"), downloadDirectory: path.join(directory, "guest-saved"),
  executablePath: process.env.CAGE_BROWSER_TEST_CHROMIUM,
  chromium: { launchPersistentContext: async (...args) => browser = await chromium.launchPersistentContext(...args) },
  modules: { BrowserClipboard, UploadManager, DownloadManager, browserCookies, BrowserVault },
});
try {
  await service.request({ operation: "configure", input: { proxy: "http://127.0.0.1:18501" } });
  await service.request({ method: "POST", path: "/new", input: { url: `http://127.0.0.1:${server.address().port}/` } });
  const page = browser.pages().find(item => item.url().startsWith("http://127.0.0.1:"));
  await page.getByRole("link", { name: "Download test file" }).click();
  for await (const line of lines(process.stdin)) {
    const result = await service.request(JSON.parse(line));
    await new Promise((resolve, reject) => process.stdout.write(JSON.stringify(result) + "\n", error => error ? reject(error) : resolve()));
  }
} finally {
  await service.close();
  await new Promise(resolve => server.close(resolve));
  await rm(directory, { recursive: true, force: true });
}
