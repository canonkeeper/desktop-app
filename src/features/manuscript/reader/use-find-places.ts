import { useState } from 'react';
import type { ChapterListItem, ReaderChapter } from '../../../entities/manuscript/api';
import type { TextMark } from '../../../ui/ReaderParagraph';
import { chapterWords, type ContentsPlace } from './ReaderContents';
import type { FindBarState } from './ReaderFindBar';
import { minFindLength, snippet, type FoundPlace } from './reader-find';
import type { BookFind } from './use-book-find';

const keyOf = (place: FoundPlace) => `${place.chapterId}:${place.paragraphId}:${place.start}`;

/**
 * The current place of «Найти» among the places of the book (RD2.1, RD2.2): the first place of the open chapter when the
 * search starts, the next and the previous by «›» and «‹» across the chapters of the book, a place of «Найдено» by a press.
 * Which place is current is a fact of the screen; the words searched are in the address.
 */
export function useFindPlaces({ chapter, find, findQuery }: { chapter: ReaderChapter | null; find: BookFind; findQuery: string | null }) {
  const [chosen, setChosen] = useState<{ query: string; key: string } | null>(null);
  const places = find.state === 'ready' ? find.places : [];
  const query = findQuery ?? '';
  const chosenPlace = chosen?.query === query ? places.find((place) => keyOf(place) === chosen.key) : undefined;
  const current = chosenPlace ?? firstPlace(places, chapter);
  const index = current ? places.indexOf(current) : -1;

  const choose = (key: string) => {
    const place = places.find((item) => keyOf(item) === key) ?? null;
    if (place) setChosen({ query, key });
    return place;
  };
  const step = (by: 1 | -1) => {
    if (!places.length) return null;
    const next = places[((index < 0 ? (by === 1 ? -1 : 0) : index) + by + places.length) % places.length]!;
    setChosen({ query, key: keyOf(next) });
    return next;
  };

  const marks = new Map<string, TextMark[]>();
  for (const place of places) {
    if (place.chapterId !== chapter?.id) continue;
    marks.set(place.paragraphId, [...(marks.get(place.paragraphId) ?? []), { start: place.start, end: place.end, current: place === current }]);
  }

  const bar: FindBarState = query.trim().length < minFindLength ? { kind: 'typing' }
    : find.state === 'failed' ? { kind: 'failed', retry: find.retry }
      : find.state !== 'ready' ? { kind: 'searching' }
        : !places.length ? (find.complete ? { kind: 'none' } : { kind: 'searching' })
          : { kind: 'found', current: index + 1, total: places.length, complete: find.complete };

  /** The places in words for «Найдено»: «гл. 12 «Белый порт», абзац 3» and a piece of the text around the words. */
  const list = (chapters: readonly ChapterListItem[]): ContentsPlace[] => {
    if (find.state !== 'ready') return [];
    return places.map((place) => {
      const listed = chapters.find((item) => item.id === place.chapterId);
      const text = find.texts.get(place.chapterId)?.paragraphs.find((paragraph) => paragraph.id === place.paragraphId)?.text ?? '';
      return {
        key: keyOf(place),
        label: `${listed ? chapterWords(listed) : 'глава'}, абзац ${place.paragraph}`,
        ...snippet(text, place),
        current: place === current,
      };
    });
  };

  return { marks: query ? marks : undefined, bar, step, choose, list, current };
}

/** Where a search starts: the first place of the open chapter, else the first of the book. */
function firstPlace(places: readonly FoundPlace[], chapter: ReaderChapter | null) {
  return places.find((place) => place.chapterId === chapter?.id) ?? places[0];
}
