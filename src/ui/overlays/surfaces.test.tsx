import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ActionSheet } from './ActionSheet';
import { Drawer } from './Drawer';
import { Sheet } from './Sheet';

describe('overlay surfaces', () => {
  it('uses the shared dialog semantics for drawers', () => {
    render(<Drawer canDismiss label="Навигация" onDismiss={() => undefined}>Содержимое</Drawer>);
    expect(screen.getByRole('dialog', { name: 'Навигация' }).getAttribute('data-kind')).toBe('drawer');
  });

  it('dismisses a sheet on a downward swipe only when allowed', () => {
    const onDismiss = vi.fn();
    const view = render(<Sheet canDismiss label="Панель" onDismiss={onDismiss}>Содержимое</Sheet>);
    const sheet = screen.getByRole('dialog', { name: 'Панель' });
    fireEvent(sheet, new MouseEvent('pointerdown', { bubbles: true, clientY: 10 }));
    fireEvent(sheet, new MouseEvent('pointerup', { bubbles: true, clientY: 90 }));
    expect(onDismiss).toHaveBeenCalledTimes(1);

    view.rerender(<Sheet canDismiss={false} label="Панель" onDismiss={onDismiss}>Содержимое</Sheet>);
    fireEvent(sheet, new MouseEvent('pointerdown', { bubbles: true, clientY: 10 }));
    fireEvent(sheet, new MouseEvent('pointerup', { bubbles: true, clientY: 90 }));
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('keeps ActionSheet rows touch sized', () => {
    render(
      <ActionSheet canDismiss label="Действия" onDismiss={() => undefined}>
        <button type="button">Удалить</button>
      </ActionSheet>,
    );

    expect(getComputedStyle(screen.getByRole('button', { name: 'Удалить' })).minHeight).toBe('44px');
  });
});
