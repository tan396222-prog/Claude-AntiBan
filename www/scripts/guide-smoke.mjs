import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdir } from 'node:fs/promises';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.CAGE_PLAYWRIGHT_MODULE || 'playwright');
const origin = process.env.CAGE_SITE_URL || 'http://127.0.0.1:4822/Claude-Cage/';
const output = process.env.CAGE_SITE_REPORT || '_output/docs-website-20261107/shots';
await mkdir(output, { recursive: true });
const browser = await chromium.launch({ headless: true });
const errors = [];
try {
  for (const width of [1280, 390]) {
    const page = await browser.newPage({ viewport: { width, height: width === 390 ? 844 : 900 } });
    page.on('pageerror', error => errors.push(error.message));
    await page.goto(origin);
    await page.locator('header nav').getByRole('link', { name: '使用文档', exact: true }).click();
    await page.getByRole('heading', { level: 1, name: '从这里开始' }).waitFor();
    assert.equal(await page.locator('body').evaluate(body => body.scrollWidth <= innerWidth + 1), true, `${width}: guide overflows`);
    await page.screenshot({ path: `${output}/guide-${width}.png` });
    const search = page.getByRole('searchbox', { name: '搜索文档' });
    await search.fill('多账号');
    await page.locator('#search-list').getByRole('link', { name: '多账号与额度', exact: true }).waitFor();
    assert.equal(await page.locator('#guide-article').isVisible(), false);
    await page.screenshot({ path: `${output}/search-${width}.png` });
    await page.locator('#search-list').getByRole('link', { name: '多账号与额度', exact: true }).click();
    await page.getByRole('heading', { level: 1, name: '多账号与额度' }).waitFor();
    await page.getByRole('navigation', { name: '本页目录' }).getByRole('link', { name: '自动分配什么时候发生' }).click();
    assert.ok(decodeURIComponent(new URL(page.url()).hash).includes('自动分配什么时候发生'));
    await page.reload();
    await page.getByRole('heading', { level: 2, name: '自动分配什么时候发生' }).waitFor();
    await page.screenshot({ path: `${output}/accounts-${width}.png` });
    await search.fill('不存在的功能xyz987');
    await page.getByRole('status').filter({ hasText: '没有找到相关文档' }).waitFor();
    await page.screenshot({ path: `${output}/empty-${width}.png` });
    await page.getByRole('button', { name: '返回正文' }).click();
    assert.equal(await page.locator('#guide-article').isVisible(), true);
    await search.fill('手机 配对');
    await page.locator('#search-list').getByRole('link', { name: '手机连接与远程会话', exact: true }).waitFor();
    await search.press('Escape');
    assert.equal(await search.inputValue(), '');
    if (width === 390) {
      await page.evaluate(() => scrollTo(0, 0));
      const menu = page.locator('.guide-mobile-menu');
      const title = page.locator('.guide-title');
      const before = (await title.boundingBox()).y;
      await menu.locator('summary').focus();
      await page.keyboard.press('Enter');
      assert.ok((await title.boundingBox()).y > before + 250, 'mobile menu must occupy layout space');
      await page.screenshot({ path: `${output}/menu-${width}.png` });
      await page.keyboard.press('Escape');
      assert.equal(await menu.getAttribute('open'), null);
      assert.ok(Math.abs((await title.boundingBox()).y - before) < 2, 'menu must restore layout');
      await menu.locator('summary').press('Enter');
      await menu.getByRole('link', { name: '手机连接与远程会话' }).click();
      await page.getByRole('heading', { level: 1, name: '手机连接与远程会话' }).waitFor();
      assert.equal(await page.locator('body').evaluate(body => body.scrollWidth <= innerWidth + 1), true, 'mobile article overflows');
    } else {
      // At browser zoom 200%, a 1280px window exposes a 640px CSS viewport.
      await page.setViewportSize({ width: 640, height: 450 });
      await page.evaluate(() => scrollTo(0, 0));
      assert.equal(await page.locator('body').evaluate(body => body.scrollWidth <= innerWidth + 1), true, '200% zoom overflows');
      await page.screenshot({ path: `${output}/zoom-200.png` });
    }
    await page.close();
  }
  const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width: 390, height: 844 } });
  const page = await context.newPage();
  await page.goto(new URL('docs/', origin).href);
  await page.getByRole('heading', { level: 1, name: '从这里开始' }).waitFor();
  assert.equal(await page.locator('.guide-search').isVisible(), false);
  await page.locator('.guide-mobile-menu summary').click();
  await page.locator('.guide-mobile-menu').getByRole('link', { name: '故障排查', exact: true }).click();
  await page.getByRole('heading', { level: 1, name: '故障排查' }).waitFor();
  await page.screenshot({ path: `${output}/no-javascript.png` });
  await context.close();
  assert.deepEqual(errors, []);
  console.log('PASS: homepage entry, desktop/mobile guide, full-text search, no results/recovery, cross-page and hash navigation, inline mobile menu keyboard/layout, 200% zoom, no-JavaScript reading');
} finally { await browser.close(); }
