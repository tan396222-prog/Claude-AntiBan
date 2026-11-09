/* Claude Cage promo · every frame is a pure function of time.
 * render(t) sets all styles from t, so preview playback, scrubbing and the
 * frame-by-frame export in render.mjs all show exactly the same picture. */
(() => {
  'use strict';
  const T = window.TL;
  // ?portrait renders the 9:16 cut: same timeline, re-laid out for 1080 × 1920.
  const PORTRAIT = new URLSearchParams(location.search).has('portrait');
  if (PORTRAIT) { T.width = 1080; T.height = 1920; document.body.classList.add('portrait'); }
  // Positions computed here rather than in CSS, per orientation.
  const L = PORTRAIT
    ? { chipY: 650, chipStep: 108, chipX: 680, node: [540, 1330], holdScale: 0.38, holdLift: -35, holdScroll: 0,
      panelTop: 330, colLeft: 280, colTop: 1290, pillStep: 124, pillH: 104, inputDrop: 500, zBase: 1.32, shadeFrom: 1.45, rings: 2 }
    : { chipY: 360, chipStep: 98, chipX: 972, node: [1330, 590], holdScale: 0.36, holdLift: -70, holdScroll: 430,
      panelTop: 214, colLeft: 1330, colTop: 330, pillStep: 112, pillH: 92, inputDrop: 360, zBase: 1.0, shadeFrom: 1.15, rings: 3 };
  // The real workbench, captured at a landscape or a tall window size (see capture-plates.mjs).
  const PL = window.PLATE_LAYOUT[PORTRAIT ? 'portrait' : 'landscape'];
  const PLATES = PORTRAIT ? 'plates/portrait/' : 'plates/';
  const mid = ([x, y, w, h]) => [x + w / 2, y + h / 2];
  const $ = (s, r = document) => r.querySelector(s);
  const $$ = (s, r = document) => [...r.querySelectorAll(s)];

  // ---------- math ----------
  const clamp = (x, a = 0, b = 1) => Math.min(b, Math.max(a, x));
  const p = (t, a, b) => clamp((t - a) / (b - a));
  const lerp = (a, b, k) => a + (b - a) * k;
  const E = {
    out: x => 1 - Math.pow(1 - x, 3),
    out5: x => 1 - Math.pow(1 - x, 5),
    in: x => x * x * x,
    io: x => (x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2),
    sine: x => -(Math.cos(Math.PI * x) - 1) / 2,
    back: (x, s = 1.6) => 1 + (s + 1) * Math.pow(x - 1, 3) + s * Math.pow(x - 1, 2),
  };
  // Fade in over [a, a+fi], fade out over [b-fo, b].
  const win = (t, a, b, fi = 0.5, fo = 0.5) => Math.min(E.out(p(t, a, a + fi)), 1 - E.io(p(t, b - fo, b)));
  const hash = n => { const x = Math.sin(n * 127.1 + 311.7) * 43758.5453; return x - Math.floor(x); };

  const css = (el, o) => {
    if (!el) return;
    const s = el.style;
    if (o.o !== undefined) { s.opacity = o.o.toFixed(4); s.visibility = o.o <= 0.001 ? 'hidden' : 'visible'; }
    if (o.tf !== undefined) s.transform = o.tf;
    if (o.f !== undefined) s.filter = o.f;
  };
  const blur = v => (v > 0.05 ? `blur(${v.toFixed(2)}px)` : 'none');

  // ---------- text ----------
  function split(el) {
    const text = el.textContent;
    el.textContent = '';
    el._chars = [...text].map(ch => {
      const s = document.createElement('span');
      s.className = 'c';
      s.textContent = ch;
      el.appendChild(s);
      return s;
    });
  }
  function reveal(el, t, start, { stagger = 0.045, dur = 0.7, y = 18, b = 10 } = {}) {
    el._chars.forEach((c, i) => {
      const k = E.out(p(t, start + i * stagger, start + i * stagger + dur));
      css(c, { o: k, tf: `translateY(${((1 - k) * y).toFixed(2)}px)`, f: blur((1 - k) * b) });
    });
  }
  const revealEnd = (el, start, { stagger = 0.045, dur = 0.7 } = {}) => start + (el._chars.length - 1) * stagger + dur;

  // A stack of labels that roll vertically at the given switch times.
  function flip(el, t, times, h = 18) {
    const spans = el._spans || (el._spans = [...el.children]);
    let v = 0;
    times.forEach(at => { v += E.io(p(t, at, at + 0.42)); });
    spans.forEach((s, i) => {
      const d = i - v;
      s.style.position = 'absolute';
      css(s, { o: clamp(1 - Math.abs(d) * 1.4), tf: `translateY(${(d * h).toFixed(2)}px)` });
    });
  }

  // ---------- pixel logo ----------
  const SVGNS = 'http://www.w3.org/2000/svg';
  const ORANGE = '#ef6f45', DARK = '#252321';
  function rect(svg, x, y, w, h, fill) {
    const r = document.createElementNS(SVGNS, 'rect');
    r.setAttribute('x', x); r.setAttribute('y', y); r.setAttribute('width', w); r.setAttribute('height', h);
    r.setAttribute('fill', fill);
    svg.appendChild(r);
    return r;
  }
  function buildLogo(svg) {
    svg.setAttribute('viewBox', '0 0 926 926');
    const cells = [];
    const u = 46, bx = 187, by = 280;
    const add = (x, y, w, h) => cells.push({ el: rect(svg, x, y, w + 0.8, h + 0.8, ORANGE), cx: x + w / 2, cy: y + h / 2 });
    for (let r = 0; r < 7; r++) for (let c = 0; c < 12; c++) add(bx + c * u, by + r * u, u, u);
    for (const r of [3, 4]) { add(bx - 69, by + r * u, 69, u); add(bx + 12 * u, by + r * u, 69, u); }
    for (const x of [230, 338, 535, 642]) { add(x, 602, 55, 40); add(x, 642, 55, 40); }
    cells.forEach((c, i) => {
      const d = Math.hypot(c.cx - 463, c.cy - 450) / 420;
      c.delay = d * 0.75 + hash(i) * 0.22;
    });
    // Seamless body drawn once every pixel has landed.
    const solid = [
      rect(svg, bx, by, 12 * u, 7 * u, ORANGE), rect(svg, bx - 69, by + 3 * u, 12 * u + 138, 2 * u, ORANGE),
      ...[230, 338, 535, 642].map(x => rect(svg, x, 600, 55, 82, ORANGE)),
    ];
    const settle = Math.max(...cells.map(c => c.delay)) + 0.42;
    const eyes = [rect(svg, 338, 345, 51, 95, DARK), rect(svg, 537, 345, 51, 95, DARK)];
    const frame = [
      rect(svg, 35, 80, 855, 67, DARK), rect(svg, 35, 761, 855, 67, DARK),
      rect(svg, 35, 80, 65, 748, DARK), rect(svg, 825, 80, 65, 748, DARK),
    ];
    const bars = [rect(svg, 268, 80, 54, 748, DARK), rect(svg, 604, 80, 54, 748, DARK)];
    return { svg, cells, solid, settle, eyes, frame, bars };
  }
  // Assemble the logo: pixels pop from the centre, the frame draws, two bars drop.
  function logoAt(L, t, { pixels, frame, bars, blink, speed = 1 }) {
    const whole = t >= pixels + L.settle * speed;
    L.solid.forEach(r => css(r, { o: whole ? 1 : 0 }));
    L.cells.forEach(c => {
      if (whole) { css(c.el, { o: 0 }); return; }
      const k = p(t, pixels + c.delay * speed, pixels + c.delay * speed + 0.42 * speed);
      const e = k >= 1 ? 1 : Math.max(0, E.back(k, 2.2));
      css(c.el, { o: k > 0 ? 1 : 0, tf: `scale(${e.toFixed(4)})` });
    });
    const eye = E.back(p(t, pixels + 0.95 * speed, pixels + 1.3 * speed), 1.8);
    const bl = blink.reduce((m, b) => Math.min(m, 1 - Math.sin(Math.PI * p(t, b, b + 0.2)) * 0.92), 1);
    L.eyes.forEach(e => css(e, { o: eye > 0 ? 1 : 0, tf: `scaleY(${(Math.max(0, eye) * bl).toFixed(4)})` }));
    L.frame.forEach((r, i) => {
      const k = E.io(p(t, frame + (i >> 1) * 0.12, frame + (i >> 1) * 0.12 + 0.5 * speed));
      css(r, { o: k > 0 ? 1 : 0, tf: i < 2 ? `scaleX(${k.toFixed(4)})` : `scaleY(${k.toFixed(4)})` });
    });
    L.bars.forEach((r, i) => {
      const k = p(t, bars[i], bars[i] + 0.34 * speed);
      const e = E.back(k, 1.1);
      css(r, { o: clamp(k * 10), tf: `translateY(${((1 - e) * -760).toFixed(2)}px)` });
    });
  }
  // Tiny landing bump for each bar.
  const barImpact = (t, bars) => bars.reduce((s, b) => s + Math.sin(Math.PI * p(t, b + 0.3, b + 0.5)) * 1, 0);

  // ---------- icons ----------
  const ico = d => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">${d}</svg>`;
  const WARN = '<path d="M12 3.5 2.8 19.5h18.4z"/><path d="M12 10v4M12 16.8v.2"/>';
  const APPS = [
    { n: '微信', c: '#3fb950', w: '异地登录提醒' },
    { n: '网上银行', c: '#d7a03b', w: '安全验证失败' },
    { n: '公司 VPN', c: '#5a8de0', w: '内网连不上' },
    { n: '国内网站', c: '#9b7fe0', w: '打开变慢' },
    { n: 'Claude', c: '#d97757', w: '时区与 IP 对不上' },
  ];

  // ---------- build ----------
  const els = {};
  function build() {
    $$('[data-split]').forEach(split);

    const chips = $('#appChips'), wires = $('#wires');
    els.apps = APPS.map((a, i) => {
      const y = L.chipY + i * L.chipStep;
      const chip = document.createElement('div');
      chip.className = 'app-chip dark-card';
      chip.style.top = y + 'px';
      chip.innerHTML = `<i style="background:${a.c}"></i>${a.n}`;
      const warn = document.createElement('div');
      warn.className = 'warn';
      warn.style.top = (y + 14) + 'px';
      warn.innerHTML = `${ico(WARN)}${a.w}`;
      chips.append(chip, warn);
      const y0 = y + 39;
      const path = document.createElementNS(SVGNS, 'path');
      const [nx, ny] = L.node;
      path.setAttribute('d', PORTRAIT
        ? `M${L.chipX} ${y0} C 900 ${y0}, ${nx + (i - 2) * 40} ${ny - 160}, ${nx + (i - 2) * 40} ${ny}`
        : `M${L.chipX} ${y0} C 1150 ${y0}, 1170 ${ny}, ${nx} ${ny}`);
      const dot = document.createElementNS(SVGNS, 'circle');
      dot.setAttribute('r', 6);
      dot.setAttribute('fill', '#ffb08f');
      wires.append(path, dot);
      const len = path.getTotalLength();
      path.style.strokeDasharray = `${len}`;
      return { chip, warn, path, dot, len };
    });

    const svgLogo = document.createElementNS(SVGNS, 'svg');
    svgLogo.classList.add('logo-svg');
    $('#logoWrap').appendChild(svgLogo);
    els.logo = buildLogo(svgLogo);
    const svgEnd = document.createElementNS(SVGNS, 'svg');
    svgEnd.classList.add('logo-svg');
    $('#endLogo').appendChild(svgEnd);
    els.endLogo = buildLogo(svgEnd);

    els.caps = $$('#caption .cap');
    els.dots = $$('#progressDots b');
    els.plates = $$('#win .plate');
    Object.assign($('#win').style, { width: PL.width + 'px', height: PL.height + 'px' });
    els.plates.forEach(el => { el.src = PLATES + el.dataset.plate + '.png'; });
    // Panes sit where the real UI leaves its remote views and terminal empty.
    const side = PL.home.sidebar[2] + 1, foot = PL.home.status[3];
    const place = (el, top) => Object.assign(el.style, { left: side + 'px', top: top + 'px', bottom: foot + 'px' });
    place($('#paneDesktop'), PL.desktop.header[3]);
    place($('#paneTerm'), PL.term.header[3]);
    place($('#paneBrowser'), PL.browser.navigation[1] + PL.browser.navigation[3] + 34);
    // One overlay per check row, cut from the "all passed" plate.
    const S = PL['safety-done'], fx = $('#checkFx');
    els.checkRows = S.rows.map(([x, y, w, h]) => {
      const row = document.createElement('div');
      row.className = 'row';
      Object.assign(row.style, {
        left: x + 'px', top: y + 'px', width: w + 'px', height: h + 'px',
        backgroundImage: `url(${PLATES}safety-done.png)`, backgroundSize: `${PL.width}px ${PL.height}px`, backgroundPosition: `${-x}px ${-y}px`,
      });
      row.innerHTML = '<div class="flash"></div>';
      const spin = document.createElement('div');
      spin.className = 'spin';
      Object.assign(spin.style, { left: (x + 20.5) + 'px', top: (y + h / 2) + 'px' });
      fx.append(row, spin);
      return { row, spin };
    });
    const [cx, cy, , ch] = S.count;
    Object.assign($('#checkCount').style, { left: cx + 'px', top: cy + 'px', height: ch + 'px' });
    // Status-bar highlights: exit, location and (when the window is wide enough to show it) timezone.
    els.rings = PL.home.statusItems.slice(0, L.rings).map(([x, y, w, h]) => {
      const ring = document.createElement('div');
      ring.className = 'ring2';
      Object.assign(ring.style, { left: (x - 5) + 'px', top: y + 'px', width: (w + 10) + 'px', height: h + 'px' });
      $('#win').insertBefore(ring, $('#ripple'));
      return ring;
    });
    els.cdReply = $$('#cdReply p');
    els.termA = $$('#termA > *');
    els.termB = $$('#termB > *');
    els.reply2 = $$('#termB [data-r]');
    els.acctBars = $$('#acctPanel .bar i').map(el => ({ el, v: parseFloat(el.style.transform.slice(7)) }));
    els.accts = $$('#acctPanel .acct');
    els.accts[2].classList.add('done');
    els.pills = $$('#termCol .tpill');
    els.pills.forEach((pl, j) => { pl.style.top = (40 + j * L.pillStep) + 'px'; });
  }

  // Window-local position of an element's centre (offsets ignore transforms).
  const win0 = () => $('#win');
  function center(el, dx = 0, dy = 0) {
    let x = 0, y = 0, n = el;
    const root = win0();
    while (n && n !== root) { x += n.offsetLeft; y += n.offsetTop; n = n.offsetParent; }
    return [x + el.offsetWidth / 2 + dx, y + el.offsetHeight / 2 + dy];
  }

  let cursorKeys, clicks;
  function measure() {
    const A = T.app;
    // Sidebar targets come from the captured layout; pane targets are measured.
    const desk = mid(PL.home.desktopNav), icon = mid(PL.home.termIcon), claudeItem = mid(PL.menu.claudeItem);
    const input = center($('#cdInput'), 0, -22);
    const go = center($('#authGo'));
    // [time, x, y, opacity]
    const shield = mid(PL.home.shield);
    cursorKeys = [
      [A.step1 - 0.2, 620, 700, 0], [A.step1 + 0.1, 420, 740, 1], [A.safetyOpen - 0.05, ...shield, 1], [A.safetyOpen + 0.25, ...shield, 1],
      [A.safetyOpen + 0.7, 260, 700, 0],
      [A.step2 + 0.1, 760, 560, 0], [A.step2 + 0.3, 620, 420, 1], [A.clickDesktop - 0.05, ...desk, 1], [A.clickDesktop + 0.25, ...desk, 1],
      [A.clickInput - 0.05, ...input, 1], [A.clickInput + 0.35, ...input, 1], [A.clickInput + 0.75, input[0] + 30, input[1] + 60, 0],
      [A.step3, 620, 520, 0], [A.step3 + 0.25, 420, 430, 1], [A.clickTermIcon - 0.05, ...icon, 1], [A.clickTermIcon + 0.2, ...icon, 1],
      [A.clickClaudeTerm - 0.05, ...claudeItem, 1], [A.clickClaudeTerm + 0.15, ...claudeItem, 1], [A.clickClaudeTerm + 0.55, 300, 520, 0],
      [A.browserOpen + 0.2, go[0] + 160, go[1] + 140, 0], [A.browserOpen + 0.45, go[0] + 120, go[1] + 110, 1],
      [A.clickAuthorize - 0.05, ...go, 1], [A.clickAuthorize + 0.25, ...go, 1], [A.clickAuthorize + 0.7, go[0] + 40, go[1] + 60, 0],
    ];
    // Wires from each new terminal to the account it lands on.
    const wires = $('#acctWires');
    wires.textContent = '';
    els.acctWires = ACCT_ASSIGN.map((ai, j) => {
      const acct = els.accts[ai];
      const ay = L.panelTop + acct.offsetTop + acct.offsetHeight / 2;
      const py = L.colTop + 40 + j * L.pillStep + L.pillH / 2;
      const path = document.createElementNS(SVGNS, 'path');
      path.setAttribute('d', PORTRAIT
        ? `M${L.colLeft + 524} ${py} C 1062 ${py}, 1078 ${ay}, 1024 ${ay}`
        : `M${L.colLeft - 4} ${py} C 1220 ${py}, 1225 ${ay}, 1114 ${ay}`);
      const dot = document.createElementNS(SVGNS, 'circle');
      dot.setAttribute('r', 8);
      dot.setAttribute('fill', '#519489');
      wires.append(path, dot);
      return { path, dot, len: path.getTotalLength() };
    });
    clicks = [[A.safetyOpen, shield], [A.clickDesktop, desk], [A.clickInput, input], [A.clickTermIcon, icon], [A.clickClaudeTerm, claudeItem], [A.clickAuthorize, go]];
  }

  // ---------- camera ----------
  // [time, zoom, focusX, focusY, anchorX, anchorY]
  const BASE = [1.0, 720, 430, 960, 612];
  const A0 = T.app;
  const CAM = [
    [A0.rise, ...BASE], [A0.safetyOpen, ...BASE],
    [A0.safetyOpen + 0.6, 1.18, 720, 520, 960, 760], [A0.allPass + 0.7, 1.18, 720, 520, 960, 760],
    [A0.rings[0] - 0.2, 1.75, 505, 846, 960, 900], [A0.step2 - 0.7, 1.75, 505, 846, 960, 900],
    [A0.step2, ...BASE], [A0.desktopBoot, ...BASE],
    [A0.desktopBoot + 0.6, 1.1, 860, 440, 960, 630], [A0.step3, 1.1, 860, 440, 960, 630],
    [A0.clickTermIcon - 0.1, 1.7, 170, 380, 720, 620], [A0.clickClaudeTerm + 0.1, 1.7, 170, 380, 720, 620],
    [A0.termLines + 0.5, 1.08, 820, 430, 960, 630], [A0.browserOpen, 1.08, 820, 430, 960, 630],
    [A0.browserOpen + 0.4, 1.12, 844, 450, 960, 630], [A0.backToTerm - 0.2, 1.12, 844, 450, 960, 630],
    [A0.backToTerm + 0.3, 1.1, 820, 420, 960, 630], [A0.out, 1.22, 760, 330, 960, 620],
  ];
  // The tall cut shows the narrow window at full width, cutting in only where a detail matters.
  const PB = [1.32, 380, 550, 540, 1026];
  const CAM_P = [
    [A0.rise, ...PB], [A0.safetyOpen, ...PB],
    [A0.safetyOpen + 0.6, 1.42, 380, 470, 540, 1010], [A0.allPass + 0.7, 1.42, 380, 470, 540, 1010],
    [A0.rings[0] - 0.2, 2.6, 380, 1084, 540, 1400], [A0.step2 - 0.7, 2.6, 380, 1084, 540, 1400],
    [A0.step2, ...PB], [A0.clickTermIcon - 0.6, ...PB],
    [A0.clickTermIcon - 0.1, 2.0, 230, 330, 490, 980], [A0.clickClaudeTerm + 0.1, 2.0, 230, 330, 490, 980],
    [A0.termLines + 0.5, ...PB], [A0.backToTerm + 0.3, ...PB],
    [A0.out, 1.42, 380, 420, 540, 1000],
  ];
  function camAt(t) {
    const K = PORTRAIT ? CAM_P : CAM;
    if (t <= K[0][0]) return K[0].slice(1);
    for (let i = 0; i < K.length - 1; i++) {
      const a = K[i], b = K[i + 1];
      if (t <= b[0]) {
        const k = E.io(p(t, a[0], b[0]));
        return a.slice(1).map((v, j) => lerp(v, b[j + 1], k));
      }
    }
    return K[K.length - 1].slice(1);
  }
  function cursorAt(t) {
    const K = cursorKeys;
    if (t <= K[0][0]) return K[0].slice(1);
    for (let i = 0; i < K.length - 1; i++) {
      const a = K[i], b = K[i + 1];
      if (t <= b[0]) {
        const k = E.io(p(t, a[0], b[0]));
        return [lerp(a[1], b[1], k), lerp(a[2], b[2], k), lerp(a[3], b[3], k)];
      }
    }
    return K[K.length - 1].slice(1);
  }

  // ---------- acts ----------
  // Fast, punchy reveal for the problem headlines.
  const punch = (el, t, at) => reveal(el, t, at, { stagger: 0.022, dur: 0.38, y: 26, b: 8 });
  const slam = (el, t, at, { from = 1.35, rot = 0, dur = 0.38 } = {}) => {
    const k = p(t, at, at + dur);
    const e = E.out5(k);
    css(el, { o: clamp(k * 5), tf: `scale(${lerp(from, 1, e).toFixed(4)}) rotate(${(rot * (1 - e * 0.6)).toFixed(2)}deg)`, f: blur((1 - clamp(k * 3)) * 10) });
  };

  function pain(t) {
    const P = T.pain;
    const layer = $('#night');
    css(layer, { o: t < P.end ? 1 : 0 });
    if (t >= P.end) return;

    // Hard cuts: every hit shakes the frame and flashes it for a few frames.
    const hits = [[P.slam, 0.5], [P.holdShrink, 0.8], [P.b2, 0.55], [P.b3, 0.55], [P.b4, 0.55], [P.vmMem, 0.45], ...P.losses.map(x => [x, 0.32]), [P.total, 0.3]];
    let shake = 0, flash = 0;
    hits.forEach(([h, k]) => {
      const q = p(t, h, h + 0.35), f = p(t, h, h + 0.13);
      if (q > 0 && q < 1) shake = Math.max(shake, (1 - q) * k);
      if (f > 0 && f < 1) flash = Math.max(flash, (1 - f) * k);
    });
    const sx = Math.sin(t * 97) * 12 * shake, sy = Math.cos(t * 83) * 8 * shake;
    css($('#flash'), { o: flash * 0.2 });
    css($('#nightGlow'), { o: t < P.b5 ? 0.45 + shake * 0.8 : 0 });

    const starts = [P.slam, P.b2, P.b3, P.b4, P.b5, P.end];
    $$('#night .beat').forEach((b, i) => {
      const a = starts[i], z = starts[i + 1];
      const k = E.out5(p(t, a, a + 0.4));
      css(b, { o: t >= a && t < z ? 1 : 0, tf: `translate(${sx.toFixed(2)}px, ${sy.toFixed(2)}px) scale(${(lerp(1.05, 1, k) + Math.max(0, t - a) * 0.006).toFixed(4)})` });
      const kick = b.querySelector('.kick');
      const ka = i === 0 ? P.holdShrink + 0.1 : a + 0.05;
      if (kick) css(kick, { o: E.out(p(t, ka, ka + 0.25)) });
      const head = b.querySelector('.head');
      if (head) {
        const at = i === 0 ? P.losses[2] + 0.2 : a + 0.12;
        punch(head.children[0], t, at);
        punch(head.children[1], t, at + 0.22);
      }
    });

    // 01 · banned: the on-hold screen people recognise, then it shrinks into evidence.
    const sc = E.io(p(t, P.holdScroll[0], P.holdScroll[1]));
    const sk = E.io(p(t, P.holdShrink, P.holdShrink + 0.5));
    const hs = lerp(1 + Math.min(t, P.holdShrink) * 0.014, L.holdScale, sk);
    const hold = $('#hold');
    css(hold, { tf: `translateY(${(L.holdLift * sk).toFixed(2)}px) scale(${hs.toFixed(4)}) rotate(${(-1.6 * sk).toFixed(3)}deg)` });
    hold.style.borderRadius = sk > 0 ? `${(16 / hs).toFixed(1)}px` : '0';
    hold.style.boxShadow = sk > 0 ? `0 0 0 ${(3 / hs).toFixed(1)}px rgba(255,106,69,${(0.7 * sk).toFixed(3)}), 0 ${(40 / hs).toFixed(0)}px ${(110 / hs).toFixed(0)}px rgba(0,0,0,${(0.6 * sk).toFixed(3)})` : 'none';
    $('#holdScroll').style.transform = `translateY(${(-L.holdScroll * sc * (1 - sk)).toFixed(2)}px)`;
    css($('#holdTint'), { o: 0.14 * sk });
    const mk = E.out(p(t, P.holdMark, P.holdMark + 0.4));
    const mark = $('#holdMark');
    mark.style.backgroundSize = `${(mk * 100).toFixed(2)}% 100%`;
    mark.style.color = mk > 0.2 ? '#b5452a' : '#8a877f';
    $$('#losses .loss').forEach((el, i) => slam(el, t, P.losses[i], { from: 1.4, rot: [-3, 2, -2][i], dur: 0.3 }));

    // 02 · bills
    const rk = E.out5(p(t, P.b2, P.b2 + 0.5));
    css($('#receipt'), { o: clamp(rk * 3), tf: `translateY(${((1 - rk) * 240 - Math.max(0, t - P.b2) * 6).toFixed(2)}px)` });
    $$('#receipt .rr').forEach((el, i) => {
      const k = E.out5(p(t, P.rows[i], P.rows[i] + 0.25));
      css(el, { o: k, tf: `translateX(${((1 - k) * -24).toFixed(2)}px)` });
    });
    slam($('#receipt .rt'), t, P.total, { from: 1.25, dur: 0.3 });
    css($('#receipt .rn'), { o: E.out(p(t, P.total + 0.35, P.total + 0.7)) });

    // 03 · the whole machine behind a global proxy
    css($('#pcLabel'), { o: E.out(p(t, P.b3 + 0.1, P.b3 + 0.4)) });
    slam($('#proxyNode'), t, P.wires + 0.15, { from: 0.6, dur: 0.45 });
    els.apps.forEach((a, i) => {
      const k = E.out5(p(t, P.b3 + 0.08 + i * 0.05, P.b3 + 0.4 + i * 0.05));
      css(a.chip, { o: k, tf: `translateX(${((1 - k) * -30).toFixed(2)}px)` });
      const w = E.io(p(t, P.wires + i * 0.06, P.wires + 0.5 + i * 0.06));
      a.path.style.strokeDashoffset = `${(a.len * (1 - w)).toFixed(2)}`;
      const pt = a.path.getPointAtLength(((t * 0.55 + i * 0.21) % 1) * a.len);
      a.dot.setAttribute('cx', pt.x.toFixed(2));
      a.dot.setAttribute('cy', pt.y.toFixed(2));
      a.dot.style.opacity = w >= 1 ? 0.9 : 0;
      const at = P.tags[i];
      const wk = Math.max(0, E.back(p(t, at, at + 0.35), 2));
      css(a.warn, { o: clamp(p(t, at, at + 0.12) * 1), tf: `scale(${wk.toFixed(4)})` });
      a.warn.style.transformOrigin = '100% 50%';
      a.chip.style.borderColor = t > at ? 'rgba(255,106,69,.65)' : 'rgba(255,240,225,.10)';
    });

    // 04 · a heavy VM that still needs everything set up
    slam($('#vmWin'), t, P.vm, { from: 1.12, dur: 0.4 });
    const dl = 0.11 + Math.max(0, t - P.vm) * 0.004;
    $('#vmDl').style.transform = `scaleX(${dl.toFixed(4)})`;
    $('#vmEta').textContent = t < P.vmMem ? '下载中 · 剩余 1 小时 52 分' : '下载中 · 剩余 2 小时 07 分';
    const mem = E.out5(p(t, P.vmMem, P.vmMem + 0.45));
    const memEl = $('#vmMem');
    memEl.style.transform = `scaleX(${lerp(0.25, 0.93, mem).toFixed(4)})`;
    memEl.style.background = mem > 0.3 ? '#ff6a45' : '#d9cfc5';
    $('#vmMemTxt').textContent = mem > 0.3 ? '14.9 GB / 16 GB · 主机开始卡顿' : '4 GB / 16 GB';
    css($('#vmTodo .lbl'), { o: E.out(p(t, P.vmTodo[0] - 0.2, P.vmTodo[0] + 0.1)) });
    $$('#vmTodo span').forEach((el, i) => slam(el, t, P.vmTodo[i], { from: 1.3, dur: 0.25 }));

    // and the question
    const [qa, qb] = $('#b5 .q').children;
    reveal(qa, t, P.b5 + 0.2, { stagger: 0.06, dur: 0.5, y: 20, b: 10 });
    reveal(qb, t, P.b5 + 0.65, { stagger: 0.06, dur: 0.5, y: 20, b: 10 });
    const qo = 1 - E.io(p(t, P.end - 0.35, P.end));
    css($('#b5 .q'), { tf: `scale(${(1 + Math.max(0, t - P.b5) * 0.015).toFixed(4)})`, f: blur((1 - qo) * 14) });
  }

  function room(t) {
    const R = T.room;
    const layer = $('#room');
    const o = E.out(p(t, R.start, R.start + 0.45));
    const gone = E.io(p(t, R.end - 0.6, R.end));
    css(layer, { o: t >= R.end ? 0 : o });
    if (t < R.start || t >= R.end) return;
    $('#roomLight').style.transform = `scale(${lerp(0.6, 1, E.out(p(t, R.start, R.start + 2.5))).toFixed(4)})`;

    logoAt(els.logo, t, { pixels: R.pixels, frame: R.frame, bars: R.bars, blink: [R.blink], speed: 0.7 });
    const bump = barImpact(t, R.bars);
    const lift = E.io(p(t, R.end - 0.9, R.end));
    css($('#logoWrap'), { o: 1 - gone, tf: `translateY(${(bump * 6 - lift * 40).toFixed(2)}px) scale(${(1 - lift * 0.06).toFixed(4)})` });

    reveal($('#wordmark'), t, R.word, { stagger: 0.035, dur: 0.6 });
    const [pa, pb] = $('#roomPos').children;
    reveal(pa, t, R.pos, { stagger: 0.035, dur: 0.6 });
    reveal(pb, t, R.pos + 0.4, { stagger: 0.07, dur: 0.6, y: 24 });
    const [sa, sb] = $('#roomSub').children;
    reveal(sa, t, R.sub, { stagger: 0.025, dur: 0.6, y: 10 });
    reveal(sb, t, R.sub + 0.75, { stagger: 0.04, dur: 0.6, y: 10 });
    ['#wordmark', '#roomPos', '#roomSub'].forEach(id => css($(id), { o: 1 - gone, tf: `translateY(${(-lift * 40).toFixed(2)}px)` }));
  }

  // Which real-UI plate is on screen at time t.
  function plateAt(t) {
    const A = T.app;
    if (t < A.safetyOpen + 0.05) return 'home';
    if (t < A.allPass) return 'safety-run';
    if (t < A.safetyClose) return 'safety-done';
    if (t < A.clickDesktop + 0.05) return 'home';
    if (t < A.clickTermIcon + 0.05) return 'desktop';
    if (t < A.clickClaudeTerm + 0.05) return 'menu';
    if (t < A.browserOpen) return 'term';
    if (t < A.backToTerm) return 'browser';
    return 'term';
  }
  const typed = (t, text, [a, b]) => {
    const chars = [...text];
    const n = t < a ? 0 : Math.min(chars.length, Math.floor(p(t, a, b) * chars.length) + 1);
    return chars.slice(0, n).join('');
  };
  const DESK_MSG = '帮我把 hello-cage 的 README 改得更清楚';
  const TERM_MSG = '给 hello-cage 加一个健康检查接口';
  const TERM_FINAL = '已添加 GET /healthz，返回 200 和当前版本号。';

  function app(t) {
    const A = T.app;
    const lo = E.out(p(t, A.rise - 0.3, A.rise + 0.4)) * (1 - E.io(p(t, A.out + 0.4, A.end)));
    css($('#app'), { o: lo });
    if (lo <= 0) return;

    // captions + progress
    const capWin = [[A.step1, A.step2 - 0.2], [A.step2, A.step3 - 0.2], [A.step3, A.step4 - 0.2], [A.step4, A.out]];
    els.caps.forEach((c, i) => {
      const [a, b] = capWin[i];
      const kin = E.out(p(t, a, a + 0.7)), kout = E.io(p(t, b - 0.35, b));
      css(c, { o: kin * (1 - kout), tf: `translateY(${((1 - kin) * 22 - kout * 14).toFixed(2)}px)`, f: blur((1 - kin) * 8 + kout * 8) });
      const k2 = E.out(p(t, a + 0.12, a + 0.9));
      css(c.children[1].lastChild, { o: k2, tf: `translateY(${((1 - k2) * 10).toFixed(2)}px)` });
      els.dots[i].style.transform = `scaleX(${E.io(p(t, a, b)).toFixed(4)})`;
    });
    css($('#progressDots'), { o: win(t, A.step1, A.out, 0.6, 0.4) });

    // camera + window entrance / exit
    const [z, fx, fy, ax, ay] = camAt(t);
    const rise = E.out5(p(t, A.rise, A.rise + 1.3));
    const exit = E.io(p(t, A.out, A.end));
    const zz = z * lerp(0.94, 1, rise) * lerp(1, 0.9, exit);
    const tx = ax - fx * zz, ty = ay - fy * zz + (1 - rise) * 160 - exit * 20;
    css($('#cam'), { o: clamp(rise * 1.6) * (1 - exit), tf: `translate(${tx.toFixed(2)}px, ${ty.toFixed(2)}px) scale(${zz.toFixed(5)})`, f: blur(exit * 10) });
    css($('#appFloor'), { o: rise * (1 - exit) * clamp(1 - (z - L.zBase) * 3) });
    css($('#capShade'), { o: clamp((z - L.shadeFrom) * 4) });

    const plate = plateAt(t);
    // The safety dialog fades in, settles to "all passed", then fades away; other changes are cuts.
    const fade = {
      'safety-run': t >= A.safetyOpen ? E.out(p(t, A.safetyOpen + 0.05, A.safetyOpen + 0.25)) : 0,
      'safety-done': t >= A.allPass && t < A.safetyClose + 0.3 ? E.io(p(t, A.allPass, A.allPass + 0.35)) * (1 - E.io(p(t, A.safetyClose, A.safetyClose + 0.25))) : 0,
    };
    els.plates.forEach(el => {
      const name = el.dataset.plate;
      let o = name === plate ? 1 : 0;
      if (name === 'home' && (plate === 'safety-run' || plate === 'safety-done')) o = 1;
      if (name === 'safety-run') o = plate === 'safety-run' ? fade[name] : plate === 'safety-done' ? 1 : 0;
      if (name === 'safety-done') o = fade[name];
      css(el, { o });
    });

    // 01 · the eleven critical checks pass one by one
    const inDialog = t >= A.safetyOpen && t < A.allPass + 0.4;
    let passed = 0;
    els.checkRows.forEach(({ row, spin }, i) => {
      const at = A.checks + 0.15 + i * A.checkStep;
      if (t >= at) passed++;
      css(row, { o: inDialog ? E.out(p(t, at, at + 0.18)) * fade['safety-run'] : 0 });
      css(row.firstChild, { o: Math.sin(Math.PI * p(t, at, at + 0.5)) });
      css(spin, { o: inDialog && t >= A.checks && t < at ? fade['safety-run'] : 0, tf: `rotate(${((t * 420) % 360).toFixed(1)}deg)` });
    });
    $('#checkCount').textContent = `${passed} / 11 通过`;
    css($('#checkCount'), { o: inDialog && t < A.allPass + 0.35 ? fade['safety-run'] : 0 });
    const press = at => 1 - Math.sin(Math.PI * p(t, at - 0.02, at + 0.16)) * 0.05;

    // 01 · ready: the status bar tells you where you exit from
    els.rings.forEach((r, i) => css(r, { o: win(t, A.rings[i], A.step2 - 0.4, 0.3, 0.4) }));

    // 02 · Claude Desktop running inside the VM display
    const desk = plate === 'desktop' || plate === 'menu';
    css($('#paneDesktop'), { o: desk ? E.out(p(t, A.desktopBoot, A.desktopBoot + 0.45)) : 0 });
    if (desk) {
      const sent = t >= A.send;
      const draft = sent ? '' : typed(t, DESK_MSG, A.typeMsg);
      $('#cdTx').textContent = draft;
      $('#cdPh').style.display = draft ? 'none' : '';
      const typing = t > A.typeMsg[0] && t < A.typeMsg[1] + 0.2;
      css($('#cdCaret'), { o: t >= A.clickInput && (typing || Math.floor(t * 2.2) % 2 === 0) ? 1 : 0 });
      $('#cdSend').style.background = draft ? '#d97757' : '#e8b8a5';
      const mv = E.io(p(t, A.send, A.send + 0.45));
      css($('#cdHello'), { o: E.out(p(t, A.desktopBoot + 0.3, A.desktopBoot + 0.8)) * (1 - mv), tf: `translateY(${(-mv * 30).toFixed(2)}px)` });
      css($('#cdInput'), { tf: `translateY(${(mv * L.inputDrop).toFixed(2)}px)` });
      $('#cdUser').textContent = DESK_MSG;
      const ck = E.out(p(t, A.send + 0.15, A.send + 0.5));
      css($('#cdChat'), { o: ck, tf: `translateY(${((1 - ck) * 12).toFixed(2)}px)` });
      els.cdReply.forEach((el, i) => {
        const at = lerp(A.reply[0], A.reply[1], i / els.cdReply.length);
        const k = E.out(p(t, at, at + 0.35));
        css(el, { o: k, tf: `translateY(${((1 - k) * 8).toFixed(2)}px)` });
      });
    }

    // 03 · Claude terminal: Claude Code starts and asks to sign in
    css($('#paneTerm'), { o: plate === 'term' ? 1 : 0 });
    const back = t >= A.backToTerm;
    css($('#termA'), { o: back ? 0 : 1 });
    css($('#termB'), { o: back ? 1 : 0 });
    els.termA.forEach((ln, i) => {
      const at = i === els.termA.length - 1 ? A.opening : A.termLines + i * 0.1;
      const k = E.out(p(t, at, at + 0.25));
      css(ln, { o: k, tf: `translateY(${((1 - k) * 6).toFixed(2)}px)` });
    });
    if (t >= A.opening) $('#termOpening').style.opacity = String(0.55 + 0.45 * Math.abs(Math.sin((t - A.opening) * 5)));

    // 04 · authorise in the Cage browser, then back to work
    css($('#paneBrowser'), { o: plate === 'browser' ? E.out(p(t, A.browserOpen, A.browserOpen + 0.3)) : 0 });
    const dn = E.out(p(t, A.authDone, A.authDone + 0.35));
    css($('#authAsk'), { o: 1 - dn });
    css($('#authDone'), { o: dn, tf: `scale(${lerp(0.96, 1, dn).toFixed(4)})` });
    $('#authGo').style.transform = `scale(${press(A.clickAuthorize).toFixed(4)})`;
    els.termB.forEach((ln, i) => {
      if (ln.dataset.r !== undefined) return;
      const at = A.backToTerm + 0.1 + i * 0.25;
      const k = E.out(p(t, at, at + 0.3));
      css(ln, { o: k, tf: `translateY(${((1 - k) * 6).toFixed(2)}px)` });
    });
    $('#termPrompt').textContent = typed(t, TERM_MSG, A.typePrompt);
    const blinkOn = Math.floor(t * 2.2) % 2 === 0;
    css($('#termCaret'), { o: t < A.enterPrompt && (t > A.typePrompt[0] || blinkOn) ? 1 : 0 });
    els.reply2.forEach((r, i) => {
      const k = E.out(p(t, A.reply2[i], A.reply2[i] + 0.35));
      css(r, { o: k, tf: `translateY(${((1 - k) * 6).toFixed(2)}px)` });
    });
    $('#termFinal').textContent = TERM_FINAL.slice(0, Math.floor(p(t, A.reply2[3], A.reply2[3] + 1.0) * TERM_FINAL.length));

    // cursor + clicks
    const [cx, cy, co] = cursorAt(t);
    const pressK = clicks.reduce((m, [at]) => Math.max(m, Math.sin(Math.PI * p(t, at - 0.04, at + 0.14))), 0);
    css($('#cursor'), { o: co, tf: `translate(${(cx - 5).toFixed(2)}px, ${(cy - 3).toFixed(2)}px) scale(${(1 - pressK * 0.16).toFixed(4)})` });
    let rip = null;
    clicks.forEach(([at, pos]) => { if (t >= at && t < at + 0.55) rip = [p(t, at, at + 0.55), pos]; });
    if (rip) {
      const [k, [x, y]] = rip;
      css($('#ripple'), { o: (1 - k) * 0.9, tf: `translate(${x.toFixed(2)}px, ${y.toFixed(2)}px) scale(${lerp(0.3, 1.5, E.out(k)).toFixed(4)})` });
    } else css($('#ripple'), { o: 0 });
  }

  // Terminal j goes to account ACCT_ASSIGN[j]: fewest terminals first, then the most quota left.
  const ACCT_ASSIGN = [0, 1, 0];
  function accounts(t) {
    const C = T.accounts;
    const o = E.io(p(t, C.start, C.start + 0.5)) * (1 - E.io(p(t, C.out, C.end)));
    css($('#accts'), { o });
    if (o <= 0) return;
    const kc = E.out(p(t, C.start + 0.1, C.start + 0.8));
    css($('#accts .cap'), { o: kc, tf: `translateY(${((1 - kc) * 22).toFixed(2)}px)`, f: blur((1 - kc) * 8) });
    $$('#accts .dots b').forEach((b, i) => { b.style.transform = `scaleX(${i < 4 ? 1 : E.io(p(t, C.start, C.out)).toFixed(4)})`; });
    const pk = E.out5(p(t, C.panel, C.panel + 0.6));
    css($('#acctPanel'), { o: clamp(pk * 1.5), tf: `translateY(${((1 - pk) * 50).toFixed(2)}px)` });
    els.accts.forEach((r, i) => {
      const k = E.out5(p(t, C.rows[i], C.rows[i] + 0.5));
      css(r, { o: k, tf: `translateY(${((1 - k) * 20).toFixed(2)}px)` });
      const mine = ACCT_ASSIGN.map((a, j) => (a === i ? C.terms[j] + 0.45 : null)).filter(x => x !== null);
      const em = r.querySelector('.load em');
      if (em) em.textContent = String(mine.filter(x => t >= x).length);
      css(r.querySelector('.ring'), { o: mine.reduce((m, x) => Math.max(m, Math.sin(Math.PI * p(t, x - 0.1, x + 0.7))), 0) });
      const skip = r.querySelector('.skip');
      if (skip) css(skip, { o: E.out(p(t, C.terms[0] + 0.2, C.terms[0] + 0.5)), tf: `translateX(${(Math.sin(t * 60) * 3 * Math.sin(Math.PI * p(t, C.terms[0] + 0.2, C.terms[0] + 0.6))).toFixed(2)}px)` });
    });
    els.acctBars.forEach(({ el, v }, n) => {
      const row = Math.floor(n / 2);
      el.style.transform = `scaleX(${(v * E.out(p(t, C.rows[row] + 0.25, C.rows[row] + 1.0))).toFixed(4)})`;
    });
    css($('#termCol .tlabel'), { o: E.out(p(t, C.terms[0] - 0.4, C.terms[0])) });
    els.pills.forEach((pl, j) => {
      const at = C.terms[j];
      const k = E.out5(p(t, at, at + 0.45));
      css(pl, { o: clamp(k * 2), tf: `translateX(${((1 - k) * 90).toFixed(2)}px)` });
      css(pl.querySelector('.to'), { o: E.out(p(t, at + 0.5, at + 0.7)), tf: `scale(${Math.max(0, E.back(p(t, at + 0.5, at + 0.8), 2)).toFixed(4)})` });
      const w = els.acctWires[j];
      const d = E.io(p(t, at + 0.1, at + 0.5));
      w.path.style.opacity = (d > 0 ? lerp(0.9, 0.35, p(t, at + 0.6, at + 1.4)) : 0).toFixed(3);
      const pt = w.path.getPointAtLength(d * w.len);
      w.dot.setAttribute('cx', pt.x.toFixed(2));
      w.dot.setAttribute('cy', pt.y.toFixed(2));
      w.dot.style.opacity = d > 0 && d < 1 ? 1 : 0;
    });
  }

  function perks(t) {
    const P = T.perks;
    const o = E.io(p(t, P.start, P.start + 0.6)) * (1 - E.io(p(t, P.out, P.end)));
    css($('#perks'), { o });
    if (o <= 0) return;
    const [a, b] = $('#perksHead').children;
    reveal(a, t, P.head, { stagger: 0.05, dur: 0.6 });
    reveal(b, t, P.head + 0.2, { stagger: 0.045, dur: 0.6 });
    $$('#perkRow .perk').forEach((el, i) => {
      const k = E.out5(p(t, P.tiles[i], P.tiles[i] + 0.75));
      css(el, { o: clamp(k * 1.5), tf: `translateY(${((1 - k) * 70).toFixed(2)}px)` });
      el.querySelector('.pi').style.transform = `scale(${Math.max(0, E.back(p(t, P.tiles[i] + 0.15, P.tiles[i] + 0.55), 2.2)).toFixed(4)})`;
    });
    const drift = p(t, P.start, P.end);
    $('#perkRow').style.transform = `translateY(${(-drift * 14).toFixed(2)}px)`;
    $('#perksHead').style.transform = `translateY(${(-drift * 8).toFixed(2)}px)`;
  }

  function letter(t) {
    const L = T.letter;
    const layer = $('#letter');
    const o = E.io(p(t, L.start, L.start + 0.9)) * (1 - E.io(p(t, L.out, L.end)));
    css(layer, { o });
    if (o <= 0) return;
    const img = $('#art img');
    const k = p(t, L.start, L.end);
    img.style.transform = `scale(${lerp(1.12, 1.0, E.out(k)).toFixed(5)}) translate(${lerp(-10, 6, k).toFixed(2)}px, ${lerp(8, -4, k).toFixed(2)}px)`;
    const ar = E.out(p(t, L.start + 0.2, L.start + 1.8));
    css($('#art'), { o: ar, f: blur((1 - ar) * 14) });
    const sweep = E.io(p(t, L.start + 0.6, L.start + 3.2));
    css($('#artLight'), { o: Math.sin(Math.PI * sweep) * 0.9, tf: `translateX(${lerp(-70, 70, sweep).toFixed(2)}%)` });
    css($('#letterKicker'), { o: E.out(p(t, L.lines[0] - 0.6, L.lines[0] + 0.4)) * 1 });
    const ls = $$('#letterText [data-split]');
    ls.forEach((el, i) => reveal(el, t, L.lines[i], { stagger: 0.055, dur: 0.8, y: 14, b: 12 }));
    const drift = E.out(p(t, L.lines[0], L.end));
    $('#letterText').style.transform = `translateY(${(-drift * 14).toFixed(2)}px)`;
  }

  function end(t) {
    const N = T.end;
    const layer = $('#end');
    const o = E.io(p(t, N.start, N.start + 0.8));
    css(layer, { o });
    if (o <= 0) return;
    const a = N.start + 0.3;
    logoAt(els.endLogo, t, { pixels: a, frame: a + 0.55, bars: [a + 0.85, a + 1.0], blink: [N.blink], speed: 0.7 });
    css($('#endLogo'), { tf: `translateY(${(barImpact(t, [a + 0.85, a + 1.0]) * 4).toFixed(2)}px)` });
    const k = (d, dur = 0.8) => E.out(p(t, a + d, a + d + dur));
    const rise = (el, d) => { const v = k(d); css(el, { o: v, tf: `translateY(${((1 - v) * 16).toFixed(2)}px)`, f: blur((1 - v) * 6) }); };
    rise($('#endWord'), 0.9);
    rise($('#endSub'), 1.15);
    $$('#endPlat span').forEach((s, i) => rise(s, 1.45 + i * 0.1));
    rise($('#endUrl'), 1.85);
    rise($('#endGit'), 2.05);
    rise($('#endNote'), 2.3);
  }

  function chrome(t, frame) {
    const dark = (t > T.pain.holdShrink && t < T.room.start + 0.4) || (t > T.letter.start + 0.4 && t < T.letter.out + 0.4);
    const g = $('#grain');
    g.style.opacity = dark ? 0.55 : 0.3;
    g.style.transform = `translate(${Math.floor(hash(frame) * 200) - 100}px, ${Math.floor(hash(frame + 0.5) * 200) - 100}px)`;
    $('#vignette').style.opacity = dark ? 1 : 0.5;
  }

  function render(t) {
    t = clamp(t, 0, T.duration);
    pain(t);
    room(t);
    app(t);
    accounts(t);
    perks(t);
    letter(t);
    end(t);
    chrome(t, Math.round(t * T.fps));
  }

  // ---------- boot ----------
  const params = new URLSearchParams(location.search);
  const renderMode = params.has('render');
  if (renderMode) document.body.classList.add('render');

  function fit() {
    const stage = $('#stage');
    const s = renderMode ? 1 : Math.min(innerWidth / T.width, innerHeight / T.height);
    stage.style.position = 'absolute';
    stage.style.left = ((innerWidth - T.width * s) / 2) + 'px';
    stage.style.top = ((innerHeight - T.height * s) / 2) + 'px';
    stage.style.transform = `scale(${s})`;
  }

  build();
  fit();
  addEventListener('resize', fit);
  render(0);

  const ready = (async () => {
    await document.fonts.ready;
    const img = $('#art img');
    if (!img.complete) await new Promise(r => { img.onload = img.onerror = r; });
    try { await img.decode(); } catch { /* the frame still renders without the image */ }
    measure();
    render(0);
  })();

  window.__promo = {
    ready,
    duration: T.duration,
    fps: T.fps,
    seek: t => render(t),
  };

  if (renderMode) return;

  // Preview player.
  let now = parseFloat(params.get('t') || '0'), playing = !params.has('t'), last = null;
  const scrub = $('#scrub'), tc = $('#tc'), play = $('#play');
  scrub.max = T.duration;
  const show = () => { render(now); scrub.value = now; tc.textContent = now.toFixed(2).padStart(5, '0'); play.textContent = playing ? '暂停' : '播放'; };
  const loop = ts => {
    if (playing && last !== null) { now += (ts - last) / 1000; if (now >= T.duration) { now = T.duration; playing = false; } }
    last = ts;
    show();
    requestAnimationFrame(loop);
  };
  ready.then(() => requestAnimationFrame(loop));
  play.onclick = () => { if (now >= T.duration) now = 0; playing = !playing; };
  $('#restart').onclick = () => { now = 0; playing = true; };
  scrub.oninput = () => { now = parseFloat(scrub.value); playing = false; };
  addEventListener('keydown', e => {
    if (e.code === 'Space') { e.preventDefault(); play.onclick(); }
    if (e.code === 'ArrowRight') { now = Math.min(T.duration, now + (e.shiftKey ? 0.1 : 1)); playing = false; }
    if (e.code === 'ArrowLeft') { now = Math.max(0, now - (e.shiftKey ? 0.1 : 1)); playing = false; }
  });
})();
