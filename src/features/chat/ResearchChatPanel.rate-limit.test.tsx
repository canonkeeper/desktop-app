import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ResearchChatPanel } from './ResearchChatPanel';

beforeEach(() => {
  FakeWebSocket.reset();
  vi.stubGlobal('WebSocket', FakeWebSocket);
});

afterEach(() => vi.unstubAllGlobals());

function rateLimit(code: string, retryAfter?: string) {
  const headers = new Headers();
  if (retryAfter !== undefined) headers.set('Retry-After', retryAfter);
  return new ApiStatusError(429, {
    code,
    detail: 'Request limited.',
    requestId: 'request-rate-limit',
    status: 429,
    title: 'Too Many Requests',
    type: 'about:blank',
  }, new Response(null, { headers, status: 429 }));
}

async function renderRateLimitedPanel(
  error: unknown,
  prepare?: (api: ReturnType<typeof createMockApiClient>) => Promise<void>,
) {
  const api = createMockApiClient();
  await prepare?.(api);
  const session = await api.createChatSession(projectId, {
    scope: 'project', sourceView: 'published', title: 'Лимиты',
  }, { idempotencyKey: 'idem-rate-limit-chat' });
  await api.createChatTurn(session.id, { body: 'Предыдущий вопрос' }, { idempotencyKey: 'idem-rate-limit-prior' });
  const createTurn = api.createChatTurn.bind(api);
  const keys: string[] = [];
  let first = true;
  vi.spyOn(api, 'createChatTurn').mockImplementation(async (...args) => {
    keys.push(args[2].idempotencyKey);
    if (first) {
      first = false;
      throw error;
    }
    return createTurn(...args);
  });
  render(
    <QueryClientProvider client={new QueryClient({
      defaultOptions: { mutations: { retry: false }, queries: { retry: false } },
    })}>
      <ResearchChatPanel
        api={api}
        canCreate
        onOpenSource={vi.fn()}
        onSelectChatId={vi.fn()}
        projectId={projectId}
        selectedChatId={session.id}
        sourceView="published"
      />
    </QueryClientProvider>,
  );
  const composer = await screen.findByRole('textbox', { name: 'Вопрос' });
  fireEvent.change(composer, { target: { value: 'Проверь лимит' } });
  const submit = screen.getByRole('button', { name: 'Спросить' });
  await waitFor(() => expect((submit as HTMLButtonElement).disabled).toBe(false));
  return { api, composer, keys, submit };
}

describe('Research Chat rate limits', () => {
  it('preserves a new question and its retry key after a network failure in an existing conversation', async () => {
    const { api, composer, keys } = await renderRateLimitedPanel(new TypeError('response lost'));
    fireEvent.click(screen.getByRole('button', { name: 'Спросить' }));
    expect(await screen.findByText('Вопрос не отправлен')).toBeTruthy();
    expect(composer).toHaveProperty('value', 'Проверь лимит');
    // The composer holds the one retry; the block under the turn adds none (R4.2.3).
    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Спросить снова' }));
    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[1]).toBe(keys[0]);
    expect((await api.listProjectJobs(projectId, { kind: 'research_chat_turn' })).data).toHaveLength(2);
  });

  it('ticks authoritative delta-seconds and retries the same turn at zero', async () => {
    const { composer, keys, submit } = await renderRateLimitedPanel(rateLimit('rate_limit.exceeded', '2'));
    fireEvent.click(submit);

    expect(await screen.findByText('Слишком много вопросов подряд')).toBeTruthy();
    expect(screen.getByText('Спросить снова можно через 0:02. Вопрос сохранён.')).toBeTruthy();
    expect(screen.getByText('request-rate-limit')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Спросить снова через 0:02' })).toHaveProperty('disabled', true);
    expect(composer).toHaveProperty('disabled', true);

    expect(await screen.findByRole('button', { name: 'Спросить снова через 0:01' }, { timeout: 1_600 }))
      .toHaveProperty('disabled', true);
    const retry = await screen.findByRole('button', { name: 'Спросить снова' }, { timeout: 1_600 });
    expect(retry).toHaveProperty('disabled', false);
    expect(screen.getByText('Можно спросить снова. Вопрос сохранён.')).toBeTruthy();
    fireEvent.click(retry);

    await waitFor(() => expect(keys).toHaveLength(2));
    expect(keys[1]).toBe(keys[0]);
  });

  it('does not invent a timer for a missing or invalid header', async () => {
    const { submit } = await renderRateLimitedPanel(rateLimit('too_many_requests', 'tomorrow'));
    fireEvent.click(submit);

    expect(await screen.findByText('Спросите снова чуть позже. Вопрос сохранён.')).toBeTruthy();
    expect(screen.queryByText(/через 0:/u)).toBeNull();
    expect(screen.getByRole('button', { name: 'Спросить снова' })).toHaveProperty('disabled', false);
  });

  it('shows a daily quota refusal from the account quota without Retry or a countdown', async () => {
    const { submit } = await renderRateLimitedPanel(rateLimit('quota.chat_turns_daily', '3600'));
    fireEvent.click(submit);

    expect(await screen.findByText(/Сегодня задано 30 вопросов — это дневной лимит/u)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /попытка|повторить|спросить снова/iu })).toBeNull();
    expect(document.body.textContent).not.toMatch(/Повторить через|можно через|оплат/iu);
  });

  it('shows the active questions refusal with the writer limit', async () => {
    const { submit } = await renderRateLimitedPanel(rateLimit('quota.active_turns'));
    fireEvent.click(submit);

    expect(await screen.findByText(/Сейчас готовятся ответы на 2 ваших вопроса/u)).toBeTruthy();
    expect(screen.queryByRole('button', { name: /попытка|повторить|спросить снова/iu })).toBeNull();
  });

  it('shows questions left from 80 % of the daily limit', async () => {
    const { api } = await renderRateLimitedPanel(new TypeError('unused'), async (api) => {
      const quota = await api.getAccountQuota();
      vi.spyOn(api, 'getAccountQuota').mockResolvedValue({ ...quota, chatTurns: { used: 24, limit: 30 } });
    });
    expect(await screen.findByText('Сегодня осталось 6 вопросов из 30. Счётчик обновится в 00:00 по Москве.')).toBeTruthy();
    expect(api.getAccountQuota).toHaveBeenCalled();
  });
});
