import type {
  Book,
  Chapter,
  ChatAttachment,
  ChatSession,
  Job,
  Project,
  Schema,
  SourceView,
} from '../../../shared/api';
import {
  createJob,
  now,
  type DeletedBookSource,
  type DeletedChapterSource,
  type DeletedSource,
  type ProjectInvitation,
  type ProjectMembership,
  type User,
} from './common';
import { createInitialGallery, type MockGalleryState } from './gallery-state';
import { createInitialPromoPermissions, type MockPromoPermission } from './promo-state';
import {
  bookId,
  chapterId,
  createProjectInvitationFixture,
  createProjectMembershipFixture,
  createReaderLocator,
  draftBookId,
  draftChapterId,
  foreignProjectId,
  paragraphId,
  projectId,
} from './fixtures';
type AuthSession = Schema<'AuthSession'>;
/** The values the administrator set (specs/2026-10-29-admin-panel.md UC7); a key without one keeps the code's. */
export type MockQuotaDefaults = Partial<Record<Schema<'AdminQuotaDefaultKey'>, number>>;
type ImportQueue = Schema<'ImportQueue'>;
export type MockBookCoverState = {
  version: number;
  mimeType: string;
  sizeBytes: number;
  active: boolean;
};

export type MockReadingTestVote = { number: number; voterId: string; chosen: string; note: string };

export type MockState = {
  user: User;
  session: AuthSession | null;
  projects: Project[];
  deletedProjects: DeletedSource<Project>[];
  invitations: ProjectInvitation[];
  memberships: ProjectMembership[];
  books: Book[];
  bookCovers: Record<string, MockBookCoverState>;
  deletedBooks: DeletedBookSource[];
  chapters: Chapter[];
  deletedChapters: DeletedChapterSource[];
  personalNotes: Array<Schema<'PersonalNote'> & { ownerUserId: string }>;
  /** Главы каждой версии текста с актуальным текстовым индексом и с готовой карточкой. */
  indexedChapterIds: Record<SourceView, string[]>;
  cardedChapterIds: Record<SourceView, string[]>;
  jobs: Job[];
  importQueues: ImportQueue[];
  chats: ChatSession[];
  /** Главы автора, приложенные к вопросам чатов (specs/2026-11-07-chat-attach-chapter.md CA6.1): по номеру в чате. */
  chatAttachments: Record<string, ChatAttachment[]>;
  /** Сессии помощника мастерской главы (specs/2026-11-02-writer-workshop.md W1.2): не чаты списка. */
  draftHelperChatIds: string[];
  nextAgentId: {
    chatSession: number;
    chatTurn: number;
  };
  nextImportQueueId: number;
  /** Галерея саги (specs/2026-10-29-saga-illustrations.md). */
  gallery: MockGalleryState;
  /** Лимиты по умолчанию платформы (specs/2026-10-29-admin-panel.md UC7): значения администратора. */
  quotaDefaults: MockQuotaDefaults;
  quotaDefaultsUpdatedAt: string | null;
  /** Разрешения на продвижение (specs/2026-10-30-promo-permissions.md). */
  promoPermissions: MockPromoPermission[];
  /** Голоса слепого сравнения текстов (specs/2026-11-05-reading-test.md). */
  readingTestVotes: MockReadingTestVote[];
  /** «Как с нами связаться» и обратная связь (specs/2026-11-07-contacts.md). */
  contacts: MockContacts;
  feedbackThreads: MockFeedbackThread[];
  feedbackImages: MockFeedbackImage[];
};

export type MockContacts = Schema<'AdminContacts'>;
export type MockFeedbackMessage = {
  id: string;
  number: number;
  from: Schema<'FeedbackSender'>;
  createdAt: string;
  blocks: Array<{ text: string } | { image: string }>;
  context?: Schema<'FeedbackContext'>;
  draftKey: string;
};
export type MockFeedbackThread = {
  id: string;
  userId: string | null;
  authorName: string;
  email: string;
  topic: Schema<'FeedbackTopic'>;
  subject: string;
  status: Schema<'FeedbackStatus'>;
  createdAt: string;
  updatedAt: string;
  authorReadAt: string | null;
  messages: MockFeedbackMessage[];
};
export type MockFeedbackImage = {
  id: string;
  userId: string | null;
  draftKey: string;
  messageId: string | null;
  width: number;
  height: number;
  content: Uint8Array<ArrayBuffer>;
  mimeType: string;
};

function createInitialState(): MockState {
  const locator = createReaderLocator();
  const user: User = {
    id: 'user-mira',
    email: 'mira@example.com',
    emailVerified: true,
    documentsAccepted: true,
    displayName: 'Мира Волкова',
    avatarUrl: null,
    createdAt: now,
  };
  const projectBase = {
    id: projectId,
    title: 'Хроники Белого порта',
    description: 'Сага о городе, который помнит каждую клятву.',
    bookCount: 4,
    chapterCount: 186,
    wordCount: 1200000,
    createdAt: now,
    updatedAt: now,
  };
  const project: Project = {
    ...projectBase,
    currentMembership: createProjectMembershipFixture(projectBase, user, 'member-owner', 'owner'),
  };
  const book: Book = {
    id: bookId,
    projectId,
    title: 'Книга II. Карта приливов',
    subtitle: 'Черновая редакция',
    order: 2,
    displayNumber: 'II',
    coverColor: '#4F46E5',
    coverUrl: null,
    status: 'ready',
    chapterCount: 48,
    wordCount: 312000,
    createdAt: now,
    updatedAt: now,
  };
  const bookOne: Book = {
    id: 'book-01',
    projectId,
    title: 'Книга I. Туманный берег',
    subtitle: 'Первая редакция',
    order: 1,
    displayNumber: 'I',
    coverColor: '#285f58',
    coverUrl: null,
    status: 'ready',
    chapterCount: 22,
    wordCount: 96000,
    createdAt: now,
    updatedAt: now,
  };
  const bookThree: Book = {
    id: draftBookId,
    projectId,
    title: 'Книга III. Северный суд',
    subtitle: 'Черновая редакция',
    order: 3,
    displayNumber: 'III',
    coverColor: '#4c3a5d',
    coverUrl: null,
    status: 'draft',
    chapterCount: 9,
    wordCount: 41000,
    createdAt: now,
    updatedAt: now,
  };
  const bookFour: Book = {
    id: 'book-04',
    projectId,
    title: 'Книга IV. Последний свет',
    subtitle: 'Черновая редакция',
    order: 4,
    displayNumber: 'IV',
    coverColor: '#30472b',
    coverUrl: null,
    status: 'draft',
    chapterCount: 3,
    wordCount: 12000,
    createdAt: now,
    updatedAt: now,
  };
  const foreignBook: Book = {
    ...book,
    id: 'book-foreign',
    projectId: foreignProjectId,
    title: 'Foreign Archive',
  };
  const chapter: Chapter = {
    id: chapterId,
    bookId,
    title: 'Белый порт',
    order: 12,
    contentVariant: 'published',
    status: 'published',
    paragraphs: [
      { id: 'p-12-01', order: 1, kind: 'heading', text: 'Глава 12. Белый порт', markdown: '## Глава 12. Белый порт' },
      {
        id: 'p-12-02',
        order: 2,
        kind: 'paragraph',
        text: 'Над портом стояла белая пыль, и мокрые канаты пахли солью сильнее обычного.',
        markdown: 'Над портом стояла белая пыль, и мокрые канаты пахли солью сильнее обычного.',
      },
      {
        id: paragraphId,
        order: 3,
        kind: 'paragraph',
        text: 'Мара первой заметила дым с северной пристани.',
        markdown: 'Мара первой заметила дым с северной пристани.',
      },
      {
        id: 'p-12-04',
        order: 4,
        kind: 'paragraph',
        text: 'Позже на допросе Лютов записал все коротко, будто боялся оставить в протоколе слишком много воздуха.',
        markdown: 'Позже на допросе Лютов записал все коротко, будто боялся оставить в протоколе слишком много воздуха.',
      },
      {
        id: 'p-12-05',
        order: 5,
        kind: 'paragraph',
        text: 'В остальном показания почти совпадали: рыбаки видели зарево, сторожа слышали крик.',
        markdown: 'В остальном показания почти совпадали: рыбаки видели зарево, сторожа слышали крик.',
      },
    ],
    wordCount: 2840,
    revision: 7,
    draftRevision: 8,
    publishedRevision: 7,
    savedAt: now,
    publishedAt: now,
    navigation: {
      displayNumber: '12',
      position: 12,
      total: 48,
      readingProgress: 0.25,
      readingTimeMinutes: 14,
      previousChapterId: 'chapter-11',
      nextChapterId: 'chapter-13',
      previous: { id: 'chapter-11', title: 'Соль на стекле', displayNumber: '11' },
      next: { id: 'chapter-13', title: 'Голоса под водой', displayNumber: '13' },
    },
    createdAt: now,
    updatedAt: now,
  };
  const chapterNote: MockState['personalNotes'][number] = {
    id: 'note-white-port-12',
    ownerUserId: user.id,
    title: '',
    body: 'Проверить, кто первым назвал порт Белым.',
    source: { kind: 'chapter', quote: locator.range?.quote ?? '', locator },
    revision: 1,
    createdAt: now,
    updatedAt: now,
  };
  const draftChapter: Chapter = {
    ...chapter,
    id: draftChapterId,
    bookId,
    title: 'Возвращение к записи',
    order: 16,
    contentVariant: 'draft',
    status: 'draft',
    paragraphs: [
      { id: 'p-16-01', order: 1, kind: 'heading', text: 'Глава 16. Возвращение к записи', markdown: '## Глава 16. Возвращение к записи' },
      {
        id: 'p-16-02',
        order: 2,
        kind: 'paragraph',
        text: 'Мара вернулась к колоколу, когда порт уже затих. Она помнила, что в протоколе её имя стояло первым, и теперь хотела понять, кто и зачем правил ту страницу.',
        markdown:
          'Мара вернулась к колоколу, когда порт уже затих. Она помнила, что в протоколе её имя стояло первым, и теперь хотела понять, кто и зачем правил ту страницу.',
      },
      {
        id: 'p-16-03',
        order: 3,
        kind: 'paragraph',
        text: 'Знак на полях оказался не случайной чертой. Кто-то отмечал строки, к которым возвращался снова и снова.',
        markdown: 'Знак на полях оказался не случайной чертой. Кто-то отмечал строки, к которым возвращался снова и снова.',
      },
      {
        id: 'p-16-04',
        order: 4,
        kind: 'paragraph',
        text: 'Сава молчал, но его молчание было громче любого признания',
        markdown: 'Сава молчал, но его молчание было громче любого признания',
      },
    ],
    wordCount: 320,
    revision: 7,
    draftRevision: 9,
    publishedRevision: 7,
    savedAt: now,
    publishedAt: null,
    navigation: {
      displayNumber: '16',
      position: 16,
      total: 48,
      readingProgress: 0.34,
      readingTimeMinutes: 5,
      previousChapterId: 'chapter-15',
      nextChapterId: null,
      previous: { id: 'chapter-15', title: 'Перед судом', displayNumber: '15' },
      next: null,
    },
    createdAt: now,
    updatedAt: now,
  };
  const bookOneChapter: Chapter = {
    ...chapter,
    id: 'chapter-01-01',
    bookId: 'book-01',
    title: 'Туманный берег',
    order: 1,
    paragraphs: [
      { id: 'p-01-01', order: 1, kind: 'heading', text: 'Глава 1. Туманный берег', markdown: '## Глава 1. Туманный берег' },
      {
        id: 'p-01-02',
        order: 2,
        kind: 'paragraph',
        text: 'Утро начиналось с низкого тумана, за которым берег казался не линией, а обещанием.',
        markdown: 'Утро начиналось с низкого тумана, за которым берег казался не линией, а обещанием.',
      },
    ],
    wordCount: 1840,
    navigation: {
      ...chapter.navigation,
      displayNumber: '1',
      position: 1,
      total: 22,
      previousChapterId: null,
      nextChapterId: null,
      previous: null,
      next: null,
    },
  };
  const bookThreeChapter: Chapter = {
    ...chapter,
    id: 'chapter-03-01',
    bookId: draftBookId,
    title: 'Северный суд',
    order: 1,
    status: 'draft',
    publishedAt: null,
    paragraphs: [
      { id: 'p-03-01', order: 1, kind: 'heading', text: 'Глава 1. Северный суд', markdown: '## Глава 1. Северный суд' },
      {
        id: 'p-03-02',
        order: 2,
        kind: 'paragraph',
        text: 'Суд начинался до рассвета, когда на каменных ступенях еще держалась соль.',
        markdown: 'Суд начинался до рассвета, когда на каменных ступенях еще держалась соль.',
      },
    ],
    wordCount: 2110,
    navigation: {
      ...chapter.navigation,
      displayNumber: '1',
      position: 1,
      total: 9,
      previousChapterId: null,
      nextChapterId: null,
      previous: null,
      next: null,
    },
  };
  const bookFourChapter: Chapter = {
    ...chapter,
    id: 'chapter-04-01',
    bookId: 'book-04',
    title: 'Последний свет',
    order: 1,
    status: 'draft',
    publishedAt: null,
    paragraphs: [
      { id: 'p-04-01', order: 1, kind: 'heading', text: 'Глава 1. Последний свет', markdown: '## Глава 1. Последний свет' },
      {
        id: 'p-04-02',
        order: 2,
        kind: 'paragraph',
        text: 'Последний маяк зажегся без смотрителя, и в городе стало слишком тихо.',
        markdown: 'Последний маяк зажегся без смотрителя, и в городе стало слишком тихо.',
      },
    ],
    wordCount: 980,
    navigation: {
      ...chapter.navigation,
      displayNumber: '1',
      position: 1,
      total: 3,
      previousChapterId: null,
      nextChapterId: null,
      previous: null,
      next: null,
    },
  };
  const foreignChapter: Chapter = {
    ...chapter,
    id: 'chapter-foreign',
    bookId: foreignBook.id,
    title: 'Foreign chapter',
  };


  return {
    user,
    session: { user },
    projects: [project],
    deletedProjects: [],
    invitations: [
      createProjectInvitationFixture(projectId, 'invitation-1', 'pending'),
      createProjectInvitationFixture(projectId, 'invitation-mira', 'pending', 'mira@example.com'),
      createProjectInvitationFixture(projectId, 'invitation-accepted', 'accepted'),
      createProjectInvitationFixture(projectId, 'invitation-canceled', 'canceled'),
    ],
    memberships: [
      createProjectMembershipFixture(project, user, 'member-owner', 'owner'),
      createProjectMembershipFixture(project, user, 'member-collaborator', 'editor'),
    ],
    books: [bookOne, book, bookThree, bookFour, foreignBook],
    bookCovers: {},
    deletedBooks: [],
    chapters: [bookOneChapter, chapter, draftChapter, bookThreeChapter, bookFourChapter, foreignChapter],
    deletedChapters: [],
    personalNotes: [chapterNote],
    // Опубликованная версия ждёт перезапуска индекса и карточек второй книги,
    // в черновике четвёртая книга ещё без индекса.
    indexedChapterIds: {
      published: [bookOneChapter.id, chapter.id, foreignChapter.id],
      draft: [bookOneChapter.id, chapter.id, draftChapter.id, bookThreeChapter.id],
    },
    cardedChapterIds: {
      published: [bookOneChapter.id, foreignChapter.id],
      draft: [bookOneChapter.id, chapter.id, draftChapter.id, bookThreeChapter.id],
    },
    jobs: [
      createJob({
        id: 'canon-text-index-job-1',
        kind: 'canon_text_index',
        status: 'queued',
        progress: 0,
        subject: { type: 'canon_text_index', projectId, sourceView: 'published', catalogRevision: 7 },
        result: null,
        canCancel: true,
      }),
      createJob({
        id: 'foreign-job-1',
        kind: 'canon_text_index',
        status: 'running',
        progress: 0.2,
        subject: { type: 'canon_text_index', projectId: foreignProjectId, sourceView: 'published', catalogRevision: 3 },
        result: null,
        canCancel: true,
      }),
    ],
    importQueues: [],
    chats: [],
    chatAttachments: {},
    draftHelperChatIds: [],
    nextAgentId: { chatSession: 1, chatTurn: 1 },
    nextImportQueueId: 1,
    gallery: createInitialGallery(user.id),
    quotaDefaults: {},
    quotaDefaultsUpdatedAt: null,
    promoPermissions: createInitialPromoPermissions(user, projectId, project.title),
    readingTestVotes: [],
    contacts: { telegram: null, email: null, note: null, updatedAt: null },
    feedbackThreads: [],
    feedbackImages: [],
  };
}
export { createInitialState };
