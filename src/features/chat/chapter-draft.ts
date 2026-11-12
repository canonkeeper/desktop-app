import type { ChapterFileInput } from '../../entities/chat/api';

/** A chapter attached at the field of a chat, not sent yet (specs/2026-11-07-chat-attach-chapter.md CA7). */
export interface ChatChapter {
  input: ChapterFileInput;
  /** The name of the file; none for a pasted text. */
  name?: string;
  /** Its words, once counted in the browser (AA4.2). */
  words?: number;
  /** The intent: the same file or text again is the same request until it went through (CA7.5). */
  key: string;
}

/**
 * The chapter attached to a question not sent yet, kept in this browser by the place of the question draft
 * (specs/2026-11-11-ux4-after-answer.md AA2.4; Ч-70): leaving for another section keeps it in the page, a reload finds it in
 * the storage of the browser. A form draft, not server state: once the question went, it is forgotten. A file is kept as
 * the browser keeps a file, in IndexedDB; where there is none, the page remembers until it reloads.
 */
const memory = new Map<string, ChatChapter>();
const database = 'canonkeeper.chat';
const store = 'chapter-drafts';
/** The drafts kept at most; the oldest leaves first. */
const kept = 10;

let opened: Promise<IDBDatabase | null> | null = null;

function open(): Promise<IDBDatabase | null> {
  if (typeof indexedDB === 'undefined') return Promise.resolve(null);
  opened ??= new Promise((resolve) => {
    try {
      const request = indexedDB.open(database, 1);
      request.onupgradeneeded = () => request.result.createObjectStore(store);
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => resolve(null);
      request.onblocked = () => resolve(null);
    } catch {
      resolve(null);
    }
  });
  return opened;
}

function write(run: (drafts: IDBObjectStore) => void) {
  void open().then((db) => {
    if (!db) return;
    try {
      run(db.transaction(store, 'readwrite').objectStore(store));
    } catch {
      // A full or closed storage keeps the chapter in the page only.
    }
  });
}

/** What the page already holds for the place: at once, without waiting for the storage. */
export function storedChapterDraft(slot: string) {
  return memory.get(slot) ?? null;
}

export async function readChapterDraft(slot: string): Promise<ChatChapter | null> {
  const known = memory.get(slot);
  if (known) return known;
  const db = await open();
  if (!db) return null;
  return new Promise((resolve) => {
    try {
      const request = db.transaction(store, 'readonly').objectStore(store).get(slot);
      request.onsuccess = () => {
        const value = request.result as ChatChapter | undefined;
        if (value && typeof value.key === 'string' && value.input) {
          memory.set(slot, value);
          resolve(value);
        } else resolve(null);
      };
      request.onerror = () => resolve(null);
    } catch {
      resolve(null);
    }
  });
}

export function keepChapterDraft(slot: string, chapter: ChatChapter) {
  memory.delete(slot);
  memory.set(slot, chapter);
  while (memory.size > kept) memory.delete(memory.keys().next().value as string);
  write((drafts) => drafts.put(chapter, slot));
}

export function forgetChapterDraft(slot: string) {
  memory.delete(slot);
  write((drafts) => drafts.delete(slot));
}

/** For tests: the page forgets what it kept, as after a reload. */
export function forgetChapterDrafts() {
  memory.clear();
}
