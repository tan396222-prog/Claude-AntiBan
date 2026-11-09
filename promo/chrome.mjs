// Locate a Chrome, Chromium or Edge executable on macOS, Windows or Linux.
import { existsSync } from 'node:fs';
import { join } from 'node:path';

export function findChrome() {
  if (process.env.CHROME_PATH) return process.env.CHROME_PATH;
  const pf = process.env.PROGRAMFILES || 'C:\\Program Files';
  const pf86 = process.env['PROGRAMFILES(X86)'] || 'C:\\Program Files (x86)';
  const local = process.env.LOCALAPPDATA || '';
  const candidates = {
    darwin: [
      '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
      '/Applications/Chromium.app/Contents/MacOS/Chromium',
      '/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge',
    ],
    win32: [
      join(pf, 'Google\\Chrome\\Application\\chrome.exe'),
      join(pf86, 'Google\\Chrome\\Application\\chrome.exe'),
      join(local, 'Google\\Chrome\\Application\\chrome.exe'),
      join(pf86, 'Microsoft\\Edge\\Application\\msedge.exe'),
      join(pf, 'Microsoft\\Edge\\Application\\msedge.exe'),
    ],
    linux: ['/usr/bin/google-chrome', '/usr/bin/google-chrome-stable', '/usr/bin/chromium', '/usr/bin/chromium-browser', '/snap/bin/chromium'],
  }[process.platform] || [];
  const found = candidates.find(p => existsSync(p));
  if (!found) throw new Error('Chrome not found. Set CHROME_PATH to a Chrome, Chromium or Edge executable.');
  return found;
}
