// The theme switch for Vibium's pages, and the "?" help toggles. Light,
// dark, or whatever the system says: a choice of light or dark is kept
// in this browser and applied before the first paint, since this loads
// in the head; "system" keeps nothing and follows the preference. The
// switch is wired once the page is there, and a choice made in another
// tab is followed here.
(function () {
  var KEY = 'vibium-theme';
  var saved = null;
  try { saved = localStorage.getItem(KEY); } catch (e) {}
  if (saved === 'light' || saved === 'dark') document.documentElement.setAttribute('data-theme', saved);
  var buttons = [];
  var ICONS = {
    light: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>',
    dark: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3a7 7 0 0 0 9.79 9.79z"/></svg>',
    system: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><rect x="2" y="3" width="20" height="14" rx="2"/><path d="M8 21h8M12 17v4"/></svg>'
  };
  function choice() { return saved === 'light' || saved === 'dark' ? saved : 'system'; }
  function applyTheme() {
    if (choice() === 'system') document.documentElement.removeAttribute('data-theme');
    else document.documentElement.setAttribute('data-theme', choice());
    buttons.forEach(function (b) { b.setAttribute('aria-pressed', b.getAttribute('data-pick') === choice() ? 'true' : 'false'); });
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
    // The Records grid: a slider sets the card size, kept per browser.
    var size = document.getElementById('cardSize');
    var cards = document.querySelector('.cards');
    if (size && cards) {
      var apply = function (px) { cards.style.setProperty('--card', px + 'px'); };
      var kept = null;
      try { kept = localStorage.getItem('station-card-size'); } catch (e) {}
      if (kept && +kept >= +size.min && +kept <= +size.max) { size.value = kept; }
      apply(size.value);
      size.addEventListener('input', function () {
        apply(size.value);
        try { localStorage.setItem('station-card-size', size.value); } catch (e) {}
      });
    }
    // A framed Record Player gets the keyboard once it has loaded, so the
    // arrow keys step through the recording without a click first.
    var player = document.querySelector('iframe.player');
    if (player) player.addEventListener('load', function () { player.focus(); });
    buttons = Array.prototype.slice.call(document.querySelectorAll('.theme [data-pick]'));
    buttons.forEach(function (b) {
      b.innerHTML = ICONS[b.getAttribute('data-pick')] || '';
      b.addEventListener('click', function () { pickTheme(b.getAttribute('data-pick')); });
    });
    applyTheme();
  });
})();
