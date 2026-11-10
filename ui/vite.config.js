import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "../internal/workbench/web",
    emptyOutDir: true,
    rolldownOptions: { input: { desktop: "index.html", mobile: "mobile.html" } },
  },
  server: { proxy: { "/api": { target: "http://localhost:17777", ws: true } } },
});
