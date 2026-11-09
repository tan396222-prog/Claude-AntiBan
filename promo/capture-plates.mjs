// Capture the real workbench UI as the promo's Act 3 plates (plates/*.png, 2x).
// Serves the built UI from internal/workbench/web against a small mock API, so
// no VM is needed. Rebuild the UI first (make, or `npm run build` in ui/) when
// the workbench changes, then run `npm run plates`.
//
// Remote panes (Claude Desktop, the browser page) and terminal text are hidden;
// promo.js draws them on top of the plates.
import { createReadStream, existsSync, mkdirSync, statSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:http';
import { extname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { findChrome } from './chrome.mjs';

const here = fileURLToPath(new URL('.', import.meta.url));
const web = resolve(here, '..', 'internal', 'workbench', 'web');
const out = join(here, 'plates');
if (!existsSync(join(web, 'index.html'))) {
  console.error(`No built workbench UI at ${web}. Build it first (make, or npm run build in ui/).`);
  process.exit(1);
}

// ---------- mock API ----------
let scene = 'home';
const until = new Date(Date.now() + 864e5).toISOString();
const WS = '/workspace/hello-cage';
const dev = { id: 't2', kind: 'shell', workspace: WS, title: '开发终端' };
const claude = { id: 't1', kind: 'claude', workspace: WS, title: 'Claude 终端', claude_account_id: 'a1' };
const authorize = { id: 'b1', kind: 'browser', title: 'Authorize · Claude', url: 'https://claude.ai/oauth/authorize?code=true&client_id=claude-code' };
// The eleven critical checks, as the safety dialog lists them.
const CHECKS = ['VM 外出站约束', '终端检测通道', 'DNS 与解析约束', '系统时区与语言', '浏览器时区与语言', '共享文件与宿主私有目录',
  '宿主管理接口', '代理故障关闭', 'UDP 出站约束', 'IPv6 出站约束', '出口符合已保存规则'];
const created = '2026-11-05T06:30:12Z';
const report = state => ({
  created_at: created,
  checks: CHECKS.map((title, i) => ({ id: `c${i}`, title, critical: true, state, checked_at: created, evidence: '' })),
});
const safety = () => scene === 'safety-run'
  ? { gate: { mode: 'checking', until }, exit_ip: '203.0.113.8', report: report('unknown') }
  : { gate: { mode: 'open', until }, exit_ip: '203.0.113.8', ...(scene === 'safety-done' ? { report: report('passed') } : {}) };
const job = () => scene === 'safety-run'
  ? { id: 'j1', running: true, kind: 'safety', message: '正在核对网络与隔离…', error: '' }
  : { id: 'j0', running: false, kind: 'start', message: '', error: '' };
const routes = {
  'GET /api/settings': () => ({ timezone: 'America/Los_Angeles', auto_timezone: true, browser_home_url: 'https://claude.ai/' }),
  'GET /api/proxy': () => ({ active: true, selected: 'la-01', groups: [], sources: [] }),
  'GET /api/account': () => ({ exists: true, unlocked: true, username: 'lee', devices: {}, require_authentication: false }),
  'GET /api/status': () => ({
    ready: true, vm_running: true, workspace: WS, environment_workspace: '/workspace', environment_directory: '/workspace',
    projects: [WS, '/workspace/api-server'], project_errors: [], can_restore: false,
    job: job(),
    location: { ip: '203.0.113.8', country: 'US', city: 'Los Angeles' },
    safety: safety(), keyboard: {},
  }),
  'GET /api/browser/tabs': () => (scene === 'browser' ? { tabs: [authorize], active: 'b1' } : { tabs: [], active: '' }),
  'GET /api/terminals': () => (scene === 'term' || scene === 'browser' ? [dev, claude] : [dev]),
  'GET /api/claude-desktop': () => ({ installed: true, running: scene === 'desktop', version: '1.2.0', managed: true }),
  'GET /api/favorites': () => ({ items: [], folders: [] }),
  'GET /api/claude-accounts': () => ({ accounts: [{ id: 'a1', label: '工作', email: 'work@example.com', enabled: true, status: 'ready', plan: 'Max', source: 'login' }] }),
  'GET /api/browser/files/saved': () => [],
  'GET /api/input/files/desktop/requests': () => ({ uploads: [] }),
};
const TYPES = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.png': 'image/png', '.svg': 'image/svg+xml' };
const server = createServer((req, res) => {
  const url = new URL(req.url, 'http://x');
  if (url.pathname.startsWith('/api/')) {
    const route = routes[`${req.method} ${url.pathname}`];
    res.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(route ? route() : {}));
    return;
  }
  let file = join(web, url.pathname === '/' ? 'index.html' : url.pathname);
  if (!file.startsWith(web) || !existsSync(file) || !statSync(file).isFile()) file = join(web, 'index.html');
  res.writeHead(200, { 'content-type': TYPES[extname(file)] || 'application/octet-stream' });
  createReadStream(file).pipe(res);
});
await new Promise(ok => server.listen(0, '127.0.0.1', ok));
const origin = `http://127.0.0.1:${server.address().port}`;

// ---------- scenes ----------
const hide = `.view-overlay,.terminal-note,.remote-canvas,.xterm{visibility:hidden!important}
*:focus,*:focus-visible{outline:none!important;box-shadow:none!important}`;
const press = label => `(() => {
  const b = ${JSON.stringify(label)}.startsWith('.') ? document.querySelector(${JSON.stringify(label)})
    : [...document.querySelectorAll('button')].find(b => b.getAttribute('aria-label') === ${JSON.stringify(label)} || b.textContent.trim().startsWith(${JSON.stringify(label)}));
  if (!b) throw new Error('button not found: ' + ${JSON.stringify(label)});
  b.click();
})()`;
const scenes = [
  ['home', 'home', []],
  ['desktop', 'desktop', ['Claude Desktop']],
  ['menu', 'desktop', ['Claude Desktop', '在 hello-cage 新建终端']],
  ['term', 'term', ['Claude 终端']],
  ['browser', 'browser', ['打开浏览器']],
  ['safety-run', 'safety-run', ['.sidebar-status']],
  ['safety-done', 'safety-done', ['.sidebar-status']],
];
const wait = ms => new Promise(ok => setTimeout(ok, ms));
// Landscape matches the 16:9 cut; portrait is a tall window the vertical cut can show at full width.
const sizes = [
  ['landscape', 1440, 860, out],
  ['portrait', 760, 1100, join(out, 'portrait')],
];
// What promo.js needs to know about each plate, measured from the live DOM.
const measure = () => {
  const box = el => { if (!el) return null; const r = el.getBoundingClientRect(); return [r.left, r.top, r.width, r.height].map(v => Math.round(v * 10) / 10); };
  const button = text => [...document.querySelectorAll('button')].find(b => b.getAttribute('aria-label') === text || b.textContent.trim().startsWith(text));
  const status = document.querySelector('.status-bar');
  return {
    sidebar: box(document.querySelector('.workspace-sidebar')),
    header: box(document.querySelector('.content-heading, .claude-desktop-toolbar')),
    status: box(status),
    statusItems: [...status.querySelectorAll(':scope > button')].filter(b => b.offsetParent).slice(0, 3).map(box),
    shield: box(document.querySelector('.sidebar-status')),
    desktopNav: box(button('Claude Desktop')),
    termIcon: box(button('在 hello-cage 新建终端')),
    claudeItem: box(document.querySelector('.terminal-create-menu button:nth-child(2)')),
    navigation: box(document.querySelector('.navigation-row')),
    dialog: box(document.querySelector('[role="dialog"]')),
    summary: box(document.querySelector('.safety-summary')),
    count: box(document.querySelector('.safety-section-heading > span')),
    rows: [...document.querySelectorAll('.safety-check > summary')].map(box),
  };
};

const { default: puppeteer } = await import('puppeteer-core');
const browser = await puppeteer.launch({ executablePath: findChrome(), headless: true, args: ['--hide-scrollbars', '--force-color-profile=srgb'] });
const layout = {};
try {
  for (const [orientation, width, height, dir] of sizes) {
    mkdirSync(dir, { recursive: true });
    layout[orientation] = { width, height };
    for (const [name, state, steps] of scenes) {
      scene = state;
      const page = await browser.newPage();
      await page.setViewport({ width, height, deviceScaleFactor: 2 });
      await page.emulateTimezone('Asia/Shanghai');
      await page.goto(origin + '/', { waitUntil: 'networkidle0' });
      await page.addStyleTag({ content: hide });
      await wait(1500);
      for (const label of steps) { await page.evaluate(press(label)); await wait(900); }
      if (name === 'menu') {
        // Hover the Claude terminal item, as the cursor is about to click it.
        await page.evaluate(() => {
          document.querySelector('.terminal-create-menu button:nth-child(2)').style.background = 'var(--hover)';
          document.activeElement?.blur();
        });
      }
      await wait(400);
      await page.screenshot({ path: join(dir, `${name}.png`) });
      layout[orientation][name] = await page.evaluate(measure);
      console.log(join(dir, `${name}.png`));
      await page.close();
    }
  }
  writeFileSync(join(out, 'layout.js'), `// Generated by capture-plates.mjs: where things sit on each plate, in workbench pixels.\nwindow.PLATE_LAYOUT = ${JSON.stringify(layout)};\n`);
} finally {
  await browser.close();
  server.close();
}
