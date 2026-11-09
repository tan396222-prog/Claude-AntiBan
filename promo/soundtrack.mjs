// Synthesise the promo soundtrack from the shared timeline: a driving pulse and
// hard impacts under the problem, a warm piano in D major once Cage appears,
// UI foley that lands on every click, keystroke and passing check, and a quiet
// melody under the closing lines. Writes out/soundtrack.wav (48 kHz, 16-bit).
import { mkdirSync, writeFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const TL = require('./timeline.js');
const here = fileURLToPath(new URL('.', import.meta.url));

const SR = 48000;
const N = Math.ceil(TL.duration * SR);
const dry = [new Float32Array(N), new Float32Array(N)];
const wet = [new Float32Array(N), new Float32Array(N)]; // reverb send

const mtof = m => 440 * Math.pow(2, (m - 69) / 12);
let seed = 7;
const rnd = () => { seed = (seed * 16807) % 2147483647; return seed / 2147483647; };

function put(i, l, r, send) {
  if (i < 0 || i >= N) return;
  dry[0][i] += l; dry[1][i] += r;
  wet[0][i] += l * send; wet[1][i] += r * send;
}
const panLR = pan => [Math.cos((pan + 1) * Math.PI / 4), Math.sin((pan + 1) * Math.PI / 4)];

// Sum of decaying partials, stepped with a rotation instead of Math.sin.
function partials(t0, list, { attack = 0.004, pan = 0, send = 0.3, len }) {
  const [gl, gr] = panLR(pan);
  const start = Math.round(t0 * SR);
  const n = Math.min(Math.round(len * SR), N - start);
  if (n <= 0) return;
  const buf = new Float32Array(n);
  for (const { f, a, decay } of list) {
    if (f >= SR / 2.2) continue;
    const w = 2 * Math.PI * f / SR, cw = Math.cos(w), sw = Math.sin(w);
    let c = 1, s = 0, env = a;
    const k = Math.exp(-decay / SR);
    for (let i = 0; i < n; i++) {
      buf[i] += s * env;
      const nc = c * cw - s * sw; s = c * sw + s * cw; c = nc;
      env *= k;
    }
  }
  const atk = Math.max(1, Math.round(attack * SR)), rel = Math.round(0.08 * SR);
  for (let i = 0; i < n; i++) {
    let e = i < atk ? i / atk : 1;
    if (i > n - rel) e *= (n - i) / rel;
    put(start + i, buf[i] * e * gl, buf[i] * e * gr, send);
  }
}

function piano(t, m, vel = 0.5, { len = 5, pan, send = 0.38, bright = 1 } = {}) {
  const f0 = mtof(m);
  const list = [];
  for (let h = 1; h <= 9; h++) {
    const f = f0 * h * Math.sqrt(1 + 0.00015 * h * h);
    const a = vel * 0.22 * Math.pow(h, -1.25 - (1 - bright) * 0.6) * (h === 1 ? 1 : 0.9 + 0.2 * rnd());
    list.push({ f, a, decay: 1.1 + h * 0.75 + f0 / 900 });
  }
  // soft hammer
  list.push({ f: f0 * 0.5 * 1.002, a: vel * 0.02, decay: 2.5 });
  partials(t, list, { attack: 0.006, pan: pan ?? clampPan((m - 62) / 30), send, len });
}
const clampPan = x => Math.max(-0.6, Math.min(0.6, x));

function pad(t0, t1, notes, vol = 0.05, { fadeIn = 1.5, fadeOut = 1.5, cutoff = 1400, send = 0.6 } = {}) {
  const start = Math.round(t0 * SR), end = Math.min(N, Math.round(t1 * SR));
  const a = 1 - Math.exp(-2 * Math.PI * cutoff / SR);
  notes.forEach((m, ni) => {
    const f0 = mtof(m);
    [-6, 0, 6].forEach((cents, vi) => {
      const f = f0 * Math.pow(2, cents / 1200);
      const [gl, gr] = panLR(vi === 0 ? -0.5 : vi === 2 ? 0.5 : 0);
      const H = Math.min(10, Math.floor(5000 / f));
      const osc = [];
      for (let h = 1; h <= H; h++) { const w = 2 * Math.PI * f * h / SR; osc.push([Math.cos(w), Math.sin(w), Math.cos(h * vi + ni), Math.sin(h * vi + ni), 1 / h]); }
      let lp = 0;
      for (let i = start; i < end; i++) {
        let x = 0;
        for (const o of osc) { const nc = o[2] * o[0] - o[3] * o[1]; o[3] = o[2] * o[1] + o[3] * o[0]; o[2] = nc; x += o[3] * o[4]; }
        lp += a * (x - lp);
        const tt = (i - start) / SR, tl = (end - i) / SR;
        const e = Math.min(1, tt / fadeIn) * Math.min(1, tl / fadeOut);
        const g = lp * e * vol / notes.length;
        put(i, g * gl, g * gr, send);
      }
    });
  });
}

function sine(t, f, vol, decay, { f1 = f, glide = 0.05, pan = 0, send = 0.25, attack = 0.002 } = {}) {
  const [gl, gr] = panLR(pan);
  const start = Math.round(t * SR), n = Math.round(Math.min(3, 7 / decay) * SR);
  let ph = 0;
  for (let i = 0; i < n; i++) {
    const tt = i / SR;
    const fr = f1 + (f - f1) * Math.exp(-tt / glide);
    ph += 2 * Math.PI * fr / SR;
    const e = Math.min(1, tt / attack) * Math.exp(-tt * decay);
    const x = Math.sin(ph) * e * vol;
    put(start + i, x * gl, x * gr, send);
  }
}

function noise(t, len, vol, { lo = 400, hi = 6000, shape = x => Math.sin(Math.PI * x), pan = 0, send = 0.4 } = {}) {
  const [gl, gr] = panLR(pan);
  const start = Math.round(t * SR), n = Math.round(len * SR);
  let l1 = 0, l2 = 0;
  for (let i = 0; i < n; i++) {
    const k = i / n;
    const fc = lo + (hi - lo) * shape(k);
    const a = 1 - Math.exp(-2 * Math.PI * fc / SR);
    l1 += a * (rnd() * 2 - 1 - l1);
    l2 += (1 - Math.exp(-2 * Math.PI * lo * 0.5 / SR)) * (l1 - l2);
    const x = (l1 - l2) * shape(k) * vol;
    put(start + i, x * gl, x * gr, send);
  }
}

const click = (t, vol = 0.12, pan = 0.1) => { noise(t, 0.018, vol, { lo: 2500, hi: 7000, shape: x => 1 - x, pan, send: 0.08 }); sine(t, 1900, vol * 0.25, 120, { pan, send: 0.05 }); };
const key = (t, i) => { noise(t, 0.012, 0.05 + 0.02 * rnd(), { lo: 1800 + rnd() * 900, hi: 5000, shape: x => 1 - x, pan: -0.15 + rnd() * 0.3, send: 0.04 }); };
const tock = (t, vol = 0.35) => { sine(t, 180, vol, 14, { f1: 70, glide: 0.04, send: 0.2 }); noise(t, 0.03, vol * 0.25, { lo: 300, hi: 1200, shape: x => 1 - x, send: 0.1 }); };
const pip = (t, f = 2200, vol = 0.05) => sine(t, f, vol, 30, { send: 0.3 });
const bell = (t, m, vol) => {
  const f = mtof(m);
  partials(t, [[1, 1, 1.4], [2.76, 0.4, 3], [5.4, 0.18, 5], [8.93, 0.08, 7]].map(([r, a, d]) => ({ f: f * r, a: a * vol, decay: d })), { attack: 0.003, len: 4, send: 0.55, pan: clampPan((m - 74) / 20) });
};

// ---------- score ----------
const { pain: P, room: R, app: A, accounts: C, perks: K, letter: L, end: En } = TL;

// Act 1 · the problem: a driving pulse, an impact on every cut, then silence.
const kick = (t, vol) => { sine(t, 95, vol, 9, { f1: 42, glide: 0.035, send: 0.08 }); noise(t, 0.012, vol * 0.25, { lo: 1500, hi: 4000, shape: x => 1 - x, send: 0.02 }); };
const boom = (t, k) => {
  sine(t, 72, 0.36 * k, 2.6, { f1: 30, glide: 0.12, send: 0.35 });
  noise(t, 0.6, 0.14 * k, { lo: 90, hi: 1600, shape: x => Math.pow(1 - x, 3), send: 0.55 });
  noise(t, 0.04, 0.12 * k, { lo: 3000, hi: 9000, shape: x => 1 - x, send: 0.2 });
};
const stamp = (t, vol = 0.3) => { tock(t, vol); noise(t, 0.05, vol * 0.3, { lo: 800, hi: 5000, shape: x => 1 - x, send: 0.15 }); };
const bpm = 60 / 128;
pad(0, P.b5 + 0.04, [26, 38, 45, 51], 0.05, { fadeIn: 0.4, fadeOut: 0.04, cutoff: 650, send: 0.45 });
for (let t = P.holdShrink, i = 0; t < P.b5 - 0.05; t += bpm, i++) {
  const build = 0.55 + 0.45 * (t / P.b5);
  kick(t, 0.22 * build);
  sine(t, mtof(i % 8 === 6 ? 39 : 38), 0.05 * build, 6, { send: 0.1 });
  noise(t + bpm / 2, 0.02, 0.035 * build, { lo: 5000, hi: 9000, shape: x => 1 - x, send: 0.05, pan: 0.3 });
}
// The on-hold screen lands like a thunderclap, then the ears ring.
boom(P.slam, 1.15);
noise(P.slam, 0.09, 0.3, { lo: 1500, hi: 12000, shape: x => 1 - x, send: 0.6 });
sine(P.slam, 3150, 0.022, 0.9, { send: 0.15 });
sine(P.slam, 3170, 0.016, 0.8, { send: 0.15, pan: 0.4 });
piano(P.holdMark, 38, 0.5, { len: 2, bright: 0.4, send: 0.5 });
piano(P.holdMark, 39, 0.3, { len: 2, bright: 0.4, send: 0.5 });
boom(P.holdShrink, 0.8);
[P.b2, P.b3, P.b4].forEach(t => boom(t, 0.7));
P.losses.forEach(t => stamp(t, 0.32));
P.rows.forEach(t => noise(t, 0.05, 0.06, { lo: 2500, hi: 6000, shape: x => 1 - x, send: 0.1 }));
stamp(P.total, 0.4); sine(P.total, mtof(50), 0.08, 2, { send: 0.4 }); sine(P.total, mtof(51), 0.07, 2, { send: 0.4 });
noise(P.wires - 0.1, 0.6, 0.05, { lo: 400, hi: 5000, shape: x => x, send: 0.35 });
P.tags.forEach((t, i) => { sine(t, mtof(84), 0.05, 10, { pan: -0.3, send: 0.3 }); sine(t + 0.09, mtof(80), 0.05, 9, { pan: -0.3, send: 0.3 }); });
boom(P.vmMem, 0.45);
sine(P.vmMem, 110, 0.06, 1.5, { send: 0.2 }); sine(P.vmMem, 116.5, 0.06, 1.5, { send: 0.2 }); // grinding beat
P.vmTodo.forEach(t => stamp(t, 0.22));
// the question: a breath of silence, one low note, then a riser into the light
piano(P.b5 + 0.2, 38, 0.5, { len: 2.5, bright: 0.5, send: 0.6 });
noise(P.end - 0.95, 0.95, 0.09, { lo: 300, hi: 9000, shape: x => x * x * x, send: 0.5 });
sine(P.end - 0.95, 900, 0.03, 0.4, { f1: 180, glide: 0.5, send: 0.6 });

// Act 2 · the answer: release into D major, pixels sparkle, bars land.
pad(R.start - 0.05, K.out + 0.6, [50, 57, 62, 66], 0.05, { fadeIn: 1.2, fadeOut: 1.4, cutoff: 1500, send: 0.7 });
[26, 38, 50, 57, 62, 66, 69, 76].forEach((m, i) => piano(R.start + i * 0.03, m, 0.48 - i * 0.03, { len: 5, bright: 0.8, send: 0.55 }));
bell(R.start, 86, 0.08);
const sparkle = [86, 88, 90, 93, 95, 98];
for (let i = 0; i < 14; i++) pip(R.pixels + 0.04 + i * 0.05 + rnd() * 0.03, mtof(sparkle[Math.floor(rnd() * sparkle.length)]), 0.025 + rnd() * 0.015);
noise(R.frame, 0.45, 0.035, { lo: 600, hi: 3500, send: 0.4 });
R.bars.forEach(b => tock(b + 0.24, 0.42));
pip(R.blink, 3100, 0.03);
[62, 66, 69, 74].forEach((m, i) => piano(R.word + i * 0.04, m, 0.26, { len: 4 }));
piano(R.pos + 0.4, 81, 0.3, { len: 4, bright: 0.9 });
piano(R.pos + 0.62, 86, 0.24, { len: 4, bright: 0.9 });
piano(R.sub + 0.75, 78, 0.26, { len: 4, bright: 0.9 });

// Act 3 · the app: an unhurried D-major arpeggio under the UI.
const beat = 60 / 76, bar = beat * 4, arpStart = A.rise + 0.45;
const chords = [
  [38, [62, 66, 69]], [37, [61, 64, 69]], [35, [62, 66, 71]], [31, [62, 67, 71]],
  [42, [62, 66, 69]], [40, [62, 67, 71]], [33, [62, 64, 69]], [33, [61, 64, 69]],
  [38, [62, 66, 69]],
];
chords.forEach(([bass, [a, b, c]], ci) => {
  const t0 = arpStart + ci * bar;
  if (t0 > C.out) return;
  piano(t0, bass, 0.42, { len: bar + 0.6, bright: 0.5, send: 0.35 });
  piano(t0 + beat * 2, bass + 7, 0.24, { len: bar, bright: 0.5, send: 0.35 });
  const pat = [a, b + 12, c, a + 12, b, c + 12, a + 12, b + 12];
  pat.forEach((m, k) => {
    const t = t0 + k * beat / 2;
    if (t > C.out + 0.3) return;
    piano(t, m, (k % 2 ? 0.16 : 0.22) * (k === 0 ? 1.2 : 1), { len: 2.6, send: 0.42 });
  });
});
// 01 · the safety dialog: one rising plink per passed check, a chime when all eleven pass.
noise(A.safetyOpen, 0.3, 0.03, { lo: 500, hi: 3000, send: 0.3 });
[74, 76, 78, 79, 81, 83, 85, 86, 88, 90, 91].forEach((m, i) =>
  sine(A.checks + 0.15 + i * A.checkStep, mtof(m), 0.05, 7, { send: 0.45, pan: -0.3 + i * 0.06 }));
[74, 78, 81, 86].forEach((m, i) => bell(A.allPass + i * 0.05, m, 0.09));
noise(A.safetyClose, 0.3, 0.025, { lo: 500, hi: 2500, send: 0.3 });
// 01 · ready: one chime per highlighted status item.
A.rings.forEach((t, i) => sine(t, mtof([79, 83, 86][i]), 0.045, 7, { send: 0.45 }));
[A.safetyOpen, A.clickDesktop, A.clickInput, A.clickTermIcon, A.clickClaudeTerm, A.clickAuthorize].forEach(t => click(t));
// 02 · Claude Desktop boots, a message is typed and answered.
noise(A.desktopBoot - 0.05, 0.45, 0.03, { lo: 500, hi: 3500, send: 0.35 });
const typeKeys = (text, [a, b]) => { const n = [...text].length; for (let i = 0; i < n; i++) key(a + (i / n) * (b - a), i); };
typeKeys('帮我把 hello-cage 的 README 改得更清楚', A.typeMsg);
click(A.send, 0.09, -0.1);
for (let i = 0; i < 4; i++) pip(A.reply[0] + i * (A.reply[1] - A.reply[0]) / 4, 1250 + i * 80, 0.02);
// 03 · the Claude terminal starts and asks to sign in.
noise(A.clickTermIcon + 0.02, 0.2, 0.025, { lo: 900, hi: 4000, send: 0.2 });
for (let i = 0; i < 7; i++) noise(A.termLines + i * 0.1, 0.015, 0.03, { lo: 2500, hi: 6000, shape: x => 1 - x, send: 0.05 });
pip(A.opening, 1500, 0.03);
// 04 · authorise in the Cage browser, then back to the terminal.
noise(A.browserOpen - 0.05, 0.4, 0.035, { lo: 600, hi: 4000, send: 0.35 });
[74, 78, 81, 86].forEach((m, i) => bell(A.authDone + i * 0.05, m, 0.085));
noise(A.backToTerm - 0.05, 0.35, 0.03, { lo: 600, hi: 3500, send: 0.3 });
typeKeys('给 hello-cage 加一个健康检查接口', A.typePrompt);
click(A.enterPrompt, 0.1, -0.1);
A.reply2.forEach(t => pip(t, 1250, 0.02));

// Act 3b · accounts: each new terminal clicks, travels and lands on its account.
noise(C.panel - 0.05, 0.4, 0.03, { lo: 500, hi: 3000, send: 0.3 });
C.rows.forEach(t => noise(t, 0.03, 0.035, { lo: 2500, hi: 6000, shape: x => 1 - x, send: 0.1 }));
C.terms.forEach((t, j) => {
  click(t, 0.1);
  noise(t + 0.1, 0.4, 0.02, { lo: 800, hi: 4000, send: 0.25, pan: 0.3 });
  sine(t + 0.45, mtof([79, 83, 86][j]), 0.05, 6, { send: 0.45, pan: -0.2 });
});
sine(C.terms[0] + 0.22, mtof(55), 0.05, 7, { send: 0.3 }); sine(C.terms[0] + 0.3, mtof(52), 0.04, 7, { send: 0.3 }); // skipped account

// Act 4 · perks: a lift to G, one chime per card.
piano(K.head, 31, 0.4, { len: 5, bright: 0.5, send: 0.45 });
[55, 59, 62, 67].forEach((m, i) => piano(K.head + 0.05 + i * 0.05, m, 0.2, { len: 4.5, send: 0.5 }));
[74, 78, 81, 86].forEach((m, i) => bell(K.tiles[i], m, 0.07));
piano(K.tiles[3] + 1.2, 33, 0.32, { len: 4, bright: 0.5, send: 0.45 });
[57, 61, 64, 69].forEach((m, i) => piano(K.tiles[3] + 1.25 + i * 0.05, m, 0.16, { len: 3.5, send: 0.5 }));

// Act 5 · the letter: chords breathe, a melody answers each line.
noise(K.out, 1.0, 0.03, { lo: 300, hi: 2500, send: 0.7 });
pad(L.start - 0.2, En.start + 1.2, [43, 55, 59, 62, 66], 0.06, { fadeIn: 1.5, fadeOut: 1.4, cutoff: 1100, send: 0.75 });
const lchords = [[L.start, 31, [55, 59, 62, 66]], [L.lines[0], 42, [57, 62, 66]], [L.lines[1], 40, [55, 59, 62, 67]], [L.lines[2], 33, [57, 62, 64]], [L.lines[2] + 1.2, 33, [57, 61, 64]]];
lchords.forEach(([t, bass, notes]) => {
  piano(t, bass, 0.4, { len: 4.5, bright: 0.45, send: 0.5 });
  notes.forEach((m, i) => piano(t + 0.06 + i * 0.07, m, 0.17, { len: 4, bright: 0.6, send: 0.55 }));
});
[[0, 0, 81], [0, 0.45, 78], [1, 0, 79], [1, 0.45, 76], [2, 0, 76], [2, 0.45, 78], [2, 1.2, 76]].forEach(([li, dt, m]) => {
  piano(L.lines[li] + dt, m, 0.3, { len: 3.5, send: 0.55, bright: 0.9 });
});

// Act 6 · end card: resolve home, bars land, one last blink.
[26, 38, 45, 54, 64, 69, 74].forEach((m, i) => piano(En.start + 0.05 + i * 0.06, m, 0.42 - i * 0.025, { len: 6.2, bright: 0.65, send: 0.6 }));
pad(En.start, TL.duration, [38, 50, 57, 64, 66], 0.05, { fadeIn: 1.2, fadeOut: 3.2, cutoff: 1200, send: 0.8 });
const endA = En.start + 0.3;
[endA + 0.85 + 0.24, endA + 1.0 + 0.24].forEach(t => tock(t, 0.22));
piano(endA + 2.5, 86, 0.16, { len: 4, send: 0.7 });
pip(En.blink, 3100, 0.03);

// ---------- reverb (Freeverb) ----------
function freeverb(input, spread) {
  const s = SR / 44100;
  const combT = [1116, 1188, 1277, 1356, 1422, 1491, 1557, 1617].map(x => Math.round((x + spread) * s));
  const apT = [556, 441, 341, 225].map(x => Math.round((x + spread) * s));
  const out = new Float32Array(N);
  const room = 0.86, damp = 0.28;
  const combs = combT.map(n => ({ b: new Float32Array(n), i: 0, f: 0 }));
  const aps = apT.map(n => ({ b: new Float32Array(n), i: 0 }));
  for (let i = 0; i < N; i++) {
    const x = input[i] * 0.015;
    let y = 0;
    for (const c of combs) {
      const o = c.b[c.i];
      c.f = o * (1 - damp) + c.f * damp;
      c.b[c.i] = x + c.f * room;
      if (++c.i >= c.b.length) c.i = 0;
      y += o;
    }
    for (const a of aps) {
      const o = a.b[a.i];
      a.b[a.i] = y + o * 0.5;
      if (++a.i >= a.b.length) a.i = 0;
      y = o - y;
    }
    out[i] = y;
  }
  return out;
}
const rv = [freeverb(wet[0], 0), freeverb(wet[1], 23)];

// ---------- master ----------
const mix = [new Float32Array(N), new Float32Array(N)];
let peak = 0;
for (let i = 0; i < N; i++) {
  for (let c = 0; c < 2; c++) {
    const v = dry[c][i] + rv[c][i] * 2.4 + rv[1 - c][i] * 0.4;
    mix[c][i] = v;
    peak = Math.max(peak, Math.abs(v));
  }
}
const gain = 0.86 / peak;
const pcm = Buffer.alloc(N * 4);
for (let i = 0; i < N; i++) {
  const t = i / SR;
  const fade = Math.min(1, t / 0.05) * Math.min(1, (TL.duration - t) / 1.6);
  for (let c = 0; c < 2; c++) {
    const v = Math.tanh(mix[c][i] * gain * 1.1) * fade;
    pcm.writeInt16LE(Math.round(Math.max(-1, Math.min(1, v)) * 32767), i * 4 + c * 2);
  }
}
const header = Buffer.alloc(44);
header.write('RIFF', 0); header.writeUInt32LE(36 + pcm.length, 4); header.write('WAVE', 8);
header.write('fmt ', 12); header.writeUInt32LE(16, 16); header.writeUInt16LE(1, 20); header.writeUInt16LE(2, 22);
header.writeUInt32LE(SR, 24); header.writeUInt32LE(SR * 4, 28); header.writeUInt16LE(4, 32); header.writeUInt16LE(16, 34);
header.write('data', 36); header.writeUInt32LE(pcm.length, 40);
mkdirSync(join(here, 'out'), { recursive: true });
const path = join(here, 'out', 'soundtrack.wav');
writeFileSync(path, Buffer.concat([header, pcm]));
console.log(`${path} · ${TL.duration}s · peak gain ${gain.toFixed(2)}`);
