import { useEffect, useRef } from 'react';

/**
 * «Назад» of the browser closes what is open over the page first (specs/2026-11-10-ux2-patterns-actions.md UC4): each open
 * window, sheet or drawer holds an entry of the history of the same page, marked with its depth. «Назад» takes the top
 * entry away and the top window closes as by its «×»; a window closed by its button takes its entry away, so opening
 * and closing leave the history as it was. The entries are the browser's own, past the router: the address does not
 * change and the page is not read again. What lives in the address (a tab, `source=`, a picture of the gallery) is
 * closed by the «Назад» of its address and takes no entry here (`history={false}`).
 */
const mark = 'ckOverlay';

interface Entry {
  back: () => void;
}

const stack: Entry[] = [];
/** Steps back the overlays started themselves and wait for. */
let pending = 0;
let pendingTimer: number | undefined;
let scheduled = false;
/** Navigations of the app on their way into the history: no step back is taken across them. */
let holds = 0;
let listening = false;

function depth() {
  const value = (window.history.state as Record<string, unknown> | null)?.[mark];
  return typeof value === 'number' ? value : 0;
}

/**
 * The entries follow the open overlays once the commit of the page is over: a window that closes as another opens keeps
 * its entry. Two turns of the microtasks let a navigation of the same press — the router writes it in a microtask, also
 * from an effect of the same commit — go into the history first, so a step to another page is not undone; and the step
 * back of a closed window is taken before anything else can start a new page.
 */
function schedule() {
  if (scheduled) return;
  scheduled = true;
  queueMicrotask(() => queueMicrotask(reconcile));
}

function reconcile() {
  scheduled = false;
  if (pending > 0 || holds > 0) return;
  const current = depth();
  const wanted = stack.length;
  if (wanted > current) {
    for (let level = current + 1; level <= wanted; level += 1) {
      // The prototype's push: the router does not see a step of the same page and reads nothing again.
      History.prototype.pushState.call(window.history, { ...(window.history.state as object | null), [mark]: level }, '', window.location.href);
    }
  } else if (wanted < current) {
    pending += 1;
    // A step the browser does not take must not hold the next ones.
    window.clearTimeout(pendingTimer);
    pendingTimer = window.setTimeout(() => {
      pending = 0;
      schedule();
    }, 1000);
    window.history.go(wanted - current);
  }
}

function popped() {
  if (pending > 0) {
    pending -= 1;
    if (!pending) window.clearTimeout(pendingTimer);
    schedule();
    return;
  }
  // «Назад» over the open overlays: the top ones close as by their «×». One that stays — it asks about unsaved words, or
  // it is busy — takes its entry again. A step of a window that is gone — left by a way to another page from the window,
  // or reached by «Вперёд» — shows the page as it is and is not stepped over: «Вперёд» and «Назад» keep their way.
  const over = stack.length - depth();
  if (over <= 0) return;
  stack.slice(-over).reverse().forEach((entry) => entry.back());
  schedule();
}

function listen() {
  if (listening) return;
  listening = true;
  window.addEventListener('popstate', popped);
}

/**
 * A navigation of the app to `href` is on its way into the history — the router writes it after its guards of unsaved
 * words, some turns later: the overlays wait with their steps back, which would take that page away, until the address is
 * there. A navigation a guard stopped lets them go after a second.
 */
export function holdOverlaySteps(href: string) {
  holds += 1;
  const started = Date.now();
  const check = () => {
    if (`${window.location.pathname}${window.location.search}` !== href && Date.now() - started < 1000) {
      window.setTimeout(check, 16);
      return;
    }
    holds -= 1;
    if (!holds) schedule();
  };
  window.setTimeout(check, 0);
}

/**
 * A step back of the app itself — «К рисункам» after a deletion asked in a window — goes from under the open windows.
 * Their entries stand over the page in the history: a bare step back takes only the top one away and leaves the author on
 * the page he left. The step takes the entries with it — as one step of the history — and the windows, which close with
 * their page before it lands or after, take no step of their own meanwhile.
 */
export function stepBackPastOverlays(go: (delta: number) => void) {
  const over = depth();
  if (over > 0) {
    listen();
    pending += 1;
    window.clearTimeout(pendingTimer);
    pendingTimer = window.setTimeout(() => {
      pending = 0;
      schedule();
    }, 1000);
  }
  go(-1 - over);
}

/** Holds an entry of the history while `active`; «Назад» calls `onBack`. */
export function useOverlayHistory(active: boolean, onBack: () => void) {
  const back = useRef(onBack);
  back.current = onBack;
  useEffect(() => {
    if (!active) return undefined;
    listen();
    const entry: Entry = { back: () => back.current() };
    stack.push(entry);
    schedule();
    return () => {
      stack.splice(stack.indexOf(entry), 1);
      schedule();
    };
  }, [active]);
}
