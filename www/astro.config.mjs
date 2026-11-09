import { defineConfig } from 'astro/config';
export default defineConfig({
  site: process.env.SITE_URL || 'https://lee-xeo.github.io',
  base: process.env.SITE_BASE || '/Claude-Cage',
  trailingSlash: 'always',
  server: { port: 4822 },
  build: { inlineStylesheets: 'auto' },
});
