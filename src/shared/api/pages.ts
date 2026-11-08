/** A page of a cursor list as the contract gives it. */
export interface CursorPage<TItem> {
  data: TItem[];
  meta: { hasMore: boolean; nextCursor: string | null };
}

/** Pages of one list a read follows before it fails: 10 000 items at the default page of 50. */
const maxListPages = 200;

/**
 * Every page of a cursor list in the order of the server, as one list, for a page that needs the whole list:
 * the reader, the shelf, the place of a chapter, the deleted sources (specs/2026-11-07-chapter-list-paging.md, P2).
 * Pages are of the default size, so the first request is the very request of a single page. A repeated cursor
 * or too many pages fail the read: a list is never cut silently (P2.2).
 */
export async function readAllPages<TPage extends CursorPage<unknown>>(
  readPage: (cursor: string | undefined) => Promise<TPage>,
): Promise<TPage> {
  const data: TPage['data'][number][] = [];
  const seen = new Set<string>();
  let cursor: string | undefined;
  for (let pages = 0; pages < maxListPages; pages += 1) {
    const page = await readPage(cursor);
    data.push(...page.data);
    if (!page.meta.hasMore) return { ...page, data, meta: { hasMore: false, nextCursor: null } };
    const next = page.meta.nextCursor;
    if (!next || seen.has(next)) throw new Error('A list page has more items but no new cursor.');
    seen.add(next);
    cursor = next;
  }
  throw new Error(`A list has more than ${maxListPages} pages.`);
}
