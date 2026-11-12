import { fireEvent, render, screen } from '@testing-library/react';
import { useState, type ReactElement } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Pencil } from 'lucide-react';
import { Dialog } from './Dialog';
import { MoreMenu } from './MoreMenu';
import { Drawer } from './Drawer';
import { Popover } from './Popover';
import { Sheet } from './Sheet';

describe('Popover', () => {
  it('dismisses on Tab while focus is still on the opening trigger', () => {
    const onDismiss = vi.fn();

    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)} type="button">Аккаунт</button>
          <Popover label="Меню аккаунта" onDismiss={() => { onDismiss(); setOpen(false); }} open={open}>
            <button type="button">Выйти</button>
          </Popover>
        </>
      );
    }

    render(<Harness />);
    const trigger = screen.getByRole('button', { name: 'Аккаунт' });
    trigger.focus();
    fireEvent.click(trigger);
    const tab = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Tab' });
    fireEvent(trigger, tab);

    expect(tab.defaultPrevented).toBe(false);
    expect(onDismiss).toHaveBeenCalledOnce();
    expect(screen.queryByRole('dialog', { name: 'Меню аккаунта' })).toBeNull();
  });

  it('dismisses on Escape without making the page modal or locking scroll', () => {
    const onDismiss = vi.fn();

    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)} type="button">Аккаунт</button>
          <Popover label="Меню аккаунта" onDismiss={() => { onDismiss(); setOpen(false); }} open={open}>
            <button type="button">Выйти</button>
          </Popover>
        </>
      );
    }

    render(<Harness />);
    const trigger = screen.getByRole('button', { name: 'Аккаунт' });
    trigger.focus();
    fireEvent.click(trigger);

    const popover = screen.getByRole('dialog', { name: 'Меню аккаунта' });
    expect(popover.hasAttribute('aria-modal')).toBe(false);
    expect(document.body.style.overflow).toBe('');
    fireEvent.keyDown(document, { key: 'Escape' });
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(trigger);
  });

  it('dismisses on pointerdown outside without eating the opening click', async () => {
    const onDismiss = vi.fn();

    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)} type="button">Аккаунт</button>
          <Popover label="Меню аккаунта" onDismiss={() => { onDismiss(); setOpen(false); }} open={open}>
            <button type="button">Выйти</button>
          </Popover>
        </>
      );
    }

    render(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));
    expect(screen.getByRole('dialog', { name: 'Меню аккаунта' })).toBeTruthy();

    fireEvent.pointerDown(document.body);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('roves enabled menu items and lets Tab dismiss without preventing browser order', () => {
    const onDismiss = vi.fn();
    render(
      <Popover label="Меню" onDismiss={onDismiss} open role="menu">
        <button role="menuitem" type="button">Первый</button>
        <button aria-disabled="true" role="menuitem" type="button">Недоступный</button>
        <button role="menuitem" type="button">Последний</button>
      </Popover>,
    );

    const items = screen.getAllByRole('menuitem');
    expect(document.activeElement).toBe(items[0]);
    fireEvent.keyDown(items[0]!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(items[2]);
    fireEvent.keyDown(items[2]!, { key: 'Home' });
    expect(document.activeElement).toBe(items[0]);

    const tab = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Tab' });
    items[0]!.dispatchEvent(tab);
    expect(tab.defaultPrevented).toBe(false);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  // The owner 11.11: «⋯» of a chat in the list «Чаты» of a folded column did nothing — the menu stood on the page, under
  // the scrim of the drawer. A menu opened from a window, a drawer or a sheet stands in the scrim of that window.
  it.each([
    ['a drawer', (menu: ReactElement) => <Drawer canDismiss label="Чаты" onDismiss={() => undefined}>{menu}</Drawer>],
    ['a dialog', (menu: ReactElement) => <Dialog label="Окно" onDismiss={() => undefined}>{menu}</Dialog>],
    ['a sheet', (menu: ReactElement) => <Sheet canDismiss label="Лист" onDismiss={() => undefined}>{menu}</Sheet>],
  ])('stands over %s it is opened from, not under its scrim', (_name, within) => {
    const view = render(within(
      <Popover label="Действия чата" onDismiss={() => undefined} open role="menu" style={{ left: 40, top: 80 }}>
        <button role="menuitem" type="button">Переименовать</button>
      </Popover>,
    ));
    const menu = screen.getByRole('menu', { name: 'Действия чата' });
    const scrim = menu.closest('[data-overlay-scrim]');
    expect(scrim).not.toBeNull();
    // Beside the window in its scrim, not inside its panel: the place of the menu is counted from the window of the browser.
    expect(menu.parentElement).toBe(scrim);
    expect(menu.inert).toBeFalsy();
    view.unmount();
  });

  it('stands on the page when no window is open', () => {
    render(<Popover label="Меню" onDismiss={() => undefined} open role="menu"><button role="menuitem" type="button">Пункт</button></Popover>);
    expect(screen.getByRole('menu', { name: 'Меню' }).parentElement).toBe(document.body);
  });
});

// specs/2026-11-11-ux9-account.md AC3.1: every «⋯» goes by the keys as the menu of the account does.
describe('MoreMenu by the keys', () => {
  it('opens by ↓ on its first item, walks the items and gives the focus back on Escape', () => {
    const rename = vi.fn();
    render(<MoreMenu items={[{ icon: Pencil, label: 'Переименовать', run: rename }, { icon: Pencil, label: 'Обложка…', run: vi.fn() }]}
      label="Действия книги" remove={{ label: 'Удалить книгу…', run: vi.fn() }} />);
    const button = screen.getByRole('button', { name: 'Действия книги' });
    button.focus();
    fireEvent.keyDown(button, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(screen.getByRole('menuitem', { name: 'Переименовать' }));
    fireEvent.keyDown(document.activeElement!, { key: 'End' });
    expect(document.activeElement).toBe(screen.getByRole('menuitem', { name: 'Удалить книгу…' }));
    fireEvent.keyDown(document.activeElement!, { key: 'ArrowDown' });
    expect(document.activeElement).toBe(screen.getByRole('menuitem', { name: 'Переименовать' }));
    fireEvent.keyDown(document.activeElement!, { key: 'Escape' });
    expect(screen.queryByRole('menu', { name: 'Действия книги' })).toBeNull();
    expect(document.activeElement).toBe(button);
  });
});
