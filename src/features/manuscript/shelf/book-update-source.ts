import type { ComponentType } from 'react';
import type { Book, ProjectJob } from '../../../entities/manuscript/api';
import type { PresenceStatus } from '../../../ui/overlays/use-presence';
import type { BookUpdatePreviewResult } from './BookUpdatePreview';

/** The start of the import Job of an update as the server answers it. */
export interface BookUpdateStart {
  job: ProjectJob;
}

/**
 * Where the new text of a book comes from besides a file the author chose (specs/2026-11-15-desk-obsidian-book-web.md OU2.1): a
 * folder of Obsidian of the app. The source starts the preview and the update and says its window and its refusals; the owner
 * leads the one cycle — the Job of the preview read and stopped, the update started and followed to its end.
 */
export interface BookUpdateSource<Answer extends BookUpdateStart = BookUpdateStart> {
  /** The start of the preview, under the key of this reading — one key for a reading and its repeat after a lost connection. */
  preview: (book: Book, key: string) => Promise<Answer>;
  /** The start of the update after the preview the window showed, under the key of that preview. */
  apply: (book: Book, preview: Answer, options: { key: string; confirmUnread: boolean }) => Promise<BookUpdateStart>;
  /** A new key of a write: one for every reading, one for the update of every preview (OU2.2). */
  key: () => string;
  /** Whether the window reads at all: a book whose text has nowhere to come from opens it without a reading (OU2.3). */
  reads: (book: Book) => boolean;
  /** The repeats of a start the source waits for itself, as the mutations of React Query take them. */
  repeat: { retry: (failures: number, error: unknown) => boolean; retryDelay: (failures: number, error: unknown) => number };
  /** The update waits in the cache of the mutations under this key while the window is open: the lines of its result know it so. */
  updateKey: readonly unknown[];
  /** The line of the update in work for the place of the notices of «Книги». */
  working: (title: string) => string;
  /** The update started: what the source marks of its own. */
  started: () => void;
  /** The window of the source over the cycle of the owner. */
  Window: ComponentType<BookUpdateWindowProps<Answer>>;
}

/** What the window of a source shows and does: the cycle of the owner as it stands. */
export interface BookUpdateWindowProps<Answer extends BookUpdateStart = BookUpdateStart> {
  source: BookUpdateSource<Answer>;
  book: Book;
  state: PresenceStatus;
  /** The reading goes: the server answers the start, then its Job reads. */
  reading: boolean;
  /** A start the source waits to repeat: how many times it failed so, and its last refusal. */
  waits: { count: number; reason: unknown };
  answer: Answer | null;
  result: BookUpdatePreviewResult | null;
  /** The reading gave no plan: the refusal of its start, or its Job that ended without one. */
  failure: { error: unknown } | { job: ProjectJob } | null;
  applying: boolean;
  applyWaits: { count: number; reason: unknown };
  applyError: unknown;
  onClose: () => void;
  /** The same reading again, under its key: a lost connection. */
  onRetry: () => void;
  /** A new reading under a new key: «Прочитать заново». */
  onReread: () => void;
  onApply: (confirmUnread: boolean) => void;
}
