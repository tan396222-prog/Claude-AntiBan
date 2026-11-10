// Run in an isolated Linux guest with chromium-l10n installed:
// CAGE_TEST_BROWSER_PATH=/usr/bin/chromium TZ=Asia/Taipei node scripts/browser-language-smoke.mjs
import assert from 'node:assert/strict';
import { createPrivacyController } from './browser-privacy-fixture.mjs';

assert.equal(process.platform, 'linux');
const languages = process.env.CAGE_TEST_LANGUAGES?.split(',') || ['en-US', 'zh-TW', 'zh-CN', 'ja-JP', 'de-DE', 'fr-FR'];
for (const language of languages) {
  process.env.CAGE_LANGUAGE = language;
  // Cage applies both the guest locale and the browser language at startup.
  process.env.LANG = process.env.LC_ALL = process.env.CAGE_TEST_SYSTEM_LOCALE || language.replace('-', '_') + '.UTF-8';
  const fixture = await createPrivacyController({
    preferences: { intl: { accept_languages: 'en-US', selected_languages: 'en-US' } },
  });
  try {
    const { api, cdp, evaluate, restart, waitFor } = fixture;
    const normal = (await api('/tabs')).active;
    const expression = '({language:navigator.language,languages:[...navigator.languages],timezone:Intl.DateTimeFormat().resolvedOptions().timeZone})';
    const page = await evaluate(normal, expression);
    if (process.env.CAGE_TEST_NETWORK === '1') {
      const cookie = await cdp(normal, 'Network.setCookie', {
        name: 'account-sentinel', value: 'must-not-enter-probe', url: 'http://198.18.0.254/',
      });
      assert.equal(cookie.success, true);
    }
    const tabsBefore = await api('/tabs');
    const streamAbort = new AbortController();
    const stream = await fixture.states(streamAbort.signal);
    const events = [];
    const reading = (async () => {
      let buffer = '';
      try {
        for await (const chunk of stream.body) {
          buffer += new TextDecoder().decode(chunk);
          let end;
          while ((end = buffer.indexOf('\n\n')) >= 0) {
            const event = buffer.slice(0, end);
            buffer = buffer.slice(end + 2);
            const data = event.split('\n').find(line => line.startsWith('data: '));
            if (data) events.push(JSON.parse(data.slice(6)));
          }
        }
      } catch (error) { if (error.name !== 'AbortError') throw error; }
    })();
    let audit;
    try {
      audit = await api('/audit', { timezone: process.env.TZ || 'UTC', network: process.env.CAGE_TEST_NETWORK === '1' });
    } finally { streamAbort.abort(); await reading; }
    console.log(JSON.stringify({ configured: language, normal: page, audit }));
    assert.equal(page.language, language);
    assert.equal(audit.language, language);
    assert.equal(audit.timezone, audit.expected_timezone);
    const tabsAfter = await api('/tabs');
    assert.equal(tabsAfter.active, tabsBefore.active);
    assert.deepEqual(tabsAfter.tabs.map(tab => tab.id), tabsBefore.tabs.map(tab => tab.id));
    assert.ok(events.length > 0);
    for (const state of events) {
      assert.equal(state.active, tabsBefore.active);
      assert.deepEqual(state.tabs.map(tab => tab.id), tabsBefore.tabs.map(tab => tab.id), 'diagnostic tab entered live state');
    }
    if (process.env.CAGE_TEST_NETWORK === '1') {
      assert.equal(audit.network.status, 200);
      assert.equal(audit.network.seen_cookie, '', 'diagnostic network request carried an account cookie');
    }

    const privatePage = await api('/new', { incognito: true });
    const privateLanguage = await evaluate(privatePage.id, 'navigator.language');
    assert.equal(privateLanguage, language);
    await api('/close', { id: privatePage.id });

    // Observe a real request header, with no Network or Emulation override.
    await cdp(normal, 'Network.enable');
    await cdp(normal, 'Page.navigate', { url: fixture.origin + '/language' });
    await waitFor(() => evaluate(normal, 'document.readyState === "complete"'), 'language page loaded');
    const header = await evaluate(normal, `fetch('/language-header').then(r=>r.text())`);
    assert.match(header, new RegExp('^' + language + '(?:,|$)', 'i'));

    // The command-line preference must also survive a profile restart.
    await restart();
    assert.equal(await evaluate((await api('/tabs')).active, 'navigator.language'), language);
    console.log(JSON.stringify({ configured: language, privateLanguage, header, staleProfileRestart: 'passed' }));
  } finally {
    await fixture.close();
  }
}
