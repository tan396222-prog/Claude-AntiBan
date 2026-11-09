import assert from "node:assert/strict";
import test from "node:test";
import { PCMQueue } from "./audio-worklet.js";

function pcm(frames, value = i => [i, -i]) {
  const data = new ArrayBuffer(frames * 4), view = new DataView(data);
  for (let i = 0; i < frames; i++) {
    const [left, right] = value(i);
    view.setInt16(i * 4, left, true);
    view.setInt16(i * 4 + 2, right, true);
  }
  return data;
}
test("PCM preserves signed stereo samples and silence before buffering", () => {
  const q = new PCMQueue(), left = new Float32Array(128), right = new Float32Array(128);
  q.push(pcm(960)); q.render(left, right, 48000);
  assert.ok(left.every(v => v === 0));
  q.push(pcm(1920, () => [-32768, 32767]));
  q.render(left, right, 48000);
  assert.equal(left[40], 40 / 32768);
  assert.equal(right[40], -40 / 32768);
  for (let i = 0; i < 8; i++) q.render(left, right, 48000);
  assert.equal(left[100], -1);
  assert.equal(right[100], 32767 / 32768);
});
test("PCM resamples to the host rate and returns silence on underrun", () => {
  const q = new PCMQueue(), left = new Float32Array(441), right = new Float32Array(441);
  q.push(pcm(4800)); q.render(left, right, 44100);
  assert.ok(Math.abs(left[147] - 160 / 32768) < 1e-7);
  assert.ok(Math.abs(right[294] + 320 / 32768) < 1e-7);
  for (let i = 0; i < 12; i++) q.render(left, right, 44100);
  assert.ok(left.every(v => v === 0));
  assert.ok(q.count < 2);
});
test("late PCM is bounded and old audio is discarded", () => {
  const q = new PCMQueue(), left = new Float32Array(128), right = new Float32Array(128);
  q.push(pcm(11000, () => [100, 100]));
  q.push(pcm(3000, () => [500, -500]));
  assert.equal(q.count, 3000);
  q.render(left, right, 48000);
  assert.equal(left[0], 500 / 32768);
  q.push(pcm(24000));
  assert.equal(q.count, q.capacity);
  q.push(new ArrayBuffer(3));
  assert.equal(q.count, q.capacity);
});
