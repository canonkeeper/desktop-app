import type { OperationId, OperationResponse, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import { desktopLinked } from './desktop-account';
import { spendsImage } from './gallery-state';
import type { MockQuotaDefaults } from './state';
import { mockPlanSettings } from './tiers';

type DefaultKey = Schema<'AdminQuotaKey'>;

/** The original value of every writer limit (backend domain/quota.Defaults), in the order of domain/quota.Keys. */
export const mockCodeDefaults: Readonly<Record<DefaultKey, number>> = {
  chat_turns_per_day: 30,
  active_turns: 2,
  imports_per_day: 30,
  projects: 3,
  project_books: 60,
  project_chapters: 3000,
  project_characters: 15_000_000,
  account_books: 100,
  account_chapters: 5000,
  account_characters: 25_000_000,
  images_per_day: 30,
  image_uploads_per_day: 30,
  chapter_drafts_per_day: 20,
  drawing_turns_per_day: 60,
  draft_turns_per_day: 60,
  draft_rewrites_per_day: 200,
  app_returns_per_day: 5,
  app_tokens_per_question: 360_000,
  app_reading_tokens_per_character: 8,
  // A book loads again by the code (specs/2026-11-18-free-tier.md FT1.1): «Бесплатно» sets 0 by its tier.
  book_reloads: 1,
};

/** A limit by default in force: the platform's value, else the code's (R7.2). */
export function mockDefault(platform: MockQuotaDefaults, key: DefaultKey) {
  return platform[key] ?? mockCodeDefaults[key];
}

/**
 * A book of the sagas of the signed-in writer that came into one (specs/2026-11-18-free-tier.md FT1.2): the journal of the
 * books of his account takes its row — the trigger of the server writes it on every insert of a book, the example aside.
 */
export function mockLoadBook(context: MockRepositoryContext, bookId: string) {
  const ownerUserId = context.currentPrincipalId();
  if (!context.state.bookLoads.some((load) => load.bookId === bookId)) context.state.bookLoads.push({ bookId, ownerUserId });
}

/**
 * The key book_reloads of the writer and the books of his account as the fence of the server reads them (FT1.3, FT1.4):
 * while a book loads once, a new version of a book and a book without text are refused with 409 quota.book_reloads
 * (`books` — 'version', 'empty'), and new books once every book that came into his sagas would pass the books of the
 * account — the mock is not kinder than the server.
 */
export function mockRefuseBookLoads(context: MockRepositoryContext, operationId: OperationId, books: number | 'version' | 'empty') {
  const plan = mockPlanSettings(context.state, context.currentPrincipalId());
  const limit = (key: DefaultKey) => plan[key] ?? mockDefault(context.state.quotaDefaults, key);
  if (limit('book_reloads') === 1) return;
  const loaded = context.state.bookLoads.filter((load) => load.ownerUserId === context.currentPrincipalId()).length;
  if (typeof books !== 'number' || loaded + books > limit('account_books')) {
    throw new MockApiError(409, 'A book of the tier is loaded once.', operationId, 'quota.book_reloads');
  }
}

/** The key of the day of «Письмо» of the signed-in writer (specs/2026-11-09-quota-accounting.md): the writing, the rewrites. */
type WritingDayKey = 'chapter_drafts_per_day' | 'draft_rewrites_per_day';
const writingDay = (context: MockRepositoryContext, key: WritingDayKey) => `${context.currentPrincipalId()}:${moscowDay(new Date())}:${key}`;

/**
 * The limit of a day a work of the model in «Письмо» spends (`ReserveDailyInTx`): 429 quota.chapter_drafts_daily or
 * quota.draft_rewrites_daily once the Moscow day of the writer is spent, else one more of it.
 */
export function mockSpendWritingDay(context: MockRepositoryContext, key: WritingDayKey, operationId: OperationId) {
  const plan = mockPlanSettings(context.state, context.currentPrincipalId());
  const limit = plan[key] ?? mockDefault(context.state.quotaDefaults, key);
  const day = writingDay(context, key);
  if ((context.state.writingDays[day] ?? 0) >= limit) {
    const code = key === 'chapter_drafts_per_day' ? 'quota.chapter_drafts_daily' : 'quota.draft_rewrites_daily';
    throw new MockApiError(429, 'The work of «Письмо» of the day is spent.', operationId, code);
  }
  context.state.writingDays[day] = (context.state.writingDays[day] ?? 0) + 1;
}

/** Mock text has no characters of its own: a word counts as six characters. */
const charactersPerWord = 6;

export function moscowDay(now: Date) {
  return new Date(now.getTime() + 3 * 60 * 60 * 1000).toISOString().slice(0, 10);
}

function nextMoscowMidnight(now: Date) {
  return new Date(`${moscowDay(now)}T00:00:00+03:00`).getTime() + 24 * 60 * 60 * 1000;
}

function counter(used: number, limit: number): Schema<'QuotaCounter'> {
  return { used, limit };
}

export function createQuotaOperations(context: MockRepositoryContext) {
  /** Drawings and region edits the writer started on a Moscow day and did not get back (images_per_day, R13.6). */
  function drawingsToday(day: string) {
    const principal = context.currentPrincipalId();
    return context.state.gallery.images.reduce((total, image) => total
      + (image.createdBy === principal && moscowDay(new Date(image.createdAt)) === day && spendsImage(image) ? 1 : 0)
      + image.layers.filter((layer) => layer.createdBy === principal && moscowDay(new Date(layer.createdAt)) === day && spendsImage(layer)).length, 0);
  }

  /**
   * Who holds the places of «разом» (specs/2026-11-15-desk-turns-here.md TH2.1): only the app linked with its helper of the
   * account says it, when the scenario gives what its site told.
   */
  function accountPlaces(): { places?: Schema<'ActiveTurnPlaces'> } {
    const places = context.scenario.accountPlaces;
    return places && context.state.aiSettings.source === 'account' && desktopLinked(context) ? { places: { ...places } } : {};
  }

  async function getAccountQuota(): Promise<OperationResponse<'getAccountQuota', '200'>> {
    context.applyOperation('getAccountQuota');
    const now = new Date();
    const day = moscowDay(now);
    // The tier of the writer stands over the platform for the keys it sets (specs/2026-11-08-subscription-tiers.md TR2.2).
    const plan = mockPlanSettings(context.state, context.currentPrincipalId());
    const limit = (key: DefaultKey) => plan[key] ?? mockDefault(context.state.quotaDefaults, key);
    const limits = {
      chatTurnsPerDay: limit('chat_turns_per_day'), activeTurns: limit('active_turns'), importsPerDay: limit('imports_per_day'),
      projects: limit('projects'), projectBooks: limit('project_books'), projectChapters: limit('project_chapters'),
      projectCharacters: limit('project_characters'), accountBooks: limit('account_books'), accountChapters: limit('account_chapters'),
      accountCharacters: limit('account_characters'), imagesPerDay: limit('images_per_day'),
    };
    const principal = context.currentPrincipalId();
    const uploadsToday = context.state.gallery.images
      .filter((image) => image.kind === 'upload' && image.createdBy === principal && moscowDay(new Date(image.createdAt)) === day).length;
    const turns = context.state.jobs.filter((job) => job.kind === 'research_chat_turn');
    const imports = context.state.jobs.filter((job) => job.kind === 'import');
    const owned = context.state.projects;
    const ownedProjects = owned.map((project) => {
      const books = context.state.books.filter((book) => book.projectId === project.id);
      const chapters = books.reduce((total, book) => total + book.chapterCount, 0);
      const words = books.reduce((total, book) => total + book.wordCount, 0);
      return {
        projectId: project.id,
        sources: {
          books: counter(books.length, limits.projectBooks),
          chapters: counter(chapters, limits.projectChapters),
          characters: counter(words * charactersPerWord, limits.projectCharacters),
        },
      };
    });
    const sum = (key: 'books' | 'chapters' | 'characters') =>
      ownedProjects.reduce((total, project) => total + project.sources[key].used, 0);
    return clone({
      day,
      resetsAt: new Date(nextMoscowMidnight(now)).toISOString(),
      // A correction of a turn is a question of the day as the turn's own question is (specs/2026-11-09-quota-accounting.md Q2.1).
      chatTurns: counter(turns.filter((job) => moscowDay(new Date(job.createdAt)) === day).reduce((total, job) => total + 1
        + (job.subject.type === 'research_chat_turn' ? context.state.gallery.turnSteers[job.subject.turnId]?.length ?? 0 : 0), 0), limits.chatTurnsPerDay),
      activeTurns: {
        ...counter(turns.filter((job) => job.status === 'queued' || job.status === 'running' || job.status === 'canceling').length, limits.activeTurns),
        ...accountPlaces(),
      },
      imports: counter(imports.filter((job) => moscowDay(new Date(job.createdAt)) === day).length, limits.importsPerDay),
      images: counter(drawingsToday(day), limits.imagesPerDay),
      // The mock keeps the day of the writing and the rewrites of «Письмо»; the turns of the helpers stand at nothing used
      // (specs/2026-11-10-ux2-tariff-account.md TA2.3).
      imageUploads: counter(uploadsToday, limit('image_uploads_per_day')),
      chapterDrafts: counter(context.state.writingDays[writingDay(context, 'chapter_drafts_per_day')] ?? 0, limit('chapter_drafts_per_day')),
      drawingTurns: counter(0, limit('drawing_turns_per_day')),
      draftTurns: counter(0, limit('draft_turns_per_day')),
      draftRewrites: counter(context.state.writingDays[writingDay(context, 'draft_rewrites_per_day')] ?? 0, limit('draft_rewrites_per_day')),
      projects: counter(owned.length, limits.projects),
      account: {
        books: counter(sum('books'), limits.accountBooks),
        chapters: counter(sum('chapters'), limits.accountChapters),
        characters: counter(sum('characters'), limits.accountCharacters),
      },
      ownedProjects,
      // The books that ever came into the sagas (specs/2026-11-18-free-tier.md FT1.5): the journal of the account.
      bookReloads: limit('book_reloads') === 1,
      booksLoaded: context.state.bookLoads.filter((load) => load.ownerUserId === principal).length,
    });
  }

  return { getAccountQuota };
}
