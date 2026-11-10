import { writeFile } from "node:fs/promises";
import { startPhoneProxy } from "/usr/local/lib/browser-phone-network.mjs";
const proxy = await startPhoneProxy();
await writeFile(process.env.XDG_RUNTIME_DIR + "/proxy", String(proxy.port), { mode: 0o600 });
