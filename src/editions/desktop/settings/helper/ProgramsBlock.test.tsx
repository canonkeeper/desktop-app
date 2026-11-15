// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { useBlocker } from '@tanstack/react-router';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { createMockApiClient } from '../../../../api/mock';
import type { CurrentUser } from '../../../../entities/session/api';
import type { OperationRequest } from '../../../../shared/api';
import { hiddenIn } from '../../../../shared/copy/dictionary';
import { createAppRoute } from '../../../../shared/navigation/app-route';
import { ToastsProvider } from '../../../../ui/feedback/Toast';
import { edition } from '../..';
import { noShell, shellOfTheWindow } from '../settings.test-helpers';
import type { ProgramAccess } from './programs-api';
import { programsWords } from './programs-words';

vi.mock('@tanstack/react-router', async (actual) => ({ ...await actual<object>(), useBlocker: vi.fn(() => ({ status: 'idle' })) }));

type Api = ReturnType<typeof createMockApiClient>;

const author = { id: 'local-author', displayName: 'Автор', email: 'local@desktop.invalid', emailVerified: true, documentsAccepted: true } as CurrentUser;

const profile: NonNullable<OperationRequest<'updateAiSettings'>['own']> = {
  preset: 'deepseek', chatModel: 'deepseek-chat', cardsModel: 'deepseek-reasoner',
  advanced: { contextWindow: 64_000, maxOutput: 16_000, reasoning: 'high', parallel: 2, priceInPerMillion: 0.27, priceOutPerMillion: 1.1, currency: 'RUB' },
};

beforeEach(() => {
  vi.stubGlobal('matchMedia', (query: string) => ({ addEventListener: () => undefined, matches: false, media: query, removeEventListener: () => undefined }));
  shellOfTheWindow();
});

afterEach(() => {
  cleanup();
  noShell();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

async function theApp() {
  const api = createMockApiClient();
  api.startDesktop();
  await api.openDesktopSession({ code: 'code-of-the-shell' });
  return api;
}

/** «Настройки → Помощник» as the edition of the app renders it: its page of «Настройки» under what its account wraps pages in. */
function helperPage(api: Api) {
  const navigate = vi.fn();
  const route = createAppRoute('/settings', { part: 'helper' });
  function Pages() {
    const account = edition.useAccount({ api, navigate, route } as unknown as Parameters<typeof edition.useAccount>[0]);
    return <>{account.wrapPages(edition.pages.settings!.render({ api, navigate, route, user: author }))}</>;
  }
  const queries = new QueryClient({ defaultOptions: { mutations: { retry: false }, queries: { retry: false } } });
  render(<QueryClientProvider client={queries}><ToastsProvider><Pages /></ToastsProvider></QueryClientProvider>);
}

async function theBlock() {
  return screen.findByRole('region', { name: programsWords.title });
}

// specs/2026-11-15-desk-mcp.md MC3.1, MC4.1: «Свой Claude или Codex» is a section of its own of «Помощник» — with the helper of
// the account, with «Свой ИИ», while the settings of the helper load and when they fail; its words of the border stand at the
// switch before it is on.
describe('«Свой Claude или Codex» of «Помощник»', () => {
  it('stands with the helper of the account and says the border before the reading is on', async () => {
    const api = await theApp();
    api.setScenario({ ...api.getScenario(), accountLinked: true });
    await api.updateAiSettings({ source: 'account' });
    helperPage(api);
    const block = await theBlock();
    expect(within(block).getByRole('heading', { name: programsWords.title })).toBeTruthy();
    expect(within(block).getByText(programsWords.what)).toBeTruthy();
    expect(within(block).getByText(programsWords.border)).toBeTruthy();
    const allow = await within(block).findByRole('switch', { name: programsWords.allow });
    expect(allow.getAttribute('aria-checked')).toBe('false');
    expect(within(block).queryByText('Claude Code')).toBeNull();
  });

  it('stands with «Свой ИИ», apart from its form: the switch acts at once and asks no «Не сохранять?»', async () => {
    const api = await theApp();
    await api.updateAiSettings({ source: 'own', own: profile });
    const update = vi.spyOn(api, 'updateProgramAccess');
    const settingsWrites = vi.spyOn(api, 'updateAiSettings');
    helperPage(api);
    const block = await theBlock();
    await screen.findByRole('button', { name: /^Провайдер/u });
    fireEvent.click(await within(block).findByRole('switch', { name: programsWords.allow }));
    await waitFor(() => expect(update).toHaveBeenCalledWith({ enabled: true }));
    expect(await within(block).findByText(programsWords.whenOff)).toBeTruthy();
    expect(within(block).getByRole('heading', { name: 'Claude Desktop (Ubuntu и Debian)' })).toBeTruthy();
    expect(within(block).getByRole('heading', { name: 'Claude Code' })).toBeTruthy();
    expect(within(block).getByRole('heading', { name: 'Codex' })).toBeTruthy();
    expect(within(block).getByText(programsWords.noReads)).toBeTruthy();
    expect(within(block).getByLabelText('Claude Code').textContent).toContain("claude mcp add --scope user canonkeeper '/usr/bin/canonkeeper-mcp'");
    expect(settingsWrites).not.toHaveBeenCalled();
    const blocked = vi.mocked(useBlocker).mock.calls.at(-1)?.[0] as { disabled?: boolean } | undefined;
    expect(blocked?.disabled).toBe(true);
    // Claude Desktop stands first.
    const headings = within(block).getAllByRole('heading', { level: 4 }).map((heading) => heading.textContent);
    expect(headings).toEqual(['Claude Desktop (Ubuntu и Debian)', 'Claude Code', 'Codex']);
  });

  // The switch of the edition, as the daily check (board 44·46): while the request goes it keeps its focus — busy, not
  // disabled — and takes no second click.
  it('keeps its focus while the request goes', async () => {
    const api = await theApp();
    let answer: (state: ProgramAccess) => void = () => undefined;
    const update = vi.spyOn(api, 'updateProgramAccess').mockReturnValue(new Promise((resolve) => { answer = resolve; }));
    helperPage(api);
    const block = await theBlock();
    const allow = await within(block).findByRole('switch', { name: programsWords.allow });
    allow.focus();
    fireEvent.click(allow);
    await waitFor(() => expect(allow.getAttribute('aria-busy')).toBe('true'));
    expect(allow.hasAttribute('disabled')).toBe(false);
    expect(document.activeElement).toBe(allow);
    fireEvent.click(allow);
    expect(update).toHaveBeenCalledTimes(1);
    answer({ ...(await api.getProgramAccess()), enabled: true, ever: true, open: true });
    await waitFor(() => expect(allow.getAttribute('aria-checked')).toBe('true'));
    expect(allow.hasAttribute('aria-busy')).toBe(false);
    expect(document.activeElement).toBe(allow);
  });

  it('stands while the settings of the helper load and when they fail', async () => {
    const loading = await theApp();
    vi.spyOn(loading, 'getAiSettings').mockReturnValue(new Promise(() => undefined));
    helperPage(loading);
    expect(await theBlock()).toBeTruthy();
    cleanup();
    const failing = await theApp();
    vi.spyOn(failing, 'getAiSettings').mockRejectedValue(new Error('down'));
    helperPage(failing);
    expect(await theBlock()).toBeTruthy();
  });

  it('says a state that did not load with «Повторить»', async () => {
    const api = await theApp();
    const read = vi.spyOn(api, 'getProgramAccess').mockRejectedValueOnce(new Error('down'));
    helperPage(api);
    const block = await theBlock();
    expect(await within(block).findByText(programsWords.loadFailed)).toBeTruthy();
    fireEvent.click(within(block).getByRole('button', { name: 'Повторить' }));
    expect(await within(block).findByRole('switch', { name: programsWords.allow })).toBeTruthy();
    expect(read).toHaveBeenCalledTimes(2);
  });

  // The commands to take the entry away in «Стереть все данные» are the commands of the server, word for word.
  it('takes away the entry with the commands of the server', () => {
    const go = readFileSync(path.join(process.cwd(), 'backend/internal/domain/desktop/programs.go'), 'utf8');
    expect(go).toContain(`claudeRemove = "${programsWords.erase.claudeRemove}"`);
    expect(go).toContain(`codexRemove  = "${programsWords.erase.codexRemove}"`);
  });

  it('says every word in the words of the author', () => {
    const texts: string[] = [];
    const walk = (value: unknown) => {
      if (typeof value === 'string') texts.push(value);
      else if (typeof value === 'function') texts.push(String((value as (...args: string[]) => string)('Claude Code', '14:05')));
      else if (value && typeof value === 'object') Object.values(value).forEach(walk);
    };
    walk(programsWords);
    for (const text of texts) {
      expect(hiddenIn(text, 'src/editions/desktop/settings/helper/ProgramsBlock.tsx'), text).toEqual([]);
    }
  });
});

// specs/2026-11-15-desk-mcp.md MC3.3, Data «Claude Desktop, Windows», «состояние»: the branch of Windows through the real build
// of the edition — Claude Desktop first with its file and the three ends of its saving, the words of PowerShell, under the
// sections who turned to the door, the last reading and the bridges of an earlier version; a door that did not open; no
// commands.
describe('«Свой Claude или Codex» on Windows', () => {
  const now = new Date().toISOString();
  const windows: ProgramAccess = {
    enabled: true, ever: true, open: true, failed: false, system: 'windows',
    commands: {
      claudeAdd: "claude mcp add --scope user canonkeeper 'C:\\p\\canonkeeper-mcp.exe' 'C:\\d'", claudeRemove: 'claude mcp remove --scope user canonkeeper',
      codexAdd: "codex mcp add canonkeeper -- 'C:\\p\\canonkeeper-mcp.exe' 'C:\\d'", codexRemove: 'codex mcp remove canonkeeper', desktopEntry: null, desktopFile: null,
    },
    extensionFile: 'CanonKeeper-1.4.0.mcpb',
    programs: [{ name: 'Claude Desktop', at: now, matches: false, extension: true }, { name: 'Claude Code', at: now, matches: false, extension: false }],
    lastRead: { name: 'Claude Code', at: now },
  };

  async function onWindows(state: ProgramAccess) {
    const api = await theApp();
    vi.spyOn(api, 'getProgramAccess').mockResolvedValue(state);
    URL.createObjectURL = vi.fn(() => 'blob:extension');
    URL.revokeObjectURL = vi.fn();
    vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => undefined);
    return api;
  }

  it('gives Claude Desktop first with its file, PowerShell, and under them who read', async () => {
    const api = await onWindows(windows);
    let saved: (file: { blob: Blob; fileName: string }) => void = () => undefined;
    const extension = vi.spyOn(api, 'getClaudeDesktopExtension').mockReturnValue(new Promise((resolve) => { saved = resolve; }));
    helperPage(api);
    const block = await theBlock();
    await within(block).findByRole('heading', { name: 'Claude Desktop' });
    expect(within(block).getAllByRole('heading', { level: 4 }).map((heading) => heading.textContent)).toEqual(['Claude Desktop', 'Claude Code', 'Codex']);
    expect(within(block).getByText(programsWords.desktopWindows.how('CanonKeeper-1.4.0.mcpb'))).toBeTruthy();
    expect(within(block).getByText(programsWords.desktopWindows.onlyHere)).toBeTruthy();
    expect(within(block).getAllByText(programsWords.commands.windows)).toHaveLength(2);
    expect(within(block).getByText(programsWords.oldExtension)).toBeTruthy();
    expect(within(block).getByText(programsWords.oldSession('Claude Code'))).toBeTruthy();
    const read = within(block).getByText(/^Последний раз читал: Claude Code, сегодня в \d\d:\d\d$/u);
    const seen = within(block).getByText(/^Обращались с запуска CanonKeeper: Claude Desktop — в \d\d:\d\d, Claude Code — в \d\d:\d\d$/u);
    // The lines of the state stand under the sections of the connection (board 44·46).
    const codex = within(block).getByRole('heading', { name: 'Codex' });
    for (const line of [read, seen]) expect(Boolean(codex.compareDocumentPosition(line) & Node.DOCUMENT_POSITION_FOLLOWING)).toBe(true);

    const save = within(block).getByRole('button', { name: /Сохранить файл расширения/u });
    fireEvent.click(save);
    await waitFor(() => expect(save.hasAttribute('disabled')).toBe(false));
    fireEvent.click(save);
    expect(await within(block).findByText(programsWords.desktopWindows.saving)).toBeTruthy();
    expect(extension).toHaveBeenCalledTimes(1);
    saved({ blob: new Blob(['PK']), fileName: 'CanonKeeper-1.4.0.mcpb' });
    expect(await screen.findByText(programsWords.desktopWindows.saved('CanonKeeper-1.4.0.mcpb'))).toBeTruthy();
  });

  it('says the file did not save, with «Повторить»', async () => {
    const api = await onWindows(windows);
    const extension = vi.spyOn(api, 'getClaudeDesktopExtension').mockRejectedValue(new Error('500'));
    helperPage(api);
    const block = await theBlock();
    fireEvent.click(await within(block).findByRole('button', { name: /Сохранить файл расширения/u }));
    expect(await screen.findByText(programsWords.desktopWindows.notSaved)).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: programsWords.desktopWindows.retry }));
    await waitFor(() => expect(extension).toHaveBeenCalledTimes(2));
  });

  it('says a door that did not open, and no commands', async () => {
    const failed = await onWindows({ ...windows, open: false, failed: true });
    helperPage(failed);
    expect(await within(await theBlock()).findByText(programsWords.failed)).toBeTruthy();
    cleanup();
    const without = await onWindows({ ...windows, commands: null, extensionFile: null });
    helperPage(without);
    const block = await theBlock();
    expect(await within(block).findByText(programsWords.noCommands)).toBeTruthy();
    expect(within(block).queryByRole('heading', { name: 'Claude Desktop' })).toBeNull();
  });
});
