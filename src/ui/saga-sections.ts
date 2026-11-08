import { createContext, useContext } from 'react';

/** A section of the saga in the menu «Разделы саги». */
export type SagaSection = 'chat' | 'manuscript' | 'reader' | 'notes' | 'saga' | 'writing' | 'drawing' | 'gallery';

/** Every section in the order of the menu. */
export const sagaSections: readonly SagaSection[] = ['manuscript', 'reader', 'chat', 'saga', 'writing', 'drawing', 'gallery', 'notes'];

const SagaSectionsContext = createContext<readonly SagaSection[]>(sagaSections);

/** The edition says which sections its menu has (specs/2026-11-07-desktop-app.md DA2.8, DA16.4). */
export const SagaSectionsProvider = SagaSectionsContext.Provider;

export function useSagaSections() {
  return useContext(SagaSectionsContext);
}
