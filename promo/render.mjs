// Render the promo to MP4: serve the repo, step the page frame by frame in
// headless Chrome, and pipe the frames plus the soundtrack into ffmpeg.
//
//   node render.mjs                     full 1920×1080 @ 60 fps → out/claude-cage-promo.mp4
//   node render.mjs --fps 30 --scale 0.5   quick draft
//   node render.mjs --frames 12,20.5,33    stills only → out/frame-*.png
//   node render.mjs --portrait             9:16 cut → out/claude-cage-promo-vertical.mp4 (combines with the flags above)
//   node render.mjs --preview           serve the page for live preview
//
// Chrome: set CHROME_PATH, otherwise the usual install locations on macOS,
// Windows and Linux are tried.
import { spawn } from 'node:child_process';
import { createReadStream, existsSync, mkdirSync, statSync } from 'node:fs';
import { createServer } from 'node:http';
import { createRequire } from 'node:module';
import { extname, join, normalize, resolve, sep } from 'node:path';
import { fileURLToPath } from 'node:url';
import { findChrome } from './chrome.mjs';

const require = createRequire(import.meta.url);
const here = fileURLToPath(new URL('.', import.meta.url));
const root = resolve(here, '..');
const outDir = join(here, 'out');
const TL = require('./timeline.js');

const args = process.argv.slice(2);
const opt = (name, fallback) => {
  const i = args.indexOf(`--${name}`);
  return i >= 0 ? args[i + 1] : fallback;
};
const fps = Number(opt('fps', TL.fps));
const scale = Number(opt('scale', 1));
const portrait = args.includes('--portrait');
const [W, H] = portrait ? [1080, 1920] : [TL.width, TL.height];
const output = resolve(opt('out', join(outDir, portrait ? 'claude-cage-promo-vertical.mp4' : 'claude-cage-promo.mp4')));
const audio = resolve(opt('audio', join(outDir, 'soundtrack.wav')));

const TYPES = {
  '.html': 'text/html; charset=utf-8', '.js': 'text/javascript; charset=utf-8', '.mjs': 'text/javascript; charset=utf-8',
  '.css': 'text/css', '.png': 'image/png', '.jpg': 'image/jpeg', '.svg': 'image/svg+xml', '.json': 'application/json',
  '.mp4': 'video/mp4', '.wav': 'audio/wav',
};

function serve(port = 0) {
  const server = createServer((req, res) => {
    const path = decodeURIComponent(new URL(req.url, 'http://x').pathname);
    const file = normalize(join(root, path.endsWith('/') ? path + 'index.html' : path));
    if (!(file + sep).startsWith(root + sep) || !existsSync(file) || !statSync(file).isFile()) {
      res.writeHead(404).end('not found');
      return;
    }
    res.writeHead(200, { 'content-type': TYPES[extname(file)] || 'application/octet-stream', 'cache-control': 'no-store' });
    createReadStream(file).pipe(res);
  });
  return new Promise(ok => server.listen(port, '127.0.0.1', () => ok(server)));
}

async function openPage(server) {
  const { default: puppeteer } = await import('puppeteer-core');
  const browser = await puppeteer.launch({
    executablePath: findChrome(),
    headless: true,
    args: ['--hide-scrollbars', '--force-color-profile=srgb', '--font-render-hinting=none', '--disable-lcd-text'],
  });
  const page = await browser.newPage();
  await page.setViewport({ width: W, height: H, deviceScaleFactor: scale });
  const { port } = server.address();
  page.on('pageerror', e => console.error('page error:', e.message));
  await page.goto(`http://127.0.0.1:${port}/promo/index.html?render${portrait ? '&portrait' : ''}`, { waitUntil: 'networkidle0', timeout: 120000 });
  await page.evaluate(() => window.__promo.ready);
  // Touch every act once so lazily loaded font subsets are in before frame 0.
  for (let t = 0; t < TL.duration; t += 0.5) await page.evaluate(x => window.__promo.seek(x), t);
  await page.evaluate(() => document.fonts.ready);
  return { browser, page };
}

async function stills(times) {
  const server = await serve();
  const { browser, page } = await openPage(server);
  mkdirSync(outDir, { recursive: true });
  for (const t of times) {
    await page.evaluate(x => window.__promo.seek(x), t);
    const path = join(outDir, `frame-${portrait ? 'v-' : ''}${t.toFixed(2)}.png`);
    await page.screenshot({ path, type: 'png' });
    console.log(path);
  }
  await browser.close();
  server.close();
}

async function video() {
  const ffmpeg = require('ffmpeg-static');
  const server = await serve();
  const { browser, page } = await openPage(server);
  mkdirSync(outDir, { recursive: true });
  const total = Math.round(TL.duration * fps);
  const hasAudio = existsSync(audio);
  if (!hasAudio) console.warn(`No soundtrack at ${audio}; run "node soundtrack.mjs" first. Rendering silent video.`);
  const w = Math.round(W * scale), h = Math.round(H * scale);
  const ff = spawn(ffmpeg, [
    '-y', '-loglevel', 'error',
    '-f', 'image2pipe', '-c:v', 'mjpeg', '-framerate', String(fps), '-i', '-',
    ...(hasAudio ? ['-i', audio] : []),
    '-vf', `scale=${w}:${h}:flags=lanczos,format=yuv420p`,
    '-c:v', 'libx264', '-preset', scale < 1 ? 'veryfast' : 'slow', '-crf', '16', '-tune', 'animation',
    '-colorspace', 'bt709', '-color_primaries', 'bt709', '-color_trc', 'bt709',
    ...(hasAudio ? ['-c:a', 'aac', '-b:a', '256k', '-shortest'] : []),
    '-movflags', '+faststart', output,
  ], { stdio: ['pipe', 'inherit', 'inherit'] });
  const done = new Promise((ok, fail) => ff.on('close', c => (c === 0 ? ok() : fail(new Error(`ffmpeg exited ${c}`)))));

  const started = Date.now();
  for (let i = 0; i < total; i++) {
    await page.evaluate(x => window.__promo.seek(x), i / fps);
    const buf = await page.screenshot({ type: 'jpeg', quality: 96, optimizeForSpeed: false });
    if (!ff.stdin.write(buf)) await new Promise(r => ff.stdin.once('drain', r));
    if (i % fps === 0) {
      const s = (Date.now() - started) / 1000;
      process.stdout.write(`\r${i}/${total} frames · ${s.toFixed(0)}s elapsed · ~${((s / (i + 1)) * (total - i)).toFixed(0)}s left   `);
    }
  }
  ff.stdin.end();
  await done;
  await browser.close();
  server.close();
  console.log(`\n${output}`);
}

if (args.includes('--preview')) {
  const server = await serve(Number(opt('port', 4173)));
  console.log(`Preview: http://127.0.0.1:${server.address().port}/promo/index.html`);
  console.log('Space play/pause · ←/→ seek 1s (Shift: 0.1s) · ?t=12.5 opens paused at a time');
} else if (opt('frames')) {
  await stills(opt('frames').split(',').map(Number));
} else {
  await video();
}
