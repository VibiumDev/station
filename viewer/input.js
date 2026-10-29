// Input for the shared viewer: pointer, touch, wheel and keyboard on the
// picture, sent as the JSON commands a Roadie host takes on its hid
// channel and /api/hid/ws. Positions are fractions of the picture scaled to
// 0..32767, marked space "picture", and the host folds its own crop in: the
// page needs to know nothing about the capture.
//
//   var input = window.__roadieInput.attach({surface: img, send: fn, mode: 'mouse'});
//   input.setMode('touch'); input.sendKeys('GUI+h'); input.releaseAll();
(function () {
  var KEY_MAP = {
    KeyA:4,KeyB:5,KeyC:6,KeyD:7,KeyE:8,KeyF:9,KeyG:10,KeyH:11,KeyI:12,
    KeyJ:13,KeyK:14,KeyL:15,KeyM:16,KeyN:17,KeyO:18,KeyP:19,KeyQ:20,
    KeyR:21,KeyS:22,KeyT:23,KeyU:24,KeyV:25,KeyW:26,KeyX:27,KeyY:28,KeyZ:29,
    Digit1:30,Digit2:31,Digit3:32,Digit4:33,Digit5:34,Digit6:35,
    Digit7:36,Digit8:37,Digit9:38,Digit0:39,
    Enter:40,Escape:41,Backspace:42,Tab:43,Space:44,
    Minus:45,Equal:46,BracketLeft:47,BracketRight:48,Backslash:49,
    Semicolon:51,Quote:52,Backquote:53,Comma:54,Period:55,Slash:56,
    CapsLock:57,
    F1:58,F2:59,F3:60,F4:61,F5:62,F6:63,F7:64,F8:65,F9:66,F10:67,F11:68,F12:69,
    PrintScreen:70,ScrollLock:71,Pause:72,Insert:73,Home:74,PageUp:75,
    Delete:76,End:77,PageDown:78,
    ArrowRight:79,ArrowLeft:80,ArrowDown:81,ArrowUp:82,
    NumLock:83,
    ControlLeft:224,ShiftLeft:225,AltLeft:226,MetaLeft:227,
    ControlRight:228,ShiftRight:229,AltRight:230,MetaRight:231
  };
  // Names a shortcut may use for the modifiers, whatever the platform calls them.
  var MODIFIERS = {GUI:227, Meta:227, Cmd:227, Command:227, Win:227, Super:227,
    Alt:226, Option:226, Ctrl:224, Control:224, Shift:225};

  // keycode turns one token of a shortcut ("GUI", "h", "Escape", "F3") into
  // a USB keycode, or undefined.
  function keycode(token) {
    if (MODIFIERS[token] !== undefined) return MODIFIERS[token];
    if (KEY_MAP[token] !== undefined) return KEY_MAP[token];
    if (token.length === 1) {
      var c = token.toUpperCase();
      if (c >= 'A' && c <= 'Z') return KEY_MAP['Key' + c];
      if (c >= '0' && c <= '9') return KEY_MAP['Digit' + c];
      if (c === ' ') return KEY_MAP.Space;
    }
    var named = {Esc: 'Escape', Return: 'Enter', Del: 'Delete', Up: 'ArrowUp', Down: 'ArrowDown', Left: 'ArrowLeft', Right: 'ArrowRight'};
    if (named[token]) return KEY_MAP[named[token]];
    return undefined;
  }

  function attach(opts) {
    var surface = opts.surface;
    var send = opts.send;
    var mode = opts.mode === 'touch' ? 'touch' : 'mouse';
    var capture = opts.capture !== false;
    var held = {};            // keycodes the host believes are down
    var pressed = false, button = 0;
    var pendingX = -1, pendingY = -1, moveDirty = false;
    var contacts = [], touchDirty = false;
    var scroll = 0, scrollDirty = false, wheelRest = 0;
    var twoFingerY = null, scrollAccum = 0;

    function pos(e) {
      var r = surface.getBoundingClientRect();
      var px = Math.max(0, Math.min(1, (e.clientX - r.left) / (r.width || 1)));
      var py = Math.max(0, Math.min(1, (e.clientY - r.top) / (r.height || 1)));
      return {x: Math.round(px * 32767), y: Math.round(py * 32767)};
    }
    function move(x, y) { send({cmd: 'mouse_move', x: x, y: y, space: 'picture'}); }
    function touch(list) { send({cmd: 'touch', contacts: list, space: 'picture'}); }

    // Only the latest position goes out, on a timer: 33 ms keeps up with
    // the picture, touch a little slower, and the wheel as notches add up.
    var timers = [
      setInterval(function () { if (moveDirty) { move(pendingX, pendingY); moveDirty = false; } }, 33),
      setInterval(function () { if (touchDirty) { touch(contacts); touchDirty = false; } }, 50),
      setInterval(function () { if (scrollDirty) { send({cmd: 'mouse_scroll', amount: scroll}); scroll = 0; scrollDirty = false; } }, 10)
    ];

    // A press is held back for HOLD_MS: one that ends sooner goes out as a
    // single tap or click, which the host performs whole, so a slow or
    // lossy link cannot stretch a tap into a long press. A press that
    // lasts, or moves more than SLOP, is sent as it is and streams from
    // then on.
    var HOLD_MS = 150, SLOP = 500;
    var pending = null;  // {x, y, mode, button, timer}
    function beginPress(p, m, b) {
      if (pending) clearTimeout(pending.timer);
      pending = {x: p.x, y: p.y, mode: m, button: b, timer: setTimeout(commitPress, HOLD_MS)};
    }
    function commitPress() {
      if (!pending) return;
      var q = pending;
      pending = null;
      clearTimeout(q.timer);
      if (q.mode === 'mouse') send({cmd: 'mouse_press', buttons: q.button});
      else { contacts = [{id: 0, tip: true, x: q.x, y: q.y}]; touchDirty = false; touch(contacts); }
    }
    function movedFar(p) { return pending && (Math.abs(p.x - pending.x) > SLOP || Math.abs(p.y - pending.y) > SLOP); }
    // endPress ends the press: still pending, it becomes one tap or click
    // where it began.
    function endPress(p) {
      pressed = false;
      if (pending) {
        var q = pending;
        pending = null;
        clearTimeout(q.timer);
        if (q.mode === 'mouse') { move(q.x, q.y); moveDirty = false; send({cmd: 'mouse_click', buttons: q.button}); }
        else send({cmd: 'tap', x: q.x, y: q.y, space: 'picture'});
        button = 0;
        return;
      }
      if (mode === 'mouse') {
        if (moveDirty) { move(pendingX, pendingY); moveDirty = false; }
        send({cmd: 'mouse_release', buttons: button || 1});
        button = 0;
      } else {
        touchDirty = false;
        touch(p ? [{id: 0, tip: false, x: p.x, y: p.y}] : []);
        setTimeout(function () { touch([]); }, 20);
      }
    }

    function onMouseMove(e) {
      var p = pos(e);
      if (pending && movedFar(p)) commitPress();
      if (mode === 'mouse') { pendingX = p.x; pendingY = p.y; moveDirty = true; }
      else if (pressed && !pending) { contacts = [{id: 0, tip: true, x: p.x, y: p.y}]; touchDirty = true; }
    }
    function onMouseDown(e) {
      e.preventDefault();
      pressed = true;
      var p = pos(e);
      if (mode === 'mouse') {
        button = e.button === 2 ? 2 : e.button === 1 ? 4 : 1;
        pendingX = p.x; pendingY = p.y; moveDirty = false;
        move(p.x, p.y);
        beginPress(p, 'mouse', button);
      } else {
        beginPress(p, 'touch', 0);
      }
    }
    function onMouseUp(e) {
      if (!pressed) return;
      endPress(pos(e));
    }
    // A line is a notch, 25 pixels are one, a page twenty; what is left
    // over adds up so small steps scroll too.
    function onWheel(e) {
      e.preventDefault();
      if (mode !== 'mouse' || !e.deltaY) return;
      var notches = e.deltaMode === 1 ? e.deltaY : e.deltaMode === 2 ? e.deltaY * 20 : e.deltaY / 25;
      wheelRest += notches;
      var whole = wheelRest > 0 ? Math.floor(wheelRest) : Math.ceil(wheelRest);
      if (!whole) return;
      wheelRest -= whole;
      scroll = Math.max(-127, Math.min(127, scroll - whole));
      scrollDirty = true;
    }
    function touchList(e) {
      var list = [];
      for (var i = 0; i < Math.min(e.touches.length, 2); i++) {
        var p = pos(e.touches[i]);
        list.push({id: i, tip: true, x: p.x, y: p.y});
      }
      return list;
    }
    function onTouchStart(e) {
      e.preventDefault();
      if (mode === 'touch') {
        if (e.touches.length === 1) { pressed = true; beginPress(pos(e.touches[0]), 'touch', 0); }
        else { commitPress(); contacts = touchList(e); touchDirty = false; touch(contacts); }
      } else if (e.touches.length === 1) {
        // A finger in mouse mode: the pointer goes there and clicks.
        var p = pos(e.touches[0]);
        pressed = true;
        button = 1;
        pendingX = p.x; pendingY = p.y; moveDirty = false;
        move(p.x, p.y);
        beginPress(p, 'mouse', 1);
      } else {
        commitPress();
      }
    }
    function onTouchMove(e) {
      e.preventDefault();
      if (mode === 'touch') {
        if (e.touches.length >= 2) commitPress();
        else if (pending && movedFar(pos(e.touches[0]))) commitPress();
        if (pending) return;
        contacts = touchList(e); touchDirty = true;
        return;
      }
      if (e.touches.length >= 2) {
        commitPress();
        var y = (e.touches[0].clientY + e.touches[1].clientY) / 2;
        if (twoFingerY !== null) {
          scrollAccum += y - twoFingerY;
          while (Math.abs(scrollAccum) >= 10) {
            scroll = Math.max(-127, Math.min(127, scroll + (scrollAccum > 0 ? 3 : -3)));
            scrollDirty = true;
            scrollAccum -= scrollAccum > 0 ? 10 : -10;
          }
        }
        twoFingerY = y;
      } else {
        twoFingerY = null; scrollAccum = 0;
        var p = pos(e.touches[0]);
        if (pending && movedFar(p)) commitPress();
        pendingX = p.x; pendingY = p.y; moveDirty = true;
      }
    }
    function onTouchEnd(e) {
      e.preventDefault();
      if (mode === 'touch') {
        if (e.touches.length) { commitPress(); contacts = touchList(e); touchDirty = false; touch(contacts); }
        else if (pressed) endPress(e.changedTouches && e.changedTouches[0] ? pos(e.changedTouches[0]) : null);
      } else if (!e.touches.length && pressed) {
        endPress(null);
      }
      if (e.touches.length < 2) { twoFingerY = null; scrollAccum = 0; }
    }
    function inForm(e) {
      var tag = e.target && e.target.tagName;
      return tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || (e.target && e.target.isContentEditable);
    }
    function onKeyDown(e) {
      if (!capture || inForm(e)) return;
      var code = KEY_MAP[e.code];
      if (code === undefined) return;
      e.preventDefault();
      held[code] = true;
      send({cmd: 'key_press', keycode: code});
    }
    function onKeyUp(e) {
      var code = KEY_MAP[e.code];
      // Release what went down, whatever capture says now: a modifier left
      // held rewrites everything typed after it.
      if (code === undefined || !held[code]) return;
      e.preventDefault();
      delete held[code];
      send({cmd: 'key_release', keycode: code});
    }
    function releaseAll() {
      if (pending) { clearTimeout(pending.timer); pending = null; }
      send({cmd: 'mouse_release', buttons: 7});
      touch([]);
      pressed = false; contacts = []; touchDirty = false;
      for (var k in held) send({cmd: 'key_release', keycode: parseInt(k, 10)});
      held = {};
    }
    function sendKeys(chord) {
      var codes = [];
      var parts = String(chord).split('+');
      for (var i = 0; i < parts.length; i++) {
        var c = keycode(parts[i].trim());
        if (c === undefined) return false;
        codes.push(c);
      }
      codes.forEach(function (c) { send({cmd: 'key_press', keycode: c}); });
      codes.slice().reverse().forEach(function (c) { send({cmd: 'key_release', keycode: c}); });
      return true;
    }
    function onVisibility() { if (document.hidden) releaseAll(); }

    surface.addEventListener('mousemove', onMouseMove);
    surface.addEventListener('mousedown', onMouseDown);
    surface.addEventListener('contextmenu', function (e) { e.preventDefault(); });
    surface.addEventListener('dragstart', function (e) { e.preventDefault(); });
    document.addEventListener('mouseup', onMouseUp);
    surface.addEventListener('wheel', onWheel, {passive: false});
    surface.addEventListener('touchstart', onTouchStart, {passive: false});
    surface.addEventListener('touchmove', onTouchMove, {passive: false});
    surface.addEventListener('touchend', onTouchEnd, {passive: false});
    document.addEventListener('keydown', onKeyDown);
    document.addEventListener('keyup', onKeyUp);
    window.addEventListener('blur', releaseAll);
    window.addEventListener('pagehide', releaseAll);
    document.addEventListener('visibilitychange', onVisibility);

    return {
      mode: function () { return mode; },
      setMode: function (m) { if (m !== mode) releaseAll(); mode = m === 'touch' ? 'touch' : 'mouse'; },
      capturing: function () { return capture; },
      setCapture: function (on) { capture = !!on; if (!capture) releaseAll(); },
      releaseAll: releaseAll,
      sendKeys: sendKeys,
      keycode: keycode,
      detach: function () {
        releaseAll();
        timers.forEach(clearInterval);
        surface.removeEventListener('mousemove', onMouseMove);
        surface.removeEventListener('mousedown', onMouseDown);
        document.removeEventListener('mouseup', onMouseUp);
        surface.removeEventListener('wheel', onWheel);
        surface.removeEventListener('touchstart', onTouchStart);
        surface.removeEventListener('touchmove', onTouchMove);
        surface.removeEventListener('touchend', onTouchEnd);
        document.removeEventListener('keydown', onKeyDown);
        document.removeEventListener('keyup', onKeyUp);
        window.removeEventListener('blur', releaseAll);
        window.removeEventListener('pagehide', releaseAll);
        document.removeEventListener('visibilitychange', onVisibility);
      }
    };
  }

  window.__roadieInput = {attach: attach, keycode: keycode, KEY_MAP: KEY_MAP};
})();
