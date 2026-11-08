import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../api/mock';
import { projectId } from '../../../api/mock/repository/fixtures';
import { parseRoute } from '../../../shared/navigation/app-route';
import { AccountSessionContext, type AccountSession } from '../../../shared/session/account-session';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
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
  const page = (value: AccountSession) => (
    <QueryClientProvider client={queryClient}>
      <AccountSessionContext.Provider value={value}>
        <ManuscriptShelfPage
          api={api}
          navigate={vi.fn()}
          onLogout={vi.fn()}
          route={parseRoute(`http://localhost/manuscript/books?projectId=${projectId}&sourceView=draft`)}
          userName="Новый автор"
        />
      </AccountSessionContext.Provider>
    </QueryClientProvider>
  );
  const view = render(page(session));
  return { rerender: (value: AccountSession) => view.rerender(page(value)) };
}

describe('ManuscriptShelfPage email verification', () => {
  // R1.4.4, R5.5: an unverified writer sees the notice over the drop zone and no import entry works.
  it('closes every import entry until the address is verified', async () => {
    const view = renderShelf(unverified);

    await screen.findByRole('article', { name: 'Книга III. Северный суд' });
    expect(screen.getByText('Подтвердите адрес new-author@example.test')).toBeTruthy();
    expect(screen.getByText('Мы отправили на него письмо со ссылкой. Пока адрес не подтверждён, книги загрузить нельзя.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Загрузить файл' })).toHaveProperty('disabled', true);
    expect(screen.getByLabelText('Файлы книг: FB2, EPUB или DOCX')).toHaveProperty('disabled', true);
    expect(screen.getByText('Перетащите FB2, EPUB или DOCX').closest('button')).toHaveProperty('disabled', true);

    // R5.6: the refreshed current user without the unverified state opens the shelf again.
    view.rerender({ busy: false });
    expect(screen.queryByText('Подтвердите адрес new-author@example.test')).toBeNull();
    expect(screen.getByRole('button', { name: 'Загрузить файл' })).toHaveProperty('disabled', false);
  });
});
