// The theme for Vibium's pages, the "?" help toggles, and a station's
// sidebar. The theme is light, dark, or whatever the system says: light
// or dark is kept in this browser and applied before the first paint,
// since this loads in the head; "system" keeps nothing and follows the
// preference. Roadie's pages and the sign-in page have a button that
// cycles the three; a station's Settings has the three as a choice. A
// choice made in another tab is followed here. The sidebar's fold to a
// rail is kept the same way and applied before the first paint too.
(function () {
  var KEY = 'vibium-theme';
  var SIDE = 'station-side';
  var saved = null;
  try { saved = localStorage.getItem(KEY); } catch (e) {}
  if (saved === 'light' || saved === 'dark') document.documentElement.setAttribute('data-theme', saved);
  var side = null;
  try { side = localStorage.getItem(SIDE); } catch (e) {}
  if (side === 'rail') document.documentElement.setAttribute('data-side', 'rail');
  var btn = null;
  var NAMES = {light: 'light', dark: 'dark', system: 'the system default'};
  var NEXT = {light: 'dark', dark: 'system', system: 'light'};
  var ICONS = {
    light: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>',
    dark: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3a7 7 0 0 0 9.79 9.79z"/></svg>',
    system: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>'
  };
  function choice() { return saved === 'light' || saved === 'dark' ? saved : 'system'; }
  function applyTheme() {
    if (choice() === 'system') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', choice());
    var pick = document.getElementById('themePick');
    if (pick) Array.prototype.forEach.call(pick.querySelectorAll('input'), function (r) { r.checked = r.value === choice(); });
    if (!btn) return;
    btn.innerHTML = ICONS[choice()];
    btn.setAttribute('aria-label', 'Theme: ' + NAMES[choice()] + '. Switch to ' + NAMES[NEXT[choice()]] + '.');
    btn.title = btn.getAttribute('aria-label');
  }
  function pickTheme(t) {
    saved = t === 'light' || t === 'dark' ? t : null;
    try { if (saved) localStorage.setItem(KEY, saved); else localStorage.removeItem(KEY); } catch (e) {}
    applyTheme();
  }
  window.addEventListener('storage', function (e) {
    if (e.key === KEY || e.key === null) { saved = e.key === null ? null : e.newValue; applyTheme(); }
  });
  document.addEventListener('DOMContentLoaded', function () {
    // The "?" buttons: each shows or hides the explanation it names.
    Array.prototype.forEach.call(document.querySelectorAll('.qhelp[aria-controls]'), function (q) {
      var text = document.getElementById(q.getAttribute('aria-controls'));
      if (!text) return;
      q.addEventListener('click', function () {
        var open = text.hidden;
        text.hidden = !open;
        q.setAttribute('aria-expanded', open ? 'true' : 'false');
      });
    });
    // A button that deletes, or opens a file to the world, is held, not
    // clicked: a bar fills across it over a moment and the form goes
    // only when it is full; letting go, or sliding off, cancels. Space
    // or Enter held down does the same from the keyboard.
    Array.prototype.forEach.call(document.querySelectorAll('button[data-hold]'), function (btn) {
      var fill = document.createElement('span');
      fill.className = 'fill';
      btn.insertBefore(fill, btn.firstChild);
      var raf = null, start = 0, HOLD = 1500;
      function stop() { if (raf) cancelAnimationFrame(raf); raf = null; start = 0; fill.style.width = '0'; }
      function step() {
        var pct = Math.min(0.2 + (Date.now() - start) / HOLD * 0.8, 1);
        fill.style.width = (pct * 100) + '%';
        if (pct < 1) { raf = requestAnimationFrame(step); return; }
        raf = null;
        btn.disabled = true;
        btn.classList.add('fired');
        if (btn.form) { if (btn.form.requestSubmit) btn.form.requestSubmit(); else btn.form.submit(); }
      }
      function begin(e) { if (btn.disabled || start) return; e.preventDefault(); start = Date.now(); raf = requestAnimationFrame(step); }
      btn.addEventListener('pointerdown', begin);
      ['pointerup', 'pointerleave', 'pointercancel'].forEach(function (ev) { btn.addEventListener(ev, function () { if (!btn.disabled) stop(); }); });
      btn.addEventListener('keydown', function (e) {
        if (e.key !== ' ' && e.key !== 'Enter') return;
        if (!e.repeat) begin(e); else e.preventDefault();
      });
      btn.addEventListener('keyup', function (e) { if ((e.key === ' ' || e.key === 'Enter') && !btn.disabled) stop(); });
      btn.addEventListener('click', function (e) { e.preventDefault(); });  // a click alone does nothing
    });
    // A filmstrip row scrolls sideways through all its frames, and only
    // those in sight are fetched. The first to load tells the row the
    // frames' shape, so the ones not yet fetched take the right room.
    Array.prototype.forEach.call(document.querySelectorAll('.strip[data-strip]'), function (strip) {
      var first = strip.querySelector('img');
      function shape() {
        if (first.naturalWidth && first.naturalHeight) strip.style.setProperty('--frame', first.naturalWidth + ' / ' + first.naturalHeight);
      }
      if (first) { first.addEventListener('load', shape); shape(); }
      // A frame the station cannot give leaves the row to the others.
      strip.addEventListener('error', function (e) { if (e.target.tagName === 'IMG') e.target.remove(); }, true);
    });
    // A voice's ▶ on Settings says the words in the box with that voice,
    // through its real server or API, and plays them here.
    var probe = document.querySelector('form[data-preview-form]');
    if (probe) {
      var status = probe.querySelector('.status');
      probe.addEventListener('submit', function (e) { e.preventDefault(); });
      Array.prototype.forEach.call(document.querySelectorAll('button[data-preview]'), function (btn) {
        btn.addEventListener('click', function () {
          var body = new FormData(probe);
          body.set('name', btn.getAttribute('data-preview'));
          // Piper's row plays the voice picked in its list.
          var pick = btn.form && btn.form.querySelector('select[name=voice]');
          var who = pick ? pick.value : btn.getAttribute('data-preview');
          if (pick) body.set('voice', pick.value);
          btn.disabled = true;
          status.textContent = 'Playing it with ' + who + '…';
          var t0 = Date.now();
          fetch('/settings/say/preview', {method: 'POST', body: body, credentials: 'same-origin'})
            .then(function (r) {
              if (!r.ok) return r.text().then(function (msg) { throw new Error(msg.trim() || ('HTTP ' + r.status)); });
              return r.blob();
            })
            .then(function (wav) {
              status.textContent = who + ' answered in ' + ((Date.now() - t0) / 1000).toFixed(1) + ' s.';
              var a = new Audio(URL.createObjectURL(wav));
              a.addEventListener('ended', function () { URL.revokeObjectURL(a.src); });
              return a.play();
            })
            .catch(function (err) { status.textContent = err.message; })
            .then(function () { btn.disabled = false; });
        });
      });
    }
    // Times in the viewer's own time zone, with its name. An expiry form
    // shows its day and time on the viewer's clock and sends them as UTC,
    // so the station's own time zone does not matter.
    var two = function (n) { return (n < 10 ? '0' : '') + n; };
    var zone = '', zoneName = '';
    try {
      zoneName = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
      new Intl.DateTimeFormat([], {timeZoneName: 'short'}).formatToParts(new Date()).forEach(function (p) {
        if (p.type === 'timeZoneName') zone = p.value;
      });
    } catch (e) {}
    Array.prototype.forEach.call(document.querySelectorAll('[data-tz]'), function (el) {
      if (!zone) return;
      el.textContent = zone;
      el.title = zoneName ? 'Your time zone, ' + zoneName : 'Your time zone';
    });
    Array.prototype.forEach.call(document.querySelectorAll('time[data-local]'), function (t) {
      var d = new Date(t.getAttribute('datetime'));
      if (isNaN(d)) return;
      var o = {month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', timeZoneName: 'short'};
      if (d.getFullYear() !== new Date().getFullYear()) o.year = 'numeric';
      t.textContent = d.toLocaleString([], o);
    });
    Array.prototype.forEach.call(document.querySelectorAll('form[data-expiry]'), function (f) {
      var day = f.querySelector('input[type=date]'), clock = f.querySelector('input[type=time]'), utc = f.querySelector('input[name=expires_utc]');
      if (!day || !clock || !utc) return;
      var was = new Date(day.getAttribute('data-utc') || '');
      if (!isNaN(was)) {
        day.value = was.getFullYear() + '-' + two(was.getMonth() + 1) + '-' + two(was.getDate());
        clock.value = two(was.getHours()) + ':' + two(was.getMinutes());
      }
      var clear = f.querySelector('[data-clear-expiry]');
      if (clear) clear.addEventListener('click', function () { day.value = ''; clock.value = '00:01'; });
      f.addEventListener('submit', function () {
        var d = new Date(day.value + 'T' + (clock.value || '00:01'));
        utc.value = day.value && !isNaN(d) ? d.toISOString() : '';
      });
    });
    // A row's ⋯ menu closes on a click anywhere else.
    document.addEventListener('click', function (e) {
      Array.prototype.forEach.call(document.querySelectorAll('details.more[open]'), function (d) {
        if (!d.contains(e.target)) d.open = false;
      });
    });
    // Piper's voice list: Use shows once another voice is picked.
    Array.prototype.forEach.call(document.querySelectorAll('select[data-current]'), function (sel) {
      var use = sel.form && sel.form.querySelector('button[type=submit]');
      if (!use) return;
      sel.addEventListener('change', function () { use.hidden = sel.value === sel.getAttribute('data-current'); });
    });
    // A select that files its form as soon as it changes, such as how
    // many records a page shows.
    Array.prototype.forEach.call(document.querySelectorAll('select[data-submit]'), function (sel) {
      sel.addEventListener('change', function () {
        if (!sel.form) return;
        if (sel.form.requestSubmit) sel.form.requestSubmit(); else sel.form.submit();
      });
    });
    // The Records grid and filmstrips: a slider sets the size of a card,
    // or the height of a strip, kept per browser.
    [['cardSize', '.cards', '--card', 'station-card-size'], ['stripSize', '.strips', '--strip', 'station-strip-size']].forEach(function (s) {
      var size = document.getElementById(s[0]), box = document.querySelector(s[1]);
      if (!size || !box) return;
      var apply = function (px) { box.style.setProperty(s[2], px + 'px'); };
      var kept = null;
      try { kept = localStorage.getItem(s[3]); } catch (e) {}
      if (kept && +kept >= +size.min && +kept <= +size.max) { size.value = kept; }
      apply(size.value);
      size.addEventListener('input', function () {
        apply(size.value);
        try { localStorage.setItem(s[3], size.value); } catch (e) {}
      });
    });
    // A framed Record Player gets the keyboard once it has loaded, so the
    // arrow keys step through the recording without a click first.
    var player = document.querySelector('iframe.player');
    if (player) player.addEventListener('load', function () { player.focus(); });
    btn = document.getElementById('themeBtn');
    if (btn) btn.addEventListener('click', function () { pickTheme(NEXT[choice()]); });
    var pick = document.getElementById('themePick');
    if (pick) pick.addEventListener('change', function (e) { if (e.target.name === 'theme') pickTheme(e.target.value); });
    applyTheme();
    // The sidebar: its toggle folds it to a rail on a wide screen and
    // closes the drawer on a phone, where the bar's menu button opens it.
    // The account menu closes on a click elsewhere or Escape.
    var aside = document.getElementById('side');
    if (aside) {
      var root = document.documentElement;
      var phone = window.matchMedia('(max-width: 640px)');
      var toggle = aside.querySelector('.side-toggle');
      var menuBtn = document.querySelector('.bar .menu');
      var backdrop = document.querySelector('.side-backdrop');
      var account = aside.querySelector('details.account');
      var drawer = function (open) {
        if (open) root.setAttribute('data-drawer', 'open'); else root.removeAttribute('data-drawer');
        if (menuBtn) menuBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
        if (open && toggle) toggle.focus(); else if (!open && menuBtn && document.activeElement && aside.contains(document.activeElement)) menuBtn.focus();
      };
      var describe = function () {
        if (!toggle) return;
        var rail = root.getAttribute('data-side') === 'rail';
        toggle.setAttribute('aria-expanded', phone.matches ? 'true' : (rail ? 'false' : 'true'));
        toggle.setAttribute('aria-label', phone.matches ? 'Close the menu' : (rail ? 'Expand the sidebar' : 'Collapse the sidebar'));
        toggle.title = toggle.getAttribute('aria-label');
      };
      if (toggle) toggle.addEventListener('click', function () {
        if (phone.matches) { drawer(false); return; }
        var rail = root.getAttribute('data-side') !== 'rail';
        if (rail) root.setAttribute('data-side', 'rail'); else root.removeAttribute('data-side');
        try { if (rail) localStorage.setItem(SIDE, 'rail'); else localStorage.removeItem(SIDE); } catch (e) {}
        describe();
      });
      if (menuBtn) menuBtn.addEventListener('click', function () { drawer(true); });
      if (backdrop) backdrop.addEventListener('click', function () { drawer(false); });
      if (phone.addEventListener) phone.addEventListener('change', function () { drawer(false); describe(); });
      document.addEventListener('keydown', function (e) {
        if (e.key !== 'Escape') return;
        if (root.getAttribute('data-drawer') === 'open') drawer(false);
        if (account && account.open) account.open = false;
      });
      if (account) document.addEventListener('click', function (e) {
        if (account.open && !account.contains(e.target)) account.open = false;
      });
      describe();
    }
  });
})();
