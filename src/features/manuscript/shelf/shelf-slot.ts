import { createContext, useContext, type ComponentType, type ReactNode } from 'react';

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
}

/** The shelf without the demo saga of the site: nothing is offered. */
export function noDemoOffer(_options: ShelfDemoOfferOptions): ShelfDemoOffer {
  return { element: null, preparing: false };
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
