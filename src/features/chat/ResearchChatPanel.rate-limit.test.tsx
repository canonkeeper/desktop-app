import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../api/mock';
import { projectId } from '../../api/mock/repository/fixtures';
import { ApiStatusError } from '../../shared/api';
import { FakeWebSocket } from '../../test/FakeWebSocket';
import { ResearchChatPanel } from './ResearchChatPanel';
import { ToastsProvider } from '../../ui/feedback/Toast';

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
    })}><ToastsProvider>
      <ResearchChatPanel
        api={api}
        canCreate
        onOpenSource={vi.fn()}
        onSelectChatId={vi.fn()}
        projectId={projectId}
        selectedChatId={session.id}
        sourceView="published"
      />
    </ToastsProvider></QueryClientProvider>,
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
    // The question may be mended while the button counts down (specs/2026-11-11-ux4-after-answer.md AA2.8, Ч-35).
    expect(composer).toHaveProperty('disabled', false);

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

  // specs/2026-11-10-ux1-chat.md CH3.3: a refusal by the limit is one block above the field, not a block of the feed.
  it('names a refusal by the questions of the day in one block above the field, without Retry or a countdown', async () => {
    const { composer, submit } = await renderRateLimitedPanel(rateLimit('quota.chat_turns_daily', '3600'));
    fireEvent.click(submit);

    // specs/2026-11-10-ux2-patterns-refusals.md PR2.1: the one block — what ended, when it comes back, «Написать нам».
    const block = await screen.findByRole('alert', { name: /^Вопросы и уточнения на сегодня закончились/u });
    expect(within(block).getByText(/^Вопросы и уточнения на сегодня закончились( — 30 из 30)?$/u)).toBeTruthy();
    expect(within(block).getByText('Новые — после 00:00 по Москве.')).toBeTruthy();
    expect(within(block).getByText('Ваш вопрос остался в поле — задайте его завтра.')).toBeTruthy();
    expect(within(block).getByRole('link', { name: /^Написать/u })).toBeTruthy();
    expect(screen.getAllByRole('alert')).toHaveLength(1);
    expect(block.compareDocumentPosition(composer) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
    expect(composer).toHaveProperty('value', 'Проверь лимит');
    expect(composer).toHaveProperty('disabled', false);
    expect(composer).toHaveProperty('placeholder', 'Вопросы на сегодня закончились');
    expect(screen.queryByRole('button', { name: /попытка|повторить|спросить снова/iu })).toBeNull();
    expect(screen.getByRole('button', { name: 'Спросить' }).getAttribute('data-variant')).toBe('secondary');
    expect(document.body.textContent).not.toMatch(/Повторить через|можно через|оплат|Сегодня задано|дневной лимит/iu);
    expect(screen.queryByRole('status')).toBeNull();
  });

  it('names the answers being prepared in the same block, without a day', async () => {
    const { composer, submit } = await renderRateLimitedPanel(rateLimit('quota.active_turns'));
    fireEvent.click(submit);

    // The mock author is on «Профи»: 4 answers at once by the grid (specs/2026-11-11-tier-limits.md TL1.1).
    const block = await screen.findByRole('alert', { name: 'Сейчас готовятся ответы на 4 ваших вопроса' });
    expect(within(block).getByText('Новый вопрос можно задать, когда один из них будет готов.')).toBeTruthy();
    expect(within(block).getByText('Ваш вопрос остался в поле.')).toBeTruthy();
    expect(screen.getAllByRole('alert')).toHaveLength(1);
    expect(composer).toHaveProperty('placeholder', 'Спросите о своей саге…');
    expect(screen.getByRole('button', { name: 'Спросить' }).getAttribute('data-variant')).toBe('primary');
    expect(screen.queryByRole('button', { name: /попытка|повторить|спросить снова/iu })).toBeNull();
  });

  // CH3.1: a short day is counted at the field from the first question; a long one from 80 % (quota spec R5.3).
  it('counts the questions left at the field', async () => {
    const { api } = await renderRateLimitedPanel(new TypeError('unused'), async (api) => {
      const quota = await api.getAccountQuota();
      vi.spyOn(api, 'getAccountQuota').mockResolvedValue({ ...quota, chatTurns: { used: 24, limit: 30 } });
    });
    expect((await screen.findByRole('status')).textContent).toBe('Осталось 6 из 30 вопросов на сегодня. Новые — после 00:00 по Москве.');
    expect(api.getAccountQuota).toHaveBeenCalled();
  });

  it('counts a day of five questions from its first one', async () => {
    await renderRateLimitedPanel(new TypeError('unused'), async (api) => {
      const quota = await api.getAccountQuota();
      vi.spyOn(api, 'getAccountQuota').mockResolvedValue({ ...quota, chatTurns: { used: 1, limit: 5 } });
    });
    expect((await screen.findByRole('status')).textContent).toBe('Осталось 4 из 5 вопросов на сегодня.');
  });

  // CH3.4: the block stands before the author presses; the page locks nothing by its own count (quota-accounting Q1.5).
  it('says that nothing is left before the author presses, and still lets the server decide', async () => {
    const { api, composer, keys, submit } = await renderRateLimitedPanel(new TypeError('unused'), async (api) => {
      const quota = await api.getAccountQuota();
      vi.spyOn(api, 'getAccountQuota').mockResolvedValue({ ...quota, chatTurns: { used: 5, limit: 5 } });
    });
    const block = await screen.findByRole('note', { name: 'Вопросы и уточнения на сегодня закончились — 5 из 5' });
    expect(within(block).getByText('Новые — после 00:00 по Москве.')).toBeTruthy();
    expect(within(block).getByText('Вопрос можно набрать уже сейчас — он сохранится в поле.')).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
    expect(screen.queryByRole('status')).toBeNull();
    expect(composer).toHaveProperty('placeholder', 'Вопросы на сегодня закончились');
    expect(submit).toHaveProperty('disabled', false);
    expect(submit.getAttribute('data-variant')).toBe('secondary');
    fireEvent.click(submit);
    await waitFor(() => expect(keys).toHaveLength(1));
    expect(api.getAccountQuota).toHaveBeenCalled();
  });
});
