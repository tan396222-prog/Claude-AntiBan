import test from "node:test";
import assert from "node:assert/strict";
import { recoverPendingMessage } from "./mobile-cli/session-recovery.mjs";

test("a lost send receipt is resolved by reading before resending", async () => {
  let message = {request_id:"original",text:"hello"};
  let sends = 0;
  const result = await recoverPendingMessage({signal:new AbortController().signal,
    pending:() => message, progress:() => {}, wait:async () => {},
    read:async () => {message = null; return {turns:[{id:"original"}]};},
    send:async () => {sends++;},
  });
  assert.equal(result,true); assert.equal(sends,0);
});

test("recovery uses five backed-off retries and keeps an unconfirmed message", async () => {
  const message = {request_id:"original",text:"hello"};
  const waits = [], progress = [];
  let reads = 0;
  const result = await recoverPendingMessage({signal:new AbortController().signal,
    pending:() => message, progress:n => progress.push(n), wait:async ms => waits.push(ms),
    read:async () => {reads++; throw Object.assign(new Error("offline"),{status:503});},
    send:async () => assert.fail("must not send before reading the host"),
  });
  assert.equal(result,false); assert.equal(reads,5);
  assert.deepEqual(progress,[1,2,3,4,5]); assert.deepEqual(waits,[1000,2000,4000,8000,8000]);
  assert.equal(message.text,"hello");
});

test("a retry after an unavailable host preserves the original request", async () => {
  const original = {request_id:"original",text:"hello"};
  let message = original, calls = 0;
  const result = await recoverPendingMessage({signal:new AbortController().signal,
    pending:() => message, progress:() => {}, wait:async () => {},
    read:async () => ({can_send:true,turns:[]}),
    send:async value => {
      assert.equal(value,original); calls++;
      if (calls===1) throw Object.assign(new Error("receipt lost"),{status:503});
      message = null;
    },
  });
  assert.equal(result,true); assert.equal(calls,2);
});

test("leaving a session cancels pending recovery before another read", async () => {
  const controller = new AbortController();
  const result = await recoverPendingMessage({signal:controller.signal,
    pending:() => ({request_id:"original",text:"hello"}), progress:() => {},
    wait:async () => controller.abort(), read:async () => assert.fail("read after cancellation"), send:async () => assert.fail("send after cancellation"),
  });
  assert.equal(result,false);
});
