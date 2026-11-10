// Run with the native Node.js binary inside the guest, e.g. node test-native-debugger.mjs win32 x64.
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { mkdtemp, realpath, rm, writeFile } from 'node:fs/promises';
import { Session } from 'node:inspector/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { isMainThread, parentPort, Worker, workerData } from 'node:worker_threads';

async function debugInvoice(url) {
  // A debugger on its own thread can inspect the paused main thread without
  // opening a TCP port or relying on an external developer service.
  const session = new Session();
  session.connectToMainThread();
  try {
    await session.post('Debugger.enable');
    const { breakpointId } = await session.post('Debugger.setBreakpointByUrl', { url, lineNumber: 2 });
    const paused = once(session, 'Debugger.paused', { signal: AbortSignal.timeout(20000) });
    parentPort.postMessage('ready');
    const [{ params }] = await paused;
    try {
      assert.ok(params.hitBreakpoints.includes(breakpointId));
      const [frame] = params.callFrames;
      assert.equal(frame.functionName, 'invoiceTotal');
      assert.equal(frame.location.lineNumber + 1, 3);
      const { result, exceptionDetails } = await session.post('Debugger.evaluateOnCallFrame', {
        callFrameId: frame.callFrameId, expression: 'subtotal', throwOnSideEffect: true, returnByValue: true,
      });
      assert.equal(exceptionDetails, undefined);
      assert.equal(result.value, 120);
      return { file: 'invoice.mjs', line: 3, function: frame.functionName, subtotal: result.value };
    } finally {
      await session.post('Debugger.resume');
    }
  } finally {
    session.disconnect();
  }
}

if (isMainThread) {
  assert.equal(process.platform, process.argv[2], 'Unexpected guest operating system');
  assert.equal(process.arch, process.argv[3], 'Unexpected Node.js architecture');
  const directory = await mkdtemp(path.join(tmpdir(), 'Cage native project '));
  let debuggerWorker;
  try {
    const url = pathToFileURL(path.join(await realpath(directory), 'invoice.mjs'));
    await writeFile(url, [
      'export function invoiceTotal(quantity, unitPrice) {',
      '  const subtotal = quantity * unitPrice;',
      '  return subtotal + 12;',
      '}',
    ].join('\n'));
    debuggerWorker = new Worker(new URL(import.meta.url), { workerData: url.href });
    const [ready] = await once(debuggerWorker, 'message');
    assert.equal(ready, 'ready');
    const report = once(debuggerWorker, 'message');
    const { invoiceTotal } = await import(url);
    const total = invoiceTotal(5, 24);
    const [breakpoint] = await report;
    assert.equal(total, 132);
    console.log(JSON.stringify({ os: process.platform, arch: process.arch, node: process.version, breakpoint, total }));
  } finally {
    await debuggerWorker?.terminate();
    await rm(directory, { recursive: true, force: true });
  }
} else {
  parentPort.ref();
  try {
    parentPort.postMessage(await debugInvoice(workerData));
  } finally {
    parentPort.unref();
  }
}
