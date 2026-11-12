import { fireEvent, render, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AccountSessionContext } from '../../shared/session/account-session';
import { SupportReference, WriteToUs } from './SupportReference';

afterEach(() => window.history.replaceState(null, '', '/'));

function withWindow(children: ReactNode) {
  const onOpenContact = vi.fn();
  render(<AccountSessionContext.Provider value={{ busy: false, onOpenContact }}>{children}</AccountSessionContext.Provider>);
  return onOpenContact;
}

// specs/2026-11-10-ux2-contact.md CT1.2: where the page has the window, «Написать нам» opens it; elsewhere — a letter.
describe('SupportReference', () => {
  it('opens «Написать нам» with the topic «Ошибка» and the code, the code staying as text to select', () => {
    window.history.replaceState(null, '', '/chat?projectId=p&chatId=c');
    const open = withWindow(<SupportReference code=" req-1 " />);

    expect(screen.getByText('req-1').closest('span')?.textContent).toBe('Код для поддержки: req-1');
    expect(screen.queryByRole('link')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Написать нам' }));
    expect(open).toHaveBeenCalledWith({ topic: 'bug', code: 'req-1' });
  });

  it('opens the window as an error without a code too', () => {
    const open = withWindow(<SupportReference />);

    expect(screen.queryByText(/Код для поддержки/u)).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Написать нам' }));
    expect(open).toHaveBeenCalledWith({ topic: 'bug' });
  });

  it('writes a letter with the code and the page path where there is no window', () => {
    window.history.replaceState(null, '', '/chat?projectId=p&chatId=c');
    render(<SupportReference code="req-1" />);

    expect(screen.queryByRole('button')).toBeNull();
    const link = screen.getByRole('link', { name: 'Написать нам' });
    const href = decodeURIComponent(link.getAttribute('href') ?? '');
    expect(href).toMatch(/^mailto:support@canonkeeper\.ru\?/u);
    expect(href).toContain('CanonKeeper: ошибка req-1');
    expect(href).toContain('Страница: /chat');
    expect(href).not.toContain('projectId');
  });

  it('offers only the letter when there is no code and no window', () => {
    render(<SupportReference />);

    expect(screen.queryByText(/Код для поддержки/u)).toBeNull();
    expect(decodeURIComponent(screen.getByRole('link', { name: 'Написать нам' }).getAttribute('href') ?? ''))
      .toContain('CanonKeeper: вопрос');
  });
});

describe('WriteToUs', () => {
  it('opens the window with what its place prepared: a refused key on «Тариф и ключ» with the reason', () => {
    const open = withWindow(<WriteToUs code="req-2" draft="Сайт ответил: такого ключа нет." topic="tariff" />);

    fireEvent.click(screen.getByRole('button', { name: 'Написать нам' }));
    expect(open).toHaveBeenCalledWith({ topic: 'tariff', code: 'req-2', draft: 'Сайт ответил: такого ключа нет.' });
  });

  it('gives a toast action the same letter where there is no window', () => {
    render(<WriteToUs code="req-2" />);

    expect(decodeURIComponent(screen.getByRole('link').getAttribute('href') ?? '')).toContain('Код для поддержки: req-2');
  });
});
