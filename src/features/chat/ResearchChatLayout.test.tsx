import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ResearchChatLayout } from './ResearchChatLayout';

afterEach(() => vi.unstubAllGlobals());

it.each([[1365, 640], [1920, 640], [834, 1112]])('makes history optional at %s×%s', (width, height) => {
  vi.stubGlobal('innerWidth', width);
  vi.stubGlobal('innerHeight', height);
  render(<ResearchChatLayout sourceOpen={false} sessions={(onNavigate) => <button onClick={onNavigate}>Сохранённый чат</button>}><p>Ответ агента</p></ResearchChatLayout>);
  expect(screen.getByText('Ответ агента')).not.toBeNull();
  fireEvent.click(screen.getByRole('button', { name: 'Чаты' }));
  expect(screen.getByRole('dialog', { name: 'Чаты' }).contains(screen.getAllByRole('button', { name: 'Сохранённый чат' }).at(-1)!)).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Сохранённый чат' }));
  expect(screen.queryByRole('dialog', { name: 'Чаты' })).toBeNull();
});
