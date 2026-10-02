(function () {
  'use strict';

  // ---- small page behaviors ----

  document.addEventListener('submit', function (e) {
    var msg = e.target.getAttribute && e.target.getAttribute('data-confirm');
    if (msg && !window.confirm(msg)) e.preventDefault();
  });

  // (i) buttons show the explanation right after them.
  document.addEventListener('click', function (e) {
    var b = e.target.closest && e.target.closest('.info-btn');
    if (!b) return;
    var t = b.nextElementSibling;
    if (!t || !t.classList.contains('info-text')) return;
    t.hidden = !t.hidden;
    b.setAttribute('aria-expanded', String(!t.hidden));
  });

  document.addEventListener('click', function (e) {
    var btn = e.target.closest && e.target.closest('[data-copy]');
    if (!btn) return;
    var el = document.querySelector(btn.getAttribute('data-copy'));
    if (!el) return;
    navigator.clipboard.writeText(el.textContent.trim()).then(function () {
      btn.textContent = 'Copied';
      setTimeout(function () { btn.textContent = 'Copy'; }, 2000);
    });
  });

  var compareForm = document.getElementById('compare-form');
  if (compareForm) {
    var btn = document.getElementById('compare-btn');
    var del = document.getElementById('delete-btn');
    var all = document.getElementById('select-all');
    var back = compareForm.querySelector('input[name=return]');
    var hint = document.getElementById('compare-hint');
    var defaultHint = hint.textContent;
    var boxes = function () { return compareForm.querySelectorAll('input[name=ids]'); };
    var sync = function () {
      var checked = compareForm.querySelectorAll('input[name=ids]:checked');
      var profiles = {}, hardware = false;
      checked.forEach(function (c) {
        profiles[c.getAttribute('data-profile')] = true;
        if (c.getAttribute('data-kind') === 'hardware') hardware = true;
      });
      var nProfiles = Object.keys(profiles).length;
      var ok = checked.length >= 2 && checked.length <= 8 && nProfiles === 1 && !hardware;
      btn.disabled = !ok;
      if (del) {
        del.disabled = checked.length === 0;
        del.textContent = checked.length ? 'Delete selected (' + checked.length + ')' : 'Delete selected';
      }
      if (all) {
        var n = boxes().length;
        all.checked = n > 0 && checked.length === n;
        all.indeterminate = checked.length > 0 && checked.length < n;
      }
      if (hardware && checked.length > 1) hint.textContent = 'Hardware-only reports have no test results to compare; select benchmark reports.';
      else if (nProfiles > 1) hint.textContent = 'The selection mixes profile versions (' + Object.keys(profiles).join(', ') + '); compare within one.';
      else if (checked.length > 8) hint.textContent = 'Select at most 8 reports to compare.';
      else if (checked.length) hint.textContent = checked.length + ' selected.';
      else hint.textContent = defaultHint;
    };
    if (all) {
      all.addEventListener('change', function () {
        boxes().forEach(function (c) { c.checked = all.checked; });
        sync();
      });
    }
    compareForm.addEventListener('change', function (e) { if (e.target !== all) sync(); });
    // The return path only goes with a delete, so compare URLs stay clean.
    if (back) back.disabled = true;
    if (del) {
      del.addEventListener('click', function (e) {
        var n = compareForm.querySelectorAll('input[name=ids]:checked').length;
        if (!window.confirm('Delete ' + n + ' selected report' + (n === 1 ? '' : 's') + ' permanently? This can\'t be undone.')) {
          e.preventDefault();
          return;
        }
        if (back) back.disabled = false;
      });
    }
    sync();
  }

  // ---- charts ----

  var reportEl = document.getElementById('report-charts');
  var compareEl = document.getElementById('compare-charts');
  if (!reportEl && !compareEl) return;

  function cssVar(n) { return getComputedStyle(document.documentElement).getPropertyValue(n).trim(); }
  function seriesColor(i) { return cssVar('--series-' + ((i % 8) + 1)); }

  function fmtElapsed(s) {
    if (s == null) return '–';
    var neg = s < 0; s = Math.abs(Math.round(s));
    var h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), sec = s % 60;
    var out = (h ? h + ':' + String(m).padStart(2, '0') : m) + ':' + String(sec).padStart(2, '0');
    return neg ? '-' + out : out;
  }

  function compact(v, base, units) {
    var i = 0, a = Math.abs(v);
    while (a >= base && i < units.length - 1) { a /= base; v /= base; i++; }
    var digits = Math.abs(v) >= 100 || i === 0 ? 0 : 1;
    return v.toFixed(digits) + (units[i] ? ' ' + units[i] : '');
  }

  var UNITS = {
    calls: { fmt: function (v) { return Math.round(v).toLocaleString('en-US'); }, tick: function (v) { return v.toLocaleString('en-US'); } },
    pct: { fmt: function (v) { return v.toFixed(1) + '%'; }, tick: function (v) { return v + '%'; } },
    ms: { fmt: function (v) { return v.toFixed(0) + ' ms'; }, tick: function (v) { return v + ' ms'; } },
    count: { fmt: function (v) { return String(Math.round(v)); }, tick: function (v) { return String(v); } },
    bitps: { fmt: function (v) { return compact(v, 1000, ['bit/s', 'kbit/s', 'Mbit/s', 'Gbit/s']); }, tick: function (v) { return compact(v, 1000, ['', 'k', 'M', 'G']); } },
    bps: { fmt: function (v) { return compact(v, 1000, ['B/s', 'kB/s', 'MB/s', 'GB/s']); }, tick: function (v) { return compact(v, 1000, ['', 'k', 'M', 'G']); } },
    bytes: { fmt: function (v) { return compact(v, 1024, ['B', 'KiB', 'MiB', 'GiB', 'TiB']); }, tick: function (v) { return compact(v, 1024, ['', 'Ki', 'Mi', 'Gi', 'Ti']); } }
  };
  var UNIT_LABEL = { calls: 'calls', pct: '%', ms: 'ms', count: 'per interval', bitps: 'bit/s', bps: 'bytes/s', bytes: 'bytes' };

  // Chart definitions for one report: each has one unit (one y axis).
  var REPORT_CHARTS = [
    { title: 'Concurrent calls', unit: 'calls', lines: [['concurrent_calls', null, 'SwarmDialer'],
      ['pbxware_active_calls', 'MT', 'PBXware MT'], ['pbxware_active_calls', 'CC', 'PBXware CC']] },
    { title: 'Host CPU', unit: 'pct', max100: true, lines: [['host_cpu_pct', null, 'CPU'], ['host_iowait_pct', null, 'I/O wait']] },
    { title: 'Host memory', unit: 'pct', max100: true, lines: [['host_mem_pct', null, 'Memory']] },
    { title: 'Asterisk CPU', unit: 'pct', groups: 'asterisk_cpu_pct' },
    { title: 'VPS CPU', unit: 'pct', groups: 'vps_cpu_pct' },
    { title: 'VPS memory', unit: 'bytes', groups: 'vps_mem_bytes' },
    { title: 'Host network', unit: 'bitps', lines: [['host_net_rx_bps', null, 'Receive'], ['host_net_tx_bps', null, 'Transmit']] },
    { title: 'Host disk', unit: 'bps', lines: [['host_disk_read_bps', null, 'Read'], ['host_disk_write_bps', null, 'Write']] },
    { title: 'Call setup time p95', unit: 'ms', lines: [['setup_ms_p95', null, 'Setup p95']] },
    { title: 'Failed calls', unit: 'count', lines: [['failed_calls', null, 'Failed']] }
  ];

  // Overlaid charts for compare: one line per report.
  var COMPARE_CHARTS = [
    { title: 'Concurrent calls', unit: 'calls', key: 'concurrent_calls' },
    { title: 'Host CPU', unit: 'pct', max100: true, key: 'host_cpu_pct' },
    { title: 'Host memory', unit: 'pct', max100: true, key: 'host_mem_pct' },
    { title: 'Host I/O wait', unit: 'pct', key: 'host_iowait_pct' },
    { title: 'Asterisk CPU (MT)', unit: 'pct', key: 'asterisk_cpu_pct', group: 'MT' },
    { title: 'Asterisk CPU (CC)', unit: 'pct', key: 'asterisk_cpu_pct', group: 'CC' },
    { title: 'Call setup time p95', unit: 'ms', key: 'setup_ms_p95' },
    { title: 'Host disk write', unit: 'bps', key: 'host_disk_write_bps' }
  ];

  function getSeries(test, key, group) {
    var s = test && test.timeseries && test.timeseries.series && test.timeseries.series[key];
    if (!s) return null;
    if (Array.isArray(s)) return group ? null : s;
    return group ? (Array.isArray(s[group]) ? s[group] : null) : null;
  }

  function groupNames(test, key) {
    var s = test && test.timeseries && test.timeseries.series && test.timeseries.series[key];
    if (!s || Array.isArray(s)) return [];
    var order = ['MT', 'CC', 'swarmdialer'];
    return Object.keys(s).sort(function (a, b) {
      var ia = order.indexOf(a), ib = order.indexOf(b);
      return (ia < 0 ? 99 : ia) - (ib < 0 ? 99 : ib) || a.localeCompare(b);
    });
  }

  function hasData(arr) {
    if (!arr) return false;
    for (var i = 0; i < arr.length; i++) if (arr[i] != null) return true;
    return false;
  }

  var charts = [];

  function makeChart(container, def, xs, ys, labels, colorIdx) {
    var card = document.createElement('div');
    card.className = 'chart';
    var h = document.createElement('h4');
    h.textContent = def.title + ' ';
    var u = document.createElement('span');
    u.className = 'unit';
    u.textContent = '(' + UNIT_LABEL[def.unit] + ')';
    h.appendChild(u);
    card.appendChild(h);
    var plot = document.createElement('div');
    card.appendChild(plot);
    container.appendChild(card);

    var unit = UNITS[def.unit];
    var build = function () {
      var muted = cssVar('--muted'), grid = cssVar('--border'), axis = cssVar('--border');
      var series = [{ label: 'Elapsed', value: function (_, v) { return fmtElapsed(v); } }];
      ys.forEach(function (_, i) {
        var c = seriesColor(colorIdx[i]);
        series.push({
          label: labels[i], stroke: c, width: 2, spanGaps: true,
          points: { show: false },
          value: function (_, v) { return v == null ? '–' : unit.fmt(v); }
        });
      });
      var width = Math.max(280, plot.clientWidth || card.clientWidth - 24);
      var opts = {
        width: width, height: 220,
        series: series,
        scales: {
          x: { time: false },
          y: {
            range: function (_, min, max) {
              var top = def.max100 ? Math.max(100, max || 0) : (max > 0 ? max : 1);
              return uPlot.rangeNum(0, top, 0.08, true);
            }
          }
        },
        axes: [
          {
            stroke: muted, grid: { stroke: grid, width: 1 }, ticks: { stroke: axis, width: 1 },
            incrs: [5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200],
            values: function (_, vals) { return vals.map(fmtElapsed); }
          },
          {
            stroke: muted, grid: { stroke: grid, width: 1 }, ticks: { stroke: axis, width: 1 }, size: 64,
            values: function (_, vals) { return vals.map(unit.tick); }
          }
        ],
        cursor: { y: false, points: { size: 8, width: 2, stroke: cssVar('--panel-alt') } },
        legend: { live: true }
      };
      return new uPlot(opts, [xs].concat(ys), plot);
    };
    var entry = { plot: plot, build: build, u: build() };
    charts.push(entry);
    return entry;
  }

  function xsFor(test, n) {
    var step = (test.timeseries && test.timeseries.interval_s) || 1;
    var xs = new Array(n);
    for (var i = 0; i < n; i++) xs[i] = i * step;
    return xs;
  }

  function renderReport(report) {
    var tests = {};
    (report.tests || []).forEach(function (t) { tests[t.id] = t; });
    reportEl.querySelectorAll('section[data-test]').forEach(function (sec) {
      var t = tests[sec.getAttribute('data-test')];
      var box = sec.querySelector('.charts');
      box.textContent = '';
      var any = false;
      REPORT_CHARTS.forEach(function (def) {
        var lines = def.lines ? def.lines : groupNames(t, def.groups).map(function (g) { return [def.groups, g, g]; });
        var ys = [], labels = [], idx = [];
        lines.forEach(function (l, i) {
          var s = getSeries(t, l[0], l[1]);
          if (hasData(s)) { ys.push(s); labels.push(l[2]); idx.push(i); }
        });
        if (!ys.length) return;
        var n = Math.max.apply(null, ys.map(function (s) { return s.length; }));
        ys = ys.map(function (s) { return s.length < n ? s.concat(new Array(n - s.length).fill(null)) : s; });
        makeChart(box, def, xsFor(t, n), ys, labels, idx);
        any = true;
      });
      if (!any) box.innerHTML = '<p class="chart-empty">No time series in this test.</p>';
    });
  }

  // Aligns each report's series on a shared elapsed-time axis (union of all
  // sample times; gaps are null), since reports may use different intervals.
  function renderCompare(reports) {
    compareEl.querySelectorAll('section[data-test]').forEach(function (sec) {
      var id = sec.getAttribute('data-test');
      var tests = reports.map(function (r) {
        return (r.tests || []).filter(function (t) { return t.id === id; })[0] || null;
      });
      var box = sec.querySelector('.charts');
      box.textContent = '';
      var any = false;
      COMPARE_CHARTS.forEach(function (def) {
        var per = tests.map(function (t) { return t ? getSeries(t, def.key, def.group) : null; });
        var present = [];
        per.forEach(function (s, i) { if (hasData(s)) present.push(i); });
        if (!present.length) return;
        var xset = {};
        present.forEach(function (i) {
          var step = tests[i].timeseries.interval_s || 1;
          for (var k = 0; k < per[i].length; k++) xset[k * step] = true;
        });
        var xs = Object.keys(xset).map(Number).sort(function (a, b) { return a - b; });
        var pos = {};
        xs.forEach(function (x, k) { pos[x] = k; });
        var ys = present.map(function (i) {
          var step = tests[i].timeseries.interval_s || 1;
          var y = new Array(xs.length).fill(null);
          for (var k = 0; k < per[i].length; k++) y[pos[k * step]] = per[i][k];
          return y;
        });
        makeChart(box, def, xs, ys, present.map(function (i) { return 'Report ' + (i + 1); }), present);
        any = true;
      });
      if (!any) box.innerHTML = '<p class="chart-empty">No time series for this test.</p>';
    });
  }

  function fail(root, err) {
    root.querySelectorAll('.charts').forEach(function (b) {
      b.innerHTML = '';
      var p = document.createElement('p');
      p.className = 'chart-empty';
      p.textContent = 'Could not load chart data: ' + err;
      b.appendChild(p);
    });
  }

  function fetchJSON(url) {
    return fetch(url, { credentials: 'same-origin' }).then(function (r) {
      if (!r.ok) throw new Error('HTTP ' + r.status);
      return r.json();
    });
  }

  if (reportEl) {
    fetchJSON(reportEl.getAttribute('data-src')).then(renderReport).catch(function (e) { fail(reportEl, e.message); });
  }
  if (compareEl) {
    var ids = compareEl.getAttribute('data-reports').split(',');
    Promise.all(ids.map(function (id) { return fetchJSON('/reports/' + encodeURIComponent(id) + '/json'); }))
      .then(renderCompare).catch(function (e) { fail(compareEl, e.message); });
  }

  // Colors are read from CSS when a chart is built, so rebuild on a theme switch.
  document.addEventListener('themechange', function () {
    charts.forEach(function (c) { c.u.destroy(); c.u = c.build(); });
  });

  var resizeTimer;
  window.addEventListener('resize', function () {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(function () {
      charts.forEach(function (c) { c.u.setSize({ width: Math.max(280, c.plot.clientWidth), height: 220 }); });
    }, 150);
  });

})();
