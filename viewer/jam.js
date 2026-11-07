// The Jams pages: example prompts fill the box, Enter sends, and a jam
// that is still running streams its transcript from the station's
// server-sent events and reloads once it ends.
(function () {
  function h(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function word(s) {
    return {passed: 'PASS', failed: 'FAIL', inconclusive: 'REVIEW'}[s] || (s || '').toUpperCase().replace(/_/g, ' ');
  }
  function now() {
    var d = new Date();
    return [d.getHours(), d.getMinutes(), d.getSeconds()].map(function (n) { return (n < 10 ? '0' : '') + n; }).join(':');
  }
  function render(kind, d) {
    var ev = h('div', 'ev ' + kind);
    ev.appendChild(h('span', 't', now()));
    var body = h('div', 'body');
    switch (kind) {
      case 'turn': body.appendChild(h('b', null, 'Prompt')); body.appendChild(h('p', null, d.text)); break;
      case 'start': case 'end': body.className += ' muted'; body.textContent = d.text || ''; break;
      case 'text': body.appendChild(h('p', null, d.text)); break;
      case 'call':
        var args = d.args && JSON.stringify(d.args);
        body.appendChild(h('code', null, d.tool + (args && args !== '{}' ? ' ' + args : '')));
        break;
      case 'result': if (d.error) ev.className += ' error'; body.textContent = d.text || ''; break;
      case 'shot':
        if (d.image) {
          var a = h('a'); a.href = d.image; a.target = '_blank'; a.rel = 'noopener';
          var img = h('img'); img.src = d.image; img.alt = 'Screenshot ' + (d.tool || '');
          a.appendChild(img); body.appendChild(a);
        }
        break;
      case 'verdict':
        var v = d.verdict || {};
        body.appendChild(h('b', 'verdict ' + (v.status || ''), word(v.status)));
        body.appendChild(document.createTextNode(' ' + (v.summary || '')));
        if (v.evidence && v.evidence.length) {
          var ul = h('ul');
          v.evidence.forEach(function (e) { ul.appendChild(h('li', null, e)); });
          body.appendChild(ul);
        }
        if (v.reason) body.appendChild(h('span', 'muted', v.reason));
        break;
      case 'error': body.textContent = d.text || ''; break;
      default: return null;
    }
    ev.appendChild(body);
    return ev;
  }
  document.addEventListener('DOMContentLoaded', function () {
    var box = document.querySelector('textarea.prompt');
    Array.prototype.forEach.call(document.querySelectorAll('.example'), function (b) {
      b.addEventListener('click', function () { if (box) { box.value = b.textContent; box.focus(); } });
    });
    Array.prototype.forEach.call(document.querySelectorAll('textarea.prompt'), function (t) {
      t.addEventListener('keydown', function (e) {
        if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); if (t.value.trim()) t.form.requestSubmit(); }
      });
    });
    var tr = document.getElementById('transcript');
    if (!tr || !tr.dataset.open) return;
    var es = new EventSource('/v1/jams/' + tr.dataset.jam + '/events?after=' + (tr.dataset.after || 0));
    ['start', 'turn', 'text', 'call', 'result', 'shot', 'verdict', 'error', 'end'].forEach(function (kind) {
      es.addEventListener(kind, function (e) {
        var d = {};
        try { d = JSON.parse(e.data); } catch (err) {}
        var node = render(kind, d);
        if (node) { tr.appendChild(node); node.scrollIntoView({block: 'nearest'}); }
        if (kind === 'end') { es.close(); setTimeout(function () { location.reload(); }, 800); }
      });
    });
  });
})();
