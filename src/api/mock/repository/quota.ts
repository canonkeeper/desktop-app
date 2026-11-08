import type { OperationResponse, Schema } from '../../../shared/api';
import { clone } from './common';
import type { MockRepositoryContext } from './context';
import { spendsImage } from './gallery-state';
import type { MockQuotaDefaults } from './state';

type DefaultKey = Schema<'AdminQuotaDefaultKey'>;

/**
 * The original limits by default (backend domain/quota.Defaults) and the trial drawings of a new
 * account (domain/illustrations.TrialDrawings), in the order of the tab «Лимиты».
 */
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
  trial_drawings: 5,
};

/** A limit by default in force: the platform's value, else the code's (R7.2). */
export function mockDefault(platform: MockQuotaDefaults, key: DefaultKey) {
  return platform[key] ?? mockCodeDefaults[key];
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

  async function getAccountQuota(): Promise<OperationResponse<'getAccountQuota', '200'>> {
    context.applyOperation('getAccountQuota');
    const now = new Date();
    const day = moscowDay(now);
    const limit = (key: DefaultKey) => mockDefault(context.state.quotaDefaults, key);
    const limits = {
      chatTurnsPerDay: limit('chat_turns_per_day'), activeTurns: limit('active_turns'), importsPerDay: limit('imports_per_day'),
      projects: limit('projects'), projectBooks: limit('project_books'), projectChapters: limit('project_chapters'),
      projectCharacters: limit('project_characters'), accountBooks: limit('account_books'), accountChapters: limit('account_chapters'),
      accountCharacters: limit('account_characters'), imagesPerDay: limit('images_per_day'),
    };
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
      chatTurns: counter(turns.filter((job) => moscowDay(new Date(job.createdAt)) === day).length, limits.chatTurnsPerDay),
      activeTurns: counter(
        turns.filter((job) => job.status === 'queued' || job.status === 'running' || job.status === 'canceling').length,
        limits.activeTurns,
      ),
      imports: counter(imports.filter((job) => moscowDay(new Date(job.createdAt)) === day).length, limits.importsPerDay),
      images: counter(drawingsToday(day), limits.imagesPerDay),
      projects: counter(owned.length, limits.projects),
      account: {
        books: counter(sum('books'), limits.accountBooks),
        chapters: counter(sum('chapters'), limits.accountChapters),
        characters: counter(sum('characters'), limits.accountCharacters),
      },
      ownedProjects,
    });
  }

  return { getAccountQuota };
}
