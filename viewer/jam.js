// The Jams pages: example prompts fill the box, Enter sends, a jam's
// title is written in place, and a jam that is still running streams its
// transcript from the station's server-sent events and reloads once it
// ends.
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
      case 'start': case 'kept': case 'end': body.className += ' muted'; body.textContent = d.text || ''; break;
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
      case 'error': case 'brake': body.textContent = d.text || ''; break;
      case 'notes':
        body = h('details', 'body');
        body.appendChild(h('summary', null, 'Notes given to the model'));
        body.appendChild(h('p', null, d.text));
        break;
      default: return null;
    }
    ev.appendChild(body);
    return ev;
  }
  // The title is its own field for someone who may steer the jam: Enter
  // or leaving it saves, Escape puts it back, and an empty one gives the
  // title up, which shows the prompt again. It returns a function that
  // runs its argument once nobody is writing.
  function editTitle(el) {
    var after = null, writing = false;
    function settle() { writing = false; if (after) { var f = after; after = null; f(); } }
    if (!el) return function (f) { f(); };
    el.contentEditable = 'true';
    try { el.contentEditable = 'plaintext-only'; } catch (e) {}
    var shown = el.textContent, max = parseInt(el.dataset.max, 10) || 120;
    function say(text) {
      var note = document.getElementById('titleNote');
      if (!text) { if (note) note.remove(); return; }
      if (!note) {
        note = h('div', 'msg error'); note.id = 'titleNote'; note.setAttribute('role', 'alert');
        var head = el.closest('header') || el.parentNode;
        head.parentNode.insertBefore(note, head.nextSibling);
      }
      note.textContent = text;
    }
    el.addEventListener('focus', function () {
      writing = true;
      // An untitled jam shows its prompt: typing replaces it.
      if (!el.dataset.title) { var r = document.createRange(); r.selectNodeContents(el); var sel = getSelection(); sel.removeAllRanges(); sel.addRange(r); }
    });
    el.addEventListener('keydown', function (e) {
      if (e.key === 'Enter') { e.preventDefault(); el.blur(); }
      else if (e.key === 'Escape') { e.preventDefault(); el.textContent = shown; el.blur(); }
    });
    el.addEventListener('paste', function (e) {
      e.preventDefault();
      var text = ((e.clipboardData && e.clipboardData.getData('text/plain')) || '').replace(/\s+/g, ' ');
      document.execCommand('insertText', false, text);
    });
    el.addEventListener('blur', function () {
      var text = el.textContent.replace(/\s+/g, ' ').trim();
      el.scrollLeft = 0;
      if (text === shown.replace(/\s+/g, ' ').trim()) { el.textContent = shown; settle(); return; }
      if (text.length > max) { el.textContent = shown; say('A title is at most ' + max + ' characters.'); settle(); return; }
      var headers = {'Content-Type': 'application/json'};
      if (el.dataset.csrf) headers['X-CSRF-Token'] = el.dataset.csrf;
      el.classList.add('saving');
      fetch('/v1/jams/' + el.dataset.jam + '/title', {method: 'POST', headers: headers, credentials: 'same-origin', body: JSON.stringify({title: text})})
        .then(function (r) { return r.json().then(function (d) { if (!r.ok) throw new Error(d.error || 'The title was not saved.'); return d; }); })
        .then(function (d) {
          var title = (d.jam && d.jam.title) || '';
          el.dataset.title = title;
          shown = title || el.dataset.prompt || '';
          el.textContent = shown;
          say('');
        })
        .catch(function (err) { el.textContent = shown; say(err.message || 'The title was not saved.'); })
        .then(function () { el.classList.remove('saving'); settle(); });
    });
    return function (f) { if (writing) after = f; else f(); };
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
    var busy = editTitle(document.querySelector('h1.jamtitle[data-jam]'));
    var tr = document.getElementById('transcript');
    if (!tr || !tr.dataset.open) return;
    // The grace after a settled result: a bar runs down to the moment
    // the jam ends, with the seconds left and the button that keeps it
    // open. A page opened during the grace picks it up from the server.
    var g = document.getElementById('grace'), timer;
    function showGrace(until) {
      if (!g || !(until > Date.now())) return;
      var total = (parseInt(g.dataset.total, 10) || 10) * 1000;
      var left = g.querySelector('.left'), bar = g.querySelector('.bar span');
      g.hidden = false;
      g.scrollIntoView({block: 'center', behavior: 'smooth'});
      clearInterval(timer);
      function tick() {
        var ms = until - Date.now();
        if (ms <= 0) { clearInterval(timer); ms = 0; }
        left.textContent = Math.ceil(ms / 1000);
        bar.style.width = Math.min(100, 100 * ms / total) + '%';
      }
      tick();
      timer = setInterval(tick, 100);
    }
    function hideGrace() { if (g) { g.hidden = true; } clearInterval(timer); }
    // The page follows the transcript while its end is on screen. A
    // reader who has scrolled away, to the Stop button say, stays there.
    function atEnd() {
      var last = tr.lastElementChild;
      return !last || last.getBoundingClientRect().top < window.innerHeight;
    }
    showGrace(parseInt(g && g.dataset.until, 10));
    var es = new EventSource('/v1/jams/' + tr.dataset.jam + '/events?after=' + (tr.dataset.after || 0));
    ['start', 'notes', 'turn', 'text', 'call', 'result', 'shot', 'verdict', 'error', 'brake', 'grace', 'kept', 'end'].forEach(function (kind) {
      es.addEventListener(kind, function (e) {
        var d = {};
        try { d = JSON.parse(e.data); } catch (err) {}
        if (kind === 'grace') { showGrace(d.until); return; }
        hideGrace();
        var follow = atEnd();
        var node = render(kind, d);
        if (node) { tr.appendChild(node); if (follow) node.scrollIntoView({block: 'nearest'}); }
        if (kind === 'end') { es.close(); setTimeout(function () { busy(function () { location.reload(); }); }, 800); }
      });
    });
  });
})();
