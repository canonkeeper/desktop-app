import { fireEvent, render, screen } from '@testing-library/react';
import { useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Dialog } from './Dialog';

describe('Dialog', () => {
  // specs/2026-11-03-narrow-windows-navigation.md N7.1: the dialog of the kit draws its panel itself — a dialog whose
  // content forgot one floated over the page (the owner 03.11: «модалки с прозрачным фоном»).
  it('draws its own panel, sized by its width, and keeps it under a shape of its own', () => {
    const view = render(
      <Dialog label="Удалить рисунок?" onDismiss={vi.fn()} width={440}>
        <h2>Удалить рисунок?</h2>
      </Dialog>,
    );
    const panel = screen.getByRole('dialog', { name: 'Удалить рисунок?' });
    // The opaque card itself is checked in a real browser by e2e/synthetic/narrow-windows.spec.ts.
    expect(panel.className).toMatch(/dialogPanel/u);
    expect(panel.style.getPropertyValue('--dialog-width')).toBe('440px');
    view.rerender(
      <Dialog label="Рисунок" onDismiss={vi.fn()} panelClassName="lightbox">
        <h2>Рисунок</h2>
      </Dialog>,
    );
    expect(screen.getByRole('dialog', { name: 'Рисунок' }).className).toMatch(/dialogPanel.* lightbox$/u);
  });

  it('portals the modal surface and optionally dismisses it through the scrim', () => {
    const onDismiss = vi.fn();

    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <div data-testid="app-root">
          <button onClick={() => setOpen(true)} type="button">Открыть</button>
          {open ? (
            <Dialog dismissOnScrim label="Подтверждение" onDismiss={() => { onDismiss(); setOpen(false); }}>
              <button type="button">Подтвердить</button>
            </Dialog>
          ) : null}
        </div>
      );
    }

    render(<Harness />);
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));

    const dialog = screen.getByRole('dialog', { name: 'Подтверждение' });
    expect(screen.getByTestId('app-root').contains(dialog)).toBe(false);
    fireEvent.pointerDown(dialog.parentElement as HTMLElement);
    expect(onDismiss).toHaveBeenCalledTimes(1);
  });

  it('blocks scrim and Escape dismissal while the surface is pending', () => {
    const onDismiss = vi.fn();
    render(
      <Dialog canDismiss={false} label="Сохранение" onDismiss={onDismiss}>
        <button type="button">Ждите</button>
      </Dialog>,
    );

    const dialog = screen.getByRole('dialog', { name: 'Сохранение' });
    fireEvent.pointerDown(dialog.parentElement as HTMLElement);
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onDismiss).not.toHaveBeenCalled();
  });
});
