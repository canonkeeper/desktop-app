import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import type { ReactElement } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ChatJob } from '../../entities/chat/api';
import { createAppRoute } from '../../shared/navigation/app-route';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import { watchTurn } from './answer-ready';
import { AnswerReadyWatcher } from './AnswerReadyWatcher';
import { BrowserNoticeToggle } from './BrowserNoticeToggle';

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

beforeEach(() => {
  document.title = 'Canon Keeper';
  setVisibility('visible');
});

afterEach(() => {
  vi.unstubAllGlobals();
  window.localStorage.clear();
});

describe('AnswerReadyWatcher', () => {
  it('says the answer is ready on another page and opens its chat', async () => {
    const { dismiss, getJob, navigate, notify, toasts } = renderWatcher('succeeded');
    await waitFor(() => expect(notify).toHaveBeenCalledOnce());
    expect(getJob).toHaveBeenCalledWith(turn.jobId);
    expect(toasts[0]).toMatchObject({ message: 'Ответ готов', persistent: true, tone: 'success' });
    render(toasts[0]!.action as ReactElement);
    fireEvent.click(screen.getByRole('button', { name: 'Открыть чат' }));
    expect(navigate).toHaveBeenCalledWith('/chat', { chatId: turn.chatId, projectId: turn.projectId });
    expect(dismiss).toHaveBeenCalledWith(1);
    expect(document.title).toBe('Canon Keeper');
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
    expect(document.title).toBe('● Ответ готов · Canon Keeper');
    expect(created).toEqual([{ body: '«Когда Мара получила кинжал?»', tag: turn.jobId, title: 'Ответ готов' }]);
    setVisibility('visible');
    expect(document.title).toBe('Canon Keeper');
  });
});

describe('BrowserNoticeToggle', () => {
  it('is absent where the browser has no notifications', () => {
    expect('Notification' in window).toBe(false);
    render(<BrowserNoticeToggle />);
    expect(screen.queryByRole('button')).toBeNull();
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
    const toggle = screen.getByRole('button', { name: 'Сообщить в браузере, когда ответ будет готов' });
    expect(toggle.getAttribute('aria-pressed')).toBe('false');
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
    const toggle = screen.getByRole('button', { name: 'Уведомления запрещены в настройках браузера для этого сайта' });
    expect((toggle as HTMLButtonElement).disabled).toBe(true);
  });
});
