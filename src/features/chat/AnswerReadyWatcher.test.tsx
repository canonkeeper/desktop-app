import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChatJob } from '../../entities/chat/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import { holdTitlePart } from '../../shared/navigation/page-title';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import { forgetAllTurns, watchTurn } from './answer-ready';
import { AnswerReadyWatcher } from './AnswerReadyWatcher';
import { BrowserNoticeToggle } from './BrowserNoticeToggle';
import { shownReason } from '../../test/disabled-reason';

const turn = { chatId: 'chat-7', jobId: 'job-7', label: 'Когда   Мара получила кинжал?', projectId: 'project-white-port' };

function job(status: ChatJob['status']) {
  return { id: turn.jobId, status } as ChatJob;
}

function renderWatcher(status: ChatJob['status'], path = '/saga', query: Record<string, string> = {}) {
  const getJob = vi.fn().mockResolvedValue(job(status));
  const navigate = vi.fn();
  const dismiss = vi.fn();
  const toasts: ToastInput[] = [];
  const notify = vi.fn((toast: ToastInput) => toasts.push(toast));
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(
    <QueryClientProvider client={queryClient}>
      <AnswerReadyWatcher api={{ getJob }} dismiss={dismiss} navigate={navigate} notify={notify} route={createAppRoute(path, query)} />
    </QueryClientProvider>,
  );
  act(() => watchTurn(turn));
  return { dismiss, getJob, navigate, notify, toasts };
}

function setVisibility(state: DocumentVisibilityState) {
  Object.defineProperty(document, 'visibilityState', { configurable: true, get: () => state });
  document.dispatchEvent(new Event('visibilitychange'));
}

let releaseSection = () => {};

beforeEach(() => {
  setVisibility('visible');
  releaseSection = holdTitlePart('section', 'Чат');
});

afterEach(() => {
  act(() => forgetAllTurns());
  releaseSection();
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('AnswerReadyWatcher', () => {
  // specs/2026-11-11-ux4-after-answer.md AA2.3: the line names the question it answers.
  it('says the answer to which question is ready on another page and opens its chat', async () => {
    const { dismiss, getJob, navigate, notify, toasts } = renderWatcher('succeeded');
    await waitFor(() => expect(notify).toHaveBeenCalledOnce());
    expect(getJob).toHaveBeenCalledWith(turn.jobId);
    expect(toasts[0]).toMatchObject({ message: 'Ответ готов: «Когда Мара получила кинжал?»', persistent: true, tone: 'success' });
    render(toasts[0]!.action as ReactElement);
    fireEvent.click(screen.getByRole('button', { name: 'Открыть' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { chatId: turn.chatId, projectId: turn.projectId });
    expect(dismiss).toHaveBeenCalledWith(1);
    expect(document.title).toBe('Чат — CanonKeeper');
  });

  it('says two ready answers in one line that opens the freshest', async () => {
    const getJob = vi.fn(async (jobId: string) => ({ id: jobId, status: jobId === 'job-8' ? 'running' : 'succeeded' }) as ChatJob);
    const navigate = vi.fn();
    const dismiss = vi.fn();
    const toasts: ToastInput[] = [];
    const notify = vi.fn((toast: ToastInput) => toasts.push(toast));
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    render(<QueryClientProvider client={queryClient}>
      <AnswerReadyWatcher api={{ getJob }} dismiss={dismiss} navigate={navigate} notify={notify} route={createAppRoute('/saga', {})} />
    </QueryClientProvider>);
    act(() => watchTurn(turn));
    await waitFor(() => expect(notify).toHaveBeenCalledTimes(1));
    getJob.mockImplementation(async (jobId: string) => ({ id: jobId, status: 'succeeded' }) as ChatJob);
    act(() => watchTurn({ chatId: 'chat-8', jobId: 'job-8', label: 'Кто такой Сава?', projectId: turn.projectId }));
    await waitFor(() => expect(notify).toHaveBeenCalledTimes(2));
    // The first line goes: one line says both, not two lines alike.
    expect(dismiss).toHaveBeenCalledWith(1);
    expect(toasts[1]).toMatchObject({ message: 'Готовы 2 ответа', persistent: true, tone: 'success' });
    render(toasts[1]!.action as ReactElement);
    fireEvent.click(screen.getByRole('button', { name: 'Открыть чаты' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { chatId: 'chat-8', projectId: turn.projectId });
  });

  it('says a failed turn got no answer (R4.2.3)', async () => {
    const { notify, toasts } = renderWatcher('failed');
    await waitFor(() => expect(notify).toHaveBeenCalledOnce());
    expect(toasts[0]).toMatchObject({ message: 'Ответ не получен', tone: 'warning' });
  });

  it.each([
    ['looks at that chat', 'succeeded', '/chat'],
    ['stopped the turn', 'canceled', '/saga'],
  ] as const)('stays silent when the author %s', async (_case, status, path) => {
    const { getJob, notify } = renderWatcher(status, path, { chatId: turn.chatId, projectId: turn.projectId });
    await waitFor(() => expect(getJob).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(notify).not.toHaveBeenCalled();
  });

  it('marks a hidden tab until the author returns and notifies the browser when allowed', async () => {
    const created: Array<{ body?: string; tag?: string; title: string }> = [];
    vi.stubGlobal('Notification', class {
      static permission = 'granted';
      onclick: (() => void) | null = null;
      constructor(title: string, options: { body?: string; tag?: string }) { created.push({ title, ...options }); }
      close() {}
    });
    window.localStorage.setItem('canonkeeper.answer-ready.browser', '1');
    setVisibility('hidden');
    const { notify } = renderWatcher('succeeded', '/chat', { chatId: turn.chatId, projectId: turn.projectId });
    await waitFor(() => expect(notify).toHaveBeenCalledOnce());
    expect(document.title).toBe('● Ответ готов · Чат — CanonKeeper');
    expect(created).toEqual([{ body: '«Когда Мара получила кинжал?»', tag: turn.jobId, title: 'Ответ готов' }]);
    setVisibility('visible');
    expect(document.title).toBe('Чат — CanonKeeper');
  });
});

describe('BrowserNoticeToggle', () => {
  // specs/2026-11-10-ux4-chat-waiting.md CW2.6: where the browser has no notifications the switch stays and says why.
  it('stays and says why where the browser has no notifications', () => {
    expect('Notification' in window).toBe(false);
    render(<BrowserNoticeToggle />);
    const toggle = screen.getByRole('button', { name: 'Сообщить, когда будет готово' });
    expect(toggle.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(toggle);
    expect(shownReason(toggle)).toBe('Этот браузер не умеет сообщать о готовом ответе. Можно уйти — ответ сохранится в этом чате.');
  });

  it('asks the browser once and remembers the choice on the device', async () => {
    const browser = {
      permission: 'default' as NotificationPermission,
      requestPermission: vi.fn(async () => {
        browser.permission = 'granted';
        return 'granted' as const;
      }),
    };
    const { requestPermission } = browser;
    vi.stubGlobal('Notification', browser);
    render(<BrowserNoticeToggle />);
    // A switch of words, not a bell.
    const toggle = screen.getByRole('button', { name: 'Сообщить, когда будет готово' });
    expect(toggle.getAttribute('aria-pressed')).toBe('false');
    expect(toggle.querySelector('svg')).toBeNull();
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle.getAttribute('aria-pressed')).toBe('true'));
    expect(requestPermission).toHaveBeenCalledOnce();
    expect(window.localStorage.getItem('canonkeeper.answer-ready.browser')).toBe('1');
    fireEvent.click(toggle);
    await waitFor(() => expect(toggle.getAttribute('aria-pressed')).toBe('false'));
    expect(requestPermission).toHaveBeenCalledOnce();
  });

  it('explains a denial in the browser settings', () => {
    vi.stubGlobal('Notification', { permission: 'denied' });
    render(<BrowserNoticeToggle />);
    // specs/2026-11-10-ux2-patterns-refusals.md PR1.1: closed, it stays in its place and says why.
    const toggle = screen.getByRole('button', { name: 'Сообщить, когда будет готово' });
    expect(toggle.getAttribute('aria-disabled')).toBe('true');
    fireEvent.click(toggle);
    expect(shownReason(toggle)).toBe('Уведомления для этого сайта запрещены в настройках браузера — разрешите их там. Ответ сохранится и так.');
  });
});
