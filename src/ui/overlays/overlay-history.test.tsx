import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { afterEach, describe, expect, it } from 'vitest';
import { Dialog } from './Dialog';
import { holdOverlaySteps, stepBackPastOverlays } from './overlay-history';
import { Popover } from './Popover';

const depth = () => ((window.history.state as { ckOverlay?: number } | null)?.ckOverlay ?? 0);

/** «Назад» or «Вперёд» of the browser, done: the step of the history has come. */
function step(go: () => void) {
  return new Promise<void>((resolve) => {
    window.addEventListener('popstate', () => resolve(), { once: true });
    go();
  });
}
const back = () => step(() => window.history.back());
const forward = () => step(() => window.history.forward());
const settled = () => new Promise((resolve) => window.setTimeout(resolve, 30));

/** A page with a window, a window inside it and a window that cannot close while it works. */
function Page() {
  const [open, setOpen] = useState(false);
  const [inner, setInner] = useState(false);
  const [busy, setBusy] = useState(false);
  return (
    <main>
      <button onClick={() => setOpen(true)} type="button">Открыть</button>
      {open ? (
        <Dialog canDismiss={!busy} label="Окно" onDismiss={() => setOpen(false)}>
          <button onClick={() => setInner(true)} type="button">Вложенное</button>
          <button onClick={() => setBusy((value) => !value)} type="button">Занято</button>
          <button onClick={() => setOpen(false)} type="button">Закрыть</button>
          {inner ? (
            <Dialog label="Вложенное окно" onDismiss={() => setInner(false)}>
              <button onClick={() => setInner(false)} type="button">Закрыть вложенное</button>
            </Dialog>
          ) : null}
        </Dialog>
      ) : null}
    </main>
  );
}

async function open() {
  fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
  await waitFor(() => expect(depth()).toBe(1));
}

afterEach(async () => {
  // The steps the windows hold go with them: the next test starts on a page without them.
  await waitFor(() => expect(depth()).toBe(0));
  await settled();
});

// specs/2026-11-10-ux2-patterns-actions.md UC4: «Назад» of the browser closes what is open over the page first.
describe('«Назад» and the windows over the page', () => {
  it('holds one step of the same page while a window is open and gives it back when its button closes it', async () => {
    const view = render(<Page />);
    const address = window.location.href;
    await open();
    expect(window.location.href).toBe(address);
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть' }));
    await waitFor(() => expect(depth()).toBe(0));
    const length = window.history.length;
    // Opened and closed again and again, the history does not grow.
    for (let time = 0; time < 3; time += 1) {
      await open();
      fireEvent.click(screen.getByRole('button', { name: 'Закрыть' }));
      await waitFor(() => expect(depth()).toBe(0));
    }
    expect(window.history.length).toBe(length);
    view.unmount();
  });

  it('closes the window by «Назад» and keeps the page', async () => {
    const view = render(<Page />);
    const address = window.location.href;
    await open();
    await back();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Окно' })).toBeNull());
    expect(window.location.href).toBe(address);
    expect(screen.getByRole('button', { name: 'Открыть' })).toBeTruthy();
    view.unmount();
  });

  it('closes the top window first: the inner one, then the outer one', async () => {
    const view = render(<Page />);
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Вложенное' }));
    await waitFor(() => expect(depth()).toBe(2));
    await back();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Вложенное окно' })).toBeNull());
    expect(screen.getByRole('dialog', { name: 'Окно' })).toBeTruthy();
    await waitFor(() => expect(depth()).toBe(1));
    await settled();
    await back();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Окно' })).toBeNull());
    view.unmount();
  });

  it('keeps a window that cannot close now, and takes its step again', async () => {
    const view = render(<Page />);
    await open();
    fireEvent.click(screen.getByRole('button', { name: 'Занято' }));
    await back();
    // The window stays and takes its step again.
    await waitFor(() => expect(depth()).toBe(1));
    await settled();
    expect(screen.getByRole('dialog', { name: 'Окно' })).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Занято' }));
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть' }));
    view.unmount();
  });

  it('opens a page that comes with a step of a window without the window', async () => {
    window.history.pushState({ ckOverlay: 1 }, '', window.location.href);
    const view = render(<Page />);
    expect(screen.queryByRole('dialog')).toBeNull();
    view.unmount();
    await back();
  });

  it('keeps «Вперёд» and «Назад» on their way after a step to another page from a window', async () => {
    function Leaving() {
      const [open, setOpen] = useState(true);
      return open ? (
        <Dialog label="Поиск" onDismiss={() => setOpen(false)}>
          <button onClick={() => { window.history.pushState({}, '', '/chapter'); setOpen(false); }} type="button">К главе</button>
        </Dialog>
      ) : <p>Глава</p>;
    }
    const start = window.location.pathname;
    const view = render(<Leaving />);
    await waitFor(() => expect(depth()).toBe(1));
    fireEvent.click(screen.getByRole('button', { name: 'К главе' }));
    await screen.findByText('Глава');
    await settled();
    expect(window.location.pathname).toBe('/chapter');
    // «Назад» comes to the page of the window without the window, and «Вперёд» goes to the chapter again.
    await back();
    await settled();
    expect(window.location.pathname).toBe(start);
    await forward();
    await settled();
    expect(window.location.pathname).toBe('/chapter');
    view.unmount();
    await back();
    expect(window.location.pathname).toBe(start);
    await back();
  });

  it('keeps the page a press of a window leads to while the step back of another window is on its way', async () => {
    function Filter() {
      const [open, setOpen] = useState(true);
      const [inner, setInner] = useState(true);
      return open ? (
        <Dialog label="Источник" onDismiss={() => setOpen(false)}>
          {inner ? <Dialog label="Сага" onDismiss={() => setInner(false)}><button onClick={() => setInner(false)} type="button">Выбрать</button></Dialog> : null}
          <button onClick={() => { window.history.pushState({}, '', '/notes?bookId=book-02'); setOpen(false); }} type="button">Применить</button>
        </Dialog>
      ) : <p>Заметки книги</p>;
    }
    const start = window.location.pathname;
    const view = render(<Filter />);
    await waitFor(() => expect(depth()).toBe(2));
    // The inner window closes and its step back goes; the press of the outer one comes before the browser takes it.
    fireEvent.click(screen.getByRole('button', { name: 'Выбрать' }));
    await Promise.resolve();
    await Promise.resolve();
    fireEvent.click(screen.getByRole('button', { name: 'Применить' }));
    await screen.findByText('Заметки книги');
    await waitFor(() => expect(window.location.search).toBe('?bookId=book-02'));
    await settled();
    await settled();
    expect(window.location.search).toBe('?bookId=book-02');
    view.unmount();
    window.history.pushState({}, '', start);
  });

  it('waits with the step back of a closed window while a navigation of the app is on its way', async () => {
    const view = render(<Page />);
    await open();
    holdOverlaySteps('/chapter?id=12');
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть' }));
    await settled();
    // The router has not written its page yet: a step back now would take it away.
    expect(depth()).toBe(1);
    window.history.pushState({}, '', '/chapter?id=12');
    await settled();
    // The page is written: the step of the closed window stays behind it, and «Вперёд» and «Назад» keep their way.
    expect(window.location.search).toBe('?id=12');
    await back();
    // «Назад» comes to the page the window stood over — without the window — and one more «Назад» goes on.
    expect(depth()).toBe(1);
    expect(screen.queryByRole('dialog')).toBeNull();
    await back();
    expect(depth()).toBe(0);
    view.unmount();
  });

  it('gives a popover no step of the history', async () => {
    const view = render(<Popover label="Меню" onDismiss={() => undefined} open role="menu"><button role="menuitem" type="button">Пункт</button></Popover>);
    await new Promise((resolve) => window.setTimeout(resolve, 20));
    expect(depth()).toBe(0);
    view.unmount();
  });
});

// The walk of TEST 11.11: a drawing opened from the list and deleted by the question of a window left the author on
// «Этого рисунка нет» — the step back of the app took the entry of the window, not of the page.
describe('a step back of the app from under an open window', () => {
  const at = () => `${window.location.pathname}${window.location.search}`;

  it('leads to the page before, whether the window closes before the step lands or after', async () => {
    for (const closes of ['after', 'before'] as const) {
      window.history.pushState({}, '', '/list');
      window.history.pushState({}, '', '/list?item=1');
      const view = render(<Page />);
      await open();
      const landed = step(() => stepBackPastOverlays((delta) => window.history.go(delta)));
      // The page of the window is gone with the press — its query was removed — or goes once the step has come.
      if (closes === 'before') view.unmount();
      await landed;
      if (closes === 'after') view.unmount();
      await settled();
      expect(at(), `the window closes ${closes} the step`).toBe('/list');
      expect(depth()).toBe(0);
    }
  });

  it('is a plain step back without a window', async () => {
    window.history.pushState({}, '', '/list');
    window.history.pushState({}, '', '/list?item=1');
    await step(() => stepBackPastOverlays((delta) => window.history.go(delta)));
    expect(at()).toBe('/list');
  });
});
