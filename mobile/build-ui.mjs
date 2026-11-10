// Build only the phone entry and its imported assets into the app bundle.
import { createRequire } from "node:module";
import { fileURLToPath, pathToFileURL } from "node:url";
import { resolve } from "node:path";

const ui = fileURLToPath(new URL("../ui/", import.meta.url));
const require = createRequire(new URL("../ui/package.json", import.meta.url));
const { build } = await import(pathToFileURL(require.resolve("vite")));
const { default: react } = await import(pathToFileURL(require.resolve("@vitejs/plugin-react")));
await build({
  root: ui,
  configFile: false,
  publicDir: false,
  plugins: [react()],
  build: {
    outDir: fileURLToPath(new URL("./.build/resources/mobile-ui/", import.meta.url)),
    emptyOutDir: true,
    rolldownOptions: { input: resolve(ui, "mobile.html") },
  },
});
