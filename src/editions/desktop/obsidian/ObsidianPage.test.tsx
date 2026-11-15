// SPDX-License-Identifier: AGPL-3.0-only
import { cleanup, fireEvent, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { MockApiError } from '../../../api/mock';
import type { FolderChoice } from '../../../shared/desktop/shell';
import { FakeWebSocket } from '../../../test/FakeWebSocket';
import { reasonOf } from '../../../test/disabled-reason';
import { keepChoice, readChoice } from './choice-memory';
import { axeViolations, renderAt, theApp, until, withoutShell, withShell } from './obsidian.test-helpers';
import { choiceRefusalWords, words } from './words';

beforeEach(() => vi.stubGlobal('WebSocket', FakeWebSocket));
afterEach(() => {
  cleanup();
  withoutShell();
  window.localStorage.clear();
  FakeWebSocket.reset();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const page = (saga: string) => `/manuscript/obsidian?projectId=${saga}`;
const chooseButton = () => screen.findByRole('button', { name: words.choose }, { timeout: 10_000 });

// specs/2026-11-15-desk-obsidian-web.md OW1.3: the folder is not chosen — the words, the button, the outcome of the window of
// the shell only from its answer in this tab.
describe('the page of the bridge while the folder is not chosen', () => {
  it('says which folder to choose and links it by the window of the system', async () => {
    const { api, saga } = await theApp();
    const calls = withShell();
    const begun = vi.spyOn(api, 'beginObsidianVaultChoice');
    const { container } = renderAt(api, page(saga));
    expect(await screen.findByText(words.startText, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(await axeViolations(container)).toEqual([]);
    fireEvent.click(await chooseButton());
    await until(() => expect(screen.getByText(words.folderName('Северный предел'))).toBeTruthy());
    const { request } = await begun.mock.results[0]!.value as { request: string };
    expect(calls).toContainEqual(['pick_folder', { request }]);
    expect(screen.getByText('/home/author/Северный предел')).toBeTruthy();
    expect(await screen.findByRole('heading', { name: words.choiceTitle('Северный предел') }, { timeout: 10_000 })).toBeTruthy();
  });

  it.each(Object.entries(choiceRefusalWords))('says the refusal %s under the button and reads nothing again', async (reason, said) => {
    const { api, saga } = await theApp({ obsidianShell: 'waits' });
    withShell(() => ({ outcome: 'refused', reason } as FolderChoice));
    renderAt(api, page(saga));
    const button = await chooseButton();
    const reads = vi.spyOn(api, 'getObsidianVault');
    fireEvent.click(button);
    expect(await screen.findByText(said)).toBeTruthy();
    expect(reads).not.toHaveBeenCalled();
  });

  it('on «expired» reads the folder again: linked meanwhile — the folder; not — the words', async () => {
    const late = await theApp();
    withShell(() => ({ outcome: 'expired' }));
    renderAt(late.api, page(late.saga));
    fireEvent.click(await chooseButton());
    await until(() => expect(screen.getByText(words.folderName('Северный предел'))).toBeTruthy());
    cleanup();

    const none = await theApp({ obsidianShell: 'waits' });
    renderAt(none.api, page(none.saga));
    fireEvent.click(await chooseButton());
    expect(await screen.findByText(words.expired)).toBeTruthy();
  });

  it('changes nothing on «canceled», says the window that did not open, and keeps the button while the window is open', async () => {
    const { api, saga } = await theApp({ obsidianShell: 'waits' });
    withShell(() => ({ outcome: 'canceled' }));
    renderAt(api, page(saga));
    fireEvent.click(await chooseButton());
    await until(() => expect(screen.getByRole('button', { name: words.choose }).getAttribute('aria-description')).toBeNull());
    expect(screen.queryByRole('alert')).toBeNull();
    cleanup();

    let close: (choice: FolderChoice) => void = () => undefined;
    withShell(() => new Promise<FolderChoice>((resolve) => { close = resolve; }));
    renderAt(api, page(saga));
    fireEvent.click(await chooseButton());
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.choose }))).toBe(words.windowOpen));
    close({ outcome: 'canceled' });
    await until(() => expect(reasonOf(screen.getByRole('button', { name: words.choose }))).toBeNull());
    cleanup();

    const headless = await theApp({ obsidianShell: 'absent' });
    withShell();
    renderAt(headless.api, page(headless.saga));
    fireEvent.click(await chooseButton());
    expect(await screen.findByText(words.unavailable)).toBeTruthy();
  });

  it('reads neither `choosing` nor `refused` of the folder of the saga, and has no button without the shell', async () => {
    const { api, saga } = await theApp();
    vi.spyOn(api, 'getObsidianVault').mockResolvedValue({ linked: false, choosing: true, refused: 'not_folder' });
    withShell();
    renderAt(api, page(saga));
    const button = await chooseButton();
    expect(reasonOf(button)).toBeNull();
    expect(screen.queryByText(choiceRefusalWords.not_folder)).toBeNull();
    cleanup();
    withoutShell();
    renderAt(api, page(saga));
    expect(await screen.findByText(words.startText, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(screen.queryByRole('button', { name: words.choose })).toBeNull();
  });
});

// OW1.4, OW1.5, OW1.6: the line of the chosen folder, its change and unlink; a folder not in its place; a check that does not answer.
describe('the page of the bridge with the folder chosen', () => {
  async function linked(scenario: Parameters<typeof theApp>[0] = {}) {
    const app = await theApp(scenario);
    await app.api.beginObsidianVaultChoice(app.saga);
    return app;
  }

  it('shows the folder with its whole path, changes it keeping the choice of the author, and says a change that went by', async () => {
    const { api, saga } = await linked();
    keepChoice('user-local', saga, { kinds: { 'Книга 3': 'book' }, notes: ['Книга 1/02 Переправа.md'] });
    withShell();
    renderAt(api, page(saga));
    const line = await screen.findByRole('region', { name: words.folderName('Северный предел') }, { timeout: 10_000 });
    expect(within(line).getByText('/home/author/Северный предел')).toBeTruthy();
    expect(within(line).getByText(words.changeNote)).toBeTruthy();
    api.setScenario({ ...api.getScenario(), obsidianShell: { picked: '/home/author/Другая папка' } });
    fireEvent.click(within(line).getByRole('button', { name: words.change }));
    expect(await screen.findByText(words.folderName('Другая папка'))).toBeTruthy();
    expect(readChoice('user-local', saga)).toEqual({ kinds: { 'Книга 3': 'book' }, notes: ['Книга 1/02 Переправа.md'] });
    cleanup();

    api.setScenario({ ...api.getScenario(), obsidianShell: 'waits' });
    withShell(() => ({ outcome: 'expired' }));
    renderAt(api, page(saga));
    const again = await screen.findByRole('region', { name: words.folderName('Другая папка') }, { timeout: 10_000 });
    fireEvent.click(within(again).getByRole('button', { name: words.change }));
    expect(await screen.findByText(words.expiredChange)).toBeTruthy();
  });

  it('unlinks the folder after its question: the line of the end, the choice of the author forgotten, the folder not chosen', async () => {
    const { api, saga } = await linked();
    keepChoice('user-local', saga, { kinds: { 'Книга 3': 'book' }, notes: [] });
    withShell();
    const { container } = renderAt(api, page(saga));
    fireEvent.click(await screen.findByRole('button', { name: words.folderActions }, { timeout: 10_000 }));
    fireEvent.click(await screen.findByRole('menuitem', { name: words.unlinkItem }));
    const question = await screen.findByRole('dialog', { name: words.unlinkTitle('Северный предел') });
    expect(within(question).getByText(words.unlinkText)).toBeTruthy();
    expect(await axeViolations(container.ownerDocument.body)).toEqual([]);
    fireEvent.click(within(question).getByRole('button', { name: words.unlink }));
    expect(await screen.findByText(words.unlinked)).toBeTruthy();
    expect(await screen.findByText(words.startText)).toBeTruthy();
    expect(readChoice('user-local', saga)).toEqual({ kinds: {}, notes: [] });
  });

  it('says a failure of the unlink by «Не получилось — попробуйте ещё раз», and a failure to read the books of the saga by its own block', async () => {
    const { api, saga } = await linked();
    withShell();
    vi.spyOn(api, 'unlinkObsidianVault').mockRejectedValue(new MockApiError(500, 'failure', 'unlinkObsidianVault', 'server.error'));
    renderAt(api, page(saga));
    fireEvent.click(await screen.findByRole('button', { name: words.folderActions }, { timeout: 10_000 }));
    fireEvent.click(await screen.findByRole('menuitem', { name: words.unlinkItem }));
    const question = await screen.findByRole('dialog', { name: words.unlinkTitle('Северный предел') });
    fireEvent.click(within(question).getByRole('button', { name: words.unlink }));
    expect(await screen.findByText(words.tryAgain)).toBeTruthy();
    expect(screen.queryByText(words.cantOther)).toBeNull();
    cleanup();

    const broken = await linked();
    vi.spyOn(broken.api, 'listBooks').mockRejectedValue(new MockApiError(500, 'failure', 'listBooks', 'server.error'));
    renderAt(broken.api, page(broken.saga));
    const block = await screen.findByRole('alert', {}, { timeout: 10_000 });
    expect(within(block).getByText(words.booksFailed)).toBeTruthy();
    expect(screen.queryByText(words.scanFailed)).toBeNull();
  });

  it('says a folder not in its place instead of the choice', async () => {
    const { api, saga } = await linked();
    api.setScenario({ ...api.getScenario(), obsidianVault: { missing: 'not_found' } });
    withShell();
    renderAt(api, page(saga));
    expect(await screen.findByText(words.missing, undefined, { timeout: 10_000 })).toBeTruthy();
    expect(screen.getByRole('button', { name: words.checkAgain })).toBeTruthy();
    expect(screen.getAllByRole('button', { name: words.change }).length).toBe(2);
    expect(screen.queryByRole('heading', { name: words.choiceTitle('Северный предел') })).toBeNull();
  });

  it('reads a folder that does not answer three times in 2 s, then says it is slow and keeps the reads open', async () => {
    const { api, saga } = await linked({ obsidianVault: 'unknown' });
    const reads = vi.spyOn(api, 'getObsidianVault');
    renderAt(api, page(saga));
    expect(await screen.findByText(words.checking, undefined, { timeout: 10_000 })).toBeTruthy();
    await until(() => expect(screen.getByText(words.slowFolder)).toBeTruthy());
    expect(reads).toHaveBeenCalledTimes(4);
    expect(await screen.findByRole('heading', { name: words.choiceTitle('Северный предел') })).toBeTruthy();
  }, 15_000);
});
