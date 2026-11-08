import { z } from 'zod';
import type { EditionRoute } from '../app/edition';
import { isExpectedRevision } from '../shared/navigation/app-route';

export const searchValue = z.string().min(1).max(200).optional();
// The router reads ?image=42 as a number and keeps it one, so the address stays image=42.
export const imageNumberSearch = z.coerce.number().int().min(1).max(999_999_999).optional().catch(undefined);

const notesSearchSchema = z.object({
  source: z.enum(['free', 'chat', 'chapter']).optional(),
  projectId: searchValue,
  bookId: searchValue,
  returnTo: z.string().max(512).optional(),
}).transform((search) => search.projectId ? search : { ...search, bookId: undefined }).catch({});
const chatSearchSchema = z
  .object({
    projectId: searchValue,
    bookId: searchValue,
    chatId: searchValue,
    // The lightbox of a drawing and its panel (illustrations R8.5).
    image: imageNumberSearch,
    panel: z.enum(['info', 'layers']).optional(),
  })
  .catch({});
const sagaSearchSchema = z
  .object({
    projectId: searchValue,
    tab: z.enum(['glossary', 'hero', 'timeline', 'contents']).optional(),
    type: searchValue,
    q: searchValue,
    entry: searchValue,
    character: searchValue,
    chapter: searchValue,
    bookId: searchValue,
    from: searchValue,
    to: searchValue,
    all: z.enum(['1']).optional(),
  })
  .catch({});
// «Память» of the saga: its filters (specs/2026-11-01-writer-assistant-index.md, R4.5).
const memorySearchSchema = z.object({
  projectId: searchValue,
  kind: z.enum(['plan', 'correction', 'style_rule', 'rejected']).optional().catch(undefined),
  q: searchValue,
}).catch({});
const expectedRevisionSearch = z
  .union([
    z.string().refine(isExpectedRevision).transform(Number),
    z.number().refine((value) => Number.isSafeInteger(value) && value > 0),
  ])
  .optional();

const manuscriptSearchSchema = z
  .object({
    chatId: searchValue,
    source: z.string().max(300).optional(),
    sourceMode: z.enum(['fragment', 'chapter', 'current']).optional(),
    projectId: searchValue,
    bookId: searchValue,
    bookMenu: z.enum(['open']).optional(),
    menu: z.enum(['open']).optional(),
    indexing: z.enum(['active']).optional(),
    mode: z.enum(['read', 'draft']).optional(),
    chapterId: searchValue,
    paragraphId: searchValue,
    expectedRevision: expectedRevisionSearch,
    chapters: z.enum(['open']).optional(),
    // «Иллюстрированная книга» and the lightbox of a drawing (illustrations R8.9).
    illustrated: z.coerce.string().pipe(z.enum(['1'])).optional(),
    image: imageNumberSearch,
    panel: z.enum(['info', 'layers']).optional(),
  })
  .catch({});

/** The home: the shell sends a signed-in author on to the shelf once he is known (R1.1). */
export const indexRoute: EditionRoute = { path: '/' };
export const sagaRoute: EditionRoute = { path: '/saga', validateSearch: sagaSearchSchema.parse };

/** The routes of the open sections, the same in every edition (specs/2026-11-07-desktop-app.md DA2.8). */
export const openRoutes: readonly EditionRoute[] = [
  indexRoute,
  { path: '/chat', validateSearch: chatSearchSchema.parse },
  { path: '/notes', validateSearch: notesSearchSchema.parse },
  sagaRoute,
  { path: '/memory', validateSearch: memorySearchSchema.parse },
  { path: '/manuscript/books', validateSearch: manuscriptSearchSchema.parse },
];
