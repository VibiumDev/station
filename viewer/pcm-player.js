// An AudioWorklet that plays PCM chunks posted to it: Float32Array samples,
// interleaved by channel, at the context's rate. It keeps at most 25
// chunks, so a slow link drops sound rather than falling behind.
class PCMPlayer extends AudioWorkletProcessor {
  constructor() {
    super();
    this.buf = [];
    this.pos = 0;
    this.port.onmessage = (e) => { this.buf.push(e.data); };
  }
  process(inputs, outputs) {
    var out = outputs[0];
    var ch = out.length;
    var frames = out[0].length;
    var written = 0;
    while (written < frames && this.buf.length > 0) {
      var chunk = this.buf[0];
      var spc = chunk.length / ch;
      var avail = spc - this.pos;
      var n = Math.min(avail, frames - written);
      for (var c = 0; c < ch; c++) {
        for (var i = 0; i < n; i++) out[c][written + i] = chunk[(this.pos + i) * ch + c];
      }
      written += n;
      this.pos += n;
      if (this.pos >= spc) { this.buf.shift(); this.pos = 0; }
    }
    for (var c2 = 0; c2 < ch; c2++) {
      for (var j = written; j < frames; j++) out[c2][j] = 0;
    }
    while (this.buf.length > 25) { this.buf.shift(); this.pos = 0; }
    return true;
  }
}
registerProcessor('pcm-player', PCMPlayer);
