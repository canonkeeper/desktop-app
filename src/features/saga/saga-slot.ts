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

/** «Добавить из файла» of the materials of the author on the site: an Excel or Word file read into records (saga map R9.2). */
export interface MaterialsImport {
  /** The step of a file that takes the panel: the source being added, the import running or waiting for the author. */
  step: ReactNode;
  /** What the last import saved. */
  saved: ReactNode;
  /** The way to add from a file. */
  add: ReactNode;
}

export interface MaterialsImportSlot {
  useImport: (options: { canEdit: boolean; projectId: string }) => MaterialsImport;
}

/** The closed blocks of the saga map (specs/2026-11-07-desktop-app.md DA16.4, DA13.2); an absent one is not shown. */
export interface SagaSlot {
  drawings?: SagaDrawingsSlot;
  materialsImport?: MaterialsImportSlot;
}

const SagaSlotContext = createContext<SagaSlot | null>(null);

export const SagaSlotProvider = SagaSlotContext.Provider;

export function useSagaSlot() {
  return useContext(SagaSlotContext);
}
