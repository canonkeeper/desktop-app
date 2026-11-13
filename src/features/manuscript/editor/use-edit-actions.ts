import { useQueryClient } from '@tanstack/react-query';
import { createElement, useState } from 'react';
import { isApiStatusFailure, publicApiErrorMessage } from '../../../entities/api-errors';
import {
  chapterEditQueryKeys, manuscriptQueryKeys, type ChapterEditApiClient, type EditedDraft, type EditedDraftVersionList,
} from '../../../entities/manuscript/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { editorWords } from './editor-words';
import type { useEditSaves } from './use-edit-saves';

type Saves = ReturnType<typeof useEditSaves>;
export type EditAction = 'replace' | 'add' | 'reopen' | 'revert' | 'take' | 'discard';

/**
 * The refusals of the editor in words, by their codes: the words of «Письмо» for the same refusals (ED3.3); a draft that moved
 * on in another place is read again, so «Нажмите ещё раз» is true.
 */
const refusals = {
  codes: {
    'chapter_draft.chapter_changed': editorWords.changed,
    'chapter_draft.chapter_gone': editorWords.gone,
    'chapter_draft.no_text': editorWords.empty,
    'chapter_draft.added': 'Эта глава уже в книге.',
    'chapter_draft.no_destination': 'Такого места в саге нет. Выберите книгу и главу заново.',
    'chapter_draft.busy': 'Над главой уже идёт работа. Дождитесь её конца.',
    conflict: 'Главу только что изменили в другом месте. Нажмите ещё раз.',
  },
  forbidden: 'Править главы может владелец саги, администратор или редактор.',
};
/** What an action that did not pass for another reason says: the words of «Письмо» for the same operations (ED3.3). */
const failures = {
  replace: 'Не удалось заменить главу.',
  add: 'Не удалось добавить главу.',
  take: 'Не удалось взять из версии.',
  // «Вернуть прежний текст», «Открыть главу заново» and the giving up of «Письмо».
  again: 'Не удалось. Попробуйте ещё раз.',
};

interface EditActionsOptions {
  api: ChapterEditApiClient;
  projectId: string;
  navigate: RouteNavigate;
  saves: Saves;
  /** The book the chapter stands in, and where its reader opens. */
  bookId: string;
}

/**
 * What the buttons of the editor do (specs/2026-11-13-desk-editor.md ED1.3–ED1.5, ED2.3, ED2.4): each saves what is typed
 * first and acts on the draft as it then stands; the answer of the server goes into the cache, its result into the one
 * area of the lines, the book read again where it changed.
 */
export function useEditActions({ api, bookId, navigate, projectId, saves }: EditActionsOptions) {
  const queryClient = useQueryClient();
  const toasts = useToasts();
  const [busy, setBusy] = useState<EditAction | null>(null);
  // «Заменить» was refused: the chapter changed in the book since the edit took it (ED1.5).
  const [refusedChanged, setRefusedChanged] = useState(false);

  const keep = (draft: EditedDraft) => queryClient.setQueryData(chapterEditQueryKeys.draft(projectId, draft.id), draft);
  const bookChanged = () => queryClient.invalidateQueries({ queryKey: manuscriptQueryKeys.all });
  const versionsChanged = (draftId: string) => queryClient.invalidateQueries({ queryKey: chapterEditQueryKeys.versions(projectId, draftId) });
  const failed = (error: unknown, fallback: string) => toasts.push({ message: publicApiErrorMessage(error, fallback, refusals), tone: 'error' });
  const toReader = (chapterId: string) => navigate('/manuscript/books', { projectId, bookId, chapterId, mode: 'read' }, { replace: true });

  /** The draft as the server holds it now: after a refusal that says the chapter or the draft moved on. */
  const reread = () => {
    const draftId = saves.draftId();
    if (draftId) void queryClient.invalidateQueries({ queryKey: chapterEditQueryKeys.draft(projectId, draftId) });
  };

  async function run(action: EditAction, work: () => Promise<void>, fallback: string) {
    setBusy(action);
    try {
      await work();
    } catch (error) {
      const code = isApiStatusFailure(error) ? error.problem.code : undefined;
      if (code === 'chapter_draft.chapter_changed' || code === 'conflict') reread();
      if (code === 'chapter_draft.chapter_changed') {
        setRefusedChanged(true);
        return;
      }
      failed(error, fallback);
    } finally {
      setBusy(null);
    }
  }

  /** The author's text before the text of the book takes its place: a version «Ваш вариант», the revision stays. */
  async function keptAsVariant(draft: EditedDraft) {
    await api.updateChapterDraft(projectId, draft.id, { expectedRevision: draft.revision, variant: { text: draft.text } });
  }

  /**
   * «Отменить» of a replacement: the chapter takes back the text it had (revertChapterFromDraft), and the author's edit stays
   * a version first, as before «Вернуть как было» — the server keeps none when it gives the text back.
   */
  async function undoReplace(draftId: string) {
    try {
      const now = queryClient.getQueryData<EditedDraft>(chapterEditQueryKeys.draft(projectId, draftId)) ?? await api.getChapterDraft(projectId, draftId);
      await keptAsVariant(now);
      keep(await api.revertChapterFromDraft(projectId, draftId, { expectedRevision: now.revision }));
      void versionsChanged(draftId);
      await bookChanged();
      toasts.push({ message: editorWords.reverted, tone: 'success' });
    } catch (error) {
      failed(error, failures.again);
    }
  }

  const replace = () => run('replace', async () => {
    const saved = await saves.flush();
    if (!saved?.edits) return;
    if (saved.edits.same) {
      toasts.push({ message: editorWords.same, tone: 'neutral' });
      return;
    }
    const done = await api.replaceChapterFromDraft(projectId, saved.id, {
      expectedRevision: saved.revision, title: saved.title.trim() || saved.edits.chapterTitle || '',
    });
    keep(done);
    await bookChanged();
    toasts.push({
      message: editorWords.replaced, tone: 'success',
      action: createElement(Button, { onClick: () => void undoReplace(done.id), size: 'sm', variant: 'ghost' }, editorWords.undo),
    });
    toReader(saved.edits.chapterId);
  }, failures.replace);

  const reopen = () => run('reopen', async () => {
    const saved = await saves.flush();
    if (!saved) return;
    await keptAsVariant(saved);
    keep(await api.reopenChapterFromBook(projectId, saved.id, { expectedRevision: saved.revision }));
    setRefusedChanged(false);
    void versionsChanged(saved.id);
    toasts.push({ message: editorWords.reopened, detail: editorWords.reopenedKept, tone: 'success' });
  }, failures.again);

  const revert = () => run('revert', async () => {
    const saved = await saves.flush();
    if (!saved) return;
    await keptAsVariant(saved);
    keep(await api.revertChapterFromDraft(projectId, saved.id, { expectedRevision: saved.revision }));
    void versionsChanged(saved.id);
    await bookChanged();
    toasts.push({ message: editorWords.reverted, tone: 'success' });
  }, failures.again);

  /** «Вернуть эту версию» (WA7.5): the version takes the place of the text, the text now stays in the versions. */
  const take = (number: number, when: string, onTaken: () => void) => run('take', async () => {
    const saved = await saves.flush();
    if (!saved) return;
    keep(await api.takeChapterDraftVersion(projectId, saved.id, number, { expectedRevision: saved.revision, whole: true }));
    await versionsChanged(saved.id);
    onTaken();
    const kept = queryClient.getQueryData<EditedDraftVersionList>(chapterEditQueryKeys.versions(projectId, saved.id))
      ?.versions.find((version) => version.reason === 'before_restore')?.number;
    toasts.push({
      message: `${editorWords.taken} «${when}»`, tone: 'success',
      ...(kept ? { action: createElement(Button, { onClick: () => void undoTake(saved.id, kept), size: 'sm', variant: 'ghost' }, editorWords.undo) } : {}),
    });
  }, failures.take);

  async function undoTake(draftId: string, number: number) {
    try {
      const now = queryClient.getQueryData<EditedDraft>(chapterEditQueryKeys.draft(projectId, draftId)) ?? await api.getChapterDraft(projectId, draftId);
      keep(await api.takeChapterDraftVersion(projectId, draftId, number, { expectedRevision: now.revision, whole: true }));
      void versionsChanged(draftId);
    } catch (error) {
      failed(error, failures.take);
    }
  }

  /** «Добавить в книгу» (ED2.3): on the place chosen; a chapter without a title takes the name of its first words. */
  const add = (afterChapterId: string | null) => run('add', async () => {
    let saved = await saves.flush();
    if (!saved) return;
    const place = { bookId, ...(afterChapterId ? { afterChapterId } : {}) };
    const moved = saved.place.bookId !== bookId || (saved.place.afterChapterId ?? null) !== afterChapterId;
    const title = saved.title.trim() ? null : saved.opening?.trim() || 'Новая глава';
    if (moved || title) saved = (await saves.flush({ ...(moved ? { place } : {}), ...(title ? { title } : {}) }))!;
    const done = await api.addChapterDraftToBook(projectId, saved.id, { expectedRevision: saved.revision });
    keep(done);
    void queryClient.invalidateQueries({ queryKey: chapterEditQueryKeys.begun(projectId, bookId) });
    await bookChanged();
    toasts.push({ message: editorWords.added, tone: 'success' });
    if (done.added) toReader(done.added.chapterId);
  }, failures.add);

  /** «Отказаться…» (ED1.4, ED2.4): the draft goes; the chapter of the book stays as it is. */
  const discard = (kind: 'edit' | 'new', chapterId: string | null, onDone: () => void) => run('discard', async () => {
    await saves.stop();
    const draftId = saves.draftId();
    if (draftId) {
      const now = queryClient.getQueryData<EditedDraft>(chapterEditQueryKeys.draft(projectId, draftId)) ?? await api.getChapterDraft(projectId, draftId);
      await api.deleteChapterDraft(projectId, draftId, now.revision);
      queryClient.removeQueries({ queryKey: chapterEditQueryKeys.draft(projectId, draftId) });
      void queryClient.invalidateQueries({ queryKey: chapterEditQueryKeys.begun(projectId, bookId) });
    }
    onDone();
    toasts.push({ message: kind === 'edit' ? editorWords.editDropped : editorWords.newDropped, tone: 'success' });
    if (kind === 'edit' && chapterId) toReader(chapterId);
    else navigate('/manuscript/books', { projectId, bookId }, { replace: true });
  }, failures.again);

  return { busy, refusedChanged, replace, reopen, revert, take, add, discard };
}
