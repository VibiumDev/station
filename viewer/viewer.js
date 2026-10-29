// The station's viewer page: takes a lease on the device, starts the
// WebRTC transport with the grant in its hello and the station's ICE
// servers, keeps the lease fresh, falls back to the MJPEG stream through
// the station when WebRTC cannot connect, and wires the toolbar.
// Everything it knows about the device comes from the page's data
// attributes and the device's own announcement. ?ice=relay forces the
// media through TURN, for a check of the relay path; ?transport=mjpeg
// skips WebRTC and uses the fallback from the start.
(function () {
  var body = document.body;
  var device = body.dataset.device;
  var api = body.dataset.api || '';
  var csrf = body.dataset.csrf || '';
  var shortcuts = [];
  try { shortcuts = JSON.parse(body.dataset.shortcuts || '[]'); } catch (e) {}
  function $(id) { return document.getElementById(id); }
  var img = $('feed'), note = $('note'), status = $('status');

  var grant = null, grantUsed = false, lease = null, observer = false, released = false;
  var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  var params = new URLSearchParams(location.search);
  var relayOnly = params.get('ice') === 'relay';
  var mjpegOnly = params.get('transport') === 'mjpeg';
  window.__roadieRTCConfig = {
    autostart: false,
    signalURL: proto + '//' + location.host + api + '/v1/devices/' + device + '/signal',
    // The rig takes a grant once per connection, so a reconnect gets a
    // fresh one; the first connection uses the grant the page just took.
    hello: function () {
      if (grant && !grantUsed) { grantUsed = true; return {grant: grant}; }
      return takeLease(observer ? ['view'] : ['view', 'input'], true).then(function () {
        grantUsed = true;
        return grant ? {grant: grant} : {};
      });
    },
    iceServers: [],
    iceTransportPolicy: relayOnly ? 'relay' : undefined
  };

  // The MJPEG fallback: the picture as the capped stream relayed through
  // the station, input over a relayed WebSocket. webrtc.js starts it when
  // WebRTC gives up.
  var fallen = false, hidSock = null;
  var streamURL = api + '/v1/devices/' + device + '/stream';
  function openHID() {
    closeHID();
    if (observer || released) return;
    var sock = new WebSocket(proto + '//' + location.host + api + '/v1/devices/' + device + '/hid');
    sock.onclose = function () {
      if (hidSock !== sock) return;
      hidSock = null;
      if (fallen && !released) setTimeout(openHID, 2000);
    };
    hidSock = sock;
  }
  function closeHID() {
    if (!hidSock) return;
    var sock = hidSock;
    hidSock = null;
    try { sock.close(); } catch (e) {}
  }
  window.__roadieFeed = {
    start: function () {
      if (fallen || released) return;
      fallen = true;
      img.src = streamURL + '?ts=' + Date.now();
      openHID();
    },
    stop: function () {
      if (!fallen) return;
      fallen = false;
      closeHID();
    }
  };

  var input = window.__roadieInput.attach({
    surface: img,
    mode: localStorage.getItem('station-input-mode') === 'touch' ? 'touch' : 'mouse',
    send: function (msg) {
      if (observer || released) return;
      if (fallen) {
        if (hidSock && hidSock.readyState === 1) hidSock.send(JSON.stringify(msg));
        return;
      }
      if (window.__roadieRTC) window.__roadieRTC.sendHID(msg);
    }
  });

  function say(text, kind) {
    note.textContent = text || '';
    note.className = kind || '';
    note.style.display = text ? 'flex' : 'none';
  }
  function showStatus() {
    if (!lease) { status.textContent = ''; return; }
    status.textContent = observer ? 'Watching ' + lease.subject + "'s session" : 'You control this device (lease ' + lease.seq + ')';
    $('takeBtn').style.display = observer ? '' : 'none';
    $('releaseBtn').style.display = observer ? 'none' : '';
  }

  function request(method, path, data) {
    return fetch(api + path, {
      method: method,
      headers: {'Content-Type': 'application/json', 'X-CSRF-Token': csrf},
      body: data ? JSON.stringify(data) : undefined,
      credentials: 'same-origin'
    }).then(function (r) {
      return r.json().then(function (j) { if (!r.ok) throw new Error(j.error || r.status); return j; });
    });
  }

  // takeLease asks for the device; the answer is a grant on a lease, ours
  // or, as an observer, the holder's. With quiet, it only refreshes the
  // grant for a connection being made.
  function takeLease(scopes, quiet) {
    return request('POST', '/v1/devices/' + device + '/lease', {scopes: scopes}).then(function (res) {
      grant = res.grant;
      grantUsed = false;
      lease = res.lease;
      observer = !!res.observer;
      released = false;
      window.__roadieRTCConfig.iceServers = res.ice || [];
      showStatus();
      if (quiet || fallen) return;
      if (mjpegOnly) { window.__roadieFeed.start(); return; }
      var rtc = window.__roadieRTC;
      if (!rtc) { say('This browser cannot do WebRTC.', 'error'); return; }
      if (!rtc.active()) { say('Connecting…'); rtc.start(); }
      else rtc.sendControl({type: 'grant', grant: grant});
    }).catch(function (err) { say('Could not take the device: ' + err.message, 'error'); });
  }
  function release() {
    released = true;
    input.releaseAll();
    if (window.__roadieRTC) window.__roadieRTC.stop();
    closeHID();
    if (fallen) img.src = '';
    if (lease && !observer) request('DELETE', '/v1/devices/' + device + '/lease').catch(function () {});
    lease = null; grant = null;
    showStatus();
    say('Released. Reload the page to take the device again.');
  }

  // Grants last ten minutes; a fresh one every three keeps the lease from
  // going idle and reaches the host over the control channel.
  setInterval(function () { if (lease && !released) takeLease(observer ? ['view'] : ['view', 'input']); }, 3 * 60 * 1000);
  window.addEventListener('pagehide', function () {
    if (lease && !observer && !released) {
      try { navigator.sendBeacon(api + '/v1/devices/' + device + '/lease/release', new Blob([JSON.stringify({csrf: csrf})], {type: 'application/json'})); } catch (e) {}
    }
  });

  window.addEventListener('roadie-transport', function (e) {
    var reason = e.detail && e.detail.reason;
    say('No WebRTC connection (' + reason + '). Showing the MJPEG stream through the station instead, a few frames a second; reload to try WebRTC again.');
    setTimeout(function () { if (fallen) say(''); }, 6000);
  });
  window.__roadieTransportChanged = function () {
    if (window.__roadieRTC && window.__roadieRTC.isPlaying()) say('');
  };

  // The toolbar.
  var muted = true;
  $('speakerBtn').onclick = function () {
    muted = !muted;
    this.classList.toggle('on', !muted);
    if (window.__roadieRTC) window.__roadieRTC.setMuted(muted);
  };
  var micStream = null;
  $('micBtn').onclick = function () {
    var btn = this;
    if (micStream) {
      micStream.getTracks().forEach(function (t) { t.stop(); });
      micStream = null;
      if (window.__roadieRTC) window.__roadieRTC.setMic(null);
      btn.classList.remove('on');
      return;
    }
    if (!navigator.mediaDevices) { say('The microphone needs a secure page.', 'error'); return; }
    navigator.mediaDevices.getUserMedia({audio: {channelCount: 1, echoCancellation: true, noiseSuppression: true}}).then(function (stream) {
      micStream = stream;
      if (!window.__roadieRTC || !window.__roadieRTC.setMic(stream.getAudioTracks()[0])) {
        stream.getTracks().forEach(function (t) { t.stop(); });
        micStream = null;
        say('The microphone needs the WebRTC connection.', 'error');
        return;
      }
      btn.classList.add('on');
    }).catch(function (err) { say('Microphone: ' + err.message, 'error'); });
  };
  function applyMode(m) {
    input.setMode(m);
    $('mouseBtn').classList.toggle('on', m === 'mouse');
    $('touchBtn').classList.toggle('on', m === 'touch');
    localStorage.setItem('station-input-mode', m);
  }
  $('mouseBtn').onclick = function () { applyMode('mouse'); };
  $('touchBtn').onclick = function () { applyMode('touch'); };
  applyMode(input.mode());
  $('kbdBtn').onclick = function () {
    input.setCapture(!input.capturing());
    this.classList.toggle('on', input.capturing());
  };
  $('kbdBtn').classList.add('on');
  $('statsBtn').onclick = function () {
    if (!window.__roadieRTC) return;
    window.__roadieRTC.showStats(!window.__roadieRTC.statsShown());
    this.classList.toggle('on', window.__roadieRTC.statsShown());
  };
  $('takeBtn').onclick = function () { takeLease(['view', 'input']); };
  $('releaseBtn').onclick = release;
  var bar = $('shortcuts');
  shortcuts.forEach(function (s) {
    if (!s || !s.label || !s.keys) return;
    var b = document.createElement('button');
    b.type = 'button';
    b.textContent = s.label;
    b.title = s.keys;
    b.onclick = function () { if (!input.sendKeys(s.keys)) say('Unknown keys: ' + s.keys, 'error'); };
    bar.appendChild(b);
  });

  takeLease(['view', 'input']);
})();
