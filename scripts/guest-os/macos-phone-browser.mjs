// The phone owns a separate browser profile and the shared public-web policy.
import { homedir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { PhoneBrowser } from "./modules/browser-phone.mjs";
import { phoneError } from "./modules/browser-phone-network.mjs";
import { lines } from "./windows-browser.mjs";

export async function runMacOSPhoneBrowser(input = process.stdin, output = process.stdout, options = {}) {
  if (process.platform !== "darwin" && options.testing !== true) throw new Error("此浏览器需要 macOS 客机");
  const phone = new PhoneBrowser({
    binary: options.binary || "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    directory: options.directory || path.join(homedir(), "Library", "Application Support", "Cage", "PhoneBrowser"),
    language: options.language || Intl.DateTimeFormat().resolvedOptions().locale,
    connect: options.connect,
  });
  // This controller is private to one paired phone. Keep its cold launch
  // alive between frame polls, while close, SSH EOF and timeout can stop it.
  let opening;
  async function cancelOpen() {
    if (opening) {
      opening.abort.abort();
      await opening.done;
      opening = undefined;
    }
    await phone.dispose();
  }
  async function operate(operation, input) {
    if (opening && input?.id !== opening.id) throw phoneError(410, "手机浏览器会话已结束");
    if (opening && operation === "close") {
      await cancelOpen();
      return { id: input.id };
    }
    if (!opening && operation === "open") {
      const entry = { id: input?.id, abort: new AbortController(), settled: false };
      opening = entry;
      const deadline = setTimeout(() => entry.abort.abort(), 75000);
      entry.done = phone.run("open", input, entry.abort.signal).catch(error => {
        entry.error = entry.abort.signal.aborted ? phoneError(503, "客机浏览器启动超时") : error;
      }).finally(() => {
        entry.settled = true;
        clearTimeout(deadline);
      });
      // Return quick URL/parameter rejections on the original open request.
      let timer;
      try { await Promise.race([entry.done, new Promise(resolve => { timer = setTimeout(resolve, 200); })]); }
      finally { clearTimeout(timer); }
    }
    if (opening) {
      if (opening.settled) {
        const { error } = opening;
        opening = undefined;
        if (error) throw error;
      } else if (operation !== "open") {
        throw phoneError(425, "正在启动虚拟 Mac 中的浏览器");
      }
      if (operation === "open") return { id: input.id };
    }
    return phone.run(operation, input);
  }
  try {
    for await (const line of lines(input)) {
      let result;
      try {
        const request = JSON.parse(line);
        if (request.method !== "POST" || !/^\/phone-browser\/(open|frame|action|close)$/.test(request.path || "")) throw new Error("手机浏览器请求无效");
        result = await operate(request.path.split("/").at(-1), request.input);
      } catch (error) {
        result = { error: "手机浏览器操作未完成，请核对授权和最新画面", code: "phone_browser_error", status: [403, 409, 410, 425, 503].includes(error.status) ? error.status : 400 };
      }
      await new Promise((resolve, reject) => output.write(JSON.stringify(result) + "\n", error => error ? reject(error) : resolve()));
    }
  } finally { await cancelOpen(); }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  await runMacOSPhoneBrowser().catch(() => { process.exitCode = 1; });
}
