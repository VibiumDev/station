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
    // A filmstrip shows as many of a recording's frames as its row has
    // room for, spread evenly from the first to the last. The first
    // frame's shape says how wide each one is. Only the frames shown are
    // fetched.
    Array.prototype.forEach.call(document.querySelectorAll('.strip[data-strip]'), function (strip) {
      var imgs = Array.prototype.slice.call(strip.querySelectorAll('img')), shown = 0;
      function lay() {
        var first = imgs[0];
        if (!first || !first.naturalWidth || !first.naturalHeight) return;
        var gap = 3, w = strip.clientHeight * first.naturalWidth / first.naturalHeight;
        var fit = Math.max(1, Math.floor((strip.clientWidth + gap) / (w + gap)));
        var n = Math.min(imgs.length, fit);
        strip.classList.toggle('full', fit <= imgs.length && n > 1);
        if (n === shown) return;
        shown = n;
        var show = {};
        for (var j = 0; j < n; j++) show[n === 1 ? 0 : Math.round(j * (imgs.length - 1) / (n - 1))] = true;
        imgs.forEach(function (img, i) {
          img.hidden = !show[i];
          if (show[i] && !img.getAttribute('src')) { img.loading = 'lazy'; img.src = img.getAttribute('data-src'); }
        });
      }
      imgs.forEach(function (img) {
        // A frame the station cannot give leaves the strip to the others.
        img.addEventListener('error', function () {
          var i = imgs.indexOf(img);
          if (i < 0) return;
          imgs.splice(i, 1);
          img.remove();
          shown = 0;
          lay();
        });
      });
      if (imgs[0]) imgs[0].addEventListener('load', lay);
      if (window.ResizeObserver) new ResizeObserver(lay).observe(strip); else window.addEventListener('resize', lay);
      lay();
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
    // or the height of a strip, kept per browser. A strip that changes
    // height lays its frames out again by itself.
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
