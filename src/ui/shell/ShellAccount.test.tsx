import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AccountSessionContext, type AccountSession } from '../../shared/session/account-session';
import { SITE_THEME_STORAGE_KEY, useSiteThemeStore } from '../../shared/theme/site-theme';
import { ShellAccount } from './ShellAccount';

// The account at the end of the one top bar (specs/2026-11-10-ux2-shell.md S1.2, S4.1): the menu in five lines.
function renderAccount(session: AccountSession | null, onLogout = vi.fn()) {
  render(
    <AccountSessionContext.Provider value={session}>
      <ShellAccount onLogout={onLogout} userEmail="mira@example.com" userName="Мира Волкова" />
    </AccountSessionContext.Provider>,
  );
  return onLogout;
}

describe('the account of the top bar', () => {
  it('opens the menu by the avatar alone, a touch target, and gives the focus back on Escape', () => {
    const onLogout = renderAccount({ busy: false });
    expect(screen.queryByRole('button', { name: 'Личный кабинет' })).toBeNull();
    const account = screen.getByRole('button', { name: 'Аккаунт' });
    expect(account.dataset.size).toBe('touch');
    account.focus();
    fireEvent.click(account);
    const menu = screen.getByRole('dialog', { name: 'Меню аккаунта' });
    expect(within(menu).getByText('Мира Волкова')).toBeTruthy();
    const logout = within(menu).getByRole('button', { name: 'Выйти' });
    logout.focus();
    fireEvent.click(logout);
    expect(onLogout).toHaveBeenCalledOnce();
    fireEvent.keyDown(logout, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Меню аккаунта' })).toBeNull();
    expect(document.activeElement).toBe(account);
  });

  // specs/2026-11-11-ux9-account.md AC3.1: the menu by the keys alone — ↓ on the button opens it on its first line, ↑↓ and
  // Home/End walk the lines down to «Выйти», Esc closes it and gives the focus back, Tab goes on and closes it.
  it('is walked by the keys to «Выйти» and closed by Escape and by Tab', () => {
    renderAccount({ busy: false, onOpenContact: vi.fn(), onOpenTariff: vi.fn(), onOpenAccount: vi.fn() });
    const account = screen.getByRole('button', { name: 'Аккаунт' });
    account.focus();
    fireEvent.keyDown(account, { key: 'ArrowDown' });
    const menu = screen.getByRole('dialog', { name: 'Меню аккаунта' });
    expect(document.activeElement).toBe(within(menu).getByRole('button', { name: 'Тариф' }));
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(within(menu).getByRole('button', { name: 'Аккаунт' }));
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    expect(document.activeElement).toBe(within(menu).getByRole('button', { name: 'Выйти' }));
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(within(menu).getByRole('button', { name: 'Тариф' }));
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(within(menu).getByRole('button', { name: 'Выйти' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Home' });
    expect(document.activeElement).toBe(within(menu).getByRole('button', { name: 'Тариф' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(screen.queryByRole('dialog', { name: 'Меню аккаунта' })).toBeNull();
    expect(document.activeElement).toBe(account);

    // Enter on the button opens it too (a click of the button); Tab from a line leaves the menu closed behind it.
    fireEvent.click(account);
    expect(document.activeElement).toBe(within(screen.getByRole('dialog', { name: 'Меню аккаунта' })).getByRole('button', { name: 'Тариф' }));
    const tab = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Tab' });
    fireEvent(document.activeElement!, tab);
    expect(tab.defaultPrevented).toBe(false);
    expect(screen.queryByRole('dialog', { name: 'Меню аккаунта' })).toBeNull();
    expect(document.activeElement).toBe(account);
  });

  it('shows whom the admin works as and returns from the account menu', () => {
    const session: AccountSession = {
      actingAs: { name: 'Анна Соколова', email: 'anna@example.org', adminName: 'Илья Петров', expiresAt: '2026-09-04T10:05:00.000Z', expiresSoon: false },
      busy: false,
      onReturnToAdmin: vi.fn(),
      onOpenContact: vi.fn(),
    };
    renderAccount(session);
    const account = screen.getByRole('button', { name: 'Аккаунт: вы работаете как Анна Соколова' });
    fireEvent.click(account);
    expect(screen.getByText('Вы работаете как Анна Соколова')).toBeTruthy();
    expect(screen.getByText('Администратор: Илья Петров · до 13:05')).toBeTruthy();
    // R4.2.6: the admin does not write as the author.
    expect(screen.queryByText('Написать нам')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Вернуться к администратору' }));
    expect(session.onReturnToAdmin).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(account);
  });

  // specs/2026-11-10-ux2-tariff-account.md TA1.1, А-51: the name and under it the address; «Тариф · …», «Аккаунт»,
  // «Написать нам» with the replies, «Выйти» — and nothing else of the account in the menu.
  it('holds five lines: who, «Тариф», «Аккаунт», «Написать нам» with the replies, «Выйти»', () => {
    const onOpenContact = vi.fn();
    const onOpenTariff = vi.fn();
    const onOpenAccount = vi.fn();
    renderAccount({
      busy: false, onOpenContact, contactUnread: 1, onOpenTariff, onOpenAccount, onChangePassword: vi.fn(), onLogoutEverywhere: vi.fn(),
      plan: { badge: <a href="/tariff">Писатель</a>, ending: false, line: 'Тариф · «Писатель» до 8 декабря 2026 года' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт: есть ответ поддержки' }));
    const menu = screen.getByRole('dialog', { name: 'Меню аккаунта' });
    expect(within(menu).getByText('Мира Волкова').nextElementSibling?.textContent).toBe('mira@example.com');
    expect(within(menu).getAllByRole('button').map((item) => item.textContent)).toEqual([
      'Тариф · «Писатель» до 8 декабря 2026 года', 'Аккаунт', 'Написать нам · 1 ответ', 'Выйти',
    ]);
    for (const gone of ['Тариф и ключ', 'Тарифы', 'Сменить пароль', 'Выйти на всех устройствах', 'Разрешения на продвижение', 'Документы']) {
      expect(within(menu).queryByRole('button', { name: gone })).toBeNull();
    }
    fireEvent.click(within(menu).getByRole('button', { name: /^Написать нам/u }));
    expect(onOpenContact).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт: есть ответ поддержки' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Меню аккаунта' })).getByRole('button', { name: 'Аккаунт' }));
    expect(onOpenAccount).toHaveBeenCalledOnce();
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт: есть ответ поддержки' }));
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Меню аккаунта' })).getByRole('button', { name: /^Тариф ·/u }));
    expect(onOpenTariff).toHaveBeenCalledOnce();
  });

  // TA1.2: the line of the administrator working as the author and the line of an unverified address name the address.
  it('names the address once, in the line of the work as the author or of an unverified address', () => {
    renderAccount({
      busy: false, onReturnToAdmin: vi.fn(),
      actingAs: { name: 'Анна Соколова', email: 'mira@example.com', adminName: 'Илья Петров', expiresAt: '2026-09-04T10:05:00.000Z', expiresSoon: false },
    });
    fireEvent.click(screen.getByRole('button', { name: /^Аккаунт/u }));
    expect(within(screen.getByRole('dialog', { name: 'Меню аккаунта' })).getAllByText('mira@example.com')).toHaveLength(1);
  });

  it('says «Тариф» while the plan loads, and two replies in their word', () => {
    renderAccount({ busy: false, onOpenContact: vi.fn(), contactUnread: 2, onOpenTariff: vi.fn(), onOpenAccount: vi.fn() });
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт: есть ответ поддержки' }));
    const menu = screen.getByRole('dialog', { name: 'Меню аккаунта' });
    expect(within(menu).getByRole('button', { name: 'Тариф' })).toBeTruthy();
    expect(within(menu).getByRole('button', { name: /^Написать нам/u }).textContent).toBe('Написать нам · 2 ответа');
  });

  // specs/2026-11-11-ux9-account.md AC3.7: while a request of CanonKeeper waits, a mark on the button and its line in the menu.
  it('marks the button and the menu while a request of CanonKeeper waits', () => {
    const onOpen = vi.fn();
    renderAccount({ busy: false, onOpenTariff: vi.fn(), onOpenAccount: vi.fn(), promoRequest: { onOpen } });
    const account = screen.getByRole('button', { name: 'Аккаунт' });
    expect(account.querySelector('[data-account-unread]')).toBeTruthy();
    fireEvent.click(account);
    fireEvent.click(within(screen.getByRole('dialog', { name: 'Меню аккаунта' })).getByRole('button', { name: 'Запрос CanonKeeper ждёт ответа' }));
    expect(onOpen).toHaveBeenCalledOnce();
  });

  // specs/2026-11-12-ux11-dark-core.md TH1.2: «Тема: Светлая · Тёмная · Как в системе» before «Выйти», one choice — one
  // stop of the keys, ←→ inside it.
  it('chooses the theme of the site by «Тема» before «Выйти», walked by the keys as one stop', () => {
    localStorage.clear();
    useSiteThemeStore.setState({ choice: 'light', theme: 'light' });
    renderAccount({ busy: false, onOpenContact: vi.fn(), onOpenTariff: vi.fn(), onOpenAccount: vi.fn() });
    const account = screen.getByRole('button', { name: 'Аккаунт' });
    account.focus();
    fireEvent.click(account);
    const menu = screen.getByRole('dialog', { name: 'Меню аккаунта' });
    const theme = within(menu).getByRole('radiogroup', { name: 'Тема' });
    expect(within(theme).getAllByRole('radio').map((radio) => `${radio.textContent}:${radio.getAttribute('aria-checked')}`))
      .toEqual(['Светлая:true', 'Тёмная:false', 'Как в системе:false']);
    expect(theme.compareDocumentPosition(within(menu).getByRole('button', { name: 'Выйти' })) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowUp' });
    expect(document.activeElement).toBe(within(theme).getByRole('radio', { name: 'Светлая' }));
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowRight' });
    expect(useSiteThemeStore.getState()).toMatchObject({ choice: 'dark', theme: 'dark' });
    expect(localStorage.getItem(SITE_THEME_STORAGE_KEY)).toBe('dark');
    expect(document.activeElement).toBe(within(theme).getByRole('radio', { name: 'Тёмная' }));
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(within(menu).getByRole('button', { name: 'Выйти' }));
    fireEvent.click(within(theme).getByRole('radio', { name: 'Как в системе' }));
    expect(useSiteThemeStore.getState().choice).toBe('system');
    // The menu stays open: the author sees the site change under it.
    expect(screen.getByRole('dialog', { name: 'Меню аккаунта' })).toBeTruthy();
    useSiteThemeStore.setState({ choice: 'light', theme: 'light' });
  });

  it('names the tier in force before the account button', () => {
    renderAccount({ busy: false, plan: { badge: <a href="/tariff">Писатель</a>, ending: false, line: 'Тариф · «Писатель»' } });
    expect(screen.getByRole('link', { name: 'Писатель' }).nextElementSibling).toBe(screen.getByRole('button', { name: 'Аккаунт' }));
  });
});
