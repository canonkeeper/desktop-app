import { useState } from 'react';
import { shiftBlanks, type Blank } from './brackets';

/**
 * What the author typed in the question field and has not sent (specs/2026-11-10-ux1-chat.md CH1): kept in this browser
 * by chat — a new chat by its saga — so leaving for another section, a reload or a new sign-in loses nothing. It is a
 * form draft, not server state: once the server took the question or the correction, the draft is forgotten. With the
 * text go the words in brackets an example put there and the author has not replaced yet (specs/2026-11-12-ux12-shell.md
 * SH1.1): a draft without them is kept as its text alone.
 */
const storageKey = 'canonkeeper.chat.question-drafts';
const kept = 30;

interface Draft {
  text: string;
  blanks: Blank[];
}

/** The storage is off or full: the page remembers until it reloads. */
let memory: Map<string, Draft> | null = null;

function blanksIn(value: unknown): Blank[] | null {
  if (!Array.isArray(value)) return null;
  const blanks = value.filter((blank): blank is Blank => typeof blank?.text === 'string' && Number.isInteger(blank?.start) && blank.start >= 0);
  return blanks.length === value.length ? blanks.map(({ start, text }) => ({ start, text })) : null;
}

function draftIn(value: unknown): Draft | null {
  if (typeof value === 'string') return value.trim() ? { text: value, blanks: [] } : null;
  if (value === null || typeof value !== 'object') return null;
  const { blanks, text } = value as { blanks?: unknown; text?: unknown };
  const valid = blanksIn(blanks);
  return typeof text === 'string' && text.trim() && valid ? { text, blanks: valid } : null;
}

/** The drafts in the order of writing: the oldest leaves first. */
function read(): Map<string, Draft> {
  if (memory) return memory;
  let raw: string | null;
  try {
    raw = window.localStorage.getItem(storageKey);
  } catch {
    memory = new Map();
    return memory;
  }
  const drafts = new Map<string, Draft>();
  try {
    const stored: unknown = JSON.parse(raw ?? '{}');
    if (stored === null || typeof stored !== 'object') return drafts;
    for (const [slot, value] of Object.entries(stored)) {
      const draft = draftIn(value);
      if (draft) drafts.set(slot, draft);
    }
  } catch {
    // An unreadable record is as good as none.
  }
  return drafts;
}

function write(drafts: Map<string, Draft>) {
  if (memory) return;
  try {
    const stored = Object.fromEntries([...drafts].map(([slot, draft]) => [slot, draft.blanks.length ? draft : draft.text]));
    window.localStorage.setItem(storageKey, JSON.stringify(stored));
  } catch {
    memory = drafts;
  }
}

/**
 * The place of a draft: the chat of a saga, or the new chat of the saga — of one author, so another author of a shared
 * saga in the same browser does not find it in his field.
 */
export function questionSlot(projectId: string, chatId: string, authorId = '') {
  return `${authorId ? `${authorId}:` : ''}${projectId}:${chatId || 'new'}`;
}

export function readQuestionDraft(slot: string) {
  return read().get(slot)?.text ?? null;
}

/** The words in brackets of an example in the draft of a place, as the field put them. */
export function readQuestionBlanks(slot: string): Blank[] {
  return read().get(slot)?.blanks ?? [];
}

/** Keeps the text of the field with the blanks of an example in it; an empty field forgets its draft. */
export function keepQuestionDraft(slot: string, text: string, blanks: readonly Blank[] = []) {
  if (!text.trim()) {
    forgetQuestionDraft(slot);
    return;
  }
  const drafts = read();
  const former = drafts.get(slot);
  if (former?.text === text && JSON.stringify(former.blanks) === JSON.stringify(blanks)) return;
  drafts.delete(slot);
  drafts.set(slot, { text, blanks: [...blanks] });
  while (drafts.size > kept) drafts.delete(drafts.keys().next().value as string);
  write(drafts);
}

export function forgetQuestionDraft(slot: string) {
  const drafts = read();
  if (drafts.delete(slot)) write(drafts);
}

/** For tests: the page forgets what it kept without the storage, as after a reload. */
export function forgetQuestionDrafts() {
  memory = null;
}

/**
 * The text of the question field of the chat open on the page (CH1). `null` — the author has not written there: the
 * field shows what the server keeps for the chat. What he writes is kept by chat until the server takes it; `chatId` is
 * '' for a new chat. `blanks` are the words in brackets an example put into the text (SH1.1): they follow the text the
 * author changes, and only those that stand as they were hold the question.
 */
export function useQuestionField(place: { projectId: string; authorId?: string | undefined }, chatId: string, initialQuestion?: string) {
  const slot = (id: string) => questionSlot(place.projectId, id, place.authorId);
  const stored = (id: string): { text: string | null; blanks: Blank[] } => ({ text: readQuestionDraft(slot(id)), blanks: readQuestionBlanks(slot(id)) });
  // A question brought from the saga map stands over the draft (saga map R5.6).
  const [field, setField] = useState(() => (initialQuestion === undefined ? stored(chatId) : { text: initialQuestion, blanks: [] as Blank[] }));
  return {
    text: field.text,
    blanks: field.blanks,
    /**
     * The author wrote, or a button put what goes into the field: `placed` are the blanks it put — none for a whole
     * question — and without it the blanks follow what the author changed.
     */
    change(next: string, placed?: readonly Blank[]) {
      const blanks = next.trim() ? [...(placed ?? shiftBlanks(field.text ?? '', next, field.blanks))] : [];
      setField({ text: next, blanks });
      keepQuestionDraft(slot(chatId), next, blanks);
    },
    /** The question of a turn that gave no answer comes back only if the author has not written there (chat steering R4.4). */
    restore(task: string) {
      setField((current) => (current.text?.trim() ? current : { text: task, blanks: [] }));
    },
    /** The author opened this chat: its draft stands in the field (CH1.5). */
    open(id: string) {
      setField(stored(id));
    },
    /** The first question got its chat: the draft of the new chat is the draft of that chat now (CH1.4). */
    moved(id: string, question: string) {
      forgetQuestionDraft(slot(''));
      keepQuestionDraft(slot(id), question, question === field.text ? field.blanks : []);
    },
    /** The server took the question of this chat, or the chat is deleted (CH1.3). */
    sent(id: string) {
      setField({ text: '', blanks: [] });
      forgetQuestionDraft(slot(id));
    },
    /** The server took a correction; what the author wrote while it went stays (chat steering R4.3). */
    corrected(id: string, message: string) {
      if (readQuestionDraft(slot(id))?.trim() === message) forgetQuestionDraft(slot(id));
      setField((current) => (current.text?.trim() === message ? { text: '', blanks: [] } : current));
    },
  };
}
