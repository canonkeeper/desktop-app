// The theme of the site before the first paint (specs/2026-11-12-ux11-dark-core.md TH1.1): a page opened in the dark theme
// does not flash light. The same key and rule as src/shared/theme/site-theme.ts; the app keeps <html> in the theme after.
// A file of its own, not a script in the page: the policy of the site runs no script written into the page.
/* global window, document */
(function () {
  // The theme of the edition until the author chooses one: the build of the app writes it on <html>; the site is light
  // (specs/2026-11-13-desk-settings.md ST3.1).
  var edition = document.documentElement.getAttribute('data-theme-default');
  var choice = edition === 'dark' || edition === 'system' ? edition : 'light';
  try {
    var stored = window.localStorage.getItem('canonkeeper.site.theme.v1');
    if (stored === 'light' || stored === 'dark' || stored === 'system') choice = stored;
  } catch {
    // A closed storage leaves the theme of the edition.
  }
  var dark = choice === 'dark'
    || (choice === 'system' && typeof window.matchMedia === 'function' && window.matchMedia('(prefers-color-scheme: dark)').matches);
  document.documentElement.setAttribute('data-theme', dark ? 'dark' : 'light');
})();
