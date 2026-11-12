import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { parseRoute } from '../../../shared/navigation/app-route';
import { AccountSessionContext, type AccountSession } from '../../../shared/session/account-session';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { InShell } from '../../../app/shell/AppShell.test-helpers';
import { ManuscriptShelfPage } from './ManuscriptShelfPage';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const unverified: AccountSession = {
  busy: false,
  emailVerification: { email: 'new-author@example.test', status: 'idle', canSend: true, onSend: vi.fn() },
};

function renderShelf(session: AccountSession) {
  const api = createMockApiClient();
  vi.spyOn(api, 'getAgentTimeline').mockResolvedValue({ events: [], eof: true, nextCursor: null } as never);
  const queryClient = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  const route = parseRoute(`http://localhost/manuscript/books?projectId=${projectId}&sourceView=draft`);
  // The line of the address stands under the top bar of the shell (specs/2026-11-10-ux2-shell-core.md SC1.6).
  const page = (value: AccountSession) => (
    <QueryClientProvider client={queryClient}>
      <AccountSessionContext.Provider value={value}>
        <InShell api={api} route={route}><ManuscriptShelfPage api={api} navigate={vi.fn()} route={route} /></InShell>
      </AccountSessionContext.Provider>
    </QueryClientProvider>
  );
  const view = render(page(session));
  return { rerender: (value: AccountSession) => view.rerender(page(value)) };
}

describe('ManuscriptShelfPage email verification', () => {
  const reason = 'Подтвердите почту new-author@example.test — и можно будет загружать книги, спрашивать, рисовать и писать.';

  // R1.4.4, R5.5, specs/2026-11-10-ux1-shell.md SH4: one reason under the top bar and over the drop zone, and no import
  // entry works.
  it('closes every import entry until the address is verified', async () => {
    const view = renderShelf(unverified);

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    const strip = screen.getByRole('note', { name: 'Почта не подтверждена' });
    expect(within(strip).getByText(reason)).toBeTruthy();
    expect(strip.closest('[data-top-bar-strip]')?.previousElementSibling?.tagName).toBe('HEADER');
    expect(screen.getAllByText(reason)).toHaveLength(2);
    expect(screen.getByLabelText('Файлы книг: FB2, EPUB, Word (.docx), RTF, ODT или текст (.txt, .md)')).toHaveProperty('disabled', true);
    expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBe('true');

    // R5.6: the refreshed current user without the unverified state opens the shelf again.
    view.rerender({ busy: false });
    expect(screen.queryByText(reason)).toBeNull();
    expect(screen.queryByRole('note', { name: 'Почта не подтверждена' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Добавить книги' })).toHaveProperty('disabled', false);
    expect(screen.getByRole('button', { name: 'Добавить книги' }).getAttribute('aria-disabled')).toBeNull();
  });

  // SH4.4: «Добавить книги» stays in its place, dimmed, says the reason beside it and opens no window of files.
  it('says why «Добавить книги» is closed and opens no window of files', async () => {
    const choose = vi.spyOn(HTMLInputElement.prototype, 'click');
    renderShelf(unverified);
    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    const upload = screen.getByRole('button', { name: 'Добавить книги' });
    expect(upload).toHaveProperty('disabled', false);
    expect(upload.getAttribute('aria-disabled')).toBe('true');
    expect(screen.queryByText('Сначала подтвердите почту — письмо со ссылкой уже у вас.')).toBeNull();
    fireEvent.click(upload);
    expect(screen.getByText('Сначала подтвердите почту — письмо со ссылкой уже у вас.')).toBeTruthy();
    expect(choose).not.toHaveBeenCalled();
  });
});
