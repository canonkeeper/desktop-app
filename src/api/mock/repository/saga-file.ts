import type { Book, CanonKeeperApiClient, Chapter, OperationResponse, Project, RequiredIdempotencyOptions, Schema } from '../../../shared/api';
import { bytesOf, zipFiles, zipText } from '../../../shared/files/zip';
import { MockApiError } from '../scenarios';
import { clone, now } from './common';
import type { MockRepositoryContext } from './context';
import { chapterId as templateChapterId, createProjectMembershipForUser } from './fixtures';
import { chapterKey, chapterPlaces } from './places';

type SagaFileOperations = Pick<CanonKeeperApiClient, 'getSagaArchive' | 'openSagaArchive' | 'listSagaTranscripts' | 'getSagaTranscript'>;
type Report = Schema<'SagaArchiveReport'>;

const encoder = new TextEncoder();
const json = (value: unknown) => encoder.encode(JSON.stringify(value));

/** The leftOut of a file that names nothing. */
const nothingLeftOut: Report['leftOut'] = { drafts: 0, drawings: 0, trashedBooks: 0, trashedChapters: 0, othersTurns: 0 };

/** A name of a file without the signs a system refuses in one, as the server names the file of a book. */
const fileName = (title: string) => `${title.replace(/[\\/:*?"<>|]+/gu, ' ').replace(/\s+/gu, ' ').trim() || 'Сага'}.cksaga`;

/** «13 ноября» of the day a file was made, by the clock of Moscow, as the server names a saga opened twice. */
function dayOf(at: string) {
  return new Intl.DateTimeFormat('ru-RU', { day: 'numeric', month: 'long', timeZone: 'Europe/Moscow' }).format(new Date(at));
}

/** A cheap print of the bytes of a file: one file — one key of a repeat (the server takes the SHA-256 of the file). */
function printOf(bytes: Uint8Array) {
  let hash = 2166136261;
  for (const byte of bytes) {
    hash ^= byte;
    hash = Math.imul(hash, 16777619);
  }
  return `${bytes.length}:${(hash >>> 0).toString(16)}`;
}

function refuse(status: number, code: string, detail: string): never {
  throw new MockApiError(status, detail, 'openSagaArchive', code);
}

/**
 * The saga file of the mock (specs/2026-11-13-desk-sagafile.md SF1–SF3, SF3.8; specs/2026-11-13-desk-sagafile-web.md):
 * «Сохранить сагу в файл» gives a small zip with the name of the saga and its manifest; «Открыть файл саги» reads such a
 * zip — not a zip is `not_archive`, a zip without the manifest `damaged`, a manifest of a newer version `newer_version` — and
 * opens a new saga of one ready book with two chats a file brought: a saga of this name already there makes the new one
 * «… — из файла от …», and what the manifest says was left out comes into the report as the server writes it.
 */
export function createSagaFileOperations(context: MockRepositoryContext): SagaFileOperations {
  return {
    async getSagaArchive(projectId) {
      context.applyOperation('getSagaArchive');
      const project = context.projectFor(projectId, 'getSagaArchive');
      if (context.actorRole(projectId) !== 'owner') throw new MockApiError(403, 'only the owner saves a saga into a file', 'getSagaArchive', 'forbidden');
      const createdAt = new Date().toISOString();
      const bytes = zipFiles([
        { name: 'saga.json', bytes: json({ name: project.title, description: project.description ?? '', bookKeyNumbers: project.bookCount }) },
        {
          name: 'manifest.json', bytes: json({
            format: 'canonkeeper-saga', version: 1, createdBy: 'CanonKeeper', createdAt, saga: { name: project.title },
            counts: { books: project.bookCount, chapters: project.chapterCount, cards: 0, materials: 0, memory: 0, notes: 0, chats: 0 },
            chatsNotRead: [], leftOut: nothingLeftOut, sections: [], files: {},
          }),
        },
      ]);
      return { blob: new Blob([bytes.slice()], { type: 'application/zip' }), fileName: fileName(project.title) };
    },

    async openSagaArchive(file, options: RequiredIdempotencyOptions) {
      context.applyOperation('openSagaArchive');
      const bytes = await bytesOf(file);
      return context.idempotentCommand('openSagaArchive', 'saga-archives', options, { file: printOf(bytes) }, async () => {
        if (bytes.length < 4 || bytes[0] !== 0x50 || bytes[1] !== 0x4b) refuse(422, 'saga_archive.not_archive', 'the file is not a saga file this app opens');
        const manifestText = await zipText(bytes, 'manifest.json');
        if (manifestText === undefined) refuse(422, 'saga_archive.damaged', 'the file is not a saga file this app opens');
        const manifest = JSON.parse(manifestText) as {
          version?: number; createdAt?: string; saga?: { name?: string };
          chatsNotRead?: string[]; leftOut?: Report['leftOut']; sections?: string[];
        };
        if ((manifest.version ?? 1) > 1) refuse(422, 'saga_archive.newer_version', 'the file is not a saga file this app opens');
        const name = manifest.saga?.name?.trim() || file.name.replace(/\.cksaga$/iu, '');
        const taken = context.state.projects.some((project) => project.title === name);
        const title = taken ? `${name} — из файла от ${dayOf(manifest.createdAt ?? now)}` : name;
        const project = openedSaga(context, title);
        return {
          projectId: project.id, name: title, renamed: taken,
          report: {
            books: 1, chapters: 2, cards: 2, chaptersWithoutCards: { count: 0, places: [] }, namesFresh: true, threads: false,
            materials: 0, memory: 0, notes: 0, chats: 2, chatsNotRead: manifest.chatsNotRead ?? [], coversSkipped: [],
            leftOut: manifest.leftOut ?? nothingLeftOut, sections: manifest.sections ?? [],
          },
        } satisfies OperationResponse<'openSagaArchive', '201'>;
      });
    },

    async listSagaTranscripts(projectId) {
      context.applyOperation('listSagaTranscripts');
      context.projectFor(projectId, 'listSagaTranscripts');
      const data = context.state.sagaTranscripts.filter((chat) => chat.projectId === projectId)
        .sort((left, right) => right.lastTurnAt.localeCompare(left.lastTurnAt))
        .map(({ id, title, createdAt, lastTurnAt, turns, othersTurns }) => ({ id, title, createdAt, lastTurnAt, turns: turns.length, othersTurns }));
      return { data };
    },

    async getSagaTranscript(projectId, transcriptId) {
      context.applyOperation('getSagaTranscript');
      context.projectFor(projectId, 'getSagaTranscript');
      const chat = context.state.sagaTranscripts.find((item) => item.projectId === projectId && item.id === transcriptId);
      if (!chat) throw new MockApiError(404, 'chat not found', 'getSagaTranscript', 'not_found');
      // The places are those of the saga now, as the server writes them beside the keys of the answers (SF3.8).
      const places = chapterPlaces(context, projectId);
      const keys = new Set(chat.turns.flatMap((turn) => [...(turn.answer ?? '').matchAll(/source:([a-z0-9]+)\//gu)].map((match) => match[1]!)));
      const { projectId: _projectId, ...transcript } = clone(chat);
      return { ...transcript, places: Object.fromEntries([...keys].flatMap((key) => (places.has(key) ? [[key, places.get(key)!]] : []))) };
    },
  };
}

/**
 * The new saga of an opened file: one book «Белый порт» with two chapters, read and known to the helper, and two chats the
 * file brought — one with the turns that have no answer and a chapter attached to a question (board 44·38).
 */
function openedSaga(context: MockRepositoryContext, title: string): Project {
  const taken = new Set([...context.state.projects.map((project) => project.id), ...context.state.deletedProjects.map((source) => source.resource.id)]);
  let number = context.state.projects.length + 1;
  while (taken.has(`project-${number}`)) number += 1;
  const id = `project-${number}`;
  const base = { id, title, description: null, bookCount: 1, chapterCount: 2, wordCount: 0, createdAt: now, updatedAt: now };
  const membershipId = `member-created-${id}`;
  const project: Project = { ...base, currentMembership: createProjectMembershipForUser(base, context.state.user, membershipId, 'owner') };
  context.state.projects.push(project);
  context.state.memberships.push(createProjectMembershipForUser(project, context.state.user, membershipId, 'owner'));
  const template = context.state.chapters.find((chapter) => chapter.id === templateChapterId);
  const bookId = `book-file-${id}`;
  const book: Book = {
    id: bookId, projectId: id, title: 'Белый порт', subtitle: null, order: 1, place: 1, displayNumber: '1', coverColor: null,
    coverUrl: null, status: 'ready', chapterCount: 2, wordCount: (template?.wordCount ?? 0) * 2, createdAt: now, updatedAt: now,
  };
  context.state.books.push(book);
  const chapters: Chapter[] = template ? [{ order: 12, title: 'Белый порт' }, { order: 14, title: 'Колокол' }].map(({ order, title: chapterTitle }) => ({
    ...clone(template), id: `${bookId}-chapter-${order}`, bookId, title: chapterTitle, order, status: 'published', publishedAt: now,
    paragraphs: template.paragraphs.map((paragraph) => ({ ...clone(paragraph), id: `${bookId}-${order}-p-${paragraph.order}` })),
    navigation: { ...clone(template.navigation), total: 2, previousChapterId: null, nextChapterId: null, previous: null, next: null },
  })) : [];
  context.state.chapters.push(...chapters);
  for (const prepared of [context.state.indexedChapterIds, context.state.cardedChapterIds]) {
    for (const chapter of chapters) {
      prepared.draft.push(chapter.id);
      prepared.published.push(chapter.id);
    }
  }
  // The cards of the file come with its chapters, as the report says (`cards: 2`): each carries the card of the chapter it
  // copies — the map of the opened saga is that of a saga the helper has read.
  for (const chapter of chapters) context.state.cardsFromFile[chapter.id] = templateChapterId;
  Object.assign(project, { wordCount: book.wordCount });
  const port = chapterKey(1, 12);
  const bell = chapterKey(1, 14);
  context.state.sagaTranscripts.push(
    {
      projectId: id, id: `transcript-${id}-1`, title: 'Мара и северная пристань', createdAt: '2026-10-12T15:10:00.000Z',
      lastTurnAt: '2026-10-12T15:40:00.000Z', othersTurns: 3, places: {},
      turns: [
        {
          askedAt: '2026-10-12T15:10:00.000Z', question: 'Кто первым заметил дым в Белом порту?', outcome: 'answered', attached: 'Белый порт',
          answer: `Первой дым заметила Мара — с северной пристани, ещё до колокола ([место](source:${port}/3-5)). Сава увидел пожар позже, уже у ворот ([место](source:${bell}/2)).`,
        },
        {
          askedAt: '2026-10-12T15:20:00.000Z', question: 'Что Сава говорил о том, что видел?', outcome: 'answered',
          answer: `Сава клялся, что видел только отражение зарева в окне сторожки, а не сам огонь ([место](source:${port}/1)).`,
        },
        { askedAt: '2026-10-12T15:30:00.000Z', question: 'Кто стоял на стене заставы?', outcome: 'interrupted' },
        { askedAt: '2026-10-12T15:40:00.000Z', question: 'Что стало с колоколом после пожара?', outcome: 'failed' },
      ],
    },
    {
      projectId: id, id: `transcript-${id}-2`, title: 'Родословная Савы', createdAt: '2026-10-03T09:00:00.000Z',
      lastTurnAt: '2026-10-03T09:00:00.000Z', othersTurns: 0, places: {},
      turns: [{ askedAt: '2026-10-03T09:00:00.000Z', question: 'Кто родители Савы?', outcome: 'answered', answer: 'В книге об этом не сказано.' }],
    },
  );
  return project;
}
