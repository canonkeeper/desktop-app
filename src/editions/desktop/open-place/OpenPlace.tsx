// SPDX-License-Identifier: AGPL-3.0-only
import { useQuery } from '@tanstack/react-query';
import { useEffect, useRef } from 'react';
import type { EditionPageProps } from '../../../app/edition';
import { isTransientApiFailure } from '../../../entities/api-errors';
import { canonKeeperQueryKeys } from '../../../entities/session/api';
import { ReaderLoadingState, staleWords } from '../../../features/manuscript/reader/ReaderStates';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { isExpectedRevision, type AppRoute } from '../../../shared/navigation/app-route';
import { paragraphNumbered } from '../../../shared/text/place';
import { useToasts } from '../../../ui/feedback/useToastQueue';

/**
 * The page a link `canonkeeper://open?…` opens in the window of the app (specs/2026-11-16-desk-app-links.md AL1.6): the shell
 * gives it the checked values of the link, the page finds the book of the chapter and the N-th paragraph and replaces its
 * address with the address of the reader. The site has no such address.
 */
export const openPlacePath = '/manuscript/open';

/** The words of the lines of the page (AL1.6, Data); «абзаца нет» is the reader's own. */
export const openPlaceWords = {
  noChapter: 'Ссылка ведёт на главу, которой здесь нет: её удалили или сагу перенесли с другого компьютера',
  refused: 'Эту ссылку не открыть: в ней ошибка',
} as const;

const paragraphNumber = /^[1-9]\d{0,4}$/u;

interface Asked {
  saga: string;
  chapter: string;
  paragraph: number | null;
  revision: string | null;
}

/** What the address asks for; nothing when the link had a mistake — the shell said so, or the page sees it itself. */
function askedOf(route: AppRoute): Asked | null {
  const { query } = route;
  const [saga, chapter, paragraph, revision] = [query.get('saga'), query.get('chapter'), query.get('p'), query.get('rev')];
  if (query.has('refused') || !saga || !chapter) return null;
  if (paragraph !== null && !paragraphNumber.test(paragraph)) return null;
  if (revision !== null && !isExpectedRevision(revision)) return null;
  return { saga, chapter, paragraph: paragraph === null ? null : Number(paragraph), revision };
}

/** What the page reads: the chapter, its book, the saga of the link. */
type OpenPlaceClient = Pick<CanonKeeperApiClient, 'getChapter' | 'getBook' | 'getProject'>;

const openPlaceKeys = {
  all: [...canonKeeperQueryKeys.all, 'open-place'] as const,
  chapter: (chapterId: string) => [...openPlaceKeys.all, 'chapter', chapterId] as const,
  book: (bookId: string) => [...openPlaceKeys.all, 'book', bookId] as const,
  saga: (projectId: string) => [...openPlaceKeys.all, 'saga', projectId] as const,
};

function clientOf(meta: unknown) {
  const api = (meta as { openPlaceApi?: OpenPlaceClient } | undefined)?.openPlaceApi;
  if (!api) throw new Error('The page of a link is missing the CanonKeeper API client metadata.');
  return api;
}

/** A failure that a second try may cure; a missing chapter, book or saga is not one. */
function retry(failures: number, error: unknown) {
  return failures < 2 && isTransientApiFailure(error);
}

/**
 * Each read of the page is a read of now: what an earlier link read — the chapter before its paragraphs moved, a chapter
 * since deleted — is read again, and the page decides only by what it read after it opened.
 */
const now = { staleTime: 0, refetchOnMount: 'always', retry } as const;

/** A read the page decides by: done after the page opened and not going on. */
function readNow(query: { isFetchedAfterMount: boolean; isFetching: boolean }) {
  return query.isFetchedAfterMount && !query.isFetching;
}

/**
 * Opens the place of a link: the published text of the chapter, its book — of the saga of the link —, then the reader at the
 * N-th paragraph by its order, with the revision of the link to compare. A chapter that is not here, a book of another saga,
 * a saga that does not read, a link with a mistake: the shelf — of the saga, while it lives — and the line that says why.
 * Only reads.
 */
export function OpenPlace({ api, navigate, route }: Pick<EditionPageProps, 'api' | 'navigate' | 'route'>) {
  const toasts = useToasts();
  const asked = askedOf(route);
  const client = { openPlaceApi: api };
  const chapterId = asked?.chapter ?? '';
  const chapter = useQuery({
    queryKey: openPlaceKeys.chapter(chapterId),
    queryFn: ({ meta }) => clientOf(meta).getChapter(chapterId, 'published'),
    meta: client,
    enabled: asked !== null,
    ...now,
  });
  const chapterNow = readNow(chapter);
  const bookId = chapterNow && !chapter.isError ? chapter.data?.bookId ?? '' : '';
  const book = useQuery({
    queryKey: openPlaceKeys.book(bookId),
    queryFn: ({ meta }) => clientOf(meta).getBook(bookId),
    meta: client,
    enabled: bookId !== '',
    ...now,
  });
  const bookNow = readNow(book);
  const missing = asked !== null && chapterNow
    && (chapter.isError || (bookNow && (book.isError || book.data?.projectId !== asked.saga)));
  const sagaId = asked?.saga ?? '';
  const saga = useQuery({
    queryKey: openPlaceKeys.saga(sagaId),
    queryFn: ({ meta }) => clientOf(meta).getProject(sagaId),
    meta: client,
    enabled: missing,
    ...now,
  });
  const sagaNow = readNow(saga);
  const sagaLives = saga.isSuccess;
  const decided = useRef(false);

  useEffect(() => {
    if (decided.current) return;
    if (!asked) {
      decided.current = true;
      navigate('/manuscript/books', {}, { replace: true });
      toasts.push({ message: openPlaceWords.refused, tone: 'error' });
      return;
    }
    if (missing) {
      if (!sagaNow) return;
      decided.current = true;
      navigate('/manuscript/books', sagaLives ? { projectId: asked.saga } : {}, { replace: true });
      toasts.push({ message: openPlaceWords.noChapter, tone: 'error' });
      return;
    }
    if (!chapterNow || !bookNow || !chapter.data || !book.data) return;
    decided.current = true;
    const paragraph = asked.paragraph === null ? undefined : paragraphNumbered(chapter.data.paragraphs, asked.paragraph);
    navigate('/manuscript/books', {
      projectId: asked.saga,
      bookId: book.data.id,
      chapterId: chapter.data.id,
      mode: 'read',
      ...(paragraph ? { paragraphId: paragraph.id } : {}),
      ...(asked.revision ? { expectedRevision: asked.revision } : {}),
    }, { replace: true });
    // Past the end of the chapter: the reader opens it from its top with the reader's own words; a revision that is not the
    // chapter's says its own line in the reader.
    const sameRevision = asked.revision === null || Number(asked.revision) === chapter.data.revision;
    if (asked.paragraph !== null && !paragraph && sameRevision) toasts.push({ message: staleWords.paragraph, tone: 'info' });
  }, [asked, book.data, bookNow, chapter.data, chapterNow, missing, navigate, sagaLives, sagaNow, toasts]);

  return <ReaderLoadingState />;
}
