import { createContext, useContext, type ReactNode } from 'react';
import type { SagaChapterRef, SourceView } from '../../entities/saga/api';
import type { RouteNavigate } from '../../shared/navigation/app-route';

export interface SagaDrawingsOptions {
  canEdit: boolean;
  navigate: RouteNavigate;
  projectId: string;
  sourceView: SourceView;
}

/** The drawings of the saga map on the site (illustrations R8.7, R11.9, image-workshop IW1.1). */
export interface SagaDrawings {
  /** «Облик по ходу саги» of a node of the map: its references and drawings (saga map R5.5). */
  likeness: (props: { chapters: readonly SagaChapterRef[]; entry: { key: string; name: string; type: string } }) => ReactNode;
  overlays: ReactNode;
}

export interface SagaDrawingsSlot {
  useDrawings: (options: SagaDrawingsOptions) => SagaDrawings;
}

/**
 * «Добавить файл или текст» of the materials of the author on the site: an Excel or Word file or a text read into records
 * (saga map R9.2), on the part «Ваши материалы» of «Что помнит помощник» (specs/2026-11-10-ux2-memory-page.md MP1.3).
 */
export interface MaterialsImport {
  /** The step of a file that takes the part: the source being added, the import running or waiting for the author. */
  step: ReactNode;
  /** What the last import saved. */
  saved: ReactNode;
  /** The way to add a file or a text. */
  add: ReactNode;
}

/**
 * What the import of a file of the materials has for the author (specs/2026-11-11-ux9-shelf-chat.md SC3.6): the file is read
 * now, and how many records of an ended import wait for his check.
 */
export interface MaterialsImportMark {
  running: boolean;
  waiting: number;
}

export interface MaterialsImportSlot {
  /** navigate leads from the block after saving to the map (specs/2026-11-10-ux8-memory-records.md MR3.5). */
  useImport: (options: { canEdit: boolean; navigate: RouteNavigate; projectId: string }) => MaterialsImport;
  /** How many records of an ended import wait for the check of the author: the tab of the part says it (MP1.3). */
  useWaiting: (options: { canEdit: boolean; projectId: string }) => MaterialsImportMark;
}

/**
 * The closed blocks of the saga map and of «Что помнит помощник» (specs/2026-11-07-desktop-app.md DA16.4, DA13.2); an absent
 * one is not shown.
 */
export interface SagaSlot {
  drawings?: SagaDrawingsSlot;
  materialsImport?: MaterialsImportSlot;
}

const SagaSlotContext = createContext<SagaSlot | null>(null);

export const SagaSlotProvider = SagaSlotContext.Provider;

export function useSagaSlot() {
  return useContext(SagaSlotContext);
}
