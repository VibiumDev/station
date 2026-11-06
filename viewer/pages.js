// The theme switch for Vibium's pages, and the "?" help toggles: light or dark as the system has
// it, unless the person picked one here, which this browser remembers.
// Loaded in the head, so a remembered choice applies before the first
// paint; the button is wired once the page is there.
(function () {
  var KEY = 'vibium-theme';
  var SUN = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M4.93 19.07l1.41-1.41M17.66 6.34l1.41-1.41"/></svg>';
  var MOON = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12.79A9 9 0 1 1 11.21 3a7 7 0 0 0 9.79 9.79z"/></svg>';
  var saved = null;
  try { saved = localStorage.getItem(KEY); } catch (e) {}
  if (saved === 'light' || saved === 'dark') document.documentElement.setAttribute('data-theme', saved);
  function current() {
    var t = document.documentElement.getAttribute('data-theme');
    if (t === 'light' || t === 'dark') return t;
    return window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }
  function paint(btn) {
    var dark = current() === 'dark';
    btn.setAttribute('aria-label', dark ? 'Switch to light mode' : 'Switch to dark mode');
    btn.title = btn.getAttribute('aria-label');
    btn.innerHTML = dark ? SUN : MOON;
  }
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
    var btn = document.getElementById('themeBtn');
    if (!btn) return;
    paint(btn);
    btn.addEventListener('click', function () {
      var next = current() === 'dark' ? 'light' : 'dark';
      document.documentElement.setAttribute('data-theme', next);
      try { localStorage.setItem(KEY, next); } catch (e) {}
      paint(btn);
    });
  });
})();
