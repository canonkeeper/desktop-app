import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { createIdempotencyKey } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ResearchChatPanel } from './ResearchChatPanel';

const examples = [
  'Что [имя героя] знает о [событии] к главе [номер]?',
  'Где впервые упоминается [предмет или место]?',
  'Сколько лет [имя героя] в главе [номер] и не противоречит ли это раннему тексту?',
  'Перескажи, что происходило с [имя героя] в книге [номер].',
  'Не спорит ли с каноном эта сцена: [вставьте сцену]',
];

beforeEach(() => {
  vi.stubGlobal('innerWidth', 1440);
  vi.stubGlobal('innerHeight', 900);
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => vi.unstubAllGlobals());

function renderPanel(api: ReturnType<typeof createMockApiClient>, selectedChatId = '', canCreate = true) {
  render(
    <QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}>
      <ResearchChatPanel api={api} canCreate={canCreate} onOpenSource={vi.fn()} onSelectChatId={vi.fn()}
        projectId={projectId} selectedChatId={selectedChatId} sourceView="published" />
    </QueryClientProvider>,
  );
}

describe('ChatExamples (UC2.3)', () => {
  it('a new chat offers five examples and what to expect; an example goes into the field, not to the model', async () => {
    const api = createMockApiClient();
    const createSession = vi.spyOn(api, 'createChatSession');
    const createTurn = vi.spyOn(api, 'createChatTurn');
    renderPanel(api);

    const empty = await screen.findByRole('region', { name: 'Спросите о своей саге' });
    expect(within(empty).getByRole('heading', { name: 'Спросите о своей саге' })).toBeTruthy();
    expect(within(empty).getByText('Задайте первый вопрос — чат появится в списке чатов.')).toBeTruthy();
    expect(within(empty).getAllByRole('button').map((button) => button.textContent)).toEqual(examples);
    expect(within(empty).getByText(/^Отвечаю только по тексту ваших книг и даю ссылки на главы и абзацы \(¶\)\. Обычный вопрос — около минуты, обзор всей саги — до 15 минут\. Можно уйти с этой страницы — ответ сохранится в этом чате\.$/u)).toBeTruthy();

    const field = screen.getByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement;
    expect(field.placeholder).toBe('Спросите о своей саге…');
    expect(screen.getByRole('button', { name: 'Спросить' })).toHaveProperty('disabled', true);
    await waitFor(() => expect(field.disabled).toBe(false));

    fireEvent.click(within(empty).getByRole('button', { name: examples[0] }));

    expect(field.value).toBe(examples[0]);
    expect(document.activeElement).toBe(field);
    expect(field.value.slice(field.selectionStart, field.selectionEnd)).toBe('[имя героя]');
    expect(screen.getByRole('button', { name: 'Спросить' })).toHaveProperty('disabled', false);
    expect(createSession).not.toHaveBeenCalled();
    expect(createTurn).not.toHaveBeenCalled();
  });

  it('on a phone the field asks for «Ваш вопрос…» and keeps «Спросить» as its name (R2.3.2)', async () => {
    vi.stubGlobal('matchMedia', vi.fn((query: string) => ({
      matches: query === '(max-width: 760px)', addEventListener: vi.fn(), removeEventListener: vi.fn(),
    })));
    renderPanel(createMockApiClient());

    expect((await screen.findByRole('textbox', { name: 'Вопрос' }) as HTMLTextAreaElement).placeholder).toBe('Ваш вопрос…');
    expect(screen.getByRole('button', { name: 'Спросить' })).toBeTruthy();
  });

  it('a chat with a question shows no examples', async () => {
    const api = createMockApiClient();
    const chat = await api.createChatSession(projectId, { scope: 'project', sourceView: 'published', title: 'Мать Мирона' },
      { idempotencyKey: createIdempotencyKey('createChatSession') });
    await api.createChatTurn(chat.id, { body: 'Что известно о матери?' }, { idempotencyKey: createIdempotencyKey('createChatTurn') });
    renderPanel(api, chat.id);

    await screen.findByRole('textbox', { name: 'Вопрос' });
    expect(screen.queryByRole('region', { name: 'Спросите о своей саге' })).toBeNull();
    expect(screen.queryByRole('button', { name: examples[1] })).toBeNull();
  });

  it('a reader without questions keeps the former empty state', async () => {
    renderPanel(createMockApiClient(), '', false);

    expect(await screen.findByText('Пока нет доступных чатов.')).toBeTruthy();
    expect(screen.queryByRole('region', { name: 'Спросите о своей саге' })).toBeNull();
  });
});
