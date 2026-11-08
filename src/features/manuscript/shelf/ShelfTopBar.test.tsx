import { fireEvent, render, screen, within } from '@testing-library/react';
import { createRef, type ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ShelfTopBar, ShelfWorkspaceNavigation } from './ShelfNavigation';
import { NotesContext } from '../../../shared/notes/notes-context';

function props(): ComponentProps<typeof ShelfTopBar> {
  return {
    accountMenuOpen: false,
    onDismissAccountMenu: vi.fn(),
    onLogout: vi.fn(),
    onOpenMobileMenu: vi.fn(),
    onOpenSearch: vi.fn(),
    onToggleAccountMenu: vi.fn(),
    searchTriggerRef: createRef<HTMLButtonElement>(),
    userName: 'Мира Волкова',
  };
}

describe('ShelfTopBar', () => {
  it('keeps Notes and account in one shared grid action slot', () => {
    const open = vi.fn();
    render(<NotesContext.Provider value={{ open, openWithNavigation: vi.fn(), capture: vi.fn() }}><ShelfTopBar {...props()} /></NotesContext.Provider>);
    const notes = screen.getByRole('button', { name: 'Заметки' });
    const account = screen.getByRole('button', { name: 'Аккаунт' });
    expect(notes.parentElement).toBe(account.parentElement);
    expect(getComputedStyle(notes.parentElement!).display).toBe('flex');
    fireEvent.click(notes);
    expect(open).toHaveBeenCalledOnce();
  });
  it('uses touch-sized UIKit actions and one search trigger without owning the query', () => {
    const input = props();
    render(<ShelfTopBar {...input} />);
    const menu = screen.getByLabelText('Открыть меню');
    const account = screen.getByRole('button', { name: 'Аккаунт' });
    const search = screen.getByRole('button', { name: 'Глобальный поиск' });

    expect(menu.dataset.size).toBe('touch');
    expect(account.dataset.size).toBe('touch');
    expect(search.dataset.variant).toBe('outline');
    expect(screen.queryByRole('textbox')).toBeNull();
    expect(input.searchTriggerRef.current).toBe(search);
    fireEvent.click(menu);
    fireEvent.click(account);
    fireEvent.click(search);
    expect(input.onOpenMobileMenu).toHaveBeenCalledOnce();
    expect(input.onToggleAccountMenu).toHaveBeenCalledOnce();
    expect(input.onOpenSearch).toHaveBeenCalledOnce();
  });

  it('keeps the exact search DOM node available for the existing return-focus owner', () => {
    const input = props();
    const view = render(<ShelfTopBar {...input} />);
    const search = screen.getByRole('button', { name: 'Глобальный поиск' });

    view.rerender(<ShelfTopBar {...input} userName="Очень длинное имя пользователя" />);
    expect(input.searchTriggerRef.current).toBe(search);
    input.searchTriggerRef.current?.focus();
    expect(document.activeElement).toBe(search);
  });

  it('preserves account dismissal focus and the existing logout callback', () => {
    const input = props();
    const view = render(<ShelfTopBar {...input} />);
    const account = screen.getByRole('button', { name: 'Аккаунт' });
    account.focus();
    fireEvent.click(account);
    view.rerender(<ShelfTopBar {...input} accountMenuOpen />);
    const logout = screen.getByRole('button', { name: 'Выйти' });
    logout.focus();
    fireEvent.click(logout);
    expect(input.onLogout).toHaveBeenCalledOnce();

    fireEvent.keyDown(logout, { key: 'Escape' });
    expect(input.onDismissAccountMenu).toHaveBeenCalledOnce();
    expect(document.activeElement).toBe(account);
  });
});

// specs/2026-11-03-narrow-windows-navigation.md N2.1: the tablet column names its sections.
it('names the sections in the narrow tablet column and preserves 44px coarse targets', () => {
  render(<ShelfWorkspaceNavigation
    onOpenChat={vi.fn()}
    onOpenManuscript={vi.fn()}
    onOpenProjectMenu={vi.fn()}
    projectMeta="4 книги"
    projectTitle="Пожар в Белом порту"
  />);
  const rail = screen.getByLabelText('Планшетная навигация');
  const buttons = within(rail).getAllByRole('button', { hidden: true });
  expect(buttons.map((button) => button.textContent)).toEqual(['Мои книги', 'Чат']);
  expect(getComputedStyle(rail).padding).toBe('12px 0px');
  expect(getComputedStyle(rail).gap).toBe('2px');
  const coarse = [...document.styleSheets].flatMap((sheet) => [...sheet.cssRules])
    .filter((rule): rule is CSSMediaRule => rule instanceof CSSMediaRule && rule.conditionText === '(pointer: coarse)')
    .flatMap((rule) => [...rule.cssRules])
    .filter((rule): rule is CSSStyleRule => rule instanceof CSSStyleRule
      && rule.style.getPropertyValue('min-width') === '44px'
      && rule.style.getPropertyValue('min-height') === '44px');
  for (const button of buttons) {
    expect(getComputedStyle(button).width).toBe('72px');
    expect(getComputedStyle(button).minHeight).toBe('48px');
    expect(coarse.some((rule) => button.matches(rule.selectorText))).toBe(true);
  }
});
