import assert from "node:assert/strict";
import { test } from "node:test";
import { projectSessionRows, sessionKey } from "./session-order.mjs";

const sessions = [
  { id: "one", title: "第一段", pinned: true },
  { id: "two", title: "第二段", active: true },
  { id: "three", title: "第三段" },
];
const keys = rows => rows.map(sessionKey);
test("external running state blocks only history without an owned live terminal", () => {
  const running = [{id:"external",running:true}, {id:"owned",running:true}, {id:"stopped",running:false}];
  const tabs = [{id:"terminal:owned",cli_session_id:"owned"}, {id:"terminal:old",cli_session_id:"external",closed:true}];
  const rows = projectSessionRows(tabs, running, {}, undefined, new Map());
  assert.equal(rows.find(row=>row.cli_session_id==="external").sessionRunningElsewhere,true);
  assert.equal(rows.find(row=>row.cli_session_id==="owned").sessionRunningElsewhere,false);
  assert.equal(rows.find(row=>row.cli_session_id==="stopped").sessionRunningElsewhere,false);
  const exited = projectSessionRows(tabs, running.map(session=>({...session,running:false})), {}, undefined, new Map());
  assert.equal(exited.some(row=>row.sessionRunningElsewhere),false);
});
test("default activity, stable clicks and terminal attachment", () => {
  const baseline = new Map();
  const initial = projectSessionRows([], sessions, {}, undefined, baseline);
  assert.deepEqual(keys(initial), ["session:two", "session:one", "session:three"]);
  const open = [{ id: "terminal:new", cli_session_id: "three", kind: "terminal" }];
  assert.deepEqual(keys(projectSessionRows(open, [...sessions].reverse(), {}, undefined, baseline)), keys(initial));
  const ended = [{ ...open[0], closed: true }];
  assert.equal(projectSessionRows(ended, sessions, {}, undefined, baseline).length, 3);
});
test("manual order overrides activity and pinning, survives terminal ID changes", () => {
  const manual = ["session:three", "session:one", "session:two"];
  for (const id of ["terminal:first", "terminal:second"]) {
    const rows = projectSessionRows([{ id, cli_session_id: "three" }], sessions, {}, manual, new Map());
    assert.deepEqual(keys(rows), manual);
  }
  const rows = projectSessionRows([], [...sessions, { id: "new", active: true }], {}, manual, new Map());
  assert.deepEqual(keys(rows), [...manual, "session:new"]);
});
