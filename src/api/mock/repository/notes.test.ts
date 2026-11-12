import { describe, expect, it } from 'vitest';
import type { NotesListOptions } from '../../../shared/api';
import { createMockRepositoryContext } from './context';
import { createNotesOperations } from './notes';

function setup() {
  const context = createMockRepositoryContext();
  return { context, api: createNotesOperations(context) };
}

describe('Notes V2 mock contract', () => {
  it('keeps lightweight pages, searches full content before pagination, and binds cursors to filters and account', async () => {
    const { context, api } = setup();
    const ownerUserId = context.currentPrincipalId();
    context.state.personalNotes = Array.from({ length: 320 }, (_, index) => ({
      id: `note-${String(index).padStart(3, '0')}`, ownerUserId, title: `Запись ${index}`, body: `Мысль ${index} ${'я'.repeat(250)}${index === 0 ? ' ИСКОМОЕ %_ тело' : ''}`,
      source: null, revision: 1, createdAt: '2026-09-19T12:00:00.000Z', updatedAt: '2026-09-19T12:00:00.000Z',
    }));
    const first = await api.listNotes({ source: 'free' });
    expect(first.data).toHaveLength(30);
    expect(first.meta).toMatchObject({ hasMore: true, totalCount: 320 });
    expect(Object.keys(first.data[0]!).sort()).toEqual(['id', 'preview', 'sourceKind', 'title', 'updatedAt']);
    expect(Array.from(first.data[0]!.preview)).toHaveLength(240);
    const second = await api.listNotes({ source: 'free', cursor: first.meta.nextCursor! });
    expect(new Set([...first.data, ...second.data].map((entry) => entry.id)).size).toBe(60);
    const found = await api.listNotes({ query: '  искомое %_  ', source: 'free' });
    expect(first.data.map((entry) => entry.id)).not.toContain('note-000');
    expect(found.data.map((entry) => entry.id)).toEqual(['note-000']);
    expect(found.meta).toEqual({ hasMore: false, nextCursor: null, totalCount: 1 });
    await expect(api.listNotes({ query: 'другой', source: 'free', cursor: first.meta.nextCursor! })).rejects.toMatchObject({ status: 400 });
    context.state.session!.user.id = 'other-account';
    await expect(api.listNotes({ source: 'free', cursor: first.meta.nextCursor! })).rejects.toMatchObject({ status: 400 });
    expect((await api.listNotes({ source: 'free' })).meta.totalCount).toBe(0);
    await expect(api.getPersonalNote('note-000')).rejects.toMatchObject({ status: 404 });
  });

  it('lists only the account own notes and hides revoked source metadata', async () => {
    const { context, api } = setup();
    const fixture = context.state.personalNotes[0]!;
    if (fixture.source?.kind !== 'chapter') throw new Error('missing chapter note fixture');
    const { locator } = fixture.source;
    context.setScenario({ preset: 'normal', actorRole: 'viewer' });
    const note = await api.createPersonalNote({ body: 'Моя копия', source: { kind: 'chapter', quote: 'Копия', locator } }, { idempotencyKey: 'idem-mock-note-private' });
    const found = await api.listNotes({ query: 'моя копия', source: 'chapter', projectId: locator.projectId, bookId: locator.bookId });
    expect(found.data).toEqual([expect.objectContaining({ id: note.id, sourceKind: 'chapter', bookTitle: expect.any(String) })]);
    context.setScenario({ preset: 'normal', actorRole: 'non_member' });
    const personal = (await api.listNotes({ query: 'Моя копия' })).data[0]!;
    expect(personal).not.toHaveProperty('projectTitle');
    expect(personal).not.toHaveProperty('bookTitle');
    expect((await api.listNotes({ projectId: locator.projectId })).meta.totalCount).toBe(0);
    context.state.session!.user.id = 'other-account';
    expect((await api.listNotes()).data).toEqual([]);
  });

  it('supports Unicode title-only patches, omitted-title preservation, explicit clearing and revision protection', async () => {
    const { api } = setup();
    const note = await api.createPersonalNote({ body: 'Текст' }, { idempotencyKey: 'idem-mock-note-title' });
    expect(note.title).toBe('');
    const titled = await api.updatePersonalNote(note.id, { title: '🙂'.repeat(200), expectedRevision: 1 });
    expect(titled).toMatchObject({ body: 'Текст', title: '🙂'.repeat(200), revision: 2 });
    const updated = await api.updatePersonalNote(note.id, { body: 'Правка', expectedRevision: 2 });
    expect(updated.title).toBe(titled.title);
    expect((await api.listNotes({ query: '🙂' })).data.map((entry) => entry.id)).toEqual([note.id]);
    await expect(api.updatePersonalNote(note.id, { title: 'x', expectedRevision: 1 })).rejects.toMatchObject({ status: 409 });
    await expect(api.updatePersonalNote(note.id, { title: '🙂'.repeat(201), expectedRevision: 3 })).rejects.toMatchObject({ status: 422 });
    await expect(api.updatePersonalNote(note.id, { body: 'x'.repeat(100001), expectedRevision: 3 })).rejects.toMatchObject({ status: 422 });
    await expect(api.updatePersonalNote(note.id, { expectedRevision: 3 })).rejects.toMatchObject({ status: 422 });
    const cleared = await api.updatePersonalNote(note.id, { title: '', expectedRevision: 3 });
    expect(cleared).toMatchObject({ title: '', body: 'Правка', revision: 4 });
  });

  it('keeps captured source and caller query immutable', async () => {
    const { context, api } = setup();
    const fixture = context.state.personalNotes[0]!;
    if (fixture.source?.kind !== 'chapter') throw new Error('missing chapter note fixture');
    const source = { kind: 'chapter' as const, locator: structuredClone(fixture.source.locator), quote: 'Снимок' };
    const note = await api.createPersonalNote({ title: 'Название', body: 'Копия', source }, { idempotencyKey: 'idem-note-frozen-source' });
    source.quote = 'Поздняя правка';
    source.locator.revision += 1;
    await expect(api.getPersonalNote(note.id)).resolves.toEqual(note);
    const query = Object.freeze({ query: '  НАЗВАНИЕ ', limit: 30, source: 'chapter' as const });
    expect((await api.listNotes(query)).data.map((entry) => entry.id)).toEqual([note.id]);
    expect(query.query).toBe('  НАЗВАНИЕ ');
  });

  it('keeps the quote apart, names the place and keeps the own thoughts under a saga (specs/2026-11-11-ux7-notes.md NT1.2, NT1.3, NT2.4, NT3.2)', async () => {
    const { context, api } = setup();
    const fixture = context.state.personalNotes[0]!;
    if (fixture.source?.kind !== 'chapter') throw new Error('missing chapter note fixture');
    const { locator } = fixture.source;
    const words = 'Белая пыль над портом';
    const quoted = await api.createPersonalNote({ body: '', source: { kind: 'chapter', quote: words, locator } }, { idempotencyKey: 'idem-note-quote-only' });
    expect(quoted).toMatchObject({ body: '', source: { quote: words } });
    await expect(api.createPersonalNote({ body: ' ' }, { idempotencyKey: 'idem-note-own-blank' })).rejects.toMatchObject({ status: 422 });
    const own = await api.createPersonalNote({ body: 'Своя мысль' }, { idempotencyKey: 'idem-note-own' });
    await expect(api.updatePersonalNote(own.id, { body: '', expectedRevision: 1 })).rejects.toMatchObject({ status: 422 });
    await expect(api.updatePersonalNote(quoted.id, { body: '', expectedRevision: 1 })).resolves.toMatchObject({ body: '', revision: 2 });
    const saga = await api.listNotes({ projectId: locator.projectId });
    expect(saga.data.map((entry) => entry.id).sort()).toEqual([quoted.id, own.id, fixture.id].sort());
    const entry = saga.data.find((item) => item.id === quoted.id)!;
    expect(entry).toMatchObject({ preview: '', quote: words, projectTitle: expect.any(String), place: expect.stringMatching(/^Книга \d+, гл\. /u) });
    expect((await api.listNotes({ query: 'пыль над' })).data.map((item) => item.id)).toEqual([quoted.id]);
    await expect(api.resolvePersonalNoteSource(quoted.id)).resolves.toMatchObject({ state: 'current', place: entry.place });
    context.state.chapters = context.state.chapters.filter((chapter) => chapter.id !== locator.chapterId);
    const gone = (await api.listNotes({ projectId: locator.projectId, source: 'chapter' })).data.find((item) => item.id === quoted.id)!;
    expect(gone).toMatchObject({ quote: words, projectTitle: entry.projectTitle });
    expect(gone).not.toHaveProperty('place');
    await expect(api.resolvePersonalNoteSource(quoted.id)).resolves.toEqual({ state: 'unavailable' });
  });

  it.each([
    { query: 'я'.repeat(201) }, { type: 'note' }, { source: 'other' }, { bookId: 'book-02' }, { projectId: 2 }, { limit: 0 }, { extra: 'field' }, null, [],
  ])('rejects invalid query input %j', async (input) => {
    await expect(setup().api.listNotes(input as NotesListOptions)).rejects.toMatchObject({ status: 400 });
  });
});
