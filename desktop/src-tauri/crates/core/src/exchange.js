// SPDX-License-Identifier: AGPL-3.0-only
/* global location, history, fetch */
// The code exchange of the window (specs/2026-11-12-desk-shell.md SH1.4; specs/2026-11-07-desktop-app.md DA3.2, DA3.3).
// The window runs it at the start of every page, before the page's own scripts; anywhere but the page of the exchange
// of its backend it does nothing. There it takes the code out of the address — so the page never sees it — trades it
// for the session of the local author (`auth/csrf` → `desktop/session`), asks who the author is as the first page
// will, and goes to the first page: the place of a link that waited for the window (`to`, specs/2026-11-16-desk-app-links.md
// AL1.4) — only an address of the page of the edition of exactly its form —, otherwise the home. A refused code, or a 401
// for the first question — the window lost its session — it reports by going to `/desktop/open?refused=<status>`, which
// the shell cancels and answers with a new code.
(() => {
  if (location.protocol !== 'http:' || location.hostname !== '127.0.0.1' || location.pathname !== '/desktop/open') return;
  const found = /^#c=([A-Za-z0-9_-]{16,128})$/.exec(location.hash);
  if (!found) return;
  const first = (() => {
    const given = /^\?n=[0-9]{1,3}&to=([A-Za-z0-9%-]{1,400})$/.exec(location.search);
    if (!given) return '/';
    let to = '';
    try {
      to = decodeURIComponent(given[1]);
    } catch {
      return '/';
    }
    const place = /^\/manuscript\/open\?(?:saga=[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}&chapter=[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:&p=[1-9][0-9]{0,4})?(?:&rev=[1-9][0-9]{0,15})?|refused=1)$/;
    return place.test(to) ? to : '/';
  })();
  history.replaceState(null, '', location.pathname + location.search);
  (async () => {
    let status = 0;
    try {
      const csrf = await (await fetch('/api/v1/auth/csrf', { credentials: 'same-origin' })).json();
      const response = await fetch('/api/v1/desktop/session', {
        method: 'POST',
        credentials: 'same-origin',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf.csrfToken },
        body: JSON.stringify({ code: found[1] }),
      });
      status = response.status;
      if (status >= 200 && status < 300) {
        const session = await fetch('/api/v1/auth/session', { credentials: 'same-origin' });
        if (session.status === 401) status = 401;
      }
    } catch {
      status = 0;
    }
    if (status >= 200 && status < 300) location.replace(first);
    else location.replace('/desktop/open?refused=' + status);
  })();
})();
