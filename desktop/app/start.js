// SPDX-License-Identifier: AGPL-3.0-only
/* global window, document, location, URLSearchParams, setTimeout */
// The page the shell carries (specs/2026-11-12-desk-shell.md SH1.5, SH2.1). While the backend starts it says so, and
// after a few seconds that the first start takes longer. With `failure` in its address it shows the words the shell
// chose and three buttons: the data folder, the contact page of the site in the browser of the system, close.
const query = new URLSearchParams(location.search);
const failure = query.get('failure');
const invoke = (command, args) => window.__TAURI__.core.invoke(command, args);

if (failure) {
  document.getElementById('starting').hidden = true;
  document.getElementById('words').textContent = failure;
  document.getElementById('failure').hidden = false;
  const folder = document.getElementById('folder');
  folder.addEventListener('click', () => invoke('reveal_data_dir'));
  document.getElementById('contact').addEventListener('click', () => invoke('open_external', { url: query.get('contact') }));
  document.getElementById('close').addEventListener('click', () => invoke('close_window'));
  folder.focus();
} else {
  setTimeout(() => {
    document.getElementById('hint').hidden = false;
  }, 6000);
}
