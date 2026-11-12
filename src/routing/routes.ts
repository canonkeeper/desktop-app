import { redirect } from '@tanstack/react-router';
import { z } from 'zod';
import type { EditionRoute } from '../app/edition';
import { isExpectedRevision, toHref } from '../shared/navigation/app-route';

export const searchValue = z.string().min(1).max(200).optional();
// The router reads ?image=42 as a number and keeps it one, so the address stays image=42.
export const imageNumberSearch = z.coerce.number().int().min(1).max(999_999_999).optional().catch(undefined);

const notesSearchSchema = z.object({
  source: z.enum(['free', 'chat', 'chapter']).optional(),
  projectId: searchValue,
  bookId: searchValue,
  sagas: z.enum(['all']).optional(),
  note: searchValue,
  returnTo: z.string().max(512).optional(),
}).transform((search) => search.sagas ? { ...search, bookId: undefined } : search).catch({});
const chatSearchSchema = z
  .object({
    projectId: searchValue,
    bookId: searchValue,
    chatId: searchValue,
    // A new chat the author asked for: /chat without a chat opens the latest one (specs/2026-11-11-ux4-after-answer.md AA2.1).
    new: z.enum(['1']).optional(),
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
// «Что помнит помощник»: the open part and the filters of the records (specs/2026-11-10-ux2-memory-page.md MP1.1;
// specs/2026-11-01-writer-assistant-index.md, R4.5).
const memorySearchSchema = z.object({
  projectId: searchValue,
  part: z.enum(['memory', 'materials']).optional().catch(undefined),
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
    menu: z.enum(['open']).optional(),
    indexing: z.enum(['active']).optional(),
    mode: z.enum(['read', 'draft']).optional(),
    chapterId: searchValue,
    paragraphId: searchValue,
    expectedRevision: expectedRevisionSearch,
    chapters: z.enum(['open']).optional(),
    // «Иллюстрированная книга» and the lightbox of a drawing (illustrations R8.9). The router reads ?illustrated=1 as a
    // number and keeps it one: kept as a string it would be written back in quotes, and a chapter opened by its address
    // or read again would lose the switch (specs/2026-11-10-ux1-drawing.md DX4.1).
    illustrated: z.coerce.number().pipe(z.literal(1)).optional().catch(undefined),
    image: imageNumberSearch,
    panel: z.enum(['info', 'layers']).optional(),
  })
  .catch({});

/** The home: the shell sends a signed-in author on to the shelf once he is known (R1.1). */
export const indexRoute: EditionRoute = { path: '/' };
/**
 * The saga map. Its panel of the materials left for «Что помнит помощник» (specs/2026-11-10-ux2-memory-page.md MP1.6): an
 * old link of it opens the part «Ваши материалы» of the same saga.
 */
export const sagaRoute: EditionRoute = {
  path: '/saga',
  validateSearch: sagaSearchSchema.parse,
  beforeLoad: ({ location }) => {
    const old = new URLSearchParams(location.searchStr);
    if (!old.has('materials')) return;
    throw redirect({ href: toHref('/memory', { projectId: old.get('projectId') ?? undefined, part: 'materials' }), replace: true });
  },
};

/** The routes of the open sections, the same in every edition (specs/2026-11-07-desktop-app.md DA2.8). */
export const openRoutes: readonly EditionRoute[] = [
  indexRoute,
  { path: '/chat', validateSearch: chatSearchSchema.parse },
  { path: '/notes', validateSearch: notesSearchSchema.parse },
  sagaRoute,
  { path: '/memory', validateSearch: memorySearchSchema.parse },
  { path: '/manuscript/books', validateSearch: manuscriptSearchSchema.parse },
];
