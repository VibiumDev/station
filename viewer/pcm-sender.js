// An AudioWorklet that hands every block of its input to the page, as
// Float32Array samples at the context's rate, for the microphone socket.
class PCMSender extends AudioWorkletProcessor {
  process(inputs) {
    var ch = inputs[0][0];
    if (ch) this.port.postMessage(ch.slice(0));
    return true;
  }
}
registerProcessor('pcm-sender', PCMSender);
