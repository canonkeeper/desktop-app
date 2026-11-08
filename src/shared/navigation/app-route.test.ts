import { describe, expect, it } from 'vitest';
import { adminWriterIdFromPath, createAppRoute, parseRoute, toHref } from './app-route';
import { manuscriptSourceDestination } from './manuscript-route';

describe('standalone notes navigation', () => {
  it('keeps workspace context separate from note filters and excludes unrelated route state', () => {
    const route = parseRoute(toHref('/notes', {
      projectId: 'filter-saga',
      returnTo: '/chat?projectId=working-saga&chatId=current-chat&sourceView=published&noteId=old-highlight',
    }));
    expect(route.query.get('projectId')).toBe('filter-saga');
    expect(route.query.get('returnTo')).toBe('/chat?projectId=working-saga&chatId=current-chat');
  });

  it.each(['https://foreign.test/chat', '//foreign.test/chat', '/notes?returnTo=/chat', '/auth/login', '/manuscript/unknown', '/chat?unknown=1'])('rejects invalid Notes return context %j', (returnTo) => {
    expect(createAppRoute('/notes', { returnTo }).query.has('returnTo')).toBe(false);
  });
  it('preserves only nonsensitive filters across URL reload', () => {
    const href = toHref('/notes', {
      type: 'note', source: 'chapter', projectId: 'project-1', bookId: 'book-2',
      query: 'private search text', search: 'private search text', cursor: 'opaque', noteId: 'note-1',
    });
    const route = parseRoute(href);
    expect(route.surface).toBe('notes');
    expect(Object.fromEntries(route.query)).toEqual({ source: 'chapter', projectId: 'project-1', bookId: 'book-2' });
    expect(href).not.toContain('private');
  });

  it('retains notes filters through same-origin authentication returnTo', () => {
    const route = createAppRoute('/auth/login', { returnTo: '/notes?source=chat&projectId=p&query=private' });
    expect(route.query.get('returnTo')).toBe('/notes?source=chat&projectId=p');
    expect(createAppRoute('/auth/login', { returnTo: 'https://foreign.test/notes' }).query.has('returnTo')).toBe(false);
  });

  it('opens an old chat link with a text version as the same link without it', () => {
    const route = createAppRoute('/chat', { projectId: 'saga-a', chatId: 'chat-a', sourceView: 'draft' });
    expect(route.invalidQuery).toBe(false);
    expect(Object.fromEntries(route.query)).toEqual({ projectId: 'saga-a', chatId: 'chat-a' });
    expect(Object.fromEntries(createAppRoute('/manuscript/books', { projectId: 'saga-a', sourceView: 'published' }).query)).toEqual({ projectId: 'saga-a' });
  });

  it('drops invalid enums and a book without its project', () => {
    expect(Object.fromEntries(createAppRoute('/notes', { type: 'bad', source: 'bad', bookId: 'book-2' }).query)).toEqual({});
    expect(createAppRoute('/notes/unknown').surface).toBe('unknown');
  });
});

describe('exact source revision navigation', () => {
  it.each(['draft', 'published'] as const)('keeps the %s revision through destination, URL and reload', (targetView) => {
    const destination = manuscriptSourceDestination({
      projectId: 'project-1', bookId: 'book-1', chapterId: 'chapter-1',
      targetView, revision: 7, paragraphId: 'paragraph-18',
    });
    expect(destination.query.expectedRevision).toBe('7');
    const route = parseRoute(toHref(destination.path, destination.query));
    expect(route.invalidQuery).toBe(false);
    expect(route.query.get('expectedRevision')).toBe('7');
    expect(route.query.get('paragraphId')).toBe('paragraph-18');
    expect(route.query.get('mode')).toBe('read');
    expect(route.query.has('sourceView')).toBe(false);
  });

  it.each(['', '0', '-1', '1.5', '1e3', '07', 'wrong', '9007199254740992', '1'.repeat(201)])('marks invalid expected revision %j instead of a normal landing', (expectedRevision) => {
    const route = createAppRoute('/manuscript/books', { chapterId: 'chapter-1', expectedRevision });
    expect(route.invalidQuery).toBe(true);
    expect(route.query.has('expectedRevision')).toBe(false);
    expect(route.query.get('chapterId')).toBe('chapter-1');
  });

  it('leaves ordinary chapter navigation without expected source context unchanged', () => {
    const route = parseRoute(toHref('/manuscript/books', { chapterId: 'chapter-1', mode: 'read' }));
    expect(route.invalidQuery).toBe(false);
    expect(route.query.has('expectedRevision')).toBe(false);
  });

  it('preserves Reader focus and mobile View presentation state', () => {
    const route = parseRoute(toHref('/manuscript/books', { chapterId: 'chapter-1', focus: '1', mode: 'read', view: 'open' }));
    expect(route.query.get('focus')).toBe('1');
    expect(route.query.get('view')).toBe('open');
  });

  it('preserves a complete UUID source target through the existing auth return route', () => {
    const destination = manuscriptSourceDestination({
      projectId: '11111111-1111-4111-8111-111111111111',
      bookId: '22222222-2222-4222-8222-222222222222',
      chapterId: '33333333-3333-4333-8333-333333333333',
      paragraphId: '44444444-4444-4444-8444-444444444444',
      targetView: 'published', revision: 7,
    });
    const sourceHref = toHref(destination.path, destination.query);
    const auth = parseRoute(toHref('/auth/login', { returnTo: sourceHref }));
    expect(auth.query.get('returnTo')).toBe(sourceHref);
  });

  it('does not turn invalid expected source context into an ordinary generated link', () => {
    expect(() => toHref('/manuscript/books', { chapterId: 'chapter-1', expectedRevision: '0' })).toThrow('Invalid expected source revision');
    const auth = createAppRoute('/auth/login', { returnTo: '/manuscript/books?chapterId=chapter-1&expectedRevision=0' });
    expect(auth.query.has('returnTo')).toBe(false);
  });

  it.each([
    'https://example.test/manuscript/books',
    '//example.test/manuscript/books',
    '/auth/register',
    '/%2f%2fexample.test',
    '/manuscript/books?chapterId=chapter-1&expectedRevision=%ZZ',
    `/manuscript/books?projectId=${'a'.repeat(512)}`,
  ])('keeps the bounded same-origin auth return guard for %j', (returnTo) => {
    expect(createAppRoute('/auth/login', { returnTo }).query.has('returnTo')).toBe(false);
  });
});

describe('admin panel navigation (admin panel R6.1)', () => {
  it('owns /admin and the writer card; the tab, period and stage live in the URL', () => {
    const route = parseRoute(toHref('/admin', { tab: 'days', days: '30', stage: 'asking', q: 'mira@example.com' }));
    expect(route.surface).toBe('admin');
    expect(Object.fromEntries(route.query)).toEqual({ tab: 'days', days: '30', stage: 'asking' });
    expect(createAppRoute('/admin/writers/11111111-0000-4000-8000-000000000002').surface).toBe('admin');
    expect(adminWriterIdFromPath('/admin/writers/11111111-0000-4000-8000-000000000002')).toBe('11111111-0000-4000-8000-000000000002');
  });

  it('drops unknown tabs and periods and leaves other admin paths unknown', () => {
    expect(Object.fromEntries(createAppRoute('/admin', { tab: 'delete', days: '366', stage: 'other' }).query)).toEqual({});
    expect(createAppRoute('/admin/writers').surface).toBe('unknown');
    expect(createAppRoute('/admin/writers/a/b').surface).toBe('unknown');
    expect(adminWriterIdFromPath('/admin')).toBeNull();
  });
});
