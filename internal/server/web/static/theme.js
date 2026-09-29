// Light/dark theme, same behavior as SwarmDialer: applied before the page
// renders (no flash of the wrong theme), remembered per browser, dark by
// default. Loaded without defer from <head>.
(function () {
  var KEY = 'dtcollector-theme';
  var theme = 'dark';
  try { if (localStorage.getItem(KEY) === 'light') theme = 'light'; } catch (e) { /* storage blocked: default */ }
  document.documentElement.dataset.theme = theme;

  // Tab icons: white on dark, black on light (data-light holds the black one).
  function syncIcons() {
    var light = document.documentElement.dataset.theme === 'light';
    document.querySelectorAll('link[rel=icon][data-light]').forEach(function (l) {
      if (!l.dataset.dark) l.dataset.dark = l.getAttribute('href');
      l.setAttribute('href', light ? l.dataset.light : l.dataset.dark);
    });
  }

  function syncButton() {
    var btn = document.getElementById('theme-toggle');
    if (!btn) return;
    var light = document.documentElement.dataset.theme === 'light';
    btn.textContent = light ? '☾' : '☀'; // moon in light mode, sun in dark mode
    btn.title = light ? 'Switch to dark mode' : 'Switch to light mode';
    btn.setAttribute('aria-label', btn.title);
  }

  function toggle() {
    var next = document.documentElement.dataset.theme === 'light' ? 'dark' : 'light';
    document.documentElement.dataset.theme = next;
    try { localStorage.setItem(KEY, next); } catch (e) { /* not remembered, still switches */ }
    syncButton();
    syncIcons();
    document.dispatchEvent(new CustomEvent('themechange', { detail: next }));
  }

  syncIcons();
  document.addEventListener('DOMContentLoaded', function () {
    syncIcons();
    syncButton();
    var btn = document.getElementById('theme-toggle');
    if (btn) btn.addEventListener('click', toggle);
  });
})();
