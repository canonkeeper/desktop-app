// SPDX-License-Identifier: AGPL-3.0-only
/* global window, document, setInterval */
// The page the shell carries (specs/2026-11-12-desk-shell.md SH1.5, SH2.1; specs/2026-11-13-desk-upgrade.md UG3;
// specs/2026-11-13-desk-updates.md UB0). It asks the shell what to show — `launch_state` — and asks again every second:
// the start, a long step of it (its words instead of «CanonKeeper запускается…», under the same sign of work, without the
// hint of the first start), or a failure with its words and buttons — the way out in a row under the words, «Закрыть»
// and the actions of the reason at the bottom, the main one last (the board 44·40). It takes nothing from its address,
// and its commands take nothing from it: each button names an act of the shell, which does it only while it shows the
// failure with that button.
const invoke = (command) => window.__TAURI__.core.invoke(command);
const element = (id) => document.getElementById(id);

// The command of each act of a failure.
const acts = {
  folder: 'reveal_data_dir',
  contact: 'write_to_us',
  download: 'download_version',
  copy: 'open_copy',
  close: 'close_window',
};

// The icons of the buttons, as the board draws them: the strokes of each.
const icons = {
  folder: ['m6 14 1.5-2.9A2 2 0 0 1 9.24 10H20a2 2 0 0 1 1.94 2.5l-1.54 6a2 2 0 0 1-1.95 1.5H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h3.9a2 2 0 0 1 1.69.9l.81 1.2a2 2 0 0 0 1.67.9H18a2 2 0 0 1 2 2v2'],
  contact: ['M4 4h16a2 2 0 0 1 2 2v12a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V6a2 2 0 0 1 2-2z', 'm22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7'],
  download: ['M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4', 'm7 10 5 5 5-5', 'M12 15V3'],
  restore: ['M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8', 'M3 3v5h5', 'M12 7v5l4 2'],
  copy: ['M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z'],
};

let shown = '';
let asking = false;
let startedAt = Date.now();
// The hint of the first start: only while the start says nothing more.
let hinting = true;

function show(view) {
  for (const id of ['starting', 'failure', 'question']) element(id).hidden = id !== view;
}

function icon(id) {
  const strokes = icons[id];
  if (!strokes) return null;
  const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  for (const stroke of strokes) {
    const path = document.createElementNS('http://www.w3.org/2000/svg', 'path');
    path.setAttribute('d', stroke);
    svg.append(path);
  }
  return svg;
}

function button(spec) {
  const node = document.createElement('button');
  node.type = 'button';
  if (spec.place === 'wayOut') node.className = 'small';
  else if (!spec.main) node.className = 'quiet';
  const mark = icon(spec.id);
  if (mark) node.append(mark);
  node.append(spec.label);
  node.addEventListener('click', () => act(spec.id));
  return node;
}

function render(state) {
  const seen = JSON.stringify(state);
  if (seen === shown || asking) return;
  shown = seen;
  if (state.view === 'failure') {
    element('words').textContent = state.words;
    element('way-out').replaceChildren(...state.buttons.filter((spec) => spec.place === 'wayOut').map(button));
    element('way-out').hidden = !state.buttons.some((spec) => spec.place === 'wayOut');
    const actions = element('actions');
    actions.replaceChildren(...state.buttons.filter((spec) => spec.place === 'footer').map(button));
    show('failure');
    (actions.lastElementChild ?? element('dismiss')).focus();
    return;
  }
  hinting = state.view === 'starting';
  element('title').textContent = state.view === 'step' ? state.words : 'CanonKeeper запускается…';
  if (hinting) startedAt = Date.now();
  else element('hint').hidden = true;
  show('starting');
}

async function act(id) {
  if (id === 'restore') {
    ask(await invoke('restore_question'));
    return;
  }
  await invoke(acts[id]);
}

// «Вернуть данные из копии…»: the question of the shell, then the return or back to the failure.
function ask(question) {
  asking = true;
  element('question-title').textContent = question.title;
  element('question-text').textContent = question.text;
  const earlier = element('earlier');
  earlier.replaceChildren(
    ...question.earlier.map((line) => {
      const item = document.createElement('li');
      item.textContent = line;
      return item;
    }),
  );
  earlier.hidden = question.earlier.length === 0;
  const confirm = element('confirm');
  confirm.replaceChildren(icon('restore'), question.confirm);
  element('cancel').textContent = question.cancel;
  show('question');
  element('cancel').focus();
}

function cancel() {
  asking = false;
  shown = '';
  refresh();
}

element('confirm').addEventListener('click', async () => {
  asking = false;
  shown = '';
  await invoke('restore_copy');
  refresh();
});
element('cancel').addEventListener('click', cancel);
element('dismiss-question').addEventListener('click', cancel);
// × of the window of a failure is «Закрыть».
element('dismiss').addEventListener('click', () => act('close'));

async function refresh() {
  try {
    render(await invoke('launch_state'));
  } catch {
    // The shell answers again at the next tick.
  }
}

refresh();
setInterval(refresh, 1000);
setInterval(() => {
  // The hint of the first start, after a while of «CanonKeeper запускается…» without a step.
  element('hint').hidden = !(hinting && !element('starting').hidden && Date.now() - startedAt >= 6000);
}, 500);
