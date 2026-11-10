import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { createWorkbench, chooseInline } from './workbench-fixture.mjs';

const engine = process.env.CAGE_TEST_BROWSER || 'chromium';
const output = new URL(`../../docs/test-reports/2026-11-07-browser-close/${engine}/`, import.meta.url);
await mkdir(output, { recursive: true });
const { browser, page, state, errors } = await createWorkbench();
page.setDefaultTimeout(10000);
const checks = [];
const close = page.getByRole('button', { name: '关闭浏览器', exact: true });
const open = page.getByRole('button', { name: '打开浏览器', exact: true });
const home = page.getByRole('heading', { name: 'Cage 环境已就绪', exact: true });
const shot = name => page.screenshot({ path: fileURLToPath(new URL(name + '.png', output)) });
try {
  await page.addInitScript(() => {
    window.EventSource = class extends EventTarget {
      constructor() { super(); window.closeBrowserEvents = this; queueMicrotask(() => this.onopen?.()); }
      close() {}
    };
  });
  await page.route('**/api/claude-desktop', route => route.fulfill({ json: {
    installed: true, running: true, failed: false, managed: true, version: 'fixture',
  } }));
  state.status.ready = state.status.vm_running = true;
  state.tabs = [
    { id: 'page-one', title: '网页一', kind: 'web', url: 'https://example.test/one' },
    { id: 'page-two', title: '网页二', kind: 'web', url: 'https://example.test/two' },
  ];
  state.active = 'page-one';
  state.terminals = [
    { id: 'terminal-one', title: '工作终端一', workspace: state.settings.workspace },
    { id: 'terminal-two', title: '工作终端二', workspace: state.settings.workspace },
  ];
  await page.reload();
  await home.waitFor();
  const send = async extra => page.evaluate(snapshot => {
    window.closeBrowserEvents.onmessage({ data: JSON.stringify(snapshot) });
  }, { tabs: state.tabs, active: state.active, ...extra });
  await open.click();
  await close.waitFor();
  assert.equal(await close.count(), 1);
  const address = await page.locator('.navigation-row').boundingBox();
  const x = await close.boundingBox();
  assert.ok(x.y < address.y && x.x > address.x + address.width * 0.85, 'X sits at the top right');
  await page.getByRole('button', { name: '搜索标签页', exact: true }).click();
  const withList = await close.boundingBox();
  assert.ok(withList.y < (await page.locator('.navigation-row').boundingBox()).y, 'X stays in top row while tab search is expanded');
  await shot('horizontal-close');
  await close.focus(); await page.keyboard.press('Enter');
  await home.waitFor();
  assert.equal(await page.locator('.browser-lane').count(), 0);
  assert.equal(state.tabs.length, 2);
  await send({ focus: true });
  await home.waitFor();
  assert.equal(await page.locator('.browser-lane').count(), 0);
  checks.push('top-right X works by keyboard and with tab search expanded; returns to home; tabs retained; late focus does not reopen browser');

  await page.getByRole('tab', { name: '工作终端一', exact: true }).click();
  await page.locator('.terminal-lane:not([hidden])').waitFor();
  await open.click();
  await page.getByRole('tab', { name: '网页二', exact: true }).click();
  await close.click();
  await page.locator('.terminal-lane:not([hidden])').waitFor();
  assert.equal(await page.getByRole('tab', { name: '工作终端一', exact: true }).getAttribute('aria-selected'), 'true');
  await open.click();
  assert.equal(await page.getByRole('tab', { name: '网页二', exact: true }).getAttribute('aria-selected'), 'true');
  await page.waitForFunction(() => document.querySelector('.browser-tabs [role="tab"]')?.getBoundingClientRect().width >= 100);
  const source = await page.getByRole('tab', { name: '网页一', exact: true }).boundingBox();
  const body = await page.locator('.browser-lane .pane-body').boundingBox();
  await page.mouse.move(source.x + source.width / 2, source.y + source.height / 2);
  await page.mouse.down();
  await page.mouse.move(body.x + body.width * 0.8, body.y + body.height / 2, { steps: 12 });
  await page.mouse.up();
  await page.waitForFunction(() => document.querySelectorAll('.browser-header-cell').length === 2);
  assert.equal(await close.count(), 1, 'dual-browser layout has one workspace close button');
  await close.click();
  await page.locator('.terminal-lane:not([hidden])').waitFor();
  checks.push('returns to the original terminal after switching browser tabs or closing both browser panes; reopening resumes the selected browser tab');

  await page.getByRole('button', { name: 'Claude Desktop', exact: true }).click();
  const desktop = page.getByRole('region', { name: 'Claude Desktop', exact: true });
  await desktop.waitFor();
  await open.click();
  await close.click();
  await desktop.waitFor();
  let releaseNewPage;
  const pendingNewPage = new Promise(resolve => { releaseNewPage = resolve; });
  await page.route('**/api/browser/new', async route => { await pendingNewPage; await route.fallback(); });
  const newRequest = page.waitForRequest('**/api/browser/new');
  await page.getByRole('button', { name: 'claude.ai', exact: true }).click();
  await newRequest;
  await desktop.waitFor();
  assert.equal(await close.count(), 0, 'pending creation preserves the previous view');
  releaseNewPage();
  await close.waitFor();
  await close.click();
  await desktop.waitFor();
  checks.push('returns to Claude Desktop after opening existing tabs or asynchronously creating a new page; pending creation preserves the previous view');

  await open.click();
  await page.getByRole('button', { name: '浏览器菜单', exact: true }).click();
  await page.getByRole('menuitem', { name: '设置', exact: true }).click();
  const settings = page.getByRole('region', { name: '浏览器设置', exact: true });
  await settings.getByRole('navigation', { name: '浏览器设置分类' }).getByRole('button', { name: '外观', exact: true }).click();
  await chooseInline(page.getByRole('combobox', { name: '标签栏布局' }), 'vertical');
  await page.getByRole('button', { name: '关闭 设置', exact: true }).click();
  await page.setViewportSize({ width: 820, height: 558 });
  await page.getByRole('complementary', { name: '竖排标签栏' }).waitFor();
  await close.waitFor();
  const navigation = await page.locator('.navigation-row').boundingBox();
  const verticalX = await close.boundingBox();
  assert.ok(verticalX.x + verticalX.width <= 820 && verticalX.y >= navigation.y && verticalX.y < navigation.y + navigation.height);
  await shot('vertical-close-minimum');
  await close.click();
  await desktop.waitFor();
  checks.push('vertical tabs show X at the right of the address row at 820×558; settings do not replace the return destination');

  // If the previous terminal ends while browsing, return to home safely.
  await page.getByRole('tab', { name: '工作终端二', exact: true }).click();
  await open.click();
  state.terminals = state.terminals.filter(tab => tab.id !== 'terminal-two');
  await page.waitForResponse(response => response.url().endsWith('/api/terminals') && response.request().method() === 'GET');
  await page.getByRole('tab', { name: '工作终端二', exact: true }).waitFor({ state: 'hidden' });
  await close.click();
  await home.waitFor();
  assert.equal(state.requests.some(request => request.path === '/stop' || (request.path === '/browser/close' && request.body.id !== 'browser-settings')), false);
  checks.push('ended return terminal falls back to home; closing browser never stops environment or closes individual tabs');

  const singlePage = async () => {
    state.tabs = [{ id: 'last-page', title: '最后一页', kind: 'web', url: 'https://example.test/last' }];
    state.active = 'last-page';
    await send();
    await open.click();
    await page.getByRole('tab', { name: '最后一页', exact: true }).waitFor();
  };
  await singlePage();
  await page.getByRole('button', { name: '关闭 最后一页', exact: true }).click();
  await home.waitFor();
  assert.equal(state.tabs.length, 0);
  assert.equal(await close.count(), 0);
  await send({ focus: true });
  await home.waitFor();
  await open.click();
  await close.waitFor();
  assert.equal(state.tabs.length, 1, 'reopening an empty browser creates one page');
  checks.push('closing the last tab exits to home without a replacement; empty events stay closed; reopening creates one page');
  await close.click();

  await page.getByRole('tab', { name: '工作终端一', exact: true }).click();
  await singlePage();
  await page.keyboard.press('Meta+w');
  await page.locator('.terminal-lane:not([hidden])').waitFor();
  assert.equal(await page.getByRole('tab', { name: '工作终端一', exact: true }).getAttribute('aria-selected'), 'true');
  assert.equal(state.tabs.length, 0);
  await send();
  await page.getByRole('button', { name: 'Claude Desktop', exact: true }).click();
  await singlePage();
  // Destruction can arrive before the close request completes.
  let releaseClose;
  const pendingClose = new Promise(resolve => { releaseClose = resolve; });
  await page.route('**/api/browser/close', async route => {
    state.tabs = []; state.active = '';
    await send();
    await pendingClose;
    await route.fulfill({ json: { ok: true } });
  });
  await page.getByRole('button', { name: '关闭 最后一页', exact: true }).click();
  await desktop.waitFor();
  const closeResponse = page.waitForResponse('**/api/browser/close');
  releaseClose();
  await closeResponse;
  await desktop.waitFor();
  await page.unroute('**/api/browser/close');
  checks.push('last-tab keyboard close restores the terminal; a destruction event before the close response restores Claude Desktop');

  await singlePage();
  await page.route('**/api/browser/close', route => route.fulfill({ status: 400, body: '标签尚未关闭，请重试' }));
  await page.getByRole('button', { name: '关闭 最后一页', exact: true }).click();
  await page.getByText('标签尚未关闭，请重试', { exact: true }).waitFor();
  assert.equal(await close.count(), 1, 'failed close preserves the browser');
  await page.unroute('**/api/browser/close');
  state.tabs = []; state.active = '';
  await send();
  await desktop.waitFor();
  assert.equal(await close.count(), 0);
  checks.push('failed last-tab close preserves the page; external last-tab destruction also exits the browser');

  await singlePage();
  state.tabs = []; state.active = '';
  await page.evaluate(() => window.closeBrowserEvents.onerror());
  await desktop.waitFor();
  assert.equal(await close.count(), 0);
  checks.push('fallback polling detects the last-tab removal and restores the previous view');

  await singlePage();
  await close.click();
  await page.getByRole('tab', { name: '工作终端一', exact: true }).click();
  state.tabs = []; state.active = '';
  await send();
  assert.equal(await page.getByRole('tab', { name: '工作终端一', exact: true }).getAttribute('aria-selected'), 'true');
  assert.equal(await close.count(), 0);
  checks.push('background last-tab destruction preserves the currently selected terminal');
  assert.deepEqual(errors, []);
  await writeFile(new URL('result.json', output), JSON.stringify({ passed: true, engine, checks }, null, 2) + '\n');
  console.log(JSON.stringify({ passed: true, engine, checks }, null, 2));
} catch (error) {
  await shot('failure');
  throw error;
} finally { await browser.close(); }
