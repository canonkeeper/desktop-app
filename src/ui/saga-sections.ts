import { Brain, Images, Library, MessageSquare, PenLine, StickyNote, Trash2, Waypoints, type LucideIcon } from 'lucide-react';
import { createContext, useContext } from 'react';

/**
 * A section of the menu of the author (specs/2026-11-10-ux2-shell.md S2.1): a page of the saga, or «Недавно удалённые»,
 * which opens its window over the page where the author is.
 */
export type SagaSection = 'manuscript' | 'saga' | 'memory' | 'chat' | 'writing' | 'drawing' | 'notes' | 'deleted';

/** The groups of the menu, in their order: «Сага», «Работа», «Моё». */
export type SectionGroup = 'saga' | 'work' | 'mine';

export interface SectionEntry {
  section: SagaSection;
  group: SectionGroup;
  /** The word of the menu, the same word the top bar names the section by (S1.2). */
  label: string;
  /** One icon for a section in every place (П-15). */
  Icon: LucideIcon;
  /** The page of the section; null — a window over the page («Недавно удалённые», S2.4). */
  path: string | null;
}

/**
 * The one table of the sections (specs/2026-11-10-ux2-shell-core.md SC2.1): the column, the narrow column, the drawer of a
 * phone and the name of the section in the top bar are built by it. «Чтения» here is not: a book opens from the shelf
 * (S2.2).
 */
export const sectionTable: readonly SectionEntry[] = [
  { section: 'manuscript', group: 'saga', label: 'Книги', Icon: Library, path: '/manuscript/books' },
  { section: 'saga', group: 'saga', label: 'Карта', Icon: Waypoints, path: '/saga' },
  { section: 'memory', group: 'saga', label: 'Что помнит помощник', Icon: Brain, path: '/memory' },
  { section: 'chat', group: 'work', label: 'Чат', Icon: MessageSquare, path: '/chat' },
  { section: 'writing', group: 'work', label: 'Письмо', Icon: PenLine, path: '/writing' },
  // One section of the drawings in place of «Рисование» and «Галерея» (specs/2026-11-10-ux6-drawings-section.md DS1.1).
  { section: 'drawing', group: 'work', label: 'Рисунки', Icon: Images, path: '/drawing' },
  { section: 'notes', group: 'mine', label: 'Заметки', Icon: StickyNote, path: '/notes' },
  { section: 'deleted', group: 'mine', label: 'Недавно удалённые', Icon: Trash2, path: null },
];

/** The groups with their names, in the order of the menu. */
export const sectionGroups: readonly { group: SectionGroup; label: string }[] = [
  { group: 'saga', label: 'Сага' },
  { group: 'work', label: 'Работа' },
  { group: 'mine', label: 'Моё' },
];

/** The row of a section. */
export function sectionEntry(section: SagaSection) {
  return sectionTable.find((entry) => entry.section === section)!;
}

/** Every section of the site, in the order of the menu. */
export const sagaSections: readonly SagaSection[] = sectionTable.map((entry) => entry.section);

const SagaSectionsContext = createContext<readonly SagaSection[]>(sagaSections);

/** The edition says which sections its menu has (specs/2026-11-07-desktop-app.md DA2.8, DA16.4). */
export const SagaSectionsProvider = SagaSectionsContext.Provider;

export function useSagaSections() {
  return useContext(SagaSectionsContext);
}
