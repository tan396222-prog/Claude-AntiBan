// Standalone AudioWorklet module: Vite emits this file without dependencies.
// Keep only a short live buffer; reconnecting or a stalled UI must never replay
// seconds of old sound. Source PCM is always 48 kHz stereo signed little-endian.
export class PCMQueue {
  constructor() {
    this.capacity = 12000;
    this.left = new Float32Array(this.capacity);
    this.right = new Float32Array(this.capacity);
    this.read = 0;
    this.count = 0;
    this.fraction = 0;
    this.playing = false;
  }
  push(data) {
    if (!(data instanceof ArrayBuffer) || data.byteLength % 4) return;
    const view = new DataView(data);
    const frames = view.byteLength / 4;
    if (this.count + frames > this.capacity) {
      // Drop the backlog together so repeated late packets cannot accumulate.
      this.read = this.count = this.fraction = 0;
      this.playing = false;
    }
    for (let i = Math.max(0, frames - this.capacity); i < frames; i++) {
      const at = (this.read + this.count) % this.capacity;
      this.left[at] = view.getInt16(i * 4, true) / 32768;
      this.right[at] = view.getInt16(i * 4 + 2, true) / 32768;
      this.count++;
    }
  }
  render(left, right, outputRate) {
    left.fill(0);
    right.fill(0);
    if (!this.playing && this.count >= 2880) this.playing = true;
    if (!this.playing) return;
    const step = 48000 / outputRate;
    for (let i = 0; i < left.length; i++) {
      if (this.count < Math.max(2, Math.ceil(this.fraction + step))) {
        this.playing = false;
        this.read = this.count = this.fraction = 0;
        return;
      }
      const next = (this.read + 1) % this.capacity;
      left[i] = this.left[this.read] + (this.left[next] - this.left[this.read]) * this.fraction;
      right[i] = this.right[this.read] + (this.right[next] - this.right[this.read]) * this.fraction;
      this.fraction += step;
      const consumed = Math.floor(this.fraction);
      this.fraction -= consumed;
      this.read = (this.read + consumed) % this.capacity;
      this.count -= consumed;
    }
  }
}

if (typeof globalThis.registerProcessor === "function") {
  class CageAudioProcessor extends globalThis.AudioWorkletProcessor {
    constructor() {
      super();
      this.queue = new PCMQueue();
      this.port.onmessage = ({ data }) => {
        if (data === "reset") this.queue = new PCMQueue();
        else this.queue.push(data);
      };
    }
    process(_inputs, outputs) {
      const [left, right] = outputs[0];
      if (left && right) this.queue.render(left, right, globalThis.sampleRate);
      return true;
    }
  }
  globalThis.registerProcessor("cage-audio", CageAudioProcessor);
}
