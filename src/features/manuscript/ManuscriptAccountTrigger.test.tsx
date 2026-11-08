import { fireEvent, render, screen } from '@testing-library/react';
import { createRef, type ReactElement } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { AccountSessionContext, type AccountSession } from '../../shared/session/account-session';
import { ReaderAppBar } from './reader/ReaderNavigation';
import { ShelfTopBar } from './shelf/ShelfNavigation';

describe('manuscript account trigger', () => {
  it.each([
    [
      'shelf',
      (onToggleAccount: () => void) => (
        <ShelfTopBar
          accountMenuOpen={false}
          onDismissAccountMenu={vi.fn()}
          onLogout={vi.fn()}
          onOpenMobileMenu={vi.fn()}
          onOpenSearch={vi.fn()}
          onToggleAccountMenu={onToggleAccount}
          searchTriggerRef={createRef<HTMLButtonElement>()}
          userName="Мира Волкова"
        />
      ),
    ],
    [
      'reader',
      (onToggleAccount: () => void) => (
        <ReaderAppBar
          accountMenuState={null}
          mobileTitle="Чтение"
          onLogout={vi.fn()}
          onOpenMenu={vi.fn()}
          onOpenSearch={vi.fn()}
          onCloseAccount={vi.fn()}
          onToggleAccount={onToggleAccount}
          searchButtonRef={createRef<HTMLButtonElement>()}
          searchPanel={null}
          userName="Мира Волкова"
        />
      ),
    ],
  ] satisfies Array<[string, (onToggleAccount: () => void) => ReactElement]>)('%s shell uses only the avatar as the account trigger', (_surface, renderBar) => {
    const onToggleAccount = vi.fn();
    render(renderBar(onToggleAccount));

    expect(screen.queryByRole('button', { name: 'Личный кабинет' })).toBeNull();
    screen.getByRole('button', { name: 'Глобальный поиск' });

    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));

    expect(onToggleAccount).toHaveBeenCalledOnce();
  });
});

describe('manuscript account menu', () => {
  const actingSession = (): AccountSession => ({
    actingAs: { name: 'Анна Соколова', email: 'anna@example.org', adminName: 'Илья Петров', expiresAt: '2026-09-04T10:05:00.000Z', expiresSoon: false },
    busy: false,
    onReturnToAdmin: vi.fn(),
  });

  it.each([
    ['shelf', (onClose: () => void) => (
      <ShelfTopBar accountMenuOpen onDismissAccountMenu={onClose} onLogout={vi.fn()} onOpenMobileMenu={vi.fn()}
        onOpenSearch={vi.fn()} onToggleAccountMenu={vi.fn()} searchTriggerRef={createRef<HTMLButtonElement>()} userName="Анна Соколова" />
    )],
    ['reader', (onClose: () => void) => (
      <ReaderAppBar accountMenuState="open" mobileTitle="Чтение" onCloseAccount={onClose} onLogout={vi.fn()} onOpenMenu={vi.fn()}
        onOpenSearch={vi.fn()} onToggleAccount={vi.fn()} searchButtonRef={createRef<HTMLButtonElement>()} searchPanel={null} userName="Анна Соколова" />
    )],
  ] satisfies Array<[string, (onClose: () => void) => ReactElement]>)('%s shows whom the admin works as and returns from the account menu', (_surface, renderBar) => {
    const onClose = vi.fn();
    const session = actingSession();
    render(<AccountSessionContext.Provider value={session}>{renderBar(onClose)}</AccountSessionContext.Provider>);

    const account = screen.getByRole('button', { name: 'Аккаунт: вы работаете как Анна Соколова' });
    expect(screen.getByText('Вы работаете как Анна Соколова')).toBeTruthy();
    expect(screen.getByText('Администратор: Илья Петров · до 13:05')).toBeTruthy();
    fireEvent.click(screen.getByRole(_surface === 'reader' ? 'menuitem' : 'button', { name: 'Вернуться к администратору' }));

    expect(onClose).toHaveBeenCalledOnce();
    expect(session.onReturnToAdmin).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(account);
  });
});

describe('account menu support item (R4.2.6)', () => {
  const bars = [
    ['shelf', () => (
      <ShelfTopBar accountMenuOpen onDismissAccountMenu={vi.fn()} onLogout={vi.fn()} onOpenMobileMenu={vi.fn()}
        onOpenSearch={vi.fn()} onToggleAccountMenu={vi.fn()} searchTriggerRef={createRef<HTMLButtonElement>()} userName="Мира Волкова" />
    )],
    ['reader', () => (
      <ReaderAppBar accountMenuState="open" mobileTitle="Чтение" onCloseAccount={vi.fn()} onLogout={vi.fn()} onOpenMenu={vi.fn()}
        onOpenSearch={vi.fn()} onToggleAccount={vi.fn()} searchButtonRef={createRef<HTMLButtonElement>()} searchPanel={null} userName="Мира Волкова" />
    )],
  ] satisfies Array<[string, () => ReactElement]>;

  // specs/2026-11-07-contacts.md C2.3: «Связаться с нами» opens the window, with the number of unread replies.
  it.each(bars)('%s offers «Связаться с нами» right before «Выйти»', (_surface, renderBar) => {
    const onOpenContact = vi.fn();
    render(<AccountSessionContext.Provider value={{ busy: false, onOpenContact, contactUnread: 1 }}>{renderBar()}</AccountSessionContext.Provider>);

    const contact = screen.getByRole(_surface === 'reader' ? 'menuitem' : 'button', { name: /^Связаться с нами/u });
    expect(contact.textContent).toContain('1');
    const logout = screen.getByRole(_surface === 'reader' ? 'menuitem' : 'button', { name: 'Выйти' });
    expect(contact.compareDocumentPosition(logout) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.click(contact);
    expect(onOpenContact).toHaveBeenCalledOnce();
    expect(screen.getByRole('button', { name: 'Аккаунт: есть ответ поддержки' })).toBeTruthy();
  });

  it.each(bars)('%s hides the letter while an admin works as another user', (_surface, renderBar) => {
    render(<AccountSessionContext.Provider value={{
      actingAs: { name: 'Анна Соколова', email: 'anna@example.org', adminName: 'Илья Петров', expiresAt: '2026-09-04T10:05:00.000Z', expiresSoon: false },
      busy: false,
      onReturnToAdmin: vi.fn(),
      onOpenContact: vi.fn(),
    }}>{renderBar()}</AccountSessionContext.Provider>);

    expect(screen.queryByText('Связаться с нами')).toBeNull();
  });
});
