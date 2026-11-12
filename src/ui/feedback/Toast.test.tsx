import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import { NoticeSlot } from './notice-slot';
import { useHostToasts } from './toast-host';
import { ToastRegion } from './Toast';
import { useToastQueue } from './useToastQueue';

afterEach(() => {
  vi.useRealTimers();
});

function Harness() {
  const toasts = useToastQueue();
  return (
    <>
      <button onClick={() => toasts.push({ message: 'Готово', tone: 'success' })} type="button">Success</button>
      <button onClick={() => toasts.push({ message: 'Внимание', tone: 'warning' })} type="button">Warning</button>
      <button onClick={() => toasts.push({ message: 'Ошибка', tone: 'error' })} type="button">Error</button>
      <ToastRegion onDismiss={toasts.dismiss} queue={toasts.queue} />
    </>
  );
}

// The owner 04.11: «Ответ готов» stood at the bottom over the field and «Отправить», and its «×» did not close it
// under an open dialog. The toasts of every page stand in the place of notices above the page, in its flow.
describe('Toast place', () => {
  function Page() {
    const [slot, setSlot] = useState<HTMLElement | null>(null);
    const toasts = useToastQueue();
    return (
      <NoticeSlot.Provider value={slot}>
        <div data-testid="notices" ref={setSlot} />
        <main><button onClick={() => toasts.push({ message: 'Ответ готов', persistent: true, tone: 'success' })} type="button">Ответ</button></main>
        <ToastRegion onDismiss={toasts.dismiss} queue={toasts.queue} />
      </NoticeSlot.Provider>
    );
  }

  it('stands in the place of notices above the page and closes by its «×»', () => {
    render(<Page />);
    fireEvent.click(screen.getByRole('button', { name: 'Ответ' }));
    const toast = screen.getByRole('status');
    expect(screen.getByTestId('notices').contains(toast)).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть уведомление' }));
    expect(screen.queryByRole('status')).toBeNull();
  });
});

describe('Toast detail', () => {
  it('says what the message is about in a line under it', () => {
    function Page() {
      const toasts = useToastQueue();
      return (
        <>
          <button onClick={() => toasts.push({ detail: 'Прочитано: 2 книги, 41 глава.', message: 'Сага готова — можно спрашивать', persistent: true, tone: 'success' })} type="button">Сага</button>
          <ToastRegion onDismiss={toasts.dismiss} queue={toasts.queue} />
        </>
      );
    }
    render(<Page />);
    fireEvent.click(screen.getByRole('button', { name: 'Сага' }));
    const toast = screen.getByRole('status');
    expect(toast.textContent).toContain('Сага готова — можно спрашивать');
    expect(screen.getByText('Прочитано: 2 книги, 41 глава.').parentElement?.textContent).toBe('Сага готова — можно спрашиватьПрочитано: 2 книги, 41 глава.');
  });
});

describe('Toast queue', () => {
  it('keeps at most three items and never steals focus when adding one', () => {
    render(<Harness />);
    const success = screen.getByRole('button', { name: 'Success' });
    success.focus();

    fireEvent.click(success);
    fireEvent.click(screen.getByRole('button', { name: 'Warning' }));
    fireEvent.click(screen.getByRole('button', { name: 'Error' }));
    fireEvent.click(success);

    expect(document.activeElement).toBe(success);
    expect(screen.getAllByRole('status')).toHaveLength(2);
    expect(screen.getAllByRole('alert')).toHaveLength(1);
  });

  it('moves focus only on F6', () => {
    render(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: 'Success' }));
    const region = screen.getByRole('region', { name: 'Уведомления' });
    expect(document.activeElement).not.toBe(region);
    fireEvent.keyDown(document, { key: 'F6' });
    expect(document.activeElement).toBe(region);
  });
});

// specs/2026-11-10-ux2-patterns-actions.md PA1.2: a line without an action 5 s, a line with «Отменить» at least 10 s and
// as long as the author is on it, an error an alert that stays until it is closed.
describe('Toast terms', () => {
  function Lines() {
    const toasts = useToastQueue();
    return (
      <>
        <button onClick={() => toasts.push({ message: 'Заметка сохранена', tone: 'success' })} type="button">Plain</button>
        <button onClick={() => {
          const id = toasts.push({
            action: <button onClick={() => { toasts.dismiss(id); toasts.push({ message: 'Вернули на карту', tone: 'success' }); }} type="button">Отменить</button>,
            message: 'Убрано с карты: Мара',
            tone: 'neutral',
          });
        }} type="button">Undo</button>
        <button onClick={() => toasts.push({ message: 'Не удалось сохранить', tone: 'error' })} type="button">Error</button>
        <ToastRegion onDismiss={toasts.dismiss} onHold={toasts.hold} onRelease={toasts.release} queue={toasts.queue} />
      </>
    );
  }

  it('keeps a plain line 5 s, a line with «Отменить» 10 s and an error until it is closed', () => {
    vi.useFakeTimers();
    render(<Lines />);
    fireEvent.click(screen.getByRole('button', { name: 'Plain' }));
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    fireEvent.click(screen.getByRole('button', { name: 'Error' }));
    expect(screen.getByRole('alert').textContent).toContain('Не удалось сохранить');

    act(() => vi.advanceTimersByTime(4_900));
    expect(screen.getByText('Заметка сохранена')).toBeTruthy();
    act(() => vi.advanceTimersByTime(200));
    expect(screen.queryByText('Заметка сохранена')).toBeNull();
    act(() => vi.advanceTimersByTime(4_800));
    expect(screen.getByText('Убрано с карты: Мара')).toBeTruthy();
    act(() => vi.advanceTimersByTime(200));
    expect(screen.queryByText('Убрано с карты: Мара')).toBeNull();
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getByRole('alert')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: 'Закрыть уведомление' }));
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('waits while the pointer or the focus is on the line', () => {
    vi.useFakeTimers();
    render(<Lines />);
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    const line = screen.getByRole('status');
    act(() => vi.advanceTimersByTime(9_000));
    fireEvent.pointerEnter(line);
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getByText('Убрано с карты: Мара')).toBeTruthy();
    fireEvent.pointerLeave(line);
    act(() => vi.advanceTimersByTime(1_900));
    expect(screen.getByText('Убрано с карты: Мара')).toBeTruthy();
    fireEvent.focus(screen.getByRole('button', { name: 'Отменить' }));
    act(() => vi.advanceTimersByTime(60_000));
    expect(screen.getByText('Убрано с карты: Мара')).toBeTruthy();
    fireEvent.blur(screen.getByRole('button', { name: 'Отменить' }));
    act(() => vi.advanceTimersByTime(2_100));
    expect(screen.queryByText('Убрано с карты: Мара')).toBeNull();
  });

  it('says the result of «Отменить» in a line of its own', () => {
    render(<Lines />);
    fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
    fireEvent.click(screen.getByRole('button', { name: 'Отменить' }));
    expect(screen.queryByText('Убрано с карты: Мара')).toBeNull();
    expect(screen.getByRole('status').textContent).toContain('Вернули на карту');
  });
});

// PA1.1: a sheet that hosts the toasts while it is open keeps them in sight; after it the line goes back above the page.
describe('Toast host', () => {
  it('stands in the open host and comes back to the place of notices after it', () => {
    function Page() {
      const [slot, setSlot] = useState<HTMLElement | null>(null);
      const [sheet, setSheet] = useState<HTMLElement | null>(null);
      const [open, setOpen] = useState(true);
      useHostToasts(open ? sheet : null);
      const toasts = useToastQueue();
      return (
        <NoticeSlot.Provider value={slot}>
          <div data-testid="notices" ref={setSlot} />
          {open ? <div data-testid="sheet" ref={setSheet} /> : null}
          <button onClick={() => toasts.push({ message: 'Это не связь: Мара — Тарен', tone: 'neutral' })} type="button">Line</button>
          <button onClick={() => setOpen(false)} type="button">Close</button>
          <ToastRegion onDismiss={toasts.dismiss} queue={toasts.queue} />
        </NoticeSlot.Provider>
      );
    }
    render(<Page />);
    fireEvent.click(screen.getByRole('button', { name: 'Line' }));
    expect(screen.getByTestId('sheet').contains(screen.getByRole('status'))).toBe(true);
    fireEvent.click(screen.getByRole('button', { name: 'Close' }));
    expect(screen.getByTestId('notices').contains(screen.getByRole('status'))).toBe(true);
  });
});
