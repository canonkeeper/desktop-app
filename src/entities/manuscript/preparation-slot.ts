import { createContext, useContext, type ReactNode } from 'react';
import type { RouteNavigate } from '../../shared/navigation/app-route';
import type { IndexingStatus } from './api';

/** What the line of the reading of an edition is given: the whole status of the server, the saga and the navigation of its page. */
export interface PreparationActionInput {
  /** getIndexingStatus of the saga: `preparation` with what the reading waits for, and all that comes with it. */
  status: IndexingStatus | undefined;
  navigate: RouteNavigate;
  /** The saga of the line: what its buttons do names it. */
  projectId: string;
}

/** One line of the reading the edition says by the status of the server. */
export interface PreparationLine {
  /** The name of the line for a screen reader: «Помощник готов прочитать книги», «Чтение на паузе». */
  label: string;
  /** The words of the line: the state, its counts, why it stands. */
  title: string;
  /** The buttons of the line in their order, the main one last. */
  actions: ReactNode;
  /** «Задать вопрос» of the place stands after them: the helper answers already. */
  ask: boolean;
  /** `work` while it goes or waits, `stalled` when it stands and asks the author, `done` at its end. */
  tone: 'work' | 'stalled' | 'done';
  /** The part read, 0..1, by the counts of the server; null — no bar. */
  progress: number | null;
  /**
   * The line stands under the line of the state of the place and takes nothing of it: when no line of the edition stands in
   * its place, the open words stay (the chapters that did not read, under «Книги прочитаны — можно спрашивать», board 44·28).
   */
  under: boolean;
}

/** What the edition says of the reading of a saga in place of the open words: its lines and the windows its buttons open. */
export interface PreparationView {
  /**
   * The lines in place of the open words of the line of the reading, and those that stand `under` it — whoever says it — for
   * what it does not say (the chapters that did not read).
   */
  lines: readonly PreparationLine[];
  /** The windows of the buttons — over the page, wherever the line stands. */
  windows: ReactNode;
}

/**
 * The line of the reading by the status of the server (specs/2026-11-13-desk-helper-web.md HW3.1,
 * specs/2026-11-13-desk-consent-web.md RW1.2): one slot of the edition for the shelf, the chat and the empty map — the app says
 * there that the helper is not connected, that it is ready to read the books, how the reading goes and why it stands, with
 * the buttons of each state and the windows they open. A hook: what it gives may hold its own state. Null — the open words
 * stand. The site has no slot, and its line is as it was.
 */
export type UsePreparationView = (input: PreparationActionInput) => PreparationView | null;

const PreparationViewContext = createContext<UsePreparationView | null>(null);

export const PreparationViewProvider = PreparationViewContext.Provider;

function useNoPreparationView(_input: PreparationActionInput): PreparationView | null {
  return null;
}

/** The line of the reading the edition says; null without the slot or when the open words stand. */
export function usePreparationView(input: PreparationActionInput): PreparationView | null {
  // The slot of an edition is given once over all the pages: the same hook runs at every render.
  const useView = useContext(PreparationViewContext) ?? useNoPreparationView;
  return useView(input);
}
