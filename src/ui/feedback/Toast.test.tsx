import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useState } from 'react';
import { NoticeSlot } from './notice-slot';
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

  it('moves focus only on F6 and uses the canonical tone timers', () => {
    vi.useFakeTimers();
    render(<Harness />);

    fireEvent.click(screen.getByRole('button', { name: 'Success' }));
    fireEvent.click(screen.getByRole('button', { name: 'Warning' }));
    fireEvent.click(screen.getByRole('button', { name: 'Error' }));
    const region = screen.getByRole('region', { name: 'Уведомления' });
    expect(document.activeElement).not.toBe(region);

    fireEvent.keyDown(document, { key: 'F6' });
    expect(document.activeElement).toBe(region);

    act(() => vi.advanceTimersByTime(6_000));
    expect(screen.queryByText('Готово')).toBeNull();
    expect(screen.getByText('Внимание')).toBeTruthy();
    expect(screen.getByText('Ошибка')).toBeTruthy();

    act(() => vi.advanceTimersByTime(4_000));
    expect(screen.queryByText('Внимание')).toBeNull();
    expect(screen.getByText('Ошибка')).toBeTruthy();
  });
});
