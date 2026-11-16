// SPDX-License-Identifier: AGPL-3.0-only
/* global location, dispatchEvent, CustomEvent, CANONKEEPER_VISIT_TO */
// The move of the window to a place a link names (specs/2026-11-16-desk-app-links.md AL1.4). The shell runs this constant
// script in the window once its page is loaded; its one argument is the address of the page of the edition the core of
// the shell built of the checked values of the link (link.rs, `visit_script`). The script does not touch the history: it
// gives the address to the page by the event `canonkeeper:link`, and the edition of the app follows it by the navigation of
// its router, as a link pressed inside the app — a page with unsaved words asks first. On any page but one of its backend,
// and for any address but one of the page of the edition, it does nothing; it answers whether the page took the address.
((to) => {
  if (location.protocol !== 'http:' || location.hostname !== '127.0.0.1') return false;
  const place = /^\/manuscript\/open\?(?:saga=[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}&chapter=[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}(?:&p=[1-9][0-9]{0,4})?(?:&rev=[1-9][0-9]{0,15})?|refused=1)$/;
  if (typeof to !== 'string' || !place.test(to)) return false;
  return !dispatchEvent(new CustomEvent('canonkeeper:link', { detail: to, cancelable: true }));
})(CANONKEEPER_VISIT_TO);
