import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { TopBarStripProvider } from './TopBarStrip';
import { WorkspaceTopBar } from './WorkspaceTopBar';

it('uses the shared account/menu chrome without a global search on Notes', () => {
  const onOpenMobileMenu = vi.fn();
  const onToggleAccountMenu = vi.fn();
  render(<WorkspaceTopBar title="Заметки" userName="Мира" accountMenuOpen={false}
    onDismissAccountMenu={vi.fn()} onLogout={vi.fn()} onOpenMobileMenu={onOpenMobileMenu}
    onToggleAccountMenu={onToggleAccountMenu} />);
  expect(screen.queryByRole('button', { name: 'Глобальный поиск' })).toBeNull();
  expect(screen.getByText('Заметки')).toBeTruthy();
  fireEvent.click(screen.getByLabelText('Открыть меню'));
  fireEvent.click(screen.getByRole('button', { name: 'Аккаунт' }));
  expect(onOpenMobileMenu).toHaveBeenCalledOnce();
  expect(onToggleAccountMenu).toHaveBeenCalledOnce();
});

// specs/2026-11-03-voice-assistant.md V6.5: what the app keeps under the top bar stands right after it, in the flow of the page.
it('places the strip of the app right under the bar', () => {
  render(<TopBarStripProvider value={<p>Полоса</p>}>
    <WorkspaceTopBar title="Заметки" userName="Мира" accountMenuOpen={false} onDismissAccountMenu={vi.fn()} onLogout={vi.fn()} onToggleAccountMenu={vi.fn()} />
  </TopBarStripProvider>);
  const strip = screen.getByText('Полоса');
  expect(strip.previousElementSibling?.tagName).toBe('HEADER');
});
