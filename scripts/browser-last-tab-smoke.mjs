import assert from 'node:assert/strict';
import { createPrivacyController } from './browser-privacy-fixture.mjs';

const fixture = await createPrivacyController();
const { api, cdp, waitFor, origin } = fixture;
const events = [];
const stream = new AbortController();
let eventLoop;
try {
  const response = await fixture.states(stream.signal);
  assert.equal(response.status, 200);
  eventLoop = (async () => {
    const decoder = new TextDecoder();
    let buffer = '';
    for await (const chunk of response.body) {
      buffer += decoder.decode(chunk, { stream: true });
      let end;
      while ((end = buffer.indexOf('\n\n')) !== -1) {
        const frame = buffer.slice(0, end);
        buffer = buffer.slice(end + 2);
        if (frame.startsWith('data: ')) events.push(JSON.parse(frame.slice(6)));
      }
    }
  })().catch(error => { if (!stream.signal.aborted) throw error; });
  const assertEmpty = async () => {
    const state = await api('/tabs');
    assert.deepEqual(state.tabs, []);
    assert.equal(state.active, '');
    await waitFor(() => events.at(-1)?.tabs.length === 0, 'empty browser event');
    assert.equal(events.at(-1).active, '', 'empty snapshot must not reference a destroyed tab');
  };
  await api('/close', { id: (await api('/tabs')).active });
  await assertEmpty();
  const reopened = await api('/new', { url: origin + '/reopened' });
  assert.equal((await api('/tabs')).tabs.length, 1);
  assert.equal((await api('/tabs')).active, reopened.id);

  // Native Chromium closes must publish the same empty state as the HTTP action.
  await cdp(reopened.id, 'Target.closeTarget', { targetId: reopened.id });
  await waitFor(async () => (await api('/tabs')).tabs.length === 0, 'external close');
  await assertEmpty();
  const privateTab = await api('/new', { url: origin + '/private', incognito: true });
  const context = (await cdp(privateTab.id, 'Target.getTargetInfo')).targetInfo.browserContextId;
  await api('/close', { id: privateTab.id });
  await assertEmpty();
  const contexts = await cdp('', 'Target.getBrowserContexts');
  assert.ok(!contexts.browserContextIds.includes(context), 'last private tab must dispose its context');
  const final = await api('/new', { url: origin + '/final' });
  assert.equal((await api('/tabs')).active, final.id);
  console.log(JSON.stringify({ passed: true, lastTabClosed: true, emptyEvents: true,
    externalClose: true, privateContextDisposed: true, reopened: true }));
} finally {
  stream.abort();
  await eventLoop;
  await fixture.close();
}
