import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, mkdir } from 'node:fs/promises';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.CAGE_SITE_URL || 'http://127.0.0.1:4833/Claude-Cage/';
const output = process.env.CAGE_SITE_REPORT || '_output/platform-publication/site';
const release = JSON.parse(await readFile(new URL('../src/data/release.json', import.meta.url), 'utf8'));
const expected = {
  macOS: release.downloadURL,
  Windows: release.windowsDownloadURL || release.windowsPreview?.downloadURL,
  Linux: release.linuxDownloadURL,
};
for (const [platform, url] of Object.entries(expected)) assert.ok(url, `${platform} must have a verified public installer`);
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true });
try {
  for (const { route, width, height } of [{ route: '', width: 1280, height: 900 }, { route: '', width: 390, height: 844 }, { route: 'fr/', width: 1280, height: 900 }, { route: 'ar/', width: 390, height: 844 }]) {
    const page = await browser.newPage({ viewport: { width, height } });
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(new URL(route, origin).href);
    await page.locator('[data-download-open]').first().click();
    const dialog = page.getByRole('dialog');
    await dialog.waitFor({ state: 'visible' });
    for (const [platform, url] of Object.entries(expected)) {
      const option = dialog.locator(`[data-platform="${platform}"]`);
      await option.getByRole('heading', { name: platform, exact: true }).waitFor();
      assert.equal(await option.locator('a.button').getAttribute('href'), url);
    }
    if (!release.windowsDownloadURL) {
      const status = await dialog.locator('[data-platform="Windows"] .platform-status').textContent();
      assert.ok(status.trim() && !status.trim().startsWith('.exe'), 'unsigned Windows requires an explicit label');
      if (!route) assert.match(status, /未签名/);
    }
    const bounds = await dialog.boundingBox();
    assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width + 1, 'download chooser overflows viewport');
    await page.screenshot({ path: `${output}/${route.replaceAll('/', '') || 'zh'}-${width}.png` });
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'hidden' });
    await page.goto(new URL(`${route}#download`, origin).href);
    await dialog.waitFor({ state: 'visible' });
    await dialog.getByRole('button').click();
    await dialog.waitFor({ state: 'hidden' });
    assert.deepEqual(errors, []);
    await page.close();
  }
  const context = await browser.newContext({ javaScriptEnabled: false });
  const page = await context.newPage(); await page.goto(origin);
  for (const url of Object.values(expected)) await page.locator(`noscript a[href="${url}"]`).waitFor({ state: 'visible' });
  await context.close();
  console.log('PASS: three public installer links, unsigned Windows label, desktop/mobile/RTL layout, modal open/close, deep link and no-JavaScript downloads');
} finally { await browser.close(); }
