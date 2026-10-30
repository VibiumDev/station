// The shell: the viewer a station and a Roadie host both serve, with
// Roadie's toolbar, picture, settings panel and on-screen keyboard. It is
// told where the device is by the page's data-config (a Roadie's own
// endpoints, or a station's relayed ones) and knows nothing of what the
// device is: a rig's own buttons and panels come from an extras module
// loaded after it, through window.__roadieShell.
//
// Input goes out in picture space (fractions of the picture, 0..32767,
// "space": "picture"); the host folds its crop in. On a station the page
// takes a hold on the device first, renews it, and falls back to the
// relayed MJPEG stream and input socket when WebRTC cannot connect.
(function () {
  var cfg = {};
  try { cfg = JSON.parse(document.body.dataset.config || '{}'); } catch (e) {}
  var api = cfg.api || {};
  var station = cfg.mode === 'station';
  var assets = '';
  if (document.currentScript && document.currentScript.src) assets = document.currentScript.src.replace(/\/viewer\/shell\.js(\?.*)?$/, '');
  function $(id) { return document.getElementById(id); }
  var img = $('feed');
  var overlay = $('overlay');
  var unmuteBtn = $('unmute');
  var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  function wsURL(path) { return /^wss?:/.test(path) ? path : proto + '//' + location.host + path; }
  // Requests to the host: a station's page sends its session cookie and,
  // on writes, the CSRF token.
  function opts(o) {
    o = o || {};
    if (station) {
      o.credentials = 'same-origin';
      if (o.method && o.method !== 'GET' && cfg.csrf) {
        o.headers = o.headers || {};
        o.headers['X-CSRF-Token'] = cfg.csrf;
      }
    }
    return o;
  }
  // hostURL maps a path on the host, as a Roadie serves it, to where this
  // page reaches it.
  function hostURL(path) {
    if (path === '/health') return api.health;
    if (path === '/snapshot' || path.indexOf('/snapshot?') === 0) return api.snapshot + path.slice('/snapshot'.length);
    if (path === '/api' || path.indexOf('/api/') === 0) return api.api + path.slice('/api'.length);
    return path;
  }
  var listeners = {};
  function on(ev, fn) { (listeners[ev] = listeners[ev] || []).push(fn); }
  function emit(ev, data) { (listeners[ev] || []).forEach(function (fn) { try { fn(data); } catch (e) {} }); }
  var saved = {};
  function loadSettings() {
    try { saved = JSON.parse(localStorage.getItem('roadie-settings')) || {}; } catch (e) { saved = {}; }
    return saved;
  }
  function saveSettings(obj) {
    loadSettings();
    for (var k in obj) saved[k] = obj[k];
    try { localStorage.setItem('roadie-settings', JSON.stringify(saved)); } catch (e) {}
  }
  loadSettings();
  var qs = new URLSearchParams(location.search);

  // What the transport module and the extras read.
  window.__roadieWebRTC = !!cfg.webrtc;
  if (cfg.transport) window.__roadieTransport = cfg.transport;
  if (cfg.info && cfg.info.platform) window.__roadiePlatform = cfg.info.platform;

  var healthOk = false;
  var serving = false;
  var lastHealth = null;

  function showOverlay(msg) {
    overlay.classList.remove('notice');
    overlay.textContent = msg || 'Disconnected — waiting for capture device…';
    overlay.style.display = 'flex';
  }
  // A message over the picture rather than covering it.
  function showNotice(msg) {
    overlay.classList.add('notice');
    overlay.textContent = '';
    var span = document.createElement('span');
    span.textContent = msg;
    overlay.appendChild(span);
    overlay.style.display = 'flex';
  }
  function tryHideOverlay() {
    if (healthOk) overlay.style.display = 'none';
  }

  // --- The feed: the MJPEG stream read with fetch and each frame shown in
  // the <img>. An <img> given the stream itself freezes on its last frame
  // when the stream ends, with no event to say so; this way the page knows
  // when it ends, or stalls, and starts it again.
  var stream = null;      // the AbortController of the stream being read
  var lastFrame = 0;      // when its last frame arrived
  var shownURL = null, nextURL = null;
  var STALL_MS = 4000;
  var mjpegFrames = 0, mjpegBytes = 0;
  var shownSize = '';
  // For the stats box (webrtc.js): what the MJPEG feed delivers, and the
  // sound on the audio and mic sockets while they are open.
  window.__roadieMJPEGStats = function () {
    return {frames: mjpegFrames, bytes: mjpegBytes, size: shownSize,
            quality: extrasQuality ? extrasQuality() : 0,
            via: station ? 'through the station' : '',
            audio: audioWs && audioParams ? {format: 'PCM ' + audioParams.sampleRate / 1000 + ' kHz ' +
                     (audioParams.channels === 2 ? 'stereo' : audioParams.channels === 1 ? 'mono' : audioParams.channels + ' ch'), bytes: audioBytes} : null,
            mic: pttWs ? {bytes: micBytes} : null};
  };
  var extrasQuality = null;  // the extras may say the stream's JPEG quality
  function showFrame(bytes) {
    lastFrame = Date.now();
    mjpegFrames++;
    mjpegBytes += bytes.length;
    if (nextURL) return;  // the last one is still decoding: skip this one
    nextURL = URL.createObjectURL(new Blob([bytes], {type: 'image/jpeg'}));
    img.src = nextURL;
  }
  img.onload = function () {
    // The transport module's see-through placeholder shows the <img> too,
    // sized like the video, so input lands on it; only a frame of ours
    // counts as one.
    img.style.display = '';
    tryHideOverlay();
    if (!nextURL) { relayout(); return; }
    var size = img.naturalWidth + 'x' + img.naturalHeight;
    if (size !== shownSize) { shownSize = size; relayout(); }
    if (shownURL) URL.revokeObjectURL(shownURL);
    shownURL = nextURL;
    nextURL = null;
    emit('frame', img);
  };
  img.onerror = function () {
    if (nextURL) URL.revokeObjectURL(nextURL);
    nextURL = null;
  };
  function indexOfCRLF2(buf) {
    for (var i = 0; i + 3 < buf.length; i++) {
      if (buf[i] === 13 && buf[i + 1] === 10 && buf[i + 2] === 13 && buf[i + 3] === 10) return i;
    }
    return -1;
  }
  // takeFrames shows each whole frame in buf (parts of
  // "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: N\r\n\r\n"
  // and N bytes) and returns what is left of the next.
  var decoder = new TextDecoder();
  function takeFrames(buf) {
    for (;;) {
      var end = indexOfCRLF2(buf);
      if (end < 0) return buf;
      var m = /Content-Length:\s*(\d+)/i.exec(decoder.decode(buf.subarray(0, end)));
      var start = end + 4;
      if (!m) { buf = buf.subarray(start); continue; }
      var len = parseInt(m[1], 10);
      if (buf.length < start + len) return buf;
      showFrame(buf.slice(start, start + len));
      buf = buf.subarray(start + len);
    }
  }
  function startStream() {
    if (window.__roadieExternalFeed) return;  // WebRTC is showing the picture
    if (station && !holding()) return;        // no hold yet, or watching nobody
    if (stream) stream.abort();
    var ctl = new AbortController();
    stream = ctl;
    lastFrame = Date.now();
    // ?as=bytes: Safari hands fetch nothing of a multipart/x-mixed-replace
    // response, so ask for the same stream under a plain type.
    fetch(api.stream + (api.stream.indexOf('?') < 0 ? '?' : '&') + 'as=bytes', opts({signal: ctl.signal, cache: 'no-store'})).then(function (r) {
      if (!r.ok || !r.body) throw new Error('stream: ' + r.status);
      var reader = r.body.getReader();
      var buf = new Uint8Array(0);
      function pump() {
        return reader.read().then(function (res) {
          if (res.done) throw new Error('stream ended');
          var joined = new Uint8Array(buf.length + res.value.length);
          joined.set(buf);
          joined.set(res.value, buf.length);
          buf = takeFrames(joined);
          return pump();
        });
      }
      return pump();
    }).catch(function () {
      if (stream === ctl) stream = null;  // the watchdog starts another
    });
  }
  function stopStream() {
    if (stream) { stream.abort(); stream = null; }
  }
  // The WebRTC module takes the picture over and gives it back through
  // these; on a station the fallback's input socket comes with it.
  var fallen = false;
  window.__roadieFeed = {
    stop: function () { stopStream(); fallen = false; closeHID(); },
    start: function () {
      fallen = true;
      if (!stream && serving) startStream();
      if (driving()) openHID();
      applySpeaker();
    }
  };
  // The watchdog: a stream that ended, or has sent nothing for STALL_MS
  // (a network that dropped it silently, a laptop back from sleep), is
  // started again once the host is serving.
  setInterval(function () {
    if (stream && Date.now() - lastFrame > STALL_MS) {
      stream.abort();
      stream = null;
    }
    if (!stream && serving && (!station || fallen)) startStream();
  }, 1000);

  // --- Health: the host's state once a second. A station relays the
  // rig's own; a Roadie's says whether it serves pictures, whether the
  // target's screen is dark, and whether it is busy turning the screen.
  function poll() {
    if (station && !holding()) return;
    var ctl = new AbortController();
    var timeout = setTimeout(function () { ctl.abort(); }, 3000);
    fetch(api.health, opts({signal: ctl.signal, cache: 'no-store'})).then(function (r) {
      // On a station the answer says which hold it rode on: ours, or the
      // one we watch. Another id means the device changed hands, and a
      // free device means the hold we knew is over.
      if (station && r.status === 409) return r.json().then(function (j) { if (j.free) nobody(); throw new Error('free'); });
      var rode = r.headers.get('X-Hold');
      if (station && hold && rode && rode !== hold.id) changedHands();
      return r.json();
    }).then(function (data) {
      clearTimeout(timeout);
      lastHealth = data;
      serving = data.status === 'ok' || data.status === 'no_signal';
      hostBusy = !!data.rotating;
      setBusy(hostBusy || heldBusy, 'Rotating the screen…');
      if (serving && !stream && (!station || fallen)) startStream();
      if (data.status === 'ok') {
        healthOk = true;
        tryHideOverlay();
      } else {
        healthOk = false;
        if (data.status === 'no_signal') showNotice('No signal: the target’s screen is off or locked, or its HDMI cable is out');
        else if (data.status === 'connecting') showOverlay('Connecting…');
        else showOverlay();
      }
      if (data.audio) unmuteBtn.style.display = 'flex';
      emit('health', data);
    }).catch(function (e) {
      clearTimeout(timeout);
      healthOk = serving = false;
      if (!e || e.message !== 'free') showOverlay();  // a free device keeps its own message
    });
  }
  setInterval(poll, 1000);

  // --- Sound over a WebSocket, when the picture is MJPEG: PCM from the
  // host, played by an AudioWorklet (a ScriptProcessor where there is
  // none). Over WebRTC the sound comes with the picture.
  var audioCtx = null, audioWs = null, audioParams = null, audioBytes = 0;
  var muted = true;
  var audioQueue = [];
  var workletReady = false, workletNode = null;
  function useWorklet() { return window.isSecureContext && audioCtx && audioCtx.audioWorklet; }
  function setupWorklet(params) {
    audioCtx.audioWorklet.addModule(assets + '/viewer/pcm-player.js').then(function () {
      if (!audioCtx) return;
      workletNode = new AudioWorkletNode(audioCtx, 'pcm-player', {outputChannelCount: [params.channels]});
      workletNode.connect(audioCtx.destination);
      workletReady = true;
    }).catch(function () { setupScriptProcessor(params); });
  }
  function setupScriptProcessor(params) {
    var readPos = 0;
    var node = audioCtx.createScriptProcessor(4096, 0, params.channels);
    node.onaudioprocess = function (ev) {
      var out = ev.outputBuffer, frames = out.length, channels = out.numberOfChannels;
      for (var ch = 0; ch < channels; ch++) { var b = out.getChannelData(ch); for (var i = 0; i < frames; i++) b[i] = 0; }
      var written = 0;
      while (written < frames && audioQueue.length > 0) {
        var chunk = audioQueue[0], spc = chunk.length / channels, n = Math.min(spc - readPos, frames - written);
        for (var c = 0; c < channels; c++) {
          var b2 = out.getChannelData(c);
          for (var j = 0; j < n; j++) b2[written + j] = chunk[(readPos + j) * channels + c];
        }
        written += n; readPos += n;
        if (readPos >= spc) { audioQueue.shift(); readPos = 0; }
      }
      while (audioQueue.length > 25) { audioQueue.shift(); readPos = 0; }
    };
    node.connect(audioCtx.destination);
  }
  function int16ToFloat(i16) {
    var f = new Float32Array(i16.length);
    for (var i = 0; i < i16.length; i++) f[i] = i16[i] / 32768;
    return f;
  }
  function startAudio() {
    audioWs = new WebSocket(wsURL(api.audio));
    audioWs.binaryType = 'arraybuffer';
    var params = null;
    workletReady = false;
    workletNode = null;
    audioWs.onmessage = function (e) {
      if (!params) {
        params = JSON.parse(e.data);
        audioParams = params;
        audioCtx = new AudioContext({sampleRate: params.sampleRate});
        if (useWorklet()) setupWorklet(params); else setupScriptProcessor(params);
        return;
      }
      audioBytes += e.data.byteLength;
      var samples = params.format === 's16le' ? int16ToFloat(new Int16Array(e.data)) : new Float32Array(e.data);
      if (useWorklet()) {
        if (workletReady && workletNode) workletNode.port.postMessage(samples);
      } else {
        audioQueue.push(samples);
      }
    };
    var sock = audioWs;
    sock.onclose = function () {
      if (audioWs !== sock) return;  // closed on purpose
      audioWs = null;
      if (!muted && !rtcLive()) setTimeout(startAudio, 2000);
    };
  }
  function stopAudio() {
    if (audioWs) { var s = audioWs; audioWs = null; s.close(); }
    if (audioCtx) { try { audioCtx.close(); } catch (e) {} audioCtx = null; }
    audioQueue = [];
    workletReady = false;
    workletNode = null;
  }
  var muteIconSVG = '<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" fill="currentColor" viewBox="0 0 16 16"><path d="M6.717 3.55A.5.5 0 0 1 7 4v8a.5.5 0 0 1-.812.39L3.825 10.5H1.5A.5.5 0 0 1 1 10V6a.5.5 0 0 1 .5-.5h2.325l2.363-1.89a.5.5 0 0 1 .529-.06m7.137 2.096a.5.5 0 0 1 0 .708L12.207 8l1.647 1.646a.5.5 0 0 1-.708.708L11.5 8.707l-1.646 1.647a.5.5 0 0 1-.708-.708L10.793 8 9.146 6.354a.5.5 0 1 1 .708-.708L11.5 7.293l1.646-1.647a.5.5 0 0 1 .708 0"/></svg>';
  var volumeIconSVG = '<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" fill="currentColor" viewBox="0 0 16 16"><path d="M11.536 14.01A8.47 8.47 0 0 0 14.026 8a8.47 8.47 0 0 0-2.49-6.01l-.708.707A7.48 7.48 0 0 1 13.025 8c0 2.071-.84 3.946-2.197 5.303z"/><path d="M10.121 12.596A6.48 6.48 0 0 0 12.025 8a6.48 6.48 0 0 0-1.904-4.596l-.707.707A5.48 5.48 0 0 1 11.025 8a5.48 5.48 0 0 1-1.61 3.89z"/><path d="M8.707 11.182A4.5 4.5 0 0 0 10.025 8a4.5 4.5 0 0 0-1.318-3.182L8 5.525A3.5 3.5 0 0 1 9.025 8 3.5 3.5 0 0 1 8 10.475zM6.717 3.55A.5.5 0 0 1 7 4v8a.5.5 0 0 1-.812.39L3.825 10.5H1.5A.5.5 0 0 1 1 10V6a.5.5 0 0 1 .5-.5h2.325l2.363-1.89a.5.5 0 0 1 .529-.06"/></svg>';
  function rtcLive() { return !!(window.__roadieRTC && window.__roadieRTC.isPlaying()); }
  // The speaker: over WebRTC the sound comes with the picture and the
  // button mutes it; over MJPEG the button opens or closes the socket.
  function applySpeaker() {
    if (window.__roadieRTC) window.__roadieRTC.setMuted(muted);
    if (!muted && !rtcLive() && (!station || holding())) {
      if (!audioWs) startAudio();
    } else {
      stopAudio();
    }
  }
  unmuteBtn.onclick = function () {
    muted = !muted;
    unmuteBtn.innerHTML = muted ? muteIconSVG : volumeIconSVG;
    applySpeaker();
  };
  window.__roadieTransportChanged = function () { applySpeaker(); emit('transport', rtcLive() ? 'webrtc' : 'mjpeg'); };

  // --- The microphone: the browser's, into the device. Over WebRTC it
  // rides the connection's audio line; otherwise the mic socket carries
  // 16 kHz mono 16-bit, the way the host's own page sends it.
  var pttBtn = $('ptt');
  var pttWs = null, pttCtx = null, pttStream = null, pttNode = null;
  var talking = false;
  var micBytes = 0;
  if (!(window.isSecureContext && navigator.mediaDevices)) {
    pttBtn.style.opacity = '0.4';
    pttBtn.title = 'Voice input needs https or localhost';
  }
  function startTalk() {
    if (talking || !navigator.mediaDevices) return;
    if (!driving()) return;
    talking = true;
    pttBtn.classList.add('live');
    navigator.mediaDevices.getUserMedia({audio: {channelCount: 1, echoCancellation: true, noiseSuppression: true}}).then(function (stream) {
      if (!talking) { stream.getTracks().forEach(function (t) { t.stop(); }); return; }
      pttStream = stream;
      if (rtcLive() && window.__roadieRTC.setMic(stream.getAudioTracks()[0])) return;
      pttWs = new WebSocket(wsURL(api.mic));
      pttWs.binaryType = 'arraybuffer';
      pttCtx = new AudioContext({sampleRate: 16000});
      pttCtx.audioWorklet.addModule(assets + '/viewer/pcm-sender.js').then(function () {
        if (!talking || !pttCtx) return;
        var src = pttCtx.createMediaStreamSource(pttStream);
        pttNode = new AudioWorkletNode(pttCtx, 'pcm-sender', {numberOfInputs: 1, numberOfOutputs: 0});
        var acc = [], accLen = 0;
        pttNode.port.onmessage = function (ev) {
          if (!pttWs || pttWs.readyState !== 1) return;
          acc.push(ev.data); accLen += ev.data.length;
          if (accLen >= 320) {  // 20 ms batches: the host buffers little
            var f32 = new Float32Array(accLen), o = 0;
            for (var i = 0; i < acc.length; i++) { f32.set(acc[i], o); o += acc[i].length; }
            acc = []; accLen = 0;
            var i16 = new Int16Array(f32.length);
            for (var j = 0; j < f32.length; j++) { var s2 = Math.max(-1, Math.min(1, f32[j])); i16[j] = s2 < 0 ? s2 * 32768 : s2 * 32767; }
            pttWs.send(i16.buffer);
            micBytes += i16.buffer.byteLength;
          }
        };
        src.connect(pttNode);
      }).catch(function () { stopTalk(); });
    }).catch(function () { stopTalk(); });
  }
  function stopTalk() {
    talking = false;
    pttBtn.classList.remove('live');
    if (window.__roadieRTC) window.__roadieRTC.setMic(null);
    if (pttNode) { pttNode.disconnect(); pttNode = null; }
    if (pttCtx) { try { pttCtx.close(); } catch (e) {} pttCtx = null; }
    if (pttStream) { pttStream.getTracks().forEach(function (t) { t.stop(); }); pttStream = null; }
    if (pttWs) { var s = pttWs; pttWs = null; s.close(); }
  }
  pttBtn.onclick = function () { if (talking) stopTalk(); else startTalk(); };

  // --- Settings panel ---
  var qbtn = $('qbtn');
  var qslider = $('qslider');
  var zoomRange = $('zoomRange');
  var zoomVal = $('zoomVal');
  var alignTLBtn = $('alignTLBtn');
  var alignCenterBtn = $('alignCenterBtn');
  // Centered for a person at the page; top-left for a page embedded in a
  // frame or driven by a script (?minimal=1), where a fixed origin helps.
  var alignment = cfg.minimal ? 'top-left' : 'center';

  function positionPanel() {
    var tr = toolbarEl.getBoundingClientRect();
    var st = qslider.style;
    st.left = st.right = st.top = st.bottom = '';
    var room;
    if (toolbarSide === 'top') {
      st.top = 'calc(100% + 2px)'; st.left = '8px';
      room = window.innerHeight - tr.bottom - 10;
    } else if (toolbarSide === 'bottom') {
      st.bottom = 'calc(100% + 2px)'; st.left = '8px';
      room = tr.top - 10;
    } else {
      st.top = '8px';
      if (window.innerWidth - tr.right > 230) st.left = 'calc(100% + 2px)';
      else st.right = 'calc(100% - 2px)';
      room = window.innerHeight - tr.top - 18;
    }
    st.maxHeight = Math.max(120, room) + 'px';
    st.overflowY = 'auto';
  }
  qbtn.onclick = function () {
    var vis = qslider.style.display !== 'none';
    if (vis) {
      qslider.style.display = 'none';
      menuClosed();
    } else {
      positionPanel();
      qslider.style.display = 'block';
      emit('menu', true);
    }
  };
  // A press anywhere but the menu and its button closes the menu, as on a
  // Mac, and does nothing else: a click meant to close the menu doesn't
  // also tap the device, type a key or press another button. Everything
  // the press would set off, up to its click, is swallowed.
  var swallowing = false;
  document.addEventListener('pointercancel', function () { swallowing = false; }, true);
  document.addEventListener('pointerdown', function (e) {
    swallowing = false;  // a release lost outside the window ends it too
    if (qslider.style.display === 'none' || qslider.contains(e.target) || qbtn.contains(e.target)) return;
    qslider.style.display = 'none';
    menuClosed();
    swallowing = true;
    e.preventDefault();
    e.stopPropagation();
  }, true);
  ['mousedown', 'mouseup', 'touchstart', 'touchmove', 'touchend', 'pointermove', 'pointerup', 'click', 'contextmenu'].forEach(function (type) {
    document.addEventListener(type, function (e) {
      if (!swallowing) return;
      e.preventDefault();
      e.stopPropagation();
      // The click comes straight after the release, if at all.
      if (type === 'pointerup') setTimeout(function () { swallowing = false; }, 0);
    }, {capture: true, passive: false});
  });

  // --- Toolbar auto-hide ---
  // Tucked off the edge until the pointer comes within half a button's
  // width of it; tucked again once the pointer has left it, unless the
  // menu is open. Where the picture fits beside the toolbar's column, the
  // column stays and nothing moves. Where it doesn't (a narrow window),
  // the tucked toolbar gives its column to the picture and floats over it
  // when it comes back.
  var toolbarEl = $('toolbar');
  var REVEAL_PX = 18;
  var autoHide = false;
  var tuckTimer = null, overToolbar = false;
  var toolbarSide = 'right';
  function roomBeside() {
    var nw = img.naturalWidth, nh = img.naturalHeight;
    if (!nw || !nh) return true;
    var across = toolbarSide === 'top' || toolbarSide === 'bottom';
    var W = document.documentElement.clientWidth;
    var H = document.documentElement.clientHeight - $('onscreen-kbd').offsetHeight;
    var bar = across ? toolbarEl.offsetHeight : toolbarEl.offsetWidth;
    var cap = fitMode ? Infinity : nw;  // fit scales up too
    var fit = function (w, h) { return Math.min(cap, w, nw * h / nh); };
    var full = fit(W, H);
    var kept = across ? fit(W, H - bar) : fit(W - bar, H);
    var zoom = fitMode ? 1 : zoomPct / 100;
    var zoomedFits = across ? kept * nh / nw * zoom <= H - bar + 0.5 : kept * zoom <= W - bar + 0.5;
    return full <= kept + 0.5 && zoomedFits;
  }
  function relayout() {
    if (fitMode) layoutZoom();
    if (!autoHide) return;
    var room = roomBeside();
    if (toolbarEl.classList.contains('tucked')) toolbarEl.classList.toggle('floating', !room);
    else if (room) toolbarEl.classList.remove('floating');
  }
  function tuck() {
    if (autoHide && qslider.style.display === 'none' && !overToolbar) {
      toolbarEl.classList.add('tucked');
      relayout();
    }
  }
  function scheduleTuck(ms) { clearTimeout(tuckTimer); tuckTimer = setTimeout(tuck, ms); }
  function untuck() { clearTimeout(tuckTimer); toolbarEl.classList.remove('tucked'); }
  function menuClosed() {
    if (autoHide) scheduleTuck(600);
    else toolbarEl.classList.remove('floating', 'tucked');
    emit('menu', false);
  }
  window.addEventListener('resize', relayout);
  if (window.ResizeObserver) new ResizeObserver(relayout).observe($('viewer'));
  function applyAutoHide(on) {
    autoHide = on;
    if (on) scheduleTuck(600);
    else if (qslider.style.display === 'none') menuClosed();
  }
  toolbarEl.addEventListener('mouseenter', function () { overToolbar = true; untuck(); });
  toolbarEl.addEventListener('mouseleave', function () { overToolbar = false; scheduleTuck(600); });
  function nearEdge(e) {
    switch (toolbarSide) {
      case 'left': return e.clientX <= REVEAL_PX;
      case 'top': return e.clientY <= REVEAL_PX;
      case 'bottom': return e.clientY >= window.innerHeight - REVEAL_PX;
      default: return e.clientX >= window.innerWidth - REVEAL_PX;
    }
  }
  document.addEventListener('mousemove', function (e) {
    if (toolbarEl.classList.contains('tucked') && nearEdge(e)) {
      untuck();
      scheduleTuck(1500);  // unless the pointer goes on to it
    }
  });
  // A touch screen has no hover: a tap at the edge shows it, a tap
  // anywhere else tucks it.
  document.addEventListener('pointerdown', function (e) {
    if (e.pointerType === 'mouse' || !autoHide) return;
    if (nearEdge(e)) untuck();
    else if (!toolbarEl.contains(e.target)) { overToolbar = false; scheduleTuck(600); }
  }, true);

  // --- Keys to the device: USB HID usage codes, pressed then released in
  // reverse, as a shortcut button does.
  function sendKeys(codes) {
    codes.forEach(function (c) { hidSend({cmd: 'key_press', keycode: c}); });
    codes.slice().reverse().forEach(function (c) { hidSend({cmd: 'key_release', keycode: c}); });
  }
  var KEY_NAMES = {GUI: 227, Meta: 227, Cmd: 227, Command: 227, Win: 227, Super: 227, Alt: 226, Option: 226, Ctrl: 224, Control: 224, Shift: 225,
    Enter: 40, Return: 40, Escape: 41, Esc: 41, Backspace: 42, Tab: 43, Space: 44, Delete: 76, Del: 76, Home: 74, End: 77, PageUp: 75, PageDown: 78,
    ArrowRight: 79, Right: 79, ArrowLeft: 80, Left: 80, ArrowDown: 81, Down: 81, ArrowUp: 82, Up: 82};
  // keycodes turns a shortcut such as "GUI+h" or "128" into usage codes.
  function keycodes(chord) {
    var codes = [];
    var parts = String(chord).split('+');
    for (var i = 0; i < parts.length; i++) {
      var t = parts[i].trim(), c;
      if (!t) return null;
      if (KEY_NAMES[t] !== undefined) c = KEY_NAMES[t];
      else if (/^\d+$/.test(t)) c = parseInt(t, 10);
      else if (t.length === 1 && /[a-z]/i.test(t)) c = 4 + (t.toLowerCase().charCodeAt(0) - 97);
      else if (t.length === 1 && /[1-9]/.test(t)) c = 30 + (t.charCodeAt(0) - 49);
      else if (t === '0') c = 39;
      else if (/^F([1-9]|1[0-2])$/.test(t)) c = 57 + parseInt(t.slice(1), 10);
      else return null;
      codes.push(c);
    }
    return codes;
  }

  // --- Screenshot: the picture as it is now, saved by the browser as
  // <site>-<device>-<local time>.jpg. Nothing is sent to the device.
  function siteName() {
    var h = location.hostname;
    if (!h || h.indexOf('.') < 0 || h.indexOf(':') >= 0 || /^[\d.]+$/.test(h)) return 'roadie';
    var parts = h.split('.'), last = parts.length - 1;
    if (parts[last] === 'local') return parts[last - 1];
    var i = last - 1;
    if (i > 0 && parts[i].length <= 3 && parts[last].length === 2) i--;  // vibium.co.uk
    return parts[i];
  }
  $('shotBtn').onclick = function () {
    fetch(api.snapshot + '?quality=' + shotQuality(), opts({cache: 'no-store'})).then(function (r) {
      if (!r.ok) throw new Error(r.status);
      return r.blob();
    }).then(function (blob) {
      var d = new Date(), two = function (n) { return ('0' + n).slice(-2); };
      var stamp = d.getFullYear() + '-' + two(d.getMonth() + 1) + '-' + two(d.getDate()) + '-' + two(d.getHours()) + two(d.getMinutes()) + two(d.getSeconds());
      var a = document.createElement('a');
      a.href = URL.createObjectURL(blob);
      var plat = window.__roadiePlatform;
      var kind = plat === 'ios' ? 'iphone' : (plat || (cfg.name ? cfg.name.replace(/[^a-z0-9]+/gi, '-').toLowerCase() : ''));
      a.download = siteName() + (kind ? '-' + kind : '') + '-' + stamp + '.jpg';
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(function () { URL.revokeObjectURL(a.href); }, 1000);
    }).catch(function () {});
  };

  // --- Toolbar: Show, Auto-hide or Off, and the side it sits on ---
  var tbBtns = {show: $('tbShowBtn'), auto: $('tbAutoBtn'), off: $('tbOffBtn')};
  function setToolbar(mode) {
    if (!tbBtns[mode]) mode = 'show';
    for (var k in tbBtns) tbBtns[k].classList.toggle('on', k === mode);
    if (mode === 'off') {
      autoHide = false;
      clearTimeout(tuckTimer);
      qslider.style.display = 'none';
      toolbarEl.classList.remove('floating', 'tucked');
      toolbarEl.style.display = 'none';
    } else {
      toolbarEl.style.display = 'flex';
      applyAutoHide(mode === 'auto');
    }
  }
  function toolbarNote(text) {
    var n = $('toolbarNote');
    if (!n) { n = document.createElement('div'); n.id = 'toolbarNote'; document.body.appendChild(n); }
    n.textContent = text;
    n.style.opacity = '1';
    clearTimeout(n.timer);
    n.timer = setTimeout(function () { n.style.opacity = '0'; }, 6000);
  }
  Object.keys(tbBtns).forEach(function (mode) {
    tbBtns[mode].onclick = function () {
      saveSettings({toolbar: mode});
      setToolbar(mode);
      if (mode === 'off') toolbarNote('Toolbar off. To bring it back, add ?toolbar=show to the address.');
    };
  });
  var sideBtns = {top: $('tbSideTop'), right: $('tbSideRight'), bottom: $('tbSideBottom'), left: $('tbSideLeft')};
  function setToolbarSide(side) {
    if (!sideBtns[side]) side = 'right';
    toolbarSide = side;
    for (var k in sideBtns) {
      sideBtns[k].classList.toggle('on', k === side);
      document.body.classList.toggle('tb-' + k, k === side);
    }
    if (qslider.style.display !== 'none') positionPanel();
    relayout();
  }
  Object.keys(sideBtns).forEach(function (side) {
    sideBtns[side].onclick = function () { saveSettings({toolbarSide: side}); setToolbarSide(side); };
  });
  (function () {
    setToolbarSide(saved.toolbarSide);
    var mode = saved.toolbar || (saved.toolbarAutoHide ? 'auto' : 'show');
    var asked = qs.get('toolbar');
    if (tbBtns[asked]) mode = asked;
    if (saved.toolbar !== mode || 'toolbarAutoHide' in saved) {
      delete saved.toolbarAutoHide;
      saveSettings({toolbar: mode});
    }
    setToolbar(mode);
  })();

  // --- Zoom and alignment ---
  // The picture is laid out at its own size, held within its room by
  // max-width and max-height; zoom is a scale on that. Fit scales it to
  // the room it has, up or down, and again whenever the room or the
  // picture changes: the default on a phone or tablet, where a picture
  // at its own size leaves the small screen half empty; on a desktop a
  // choice in the menu, 100% the default. Touching the slider leaves fit.
  var zoomFitBtn = $('zoomFitBtn');
  var coarse = window.matchMedia && window.matchMedia('(pointer: coarse)').matches;
  var fitMode = false;
  var zoomPct = 100;  // the slider's, when not fitting
  function applyAlignment(mode) {
    alignment = mode;
    var viewer = $('viewer');
    var kbdI = $('kbd-inner');
    if (mode === 'center') {
      alignTLBtn.classList.remove('on'); alignCenterBtn.classList.add('on');
      viewer.style.display = 'flex';
      viewer.style.justifyContent = 'center';
      viewer.style.alignItems = 'center';
      kbdI.style.textAlign = 'center';
    } else {
      alignCenterBtn.classList.remove('on'); alignTLBtn.classList.add('on');
      viewer.style.display = '';
      viewer.style.justifyContent = '';
      viewer.style.alignItems = '';
      kbdI.style.textAlign = '';
    }
    layoutZoom();
  }
  // The scale that fills the room: the picture's laid-out size (its own,
  // or already held to the room) against the room.
  function fitScale() {
    var viewer = $('viewer');
    var W = viewer.clientWidth, H = viewer.clientHeight;
    var cs = getComputedStyle(img);
    var w = parseFloat(cs.width), h = parseFloat(cs.height);
    if (!W || !H || !w || !h || img.style.display === 'none') return 1;
    return Math.min(W / w, H / h);
  }
  function layoutZoom() {
    var scale = fitMode ? fitScale() : zoomPct / 100;
    img.style.transform = scale === 1 ? '' : 'scale(' + scale + ')';
    img.style.transformOrigin = alignment === 'center' ? 'center center' : 'top left';
    $('viewer').style.overflow = 'hidden';
    var pct = Math.round(scale * 100);
    zoomVal.textContent = pct + '%';
    zoomRange.value = pct;
    zoomFitBtn.classList.toggle('on', fitMode);
  }
  function applyZoom(pct) { fitMode = false; zoomPct = pct; layoutZoom(); }
  function applyFit() { fitMode = true; layoutZoom(); }
  function snapZoom(val) {
    var nearest = Math.round(val / 25) * 25;
    return Math.abs(val - nearest) <= 3 ? nearest : val;
  }
  zoomRange.oninput = function () { var pct = snapZoom(parseInt(zoomRange.value, 10)); applyZoom(pct); saveSettings({zoom: pct}); relayout(); };
  zoomFitBtn.onclick = function () {
    if (fitMode) { applyZoom(100); saveSettings({zoom: 100}); }
    else { applyFit(); saveSettings({zoom: 'fit'}); }
  };
  alignTLBtn.onclick = function () { applyAlignment('top-left'); saveSettings({alignment: 'top-left'}); };
  alignCenterBtn.onclick = function () { applyAlignment('center'); saveSettings({alignment: 'center'}); };
  (function () {
    var align = qs.get('align');
    applyAlignment(align === 'center' || align === 'top-left' ? align : (saved.alignment || alignment));
    var zq = qs.get('zoom');
    var zoom = parseInt(zq, 10);
    if (zq === 'fit') applyFit();
    else if (zoom >= 50 && zoom <= 400) applyZoom(zoom);
    else if (saved.zoom === 'fit') applyFit();
    else if (typeof saved.zoom === 'number') applyZoom(saved.zoom);
    else if (coarse) applyFit();
  })();

  // --- Screenshot quality: the Screenshot button's own JPEG quality.
  var shotQualityRange = $('shotQualityRange');
  var shotQualityVal = $('shotQualityVal');
  function shotQuality() { return parseInt(shotQualityRange.value, 10) || 90; }
  if (saved.shotQuality) shotQualityRange.value = saved.shotQuality;
  shotQualityVal.textContent = shotQuality();
  shotQualityRange.oninput = function () { shotQualityVal.textContent = shotQuality(); saveSettings({shotQuality: shotQuality()}); };

  // --- Video transport: MJPEG or WebRTC, when the host offers it. The
  // module (webrtc.js) does the switching; the choice is this browser's,
  // like zoom. The Bitrate row is WebRTC's; the extras may add the MJPEG
  // stream's own rows and show them only then.
  var transportRows = [];  // elements shown for MJPEG only
  function setupTransport() {
    if (!window.__roadieWebRTC || !window.__roadieRTC) return;
    var row = $('videoRow');
    var mjpegBtn = $('videoMJPEGBtn');
    var webrtcBtn = $('videoWebRTCBtn');
    var statsCheck = $('videoStatsCheck');
    var bitrateRow = $('bitrateRow');
    var bitrateRange = $('bitrateRange');
    var bitrateVal = $('bitrateVal');
    row.style.display = 'flex';
    function show(transport) {
      var rtc = transport === 'webrtc';
      webrtcBtn.classList.toggle('on', rtc);
      mjpegBtn.classList.toggle('on', !rtc);
      transportRows.forEach(function (el) { el.style.display = rtc ? 'none' : 'flex'; });
      bitrateRow.style.display = rtc ? 'flex' : 'none';
    }
    function applyBitrate(kbps) {
      var auto = kbps >= parseInt(bitrateRange.max, 10);
      bitrateRange.value = kbps;
      bitrateVal.textContent = auto ? 'auto' : (kbps >= 1000 ? (kbps / 1000).toFixed(1).replace(/\.0$/, '') + ' Mbps' : kbps + ' kbps');
      window.__roadieRTC.capBitrate(auto ? 0 : kbps * 1000);
    }
    bitrateRange.oninput = function () { var kbps = parseInt(bitrateRange.value, 10); applyBitrate(kbps); saveSettings({bitrate: kbps}); };
    applyBitrate(saved.bitrate !== undefined ? saved.bitrate : parseInt(bitrateRange.max, 10));
    mjpegBtn.onclick = function () { window.__roadieRTC.stop(); show('mjpeg'); saveSettings({transport: 'mjpeg'}); };
    webrtcBtn.onclick = function () { if (station && !holding()) return; window.__roadieRTC.start(); show('webrtc'); saveSettings({transport: 'webrtc'}); };
    statsCheck.checked = window.__roadieRTC.statsShown();
    statsCheck.onchange = function () { window.__roadieRTC.showStats(statsCheck.checked); saveSettings({stats: statsCheck.checked}); };
    window.addEventListener('roadie-transport', function (e) { show(e.detail.transport); });
    show(window.__roadieRTC.active() ? 'webrtc' : 'mjpeg');
    setupTransport.show = show;
  }

  // --- Theme ---
  var themeBtns = {dark: $('themeDarkBtn'), light: $('themeLightBtn'), system: $('themeSystemBtn')};
  function applyTheme(t) {
    document.documentElement.setAttribute('data-theme', t);
    for (var k in themeBtns) themeBtns[k].classList.toggle('on', k === t);
    saveSettings({theme: t});
  }
  themeBtns.dark.onclick = function () { applyTheme('dark'); };
  themeBtns.light.onclick = function () { applyTheme('light'); };
  themeBtns.system.onclick = function () { applyTheme('system'); };
  applyTheme(saved.theme || 'system');

  // --- Input to the device, over the WebRTC "hid" channel when it is up,
  // otherwise the input socket. Held back while the host is busy.
  var hidWs = null, hidReady = false, hidWanted = !station;
  function openHID() {
    if (!driving()) return;  // a watcher has no input to send
    hidWanted = true;
    if (hidWs) return;
    var sock = new WebSocket(wsURL(api.hid));
    hidWs = sock;
    sock.onopen = function () { hidReady = true; };
    sock.onclose = function () {
      if (hidWs !== sock) return;
      hidWs = null; hidReady = false;
      if (hidWanted) setTimeout(function () { if (hidWanted && !hidWs) openHID(); }, 2000);
    };
    sock.onerror = function () { hidReady = false; };
  }
  function closeHID() {
    hidWanted = !station;
    if (hidWs) { var s = hidWs; hidWs = null; hidReady = false; s.close(); }
  }
  function hidSend(msg) {
    if (busy) return;
    if (!driving()) return;
    if (window.__roadieRTC && window.__roadieRTC.sendHID(msg)) return;
    if (hidWs && hidReady) hidWs.send(JSON.stringify(msg));
  }
  // While the host is busy with the screen (turning it, for one), input
  // waits; whatever is held is let go first.
  var busy = false, busyMessage = 'Busy\u2026', hostBusy = false, heldBusy = false;
  function setBusy(on, message) {
    var note = $('turnNote');
    if (on && message) busyMessage = message;
    if (on) {
      var r = img.getBoundingClientRect(), v = img.parentNode.getBoundingClientRect();
      note.style.left = (r.left - v.left) + 'px'; note.style.top = (r.top - v.top) + 'px';
      note.style.width = r.width + 'px'; note.style.height = r.height + 'px';
      note.querySelector('span').textContent = busyMessage;
    }
    if (on === busy) return;
    if (on) releaseEverythingHeld();
    busy = on;
    img.classList.toggle('turning', on);
    note.style.display = on ? 'flex' : 'none';
    emit('busy', on);
  }
  if (!station) openHID();

  // --- Input mode: the mode the host resolved for this load, then the
  // one this viewer picked, then mouse (or touch on a touchscreen).
  var inputMode = (cfg.inputMode === 'touch' || cfg.inputMode === 'mouse') ? cfg.inputMode
    : (localStorage.getItem('roadie-input-mode') === 'touch' ? 'touch' : localStorage.getItem('roadie-input-mode') === 'mouse' ? 'mouse' : (coarse ? 'touch' : 'mouse'));
  var modeMouseBtn = $('modeMouseBtn');
  var modeTouchBtn = $('modeTouchBtn');
  function applyInputMode(mode) {
    if (mode !== inputMode) releaseEverythingHeld();
    inputMode = mode;
    modeMouseBtn.classList.toggle('on', mode === 'mouse');
    modeTouchBtn.classList.toggle('on', mode === 'touch');
  }
  function setInputMode(mode) { applyInputMode(mode); try { localStorage.setItem('roadie-input-mode', mode); } catch (e) {} }
  applyInputMode(inputMode);
  modeMouseBtn.onclick = function () { setInputMode('mouse'); };
  modeTouchBtn.onclick = function () { setInputMode('touch'); };

  // --- Positions, as fractions of the picture scaled to 0..32767; the
  // host folds its crop in.
  function posFromFeed(e) {
    var rect = img.getBoundingClientRect();
    var px = Math.max(0, Math.min(1, (e.clientX - rect.left) / (rect.width || 1)));
    var py = Math.max(0, Math.min(1, (e.clientY - rect.top) / (rect.height || 1)));
    return {x: Math.round(px * 32767), y: Math.round(py * 32767)};
  }
  function move(x, y) { hidSend({cmd: 'mouse_move', x: x, y: y, space: 'picture'}); }
  function touch(list) { hidSend({cmd: 'touch', contacts: list, space: 'picture'}); }

  // --- Rate-limited sending: only the latest position goes out, on a
  // timer. 33 ms keeps the pointer up with the video, touch a little
  // slower, and the wheel as notches add up.
  var pendingX = -1, pendingY = -1, mouseDirty = false;
  setInterval(function () { if (mouseDirty) { move(pendingX, pendingY); mouseDirty = false; } }, 33);
  var pendingContacts = [], touchDirty = false;
  setInterval(function () { if (touchDirty) { touch(pendingContacts); touchDirty = false; } }, 50);
  // Notches of the wheel go out as they add up. Some targets scroll only
  // for a run of notches close together (an iPhone: the extras say so
  // with wheelRuns), so there a notch goes out at least every runGap
  // while the wheel turns, and Scroll sets how fast that is.
  var pendingScroll = 0, scrollDirty = false, scrollSpeed = 1;
  var wheelDir = 0, wheelAt = 0, scrollAt = 0;
  var wheelRuns = false;
  function runGap() { return scrollSpeed <= 1 ? 80 - (scrollSpeed - 0.25) * 40 : 50 - (scrollSpeed - 1) * 25; }
  var wheelRest = 0;
  setInterval(function () {
    var now = performance.now();
    if (wheelRuns && wheelAt > scrollAt && !scrollDirty && now - scrollAt >= runGap()) {
      pendingScroll = wheelDir;
      wheelRest = 0;
      scrollDirty = true;
    }
    if (scrollDirty) {
      hidSend({cmd: 'mouse_scroll', amount: pendingScroll});
      pendingScroll = 0;
      scrollDirty = false;
      scrollAt = now;
    }
  }, 10);

  // --- A press is held back for HOLD_MS: one that ends sooner goes out as
  // a single tap or click, which the host performs whole, so a slow or
  // lossy link cannot stretch a tap into a long press. A press that lasts,
  // or moves more than SLOP, is sent as it is and streams from then on.
  var HOLD_MS = 150, SLOP = 500;
  var pending = null;
  var feedPressed = false, feedButton = 0;
  function beginPress(p, m, b) {
    if (pending) clearTimeout(pending.timer);
    pending = {x: p.x, y: p.y, mode: m, button: b, timer: setTimeout(commitPress, HOLD_MS)};
  }
  function commitPress() {
    if (!pending) return;
    var q = pending;
    pending = null;
    clearTimeout(q.timer);
    if (q.mode === 'mouse') hidSend({cmd: 'mouse_press', buttons: q.button});
    else { pendingContacts = [{id: 0, tip: true, x: q.x, y: q.y}]; touchDirty = false; touch(pendingContacts); }
  }
  function movedFar(p) { return pending && (Math.abs(p.x - pending.x) > SLOP || Math.abs(p.y - pending.y) > SLOP); }
  function endPress(p) {
    feedPressed = false;
    if (pending) {
      var q = pending;
      pending = null;
      clearTimeout(q.timer);
      if (q.mode === 'mouse') { move(q.x, q.y); mouseDirty = false; hidSend({cmd: 'mouse_click', buttons: q.button}); }
      else hidSend({cmd: 'tap', x: q.x, y: q.y, space: 'picture'});
      feedButton = 0;
      return;
    }
    if (inputMode === 'mouse') {
      if (mouseDirty) { move(pendingX, pendingY); mouseDirty = false; }
      hidSend({cmd: 'mouse_release', buttons: feedButton || 1});
      feedButton = 0;
    } else {
      touchDirty = false;
      touch(p ? [{id: 0, tip: false, x: p.x, y: p.y}] : []);
      setTimeout(function () { touch([]); }, 20);
    }
  }

  // --- Mouse on the picture ---
  img.addEventListener('mousemove', function (e) {
    var p = posFromFeed(e);
    if (pending && movedFar(p)) commitPress();
    if (inputMode === 'mouse') { pendingX = p.x; pendingY = p.y; mouseDirty = true; }
    else if (feedPressed && !pending) { pendingContacts = [{id: 0, tip: true, x: p.x, y: p.y}]; touchDirty = true; }
  });
  // The feed prevents default on mousedown to stop drag-selection, which
  // also suppresses the focus a click would normally give this document;
  // embedded in a frame that would mean no keyboard input at all. Claim
  // it on entry as well as on click.
  function claimKeyboardFocus() { try { window.focus(); } catch (err) {} }
  img.addEventListener('mouseenter', claimKeyboardFocus);
  img.addEventListener('mousedown', claimKeyboardFocus);
  img.addEventListener('mousedown', function (e) {
    e.preventDefault();
    feedPressed = true;
    var p = posFromFeed(e);
    if (inputMode === 'mouse') {
      feedButton = e.button === 2 ? 2 : e.button === 1 ? 4 : 1;
      pendingX = p.x; pendingY = p.y; mouseDirty = false;
      move(p.x, p.y);
      beginPress(p, 'mouse', feedButton);
    } else {
      beginPress(p, 'touch', 0);
    }
  });
  document.addEventListener('mouseup', function (e) {
    if (!feedPressed) return;
    endPress(posFromFeed(e));
  });
  img.addEventListener('contextmenu', function (e) { e.preventDefault(); });
  img.addEventListener('dragstart', function (e) { e.preventDefault(); });

  // --- The wheel: in notches, a line is one, 25 pixels are one, a page
  // twenty; what is left over adds up, so small steps scroll too.
  img.addEventListener('wheel', function (e) {
    e.preventDefault();
    if (inputMode !== 'mouse' || !e.deltaY) return;
    var steps = e.deltaMode === 1 ? e.deltaY : e.deltaMode === 2 ? e.deltaY * 20 : e.deltaY / 25;
    wheelRest += steps * scrollSpeed;
    wheelDir = e.deltaY > 0 ? 1 : -1;
    wheelAt = performance.now();
    var notches = Math.trunc(wheelRest);
    if (notches !== 0) {
      wheelRest -= notches;
      pendingScroll = Math.max(-127, Math.min(127, pendingScroll + notches));
      scrollDirty = true;
    }
  }, {passive: false});
  var scrollRange = $('scrollRange');
  var scrollVal = $('scrollVal');
  function applyScrollSpeed(pct) { scrollSpeed = pct / 100; scrollRange.value = pct; scrollVal.textContent = pct + '%'; }
  scrollRange.oninput = function () { var pct = parseInt(scrollRange.value, 10); applyScrollSpeed(pct); saveSettings({scrollSpeed: pct}); };
  if (saved.scrollSpeed >= 25 && saved.scrollSpeed <= 200) applyScrollSpeed(saved.scrollSpeed);

  // --- Touch on the picture ---
  function touchToContacts(e) {
    var list = [];
    for (var i = 0; i < Math.min(e.touches.length, 2); i++) {
      var p = posFromFeed(e.touches[i]);
      list.push({id: i, tip: true, x: p.x, y: p.y});
    }
    return list;
  }
  var twoFingerLastY = null, scrollAccum = 0;
  img.addEventListener('touchstart', function (e) {
    e.preventDefault();
    if (inputMode === 'touch') {
      if (e.touches.length === 1) { feedPressed = true; beginPress(posFromFeed(e.touches[0]), 'touch', 0); }
      else { commitPress(); pendingContacts = touchToContacts(e); touchDirty = false; touch(pendingContacts); }
    } else if (e.touches.length === 1) {
      // A finger in mouse mode: the pointer goes there and clicks.
      var p = posFromFeed(e.touches[0]);
      feedPressed = true;
      feedButton = 1;
      pendingX = p.x; pendingY = p.y; mouseDirty = false;
      move(p.x, p.y);
      beginPress(p, 'mouse', 1);
    } else {
      commitPress();
    }
  }, {passive: false});
  img.addEventListener('touchmove', function (e) {
    e.preventDefault();
    if (inputMode === 'touch') {
      if (e.touches.length >= 2) commitPress();
      else if (pending && movedFar(posFromFeed(e.touches[0]))) commitPress();
      if (pending) return;
      pendingContacts = touchToContacts(e); touchDirty = true;
      return;
    }
    if (e.touches.length >= 2) {
      commitPress();
      var avgY = (e.touches[0].clientY + e.touches[1].clientY) / 2;
      if (twoFingerLastY !== null) {
        scrollAccum += (avgY - twoFingerLastY);
        while (Math.abs(scrollAccum) >= 10) {
          pendingScroll = Math.max(-127, Math.min(127, pendingScroll + (scrollAccum > 0 ? 3 : -3)));
          scrollDirty = true;
          scrollAccum -= (scrollAccum > 0 ? 10 : -10);
        }
      }
      twoFingerLastY = avgY;
    } else {
      twoFingerLastY = null; scrollAccum = 0;
      var p = posFromFeed(e.touches[0]);
      if (pending && movedFar(p)) commitPress();
      pendingX = p.x; pendingY = p.y; mouseDirty = true;
    }
  }, {passive: false});
  img.addEventListener('touchend', function (e) {
    e.preventDefault();
    if (inputMode === 'touch') {
      if (e.touches.length) { commitPress(); pendingContacts = touchToContacts(e); touchDirty = false; touch(pendingContacts); }
      else if (feedPressed) endPress(e.changedTouches && e.changedTouches[0] ? posFromFeed(e.changedTouches[0]) : null);
    } else if (!e.touches.length && feedPressed) {
      endPress(null);
    }
    if (e.touches.length < 2) { twoFingerLastY = null; scrollAccum = 0; }
  }, {passive: false});

  // --- The keyboard: keys go to the device while capture is on and no
  // form control has the focus. What is held is remembered, so a keyup
  // that never arrives can still be released.
  var KEY_MAP = {
    KeyA: 4, KeyB: 5, KeyC: 6, KeyD: 7, KeyE: 8, KeyF: 9, KeyG: 10, KeyH: 11, KeyI: 12,
    KeyJ: 13, KeyK: 14, KeyL: 15, KeyM: 16, KeyN: 17, KeyO: 18, KeyP: 19, KeyQ: 20,
    KeyR: 21, KeyS: 22, KeyT: 23, KeyU: 24, KeyV: 25, KeyW: 26, KeyX: 27, KeyY: 28, KeyZ: 29,
    Digit1: 30, Digit2: 31, Digit3: 32, Digit4: 33, Digit5: 34, Digit6: 35,
    Digit7: 36, Digit8: 37, Digit9: 38, Digit0: 39,
    Enter: 40, Escape: 41, Backspace: 42, Tab: 43, Space: 44,
    Minus: 45, Equal: 46, BracketLeft: 47, BracketRight: 48, Backslash: 49,
    Semicolon: 51, Quote: 52, Backquote: 53, Comma: 54, Period: 55, Slash: 56,
    CapsLock: 57,
    F1: 58, F2: 59, F3: 60, F4: 61, F5: 62, F6: 63, F7: 64, F8: 65, F9: 66, F10: 67, F11: 68, F12: 69,
    PrintScreen: 70, ScrollLock: 71, Pause: 72, Insert: 73, Home: 74, PageUp: 75,
    Delete: 76, End: 77, PageDown: 78,
    ArrowRight: 79, ArrowLeft: 80, ArrowDown: 81, ArrowUp: 82,
    NumLock: 83,
    ControlLeft: 224, ShiftLeft: 225, AltLeft: 226, MetaLeft: 227,
    ControlRight: 228, ShiftRight: 229, AltRight: 230, MetaRight: 231
  };
  var kbdCapture = true;
  var kbdCaptureCheck = $('kbdCaptureCheck');
  kbdCaptureCheck.onchange = function () { kbdCapture = kbdCaptureCheck.checked; };
  var heldKeys = {};
  function releaseEverythingHeld() {
    if (pending) { clearTimeout(pending.timer); pending = null; }
    hidSend({cmd: 'mouse_release', buttons: 7});
    touch([]);
    feedPressed = false;
    pendingContacts = [];
    touchDirty = false;
    for (var k in heldKeys) hidSend({cmd: 'key_release', keycode: parseInt(k, 10)});
    heldKeys = {};
    for (var m in activeModifiers) hidSend({cmd: 'key_release', keycode: parseInt(m, 10)});
    activeModifiers = {};
    updateModButtons();
  }
  window.addEventListener('blur', releaseEverythingHeld);
  window.addEventListener('pagehide', releaseEverythingHeld);
  document.addEventListener('visibilitychange', function () { if (document.hidden) releaseEverythingHeld(); });
  function inForm(e) {
    var tag = e.target && e.target.tagName;
    return tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || (e.target && e.target.isContentEditable);
  }
  document.addEventListener('keydown', function (e) {
    if (!kbdCapture || inForm(e)) return;
    var hid = KEY_MAP[e.code];
    if (hid !== undefined) {
      e.preventDefault();
      heldKeys[hid] = true;
      hidSend({cmd: 'key_press', keycode: hid});
    }
  });
  document.addEventListener('keyup', function (e) {
    var hid = KEY_MAP[e.code];
    if (hid === undefined || !heldKeys[hid]) return;
    e.preventDefault();
    delete heldKeys[hid];
    hidSend({cmd: 'key_release', keycode: hid});
  });

  // --- The on-screen keyboard ---
  var oskPanel = $('onscreen-kbd');
  var kbdInner = $('kbd-inner');
  var kbdBtn = $('kbdBtn');
  var oskVisible = false;
  var kbdZoomPct = 100;
  function showKbd(show) {
    oskVisible = show;
    if (oskVisible) { oskPanel.style.display = 'block'; applyKbdZoom(kbdZoomPct); }
    else oskPanel.style.display = 'none';
    kbdBtn.style.opacity = oskVisible ? '1' : '0.4';
    relayout();
  }
  kbdBtn.onclick = function () { showKbd(!oskVisible); saveSettings({kbdVisible: oskVisible}); };
  var kbdZoomRange = $('kbdZoomRange');
  var kbdZoomVal = $('kbdZoomVal');
  function applyKbdZoom(pct) {
    kbdZoomPct = pct;
    kbdInner.style.zoom = (pct / 100);
    kbdZoomRange.value = pct;
    kbdZoomVal.textContent = pct + '%';
  }
  kbdZoomRange.oninput = function () { var pct = parseInt(kbdZoomRange.value, 10); applyKbdZoom(pct); saveSettings({kbdZoom: pct}); };
  if (saved.kbdZoom !== undefined) kbdZoomPct = saved.kbdZoom;
  var MOD_CODES = {224: 1, 225: 1, 226: 1, 227: 1, 228: 1, 229: 1, 230: 1, 231: 1, 57: 1};
  var activeModifiers = {};
  function oskPress(code) {
    if (MOD_CODES[code]) {
      if (activeModifiers[code]) { hidSend({cmd: 'key_release', keycode: code}); delete activeModifiers[code]; }
      else { hidSend({cmd: 'key_press', keycode: code}); activeModifiers[code] = true; }
      updateModButtons();
      return;
    }
    hidSend({cmd: 'key_press', keycode: code});
    hidSend({cmd: 'key_release', keycode: code});
    for (var m in activeModifiers) hidSend({cmd: 'key_release', keycode: parseInt(m, 10)});
    activeModifiers = {};
    updateModButtons();
  }
  function updateModButtons() {
    var btns = oskPanel.querySelectorAll('.kk[data-mod]');
    for (var i = 0; i < btns.length; i++) {
      var c = parseInt(btns[i].getAttribute('data-code'), 10);
      btns[i].classList.toggle('mod-active', !!activeModifiers[c]);
    }
  }
  function mkKey(label, code, w, sigil) {
    var btn = document.createElement('button');
    btn.className = 'kk';
    btn.setAttribute('data-code', code);
    if (w) btn.style.width = w + 'px';
    if (MOD_CODES[code]) btn.setAttribute('data-mod', '1');
    if (code === 0 && !label) btn.classList.add('blank');
    if (sigil === 'hide') {
      btn.style.width = '0'; btn.style.minWidth = '0'; btn.style.padding = '0'; btn.style.border = 'none';
      btn.style.visibility = 'hidden'; btn.style.marginLeft = '-3px';
    } else if (sigil === 'small') {
      btn.style.fontSize = '8px';
      btn.textContent = label;
    } else if (sigil) {
      var isShiftChar = sigil.length === 1 && sigil.charCodeAt(0) < 128;
      if (isShiftChar) btn.innerHTML = '<span style="font-size:11px;line-height:1;opacity:0.6">' + sigil + '</span><span style="font-size:11px;line-height:1">' + label + '</span>';
      else btn.innerHTML = '<span style="font-size:14px;line-height:1">' + sigil + '</span><span style="font-size:8px;line-height:1">' + label + '</span>';
      btn.style.flexDirection = 'column';
      btn.style.gap = '1px';
    } else if (label.indexOf('<svg') === 0) {
      btn.innerHTML = label;
    } else {
      btn.textContent = label;
    }
    btn.addEventListener('pointerdown', function (e) { e.preventDefault(); if (code) oskPress(code); });
    return btn;
  }
  function mkGap() { var d = document.createElement('div'); d.className = 'kgap numpad'; return d; }
  var EJECT = '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" fill="currentColor" viewBox="0 0 16 16"><path fill-rule="evenodd" d="M.5 6a.5.5 0 0 0-.488.608l1.652 7.434A2.5 2.5 0 0 0 4.104 16h5.792a2.5 2.5 0 0 0 2.44-1.958l.131-.59a3 3 0 0 0 1.3-5.854l.221-.99A.5.5 0 0 0 13.5 6zM13 12.5a2 2 0 0 1-.316-.025l.867-3.898A2.001 2.001 0 0 1 13 12.5M2.64 13.825 1.123 7h11.754l-1.517 6.825A1.5 1.5 0 0 1 9.896 15H4.104a1.5 1.5 0 0 1-1.464-1.175"/><path d="m4.4.8-.003.004-.014.019a4 4 0 0 0-.204.31 2 2 0 0 0-.141.267c-.026.06-.034.092-.037.103v.004a.6.6 0 0 0 .091.248c.075.133.178.272.308.445l.01.012c.118.158.26.347.37.543.112.2.22.455.22.745 0 .188-.065.368-.119.494a3 3 0 0 1-.202.388 5 5 0 0 1-.253.382l-.018.025-.005.008-.002.002A.5.5 0 0 1 3.6 4.2l.003-.004.014-.019a4 4 0 0 0 .204-.31 2 2 0 0 0 .141-.267c.026-.06.034-.092.037-.103a.6.6 0 0 0-.09-.252A4 4 0 0 0 3.6 2.8l-.01-.012a5 5 0 0 1-.37-.543A1.53 1.53 0 0 1 3 1.5c0-.188.065-.368.119-.494.059-.138.134-.274.202-.388a6 6 0 0 1 .253-.382l.025-.035A.5.5 0 0 1 4.4.8m3 0-.003.004-.014.019a4 4 0 0 0-.204.31 2 2 0 0 0-.141.267c-.026.06-.034.092-.037.103v.004a.6.6 0 0 0 .091.248c.075.133.178.272.308.445l.01.012c.118.158.26.347.37.543.112.2.22.455.22.745 0 .188-.065.368-.119.494a3 3 0 0 1-.202.388 5 5 0 0 1-.253.382l-.018.025-.005.008-.002.002A.5.5 0 0 1 6.6 4.2l.003-.004.014-.019a4 4 0 0 0 .204-.31 2 2 0 0 0 .141-.267c.026-.06.034-.092.037-.103a.6.6 0 0 0-.09-.252A4 4 0 0 0 6.6 2.8l-.01-.012a5 5 0 0 1-.37-.543A1.53 1.53 0 0 1 6 1.5c0-.188.065-.368.119-.494.059-.138.134-.274.202-.388a6 6 0 0 1 .253-.382l.025-.035A.5.5 0 0 1 7.4.8m3 0-.003.004-.014.019a4 4 0 0 0-.204.31 2 2 0 0 0-.141.267c-.026.06-.034.092-.037.103v.004a.6.6 0 0 0 .091.248c.075.133.178.272.308.445l.01.012c.118.158.26.347.37.543.112.2.22.455.22.745 0 .188-.065.368-.119.494a3 3 0 0 1-.202.388 5 5 0 0 1-.252.382l-.019.025-.005.008-.002.002A.5.5 0 0 1 9.6 4.2l.003-.004.014-.019a4 4 0 0 0 .204-.31 2 2 0 0 0 .141-.267c.026-.06.034-.092.037-.103a.6.6 0 0 0-.09-.252A4 4 0 0 0 9.6 2.8l-.01-.012a5 5 0 0 1-.37-.543A1.53 1.53 0 0 1 9 1.5c0-.188.065-.368.119-.494.059-.138.134-.274.202-.388a6 6 0 0 1 .253-.382l.025-.035A.5.5 0 0 1 10.4.8"/></svg>';
  // 15 columns by 6 rows, a gap, and 4 numpad columns on wide screens:
  // [label, keycode, width, sigil].
  var rows = [
    {m: [['esc', 41], ['F1', 58, 0, 'small'], ['F2', 59, 0, 'small'], ['F3', 60, 0, 'small'], ['F4', 61, 0, 'small'], ['F5', 62, 0, 'small'], ['F6', 63, 0, 'small'], ['F7', 64, 0, 'small'], ['F8', 65, 0, 'small'], ['F9', 66, 0, 'small'], ['F10', 67, 0, 'small'], ['F11', 68, 0, 'small'], ['F12', 69, 0, 'small'], [EJECT, 249], ['⌦', 76]], p: [['', 0], ['', 0], ['', 0], ['', 0]]},
    {m: [[String.fromCharCode(96), 53, 0, '~'], ['1', 30, 0, '!'], ['2', 31, 0, '@'], ['3', 32, 0, '#'], ['4', 33, 0, '$'], ['5', 34, 0, '%'], ['6', 35, 0, '^'], ['7', 36, 0, '&'], ['8', 37, 0, '*'], ['9', 38, 0, '('], ['0', 39, 0, ')'], ['-', 45, 0, '_'], ['=', 46, 0, '+'], ['delete', 42, 83, 'small']], p: [['clear', 83, 0, 'small'], ['=', 103], ['/', 84], ['*', 85]]},
    {m: [['tab', 43, 0, '⇥'], ['Q', 20], ['W', 26], ['E', 8], ['R', 21], ['T', 23], ['Y', 28], ['U', 24], ['I', 12], ['O', 18], ['P', 19], ['[', 47, 0, '{'], [']', 48, 0, '}'], ['\\', 49, 0, '|'], ['home', 74, 0, 'small']], p: [['7', 95], ['8', 96], ['9', 97], ['-', 86]]},
    {m: [['caps lock', 57, 0, '⇪'], ['A', 4], ['S', 22], ['D', 7], ['F', 9], ['G', 10], ['H', 11], ['J', 13], ['K', 14], ['L', 15], [';', 51, 0, ':'], ["'", 52, 0, '"'], ['return', 40, 83, '↩'], ['end', 77, 0, 'small']], p: [['4', 92], ['5', 93], ['6', 94], ['+', 87]]},
    {m: [['shift', 225, 0, '⇧'], ['Z', 29], ['X', 27], ['C', 6], ['V', 25], ['B', 5], ['N', 17], ['M', 16], [',', 54, 0, '<'], ['.', 55, 0, '>'], ['/', 56, 0, '?'], ['shift', 229, 0, '⇧'], ['page up', 75, 0, 'small'], ['↑', 82], ['page down', 78, 0, 'small']], p: [['1', 89], ['2', 90], ['3', 91], ['enter', 88, 0, '↩']]},
    {m: [['control', 224, 0, '⌃'], ['option', 226, 0, '⌥'], ['command', 227, 0, '⌘'], ['', 44, 255], ['command', 231, 0, '⌘'], ['option', 230, 0, '⌥'], ['control', 228, 0, '⌃'], ['←', 80], ['↓', 81], ['→', 79]], p: [['0', 98, 83], ['', 0, 0, 'hide'], ['.', 99], ['', 0]]}
  ];
  for (var ri = 0; ri < rows.length; ri++) {
    var el = $('kr-' + ri);
    var m = rows[ri].m, p = rows[ri].p;
    for (var i = 0; i < m.length; i++) el.appendChild(mkKey(m[i][0], m[i][1], m[i][2], m[i][3]));
    if (p) {
      el.appendChild(mkGap());
      for (var j = 0; j < p.length; j++) {
        var k = mkKey(p[j][0], p[j][1], p[j][2], p[j][3]);
        k.classList.add('numpad');
        el.appendChild(k);
      }
    }
  }
  if (saved.kbdVisible) showKbd(true);

  // --- The hold, on a station, and the page's role. The page drives
  // when it took the device, and watches when someone else has it or
  // ?watch=1 asked for a look only. The request on load and on every
  // reconnect asks for control if the device is free and a seat watching
  // if it is not; only the Hold button takes a held device. A watcher of
  // a device nobody holds holds nothing itself, so it never blocks the
  // next agent: it looks again every few seconds and shows the next
  // holder's picture. The hold is renewed every three minutes and
  // released by the button or when the page goes away. A grant is good
  // for one signaling connection, so each gets a fresh one.
  var grant = null, grantUsed = false, hold = null, observer = false, released = false;
  var watchOnly = station && qs.get('watch') === '1';
  var role = watchOnly ? 'watch' : 'drive';
  var lookAgain = null;
  var holdStatus = $('holdStatus');
  var takeBtn = $('takeBtn'), releaseBtn = $('releaseBtn');
  function holding() { return !station || (!!hold && !released); }
  function driving() { return !station || (holding() && role === 'drive'); }
  function wanted() { return role === 'drive' ? ['view', 'input'] : ['view']; }
  function request(method, path, data) {
    return fetch(path, opts({method: method, headers: {'Content-Type': 'application/json'}, body: data ? JSON.stringify(data) : undefined})).then(function (r) {
      return r.json().then(function (j) {
        if (r.ok) return j;
        var err = new Error(j.error || r.status);
        err.free = !!j.free;
        throw err;
      });
    });
  }
  // The hold shows as the Release button while you drive, and as the
  // Hold button with a note while you watch.
  function showHold() {
    if (!station) return;
    var watching = hold && observer && role === 'watch';
    holdStatus.textContent = watching ? 'watching ' + hold.subject : '';
    holdStatus.title = watching ? 'Watching ' + hold.subject + '’s session' + (watchOnly ? '' : '; Hold takes the device') : '';
    takeBtn.style.display = role === 'watch' && !watchOnly ? 'flex' : 'none';
    releaseBtn.style.display = hold && !observer && !watchOnly ? 'flex' : 'none';
    releaseBtn.title = hold && !observer ? 'Release the device (you hold it)' : 'Release the device';
    emit('hold', {hold: hold, observer: observer, role: role});
  }
  // takeHold asks the station: for the role's scopes with watch set (a
  // held device is then watched), or, from the Hold button, for control
  // outright. Quiet leaves the picture as it is, for renewals and the
  // signaling hello. The promise fails when there is no grant.
  function takeHold(scopes, quiet, watch) {
    return request('POST', api.hold, {scopes: scopes, watch: !!watch}).then(function (res) {
      grant = res.grant;
      grantUsed = false;
      hold = res.hold;
      observer = !!res.observer;
      released = false;
      var granted = (res.hold && res.hold.scopes) || [];
      role = !observer && !watchOnly && granted.indexOf('input') >= 0 ? 'drive' : 'watch';
      clearTimeout(lookAgain);
      lookAgain = null;
      window.__roadieRTCConfig.iceServers = res.ice || [];
      showHold();
      loadExtras();
      if (!quiet) startPicture();
    }).catch(function (err) {
      if (err.free) nobody();
      else if (!quiet) showNotice('Could not take the device: ' + err.message);
      throw err;
    });
  }
  // The picture on a hold just taken: WebRTC afresh (a connection still
  // up was on a hold that is over), or the MJPEG feed.
  function startPicture() {
    poll();
    var rtc = window.__roadieRTC;
    if (qs.get('transport') === 'mjpeg' || (!qs.get('transport') && saved.transport === 'mjpeg') || !rtc) { window.__roadieFeed.start(); return; }
    if (rtc.active()) rtc.stop();
    rtc.start();
    setupTransport.show && setupTransport.show('webrtc');
  }
  function stopPicture() {
    if (window.__roadieRTC) window.__roadieRTC.stop();
    stopStream();
    closeHID();
    stopAudio();
    stopTalk();
  }
  // Nobody holds the device: no picture, and a look every few seconds
  // for the next holder.
  function nobody() {
    hold = null; grant = null; observer = false;
    role = 'watch';
    stopPicture();
    showHold();
    showOverlay(watchOnly ? 'Nobody holds the device.' : 'Nobody holds the device. Hold takes it.');
    clearTimeout(lookAgain);
    lookAgain = setTimeout(function () { lookAgain = null; takeHold(['view'], false, true).catch(function () {}); }, 5000);
  }
  // The device changed hands under the page: ask again for the role's
  // scopes, which lands as the new holder's watcher, or, when the new
  // hold is our own from another tab, on it.
  var changing = false;
  function changedHands() {
    if (changing) return;
    changing = true;
    takeHold(wanted(), false, true).catch(function () {}).then(function () { changing = false; });
  }
  function release() {
    released = true;
    releaseEverythingHeld();
    stopPicture();
    if (hold && !observer) request('DELETE', api.hold).catch(function () {});
    nobody();
  }
  if (station) {
    window.__roadieRTCConfig = {
      autostart: false,
      signalURL: api.signal,
      iceServers: [],
      iceTransportPolicy: qs.get('ice') === 'relay' ? 'relay' : undefined,
      hello: function () {
        if (grant && !grantUsed) { grantUsed = true; return {grant: grant}; }
        return takeHold(wanted(), true, true).then(function () { grantUsed = true; return {grant: grant}; });
      }
    };
    takeBtn.onclick = function () { takeHold(['view', 'input']).catch(function () {}); };
    releaseBtn.onclick = release;
    setInterval(function () { if (hold && !released) takeHold(wanted(), true, true).catch(function () {}); }, 3 * 60 * 1000);
    window.addEventListener('pagehide', function () {
      if (hold && !observer && !released && !watchOnly) {
        try { navigator.sendBeacon(api.hold + '/release', new Blob([JSON.stringify({csrf: cfg.csrf})], {type: 'application/json'})); } catch (e) {}
      }
    });
    window.addEventListener('roadie-transport', function (e) {
      if (e.detail && e.detail.transport === 'mjpeg' && e.detail.reason) toolbarNote('No WebRTC connection (' + e.detail.reason + '): showing the MJPEG stream through the station, a few frames a second.');
    });
  } else {
    window.__roadieRTCConfig = {signalURL: api.signal};
  }

  // --- The device's extras: its own buttons and panels, a script the
  // host serves (a station relays it from the rig). Loaded once the page
  // may reach the device, so on a station the hold comes first.
  var extrasLoaded = false;
  function loadExtras() {
    if (!cfg.extras || extrasLoaded) return;
    extrasLoaded = true;
    var s = document.createElement('script');
    s.src = cfg.extras;
    document.body.appendChild(s);
  }

  // --- The device's announced shortcuts, as plain buttons, when no extras
  // module provides better ones.
  function addButton(o) {
    var b = document.createElement('button');
    b.id = o.id || '';
    b.className = 'tbtn';
    b.style.cssText = 'width:36px; height:36px; border-radius:6px; cursor:pointer; padding:0; display:flex; align-items:center; justify-content:center;';
    if (o.title) b.title = o.title;
    if (o.svg) b.innerHTML = o.svg;
    else if (o.label) { b.textContent = o.label; b.style.fontSize = '10px'; b.style.fontFamily = 'monospace'; }
    if (o.onClick) b.onclick = o.onClick;
    var where = $('deviceButtons');
    if (o.before && $(o.before)) toolbarEl.insertBefore(b, $(o.before)); else where.appendChild(b);
    return b;
  }
  if (!cfg.extras && cfg.shortcuts) {
    cfg.shortcuts.forEach(function (s) {
      if (!s || !s.label || !s.keys) return;
      addButton({label: s.label, title: s.keys, onClick: function () { var codes = keycodes(s.keys); if (codes) sendKeys(codes); else toolbarNote('Unknown keys: ' + s.keys); }});
    });
  }

  // --- What the extras get.
  window.__roadieShell = {
    config: cfg,
    station: station,
    hostURL: hostURL,
    fetch: function (path, o) {
      if (!holding()) return Promise.reject(new Error('no hold on the device'));  // a watcher of a free device asks the rig nothing
      return fetch(hostURL(path), opts(o));
    },
    addButton: addButton,
    // A settings row (an element styled like the others), in the top
    // (video) section, the middle (input) or the bottom; mjpegOnly rows
    // show only while the picture is MJPEG.
    addRow: function (el, where, mjpegOnly) {
      $(where === 'top' ? 'deviceRowsTop' : where === 'bottom' ? 'deviceRowsBottom' : 'deviceRowsMiddle').appendChild(el);
      if (mjpegOnly) { transportRows.push(el); if (rtcLive()) el.style.display = 'none'; }
      return el;
    },
    send: hidSend,
    keys: function (codes) { sendKeys(typeof codes === 'string' ? (keycodes(codes) || []) : codes); },
    busy: function (on, message) { heldBusy = !!on; setBusy(hostBusy || heldBusy, message); },
    isBusy: function () { return busy; },
    health: function () { return lastHealth; },
    on: on,
    set: function (o) {
      if ('wheelRuns' in o) wheelRuns = !!o.wheelRuns;
      if ('quality' in o) extrasQuality = o.quality;  // a function returning the stream's JPEG quality
    },
    note: toolbarNote,
    settings: function () { return loadSettings(); },
    saveSettings: saveSettings,
    img: img,
    holding: holding,
    observer: function () { return observer; },
    role: function () { return role; },  // 'drive' or 'watch'; a watcher sends no input
    admin: function () { return !station || !!(hold && hold.scopes && hold.scopes.indexOf('admin') >= 0); },
    inputMode: function () { return inputMode; },
    setInputMode: setInputMode,
    relayout: relayout,
    menuOpen: function () { return qslider.style.display !== 'none'; }
  };

  // Go: the transport module has loaded by the time this runs at the end
  // of the body; a station page takes the hold first, a Roadie's starts.
  window.addEventListener('load', function () {
    setupTransport();
    if (station) takeHold(wanted(), false, true).catch(function () {});
    else { loadExtras(); poll(); }
  });
})();
