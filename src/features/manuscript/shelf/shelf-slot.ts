import { createContext, useContext, type ComponentType, type ReactNode } from 'react';
import type { Book } from '../../../entities/manuscript/api';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import type { BookUpdateSource } from './book-update-source';

/** What the offer of the demo saga needs of the shelf it stands on (specs/2026-11-09-demo-saga.md DS1). */
export interface ShelfDemoOfferOptions {
  /** The author's address is not verified: the button stays and says the reason instead of opening the example. */
  emailUnverified: boolean;
  /** The shelf of a saga, where the opened example leads at once (specs/2026-11-11-import-speed.md IS2.2). */
  onOpenSaga: (projectId: string) => void;
}

/** The offer of the demo saga as the shelf places it. */
export interface ShelfDemoOffer {
  /** «Попробовать на примере» with its caption. */
  element: ReactNode;
  /** The example is being opened — its one request: the offer stays on the page whatever the shelf shows by then. */
  preparing: boolean;
}

/**
 * What «Вычитать мою главу» needs of the shelf it stands on (specs/2026-11-10-ux3-first-screen.md FS1.2, FS2.1): the
 * first screen and a saga without a book.
 */
export interface ShelfProofreadOptions {
  /** The saga of the shelf; null on the first screen — the card founds «Моя сага» before the chapter goes. */
  projectId: string | null;
  /** The author's address is not verified: the button stays and says the reason instead of opening the window of files. */
  emailUnverified: boolean;
  /** «Моя сага» by the operation of «Создать сагу»: its id, or null when the server refused — the shelf says why. */
  foundSaga: () => Promise<string | null>;
  /** The chapter on «Сверка» of «Письмо», where its corrections come. */
  onOpenDraft: (projectId: string, draftId: string) => void;
}

/** A drawing of the saga chosen for a cover (specs/2026-11-11-ux9-shelf-chat.md SC1.6). */
export interface CoverDrawing {
  number: number;
  title: string;
  /** The address of the picture as the page shows it: it stands on the cover before it is saved. */
  picture: string | null;
}

/** «Выбрать из рисунков саги» of the window of a cover: the drawings of the saga to choose from. */
export interface CoverDrawingsProps {
  projectId: string;
  chosen: number | null;
  onChoose: (drawing: CoverDrawing) => void;
}

/** An item of the edition in the menu of a book: its word, its icon, why it waits now (specs/2026-11-15-desk-obsidian-book-web.md OU1.1). */
export interface ShelfBookMenuItem {
  key: string;
  label: string;
  icon: ReactNode;
  reason: string | null;
  /** The press: the shelf closes the menu first. */
  onSelect: () => void;
}

/** What the items of the edition in the menu of a book know of the shelf: the facts, not the words (OU1.2). */
export interface ShelfBookMenuOptions {
  projectId: string;
  /** The author may change the books of the saga: only then the edition reads what its items need. */
  canEdit: boolean;
  navigate: RouteNavigate;
  /** The window of an update of a book from a source of the edition: the owner of «Новая версия из файла…» (OU2.1). */
  // The source is the edition's: the shelf carries it to its owner as it is.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  open: (book: Book, source: BookUpdateSource<any>) => void;
  /** The book is being updated now. */
  updating: (book: Book) => boolean;
  /** Another update of the shelf goes, or its window is open. */
  busy: boolean;
  /** The saga loads books now. */
  uploading: boolean;
}

/** The items of the edition in the menu of every book and the windows they open. */
export interface ShelfBookMenu {
  items: (book: Book) => readonly ShelfBookMenuItem[];
  overlay: ReactNode;
}

/** The closed blocks of the shelf (specs/2026-11-07-desktop-app.md DA16.4); an absent one is not shown. */
export interface ShelfSlot {
  /** «Попробовать на примере» of a shelf without a book: the page holds its state, so a reread shelf does not lose it. */
  useDemoOffer?: (options: ShelfDemoOfferOptions) => ShelfDemoOffer;
  /** Where the text of the demo saga comes from: the line under its notice. */
  demoCredit?: string;
  /** «Вычитать мою главу» of a build with «Письмо»: the card, its state held by the page as the example's is. */
  useProofreadOffer?: (options: ShelfProofreadOptions) => ReactNode;
  /**
   * «Ваши материалы» read tables and texts into records in this build (specs/2026-11-11-ux9-shelf-chat.md SC1.7): the line at
   * «Добавить книги» names the tables; without it the records are written by hand, and the line says only where they go.
   */
  materialsFromFiles?: boolean;
  /** The drawings of the saga for «Выбрать из рисунков саги» of the window of a cover (SC1.6, L-11); none without drawings. */
  CoverDrawings?: ComponentType<CoverDrawingsProps>;
  /**
   * «Новая глава» of the menu of a book: the editor of the desktop app without the helper (specs/2026-11-13-desk-editor.md
   * ED2.1); without it the menu has no such item — the site writes a chapter in «Письмо».
   */
  newChapterAt?: (book: { id: string; projectId: string }) => { path: string; query: Record<string, string> };
  /**
   * «Перенести сагу с сайта или другого компьютера» of the first screen: the card of a build that opens a saga file — the
   * app (specs/2026-11-13-desk-sagafile-web.md SW2.1); the site has none.
   */
  useSagaFileOffer?: () => ReactNode;
  /**
   * A second source of books beside «Добавить книги» (specs/2026-11-15-desk-obsidian-web.md OW1.2): «Из Obsidian…» of the app
   * in the head of «Книги» and in the first steps of a saga without a book, there with its caption; it leads to the page of
   * the source with the way back to the books. The site has none.
   */
  bookSource?: { label: string; caption: string; at: (projectId: string) => { path: string; query: Record<string, string> } };
  /**
   * Volumes of the load of the saga whose files another work of the build sends, not this tab: the load of books from an
   * Obsidian folder (OW4.6). While it says so, such a volume is «Загружается», not «Файл не дошёл». The site has none.
   */
  useLoadingElsewhere?: (projectId: string) => boolean;
  /**
   * Items of the edition in the menu of a book after «Новая версия из файла…» and the windows they open
   * (specs/2026-11-15-desk-obsidian-book-web.md OU1): the book and its folder of Obsidian in the app. The site has none.
   */
  useBookMenu?: (saga: ShelfBookMenuOptions) => ShelfBookMenu;
}

/** The shelf without the demo saga of the site: nothing is offered. */
export function noDemoOffer(_options: ShelfDemoOfferOptions): ShelfDemoOffer {
  return { element: null, preparing: false };
}

/** The shelf of a build that does not open a saga file: no card. */
export function noSagaFileOffer(): ReactNode {
  return null;
}

/** The shelf of a build whose every volume this tab sends: none goes elsewhere. */
export function noLoadingElsewhere(_projectId: string): boolean {
  return false;
}

const noItems: readonly ShelfBookMenuItem[] = [];

/** The shelf of a build without items of its own in the menu of a book. */
export function noBookMenu(_saga: ShelfBookMenuOptions): ShelfBookMenu {
  return { items: () => noItems, overlay: null };
}

/** The shelf of a build without «Письмо»: no proofreading is offered. */
export function noProofreadOffer(_options: ShelfProofreadOptions): ReactNode {
  return null;
}

const ShelfSlotContext = createContext<ShelfSlot | null>(null);

export const ShelfSlotProvider = ShelfSlotContext.Provider;

export function useShelfSlot() {
  return useContext(ShelfSlotContext);
}
