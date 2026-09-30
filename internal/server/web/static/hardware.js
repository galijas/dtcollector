(function () {
  'use strict';
  var root = document.getElementById('hw-list');
  if (!root) return;
  var D = JSON.parse(root.getAttribute('data-hw'));
  var catById = {}, statusLabel = {}, sourceLabel = {};
  D.categories.forEach(function (c) { c.columns = c.columns || []; catById[c.id] = c; });
  D.statuses.forEach(function (s) { statusLabel[s.id] = s.label; });
  D.sources.forEach(function (s) { sourceLabel[s.id] = s.label; });

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text != null) e.textContent = text;
    return e;
  }
  function fill(select, items) {
    items.forEach(function (it) {
      var o = el('option', null, it.label);
      o.value = it.id;
      select.appendChild(o);
    });
  }
  function attr(p, key) {
    var v = p.attrs[key];
    if (Array.isArray(v)) return v.join(', ');
    return v || '';
  }
  function fmtTime(s) { return s ? s.replace('T', ' ').replace(/:\d\dZ$/, ' UTC') : ''; }

  // Search ignores case, punctuation and spacing, so "E5-2699", "E5 2699" and
  // "e52699" all find "Intel Xeon E5-2699 v4". Every word must match.
  function norm(s) {
    return String(s).toLowerCase().replace(/\((r|tm)\)|[®™]/g, ' ').replace(/[^a-z0-9]+/g, ' ').trim();
  }
  D.parts.forEach(function (p) {
    var text = [p.name, p.comment, statusLabel[p.status], sourceLabel[p.source], p.created_by,
      attr(p, 'speed'), attr(p, 'ports'), attr(p, 'driver'), attr(p, 'type')].concat(p.aliases).join(' ');
    p._hay = ' ' + norm(text) + ' ';
    p._compact = p._hay.replace(/ /g, '');
  });
  function matches(p, words) {
    for (var i = 0; i < words.length; i++) {
      var w = words[i];
      if (p._hay.indexOf(w) < 0 && p._compact.indexOf(w.replace(/ /g, '')) < 0) return false;
    }
    return true;
  }
  function queryWords(q) {
    var n = norm(q);
    if (!n) return [];
    return n.split(' ');
  }

  var q = document.getElementById('hw-q');
  var fType = document.getElementById('hw-type');
  var fSource = document.getElementById('hw-source');
  var fStatus = document.getElementById('hw-status');
  fill(fType, D.categories.map(function (c) { return { id: c.id, label: c.label }; }));
  fill(fSource, D.sources);
  fill(fStatus, D.statuses);

  var collapsed = {};
  try { collapsed = JSON.parse(localStorage.getItem('dtc-hw-collapsed') || '{}') || {}; } catch (e) { collapsed = {}; }
  function saveCollapsed() { try { localStorage.setItem('dtc-hw-collapsed', JSON.stringify(collapsed)); } catch (e) {} }

  function statusPill(p) {
    return el('span', 'hw-status ' + p.status, statusLabel[p.status] || p.status);
  }

  function sourceCell(p) {
    var td = el('td', 'nowrap');
    var label = sourceLabel[p.source] || p.source;
    if (p.source === 'test_script' || p.source === 'swhw') {
      if (p.source_report_id) {
        var a = el('a', null, label);
        a.href = '/reports/' + encodeURIComponent(p.source_report_id);
        a.title = label + ': first found in the report created ' + fmtTime(p.report_created_at) +
          (p.report_key_name ? ', uploaded with API key "' + p.report_key_name + '"' : '') + '. Click to open it.';
        td.appendChild(a);
      } else {
        var s = el('span', 'hw-source', label);
        s.title = label + ': the report it came from has been deleted.';
        td.appendChild(s);
      }
    } else {
      var sp = el('span', 'hw-source', label);
      if (p.source === 'manual') sp.title = 'Entered by ' + (p.created_by || 'unknown') + ' on ' + fmtTime(p.created_at);
      else if (p.source === 'sw_analytics') sp.title = 'Reported by SERVERware installations (SW Analytics)';
      else sp.title = 'Imported from the Supported / Not Supported hardware sheets';
      if (p.updated_by && p.updated_by !== p.created_by) sp.title += '. Last edited by ' + p.updated_by + ' on ' + fmtTime(p.updated_at);
      td.appendChild(sp);
    }
    return td;
  }

  function actionsCell(p) {
    var td = el('td', 'nowrap hw-actions');
    if (D.can_edit) {
      var edit = el('button', null, 'Edit');
      edit.type = 'button';
      edit.addEventListener('click', function () { openForm(p); });
      td.appendChild(edit);
    }
    if (!D.can_delete) return td;
    var f = el('form', 'inline');
    f.method = 'post';
    f.action = '/hardware/' + p.id + '/delete';
    f.setAttribute('data-confirm', 'Delete "' + p.name + '" from the hardware list?');
    var del = el('button', 'danger', 'Delete');
    del.type = 'submit';
    f.appendChild(del);
    td.appendChild(f);
    return td;
  }

  function render() {
    var words = queryWords(q.value);
    var filtering = words.length || fType.value || fSource.value || fStatus.value;
    root.textContent = '';
    var shown = 0;
    D.categories.forEach(function (c) {
      if (fType.value && fType.value !== c.id) return;
      var all = D.parts.filter(function (p) { return p.category === c.id; });
      var rows = all.filter(function (p) {
        return (!fSource.value || p.source === fSource.value) && (!fStatus.value || p.status === fStatus.value) && matches(p, words);
      });
      if (filtering && !rows.length) return;
      shown += rows.length;
      var det = el('details', 'hw-cat');
      det.open = filtering ? true : !collapsed[c.id];
      det.addEventListener('toggle', function () {
        if (!filtering) { collapsed[c.id] = !det.open; saveCollapsed(); }
      });
      var sum = el('summary');
      sum.appendChild(el('span', 'hw-cat-name', c.label));
      sum.appendChild(el('span', 'hw-cat-count', filtering ? rows.length + ' of ' + all.length : String(all.length)));
      det.appendChild(sum);
      var wrap = el('div', 'table-wrap');
      if (!rows.length) {
        wrap.appendChild(el('p', 'muted hw-empty', 'No entries yet.'));
      } else {
        var tbl = el('table', 'hw-table');
        var hr = el('tr');
        ['Name'].concat(c.columns.map(function (x) { return x.label; })).concat(['Status', 'Comment', 'Source'])
          .concat(D.can_edit || D.can_delete ? [''] : []).forEach(function (h) { hr.appendChild(el('th', null, h)); });
        var th = el('thead'); th.appendChild(hr); tbl.appendChild(th);
        var tb = el('tbody');
        rows.forEach(function (p) {
          var tr = el('tr');
          tr.id = 'hw-' + p.id;
          var name = el('td', 'hw-name', p.name);
          var others = p.aliases.filter(function (a) { return a !== p.name; });
          if (others.length) name.title = 'Also seen as:\n' + others.join('\n');
          tr.appendChild(name);
          c.columns.forEach(function (col) { tr.appendChild(el('td', 'nowrap', attr(p, col.key) || '–')); });
          var st = el('td', 'nowrap'); st.appendChild(statusPill(p)); tr.appendChild(st);
          tr.appendChild(el('td', 'hw-comment', p.comment));
          tr.appendChild(sourceCell(p));
          if (D.can_edit || D.can_delete) tr.appendChild(actionsCell(p));
          tb.appendChild(tr);
        });
        tbl.appendChild(tb);
        wrap.appendChild(tbl);
      }
      det.appendChild(wrap);
      root.appendChild(det);
    });
    var exp = document.getElementById('hw-export');
    if (exp) {
      var params = new URLSearchParams();
      if (q.value.trim()) params.set('q', q.value.trim());
      if (fType.value) params.set('type', fType.value);
      if (fSource.value) params.set('source', fSource.value);
      if (fStatus.value) params.set('status', fStatus.value);
      var qs = params.toString();
      exp.href = '/hardware/export.pdf' + (qs ? '?' + qs : '');
      exp.textContent = filtering ? 'Export PDF (' + shown + ')' : 'Export PDF';
    }
    var count = document.getElementById('hw-count');
    count.textContent = filtering ? shown + ' of ' + D.parts.length + ' parts match.' : D.parts.length + ' parts in the list.';
    if (filtering && !shown) root.appendChild(el('p', 'card muted', 'No hardware matches. Clear the search or filters, or add the part with "Add hardware".'));
  }

  // The how-to opens in its own window (a new tab if pop-ups are blocked).
  var howto = document.getElementById('hw-howto');
  if (howto) {
    howto.addEventListener('click', function (e) {
      var w = window.open(howto.href, 'dtc-howto-swhw', 'width=900,height=820,resizable=yes,scrollbars=yes');
      if (w) { e.preventDefault(); w.focus(); }
    });
  }

  [q, fType, fSource, fStatus].forEach(function (x) { x.addEventListener(x === q ? 'input' : 'change', render); });

  // ---- add / edit form ----
  var form = document.getElementById('hw-form');
  var addBtn = document.getElementById('hw-add');
  var openForm = function () {};
  if (form && addBtn) {
    var fCat = document.getElementById('f-category');
    var details = document.getElementById('f-details');
    fill(fCat, D.categories.map(function (c) { return { id: c.id, label: c.label }; }));
    fill(document.getElementById('f-status'), D.statuses);
    fill(document.getElementById('f-drive-type'), D.drive_types.map(function (t) { return { id: t, label: t }; }));
    var fields = {
      name: document.getElementById('f-name'), status: document.getElementById('f-status'),
      comment: document.getElementById('f-comment'), speed: document.getElementById('f-speed'),
      ports: document.getElementById('f-ports'), driver: document.getElementById('f-driver'),
      drive_type: document.getElementById('f-drive-type')
    };
    var syncType = function () {
      details.hidden = !fCat.value;
      form.querySelectorAll('[data-cat]').forEach(function (r) { r.hidden = r.getAttribute('data-cat') !== fCat.value; });
    };
    fCat.addEventListener('change', function () { syncType(); if (fCat.value) fields.name.focus(); });

    var show = function (values, id) {
      form.hidden = false;
      form.action = id ? '/hardware/' + id : '/hardware';
      document.getElementById('hw-form-title').textContent = id ? 'Edit hardware' : 'Add hardware';
      document.getElementById('f-submit').textContent = id ? 'Save changes' : 'Add to list';
      fCat.value = values.category || '';
      Object.keys(fields).forEach(function (k) { fields[k].value = values[k] || ''; });
      if (!fields.status.value) fields.status.value = 'supported';
      syncType();
      form.scrollIntoView({ block: 'nearest' });
      (fCat.value ? fields.name : fCat).focus();
    };
    openForm = function (p) {
      show({
        category: p.category, name: p.name, status: p.status, comment: p.comment,
        speed: p.attrs.speed, ports: attr(p, 'ports'), driver: p.attrs.driver, drive_type: p.attrs.type
      }, p.id);
    };
    addBtn.addEventListener('click', function () { show({}, 0); });
    document.getElementById('f-cancel').addEventListener('click', function () { form.hidden = true; });
    if (D.form) show(D.form, D.form.id);
  }

  render();
})();
