import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Popover } from './Popover';

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
});
