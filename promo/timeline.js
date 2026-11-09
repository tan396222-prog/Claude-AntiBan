// Shared timing for the promo page (promo.js) and the soundtrack (soundtrack.mjs).
// All values are seconds from the start of the video. Loaded as a classic script
// in the browser and through require() in Node.
(function (root) {
  const TL = {
    duration: 61.3,
    fps: 60,
    width: 1920,
    height: 1080,

    // Act 1 · the problem: the real on-hold screen, then four hard-cut beats.
    pain: {
      slam: 0.12,
      holdScroll: [0.75, 1.75],
      holdMark: 1.55,
      holdShrink: 2.1,
      losses: [2.6, 2.9, 3.2],
      b2: 4.3,
      rows: [4.65, 4.95, 5.25, 5.55],
      total: 5.9,
      b3: 7.3,
      wires: 7.65,
      tags: [8.15, 8.45, 8.75, 9.05, 9.35],
      b4: 10.3,
      vm: 10.45,
      vmMem: 11.4,
      vmTodo: [11.8, 12.05, 12.3, 12.55],
      b5: 13.1,
      end: 14.65,
    },

    // Act 2 · the answer.
    room: {
      start: 14.65,
      pixels: 14.9,
      frame: 15.55,
      bars: [15.9, 16.1],
      blink: 16.9,
      word: 16.45,
      pos: 17.0,
      sub: 17.65,
      end: 20.3,
    },

    // Act 3 · the real workbench: ready → Claude Desktop → Claude terminal → browser login.
    app: {
      rise: 20.0,
      step1: 21.0,
      safetyOpen: 21.5,
      checks: 21.8,
      checkStep: 0.2,
      allPass: 24.0,
      safetyClose: 24.8,
      rings: [25.5, 25.8, 26.1],
      step2: 26.6,
      clickDesktop: 27.35,
      desktopBoot: 27.5,
      clickInput: 28.3,
      typeMsg: [28.45, 29.65],
      send: 29.8,
      reply: [30.1, 31.4],
      step3: 31.7,
      clickTermIcon: 32.4,
      clickClaudeTerm: 33.1,
      termLines: 33.3,
      opening: 34.25,
      step4: 34.6,
      browserOpen: 34.75,
      clickAuthorize: 35.9,
      authDone: 36.05,
      backToTerm: 36.8,
      typePrompt: [37.6, 38.7],
      enterPrompt: 38.8,
      reply2: [39.05, 39.55, 40.05, 40.65],
      out: 42.6,
      end: 43.6,
    },

    // Act 3b · several accounts, assigned per terminal.
    accounts: {
      start: 43.2,
      panel: 43.45,
      rows: [43.65, 43.8, 43.95],
      terms: [44.6, 45.45, 46.3],
      out: 48.2,
      end: 48.8,
    },

    // Act 4 · everything that comes in the box.
    perks: {
      start: 48.6,
      head: 48.9,
      tiles: [49.4, 49.65, 49.9, 50.15],
      out: 52.6,
      end: 53.2,
    },

    // Act 5 · why the cage exists.
    letter: {
      start: 53,
      lines: [53.7, 54.5, 55.3],
      out: 56.3,
      end: 56.9,
    },

    // Act 6 · end card.
    end: {
      start: 56.9,
      blink: 59.9,
    },
  };

  if (typeof module === 'object' && module.exports) module.exports = TL;
  else root.TL = TL;
})(this);
