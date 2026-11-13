import { useQueryClient } from '@tanstack/react-query';
import { useEffect, useRef, useState } from 'react';
import { isApiStatusFailure, isNetworkFailure } from '../../../entities/api-errors';
import {
  chapterEditQueryKeys, createIdempotencyKey, type ChapterEditApiClient, type EditedDraft, type EditedDraftChange,
} from '../../../entities/manuscript/api';
import { forgetUnsaved, keepUnsaved, readUnsaved } from '../../../shared/session/unsaved-copy';

/** What the author typed and the server has not taken yet: the title and the text of the chapter. */
export type EditFields = { title?: string; text?: string };
type Field = keyof EditFields;
const fields: readonly Field[] = ['title', 'text'];

/** Where a new chapter goes before it is written down: its book and the chapter it follows (ED2.3). */
export interface NewChapterPlace {
  bookId: string;
  afterChapterId?: string;
}

/** How soon what the author typed is saved after his last key (ED1.2). */
export const typingSaveMs = 1_500;
/** How long a save that did not pass for want of the connection or the server waits before its next try. */
const retryPauses = [2_000, 5_000, 15_000, 30_000] as const;

interface EditSavesOptions {
  api: ChapterEditApiClient;
  projectId: string;
  /** The author: the browser keeps what he typed under his name and the chapter's. */
  author: string;
  /** The draft as the server holds it; null — a new chapter not written down yet. */
  draft: EditedDraft | null;
  /** A new chapter: where its first save puts it. */
  place: NewChapterPlace | null;
  /** The first save wrote the new chapter down. */
  onCreated: (draft: EditedDraft) => void;
  delayMs?: number;
}

function retryable(error: unknown) {
  return isNetworkFailure(error) || (isApiStatusFailure(error) && (error.status >= 500 || error.status === 429));
}

/**
 * The saves of the editor (specs/2026-11-13-desk-editor.md ED1.2, ED2.2): 1,5 s after the author's last key and when he
 * leaves, one save after another, each at the revision the query cache holds when its turn comes. A new chapter is
 * written down by its first save, not by the way in. What is typed and not saved is kept in this browser under the
 * author and the chapter — the store of «Письмо» — so a reload of the window or a refused save loses nothing; a save
 * refused because the draft moved on is made again on the draft read anew, its text before kept as a version.
 */
export function useEditSaves({ api, author, delayMs = typingSaveMs, draft, onCreated, place, projectId }: EditSavesOptions) {
  const queryClient = useQueryClient();
  const latest = useRef({ api, author, draft, onCreated, place, projectId });
  latest.current = { api, author, draft, onCreated, place, projectId };
  const created = useRef<EditedDraft | null>(null);
  const typed = useRef<EditFields>({});
  const [edits, setEdits] = useState<EditFields>({});
  const [failed, setFailed] = useState(false);
  const [savedAt, setSavedAt] = useState<Date | null>(null);
  const timer = useRef<number | undefined>(undefined);
  const retries = useRef(0);
  const queue = useRef<Promise<unknown>>(Promise.resolve());
  // The next save keeps the text of the draft as a version first: it is the author's text over a draft that moved on.
  const keepVersion = useRef(false);

  const draftId = () => latest.current.draft?.id ?? created.current?.id ?? '';
  const storeKey = () => draftId() || `new.${latest.current.place?.bookId ?? ''}`;
  /** The draft as it stands now: the cache, which every save and action writes. */
  const current = (): EditedDraft | null => {
    const id = draftId();
    if (!id) return null;
    return queryClient.getQueryData<EditedDraft>(chapterEditQueryKeys.draft(latest.current.projectId, id)) ?? latest.current.draft ?? created.current;
  };

  function keep() {
    if (Object.keys(typed.current).length === 0) return;
    keepUnsaved(latest.current.author, storeKey(), { at: new Date().toISOString(), revision: current()?.revision ?? 0, edits: typed.current });
  }

  function show(next: EditFields) {
    typed.current = next;
    setEdits(next);
  }

  /** What the server took is no longer typed; what was typed meanwhile stays. */
  function settle(saved: EditedDraft) {
    const kept: EditFields = {};
    for (const field of fields) {
      const value = typed.current[field];
      if (value !== undefined && value !== saved[field]) kept[field] = value;
    }
    show(kept);
    if (Object.keys(kept).length === 0) forgetUnsaved(latest.current.author, saved.id);
    else keep();
    setSavedAt(new Date());
  }

  async function create(pending: EditFields) {
    const { api: client, place: where, projectId: project } = latest.current;
    const before = storeKey();
    const made = await client.createChapterDraft(project, {
      kind: 'sketch', title: pending.title ?? '', text: pending.text ?? '', ...(where ? { place: where } : {}),
    }, { idempotencyKey: createIdempotencyKey('createChapterDraft') });
    created.current = made;
    queryClient.setQueryData(chapterEditQueryKeys.draft(project, made.id), made);
    forgetUnsaved(latest.current.author, before);
    settle(made);
    latest.current.onCreated(made);
    return made;
  }

  async function saveOn(before: EditedDraft, extra: EditedDraftChange) {
    const pending: EditFields = {};
    for (const field of fields) {
      const value = typed.current[field];
      if (value !== undefined && value !== before[field]) pending[field] = value;
    }
    if (Object.keys(pending).length === 0 && Object.keys(extra).length === 0) {
      settle(before);
      return before;
    }
    const withVersion = keepVersion.current && pending.text !== undefined;
    const { api: client, projectId: project } = latest.current;
    const saved = await client.updateChapterDraft(project, before.id, {
      ...pending, ...extra, ...(withVersion ? { keepVersion: true } : {}), expectedRevision: before.revision,
    });
    if (withVersion) keepVersion.current = false;
    queryClient.setQueryData(chapterEditQueryKeys.draft(project, saved.id), saved);
    settle(saved);
    return saved;
  }

  async function saveTurn(extra: EditedDraftChange): Promise<EditedDraft | null> {
    const before = current();
    if (!before) {
      const pending = { ...typed.current };
      return Object.keys(pending).length ? create(pending) : null;
    }
    try {
      return await saveOn(before, extra);
    } catch (error) {
      if (!isApiStatusFailure(error) || error.problem.code !== 'conflict') throw error;
      // The draft moved on in another place: what the author typed goes onto it, the text it had is kept as a version.
      const { api: client, projectId: project } = latest.current;
      const fresh = await client.getChapterDraft(project, before.id);
      queryClient.setQueryData(chapterEditQueryKeys.draft(project, fresh.id), fresh);
      keepVersion.current = true;
      return saveOn(fresh, extra);
    }
  }

  function schedule(delay: number) {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => void flush().catch(() => undefined), delay);
  }

  /** Waits for the saves under way, saves what is typed by then (and `extra`) and answers the draft as it now stands. */
  function flush(extra: EditedDraftChange = {}): Promise<EditedDraft | null> {
    window.clearTimeout(timer.current);
    const turn = queue.current.catch(() => undefined).then(() => saveTurn(extra));
    queue.current = turn;
    turn.then(() => {
      setFailed(false);
      retries.current = 0;
    }, (error: unknown) => {
      keep();
      setFailed(true);
      if (retryable(error) && retries.current < retryPauses.length) schedule(retryPauses[retries.current++]!);
    });
    return turn;
  }

  function type(field: Field, value: string) {
    show({ ...typed.current, [field]: value });
    keep();
    schedule(delayMs);
  }

  /** «Отказаться…»: nothing typed is saved or kept any more; answers once the saves under way are over. */
  function stop() {
    window.clearTimeout(timer.current);
    show({});
    forgetUnsaved(latest.current.author, storeKey());
    return queue.current.catch(() => undefined);
  }

  // A copy this browser kept — the window was reloaded or a save was refused — comes back into its fields and is saved.
  const restored = useRef(false);
  useEffect(() => {
    if (restored.current || !author) return;
    restored.current = true;
    const copy = readUnsaved(author, storeKey());
    if (!copy) return;
    const back: EditFields = {};
    for (const field of fields) {
      const value = copy.edits[field];
      if (value !== undefined && value !== (draft?.[field] ?? '')) back[field] = value;
    }
    if (Object.keys(back).length === 0) {
      forgetUnsaved(author, storeKey());
      return;
    }
    // The chapter changed since the copy was kept: its text then stays as a version, nothing is lost.
    if (draft && copy.revision !== draft.revision && back.text !== undefined) keepVersion.current = true;
    show(back);
    schedule(0);
  }, [author]);

  // Leaving with a text not saved: it is kept in this browser and saved now; closing the window asks first.
  const leaving = useRef({ keep, flush });
  leaving.current = { keep, flush };
  useEffect(() => {
    const hidden = () => leaving.current.keep();
    const closing = (event: BeforeUnloadEvent) => {
      if (Object.keys(typed.current).length === 0) return;
      leaving.current.keep();
      event.preventDefault();
    };
    window.addEventListener('pagehide', hidden);
    window.addEventListener('beforeunload', closing);
    return () => {
      window.removeEventListener('pagehide', hidden);
      window.removeEventListener('beforeunload', closing);
      window.clearTimeout(timer.current);
      if (Object.keys(typed.current).length === 0) return;
      leaving.current.keep();
      void leaving.current.flush().catch(() => undefined);
    };
  }, []);

  const unsaved = failed || Object.keys(edits).length > 0;
  return { edits, type, flush, stop, unsaved, failed, savedAt, draftId };
}
