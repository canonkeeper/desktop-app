import { type AccountQuota, quotaNearLimit, supportAddress } from '../../../entities/account-quota/api';
import { pluralize } from '../../../ui/copy';

/** Characters as the writer reads them: «15 млн», «12,4 млн», «800». */
export function formatCharacters(value: number) {
  if (value < 1_000_000) return value.toLocaleString('ru-RU');
  const millions = Math.floor(value / 100_000) / 10;
  return `${millions.toLocaleString('ru-RU', { maximumFractionDigits: 1 })} млн`;
}

function books(value: number) {
  return `${value} ${pluralize(value, 'книга', 'книги', 'книг')}`;
}

function booksGenitive(value: number) {
  return `${value} ${pluralize(value, 'книги', 'книг', 'книг')}`;
}

function chaptersGenitive(value: number) {
  return `${value} ${pluralize(value, 'главы', 'глав', 'глав')}`;
}

function files(value: number) {
  return `${value} ${pluralize(value, 'файл', 'файла', 'файлов')}`;
}

/**
 * The refusal of an upload, a book, a project or a restore by its quota code, with the writer's
 * own limits (quota spec R5.3, R5.5); undefined for any other code. `volumes` is the size of the
 * queue being started.
 */
export function sourceQuotaMessage(code: string | null | undefined, quota: AccountQuota | undefined, options: { volumes?: number } = {}) {
  const support = `напишите на ${supportAddress}`;
  // Project limits are the same for every project of the writer; a project of another owner has
  // the owner's limits, which this writer does not see.
  const project = quota?.ownedProjects[0]?.sources;
  switch (code) {
    case 'quota.imports_daily': {
      if (!quota) return 'Сегодня загружено столько файлов, сколько можно за день. Загрузить ещё можно после 00:00 по Москве.';
      const { used, limit } = quota.imports;
      const left = Math.max(0, limit - used);
      if (options.volumes && options.volumes > left) {
        return `В очереди ${books(options.volumes)}, а сегодня можно загрузить ещё ${left}. Уберите лишние книги или загрузите их завтра.`;
      }
      return `Сегодня ${pluralize(limit, 'загружен', 'загружено', 'загружено')} ${files(limit)} — это дневной лимит. Загрузить ещё можно после 00:00 по Москве.`;
    }
    case 'quota.project_books':
      return project
        ? `В саге уже ${books(project.books.limit)} — больше в одну сагу не поместить. Напишите на ${supportAddress}, если нужно больше.`
        : `В саге уже столько книг, сколько в неё помещается. Напишите на ${supportAddress}, если нужно больше.`;
    case 'quota.project_chapters':
      return `Книга не поместилась: в саге стало бы больше ${project ? chaptersGenitive(project.chapters.limit) : 'глав, чем в неё помещается'}. Напишите на ${supportAddress}, если нужно больше.`;
    case 'quota.project_characters':
      return `Книга не поместилась: текст саги превысил бы ${project ? `${formatCharacters(project.characters.limit)} знаков` : 'предел'}. Удалите ненужные книги или ${support}.`;
    case 'quota.account_books':
      return `Книга не поместилась: во всех ваших сагах стало бы больше ${quota ? booksGenitive(quota.account.books.limit) : 'книг, чем помещается'}. Удалите ненужное или ${support}.`;
    case 'quota.account_chapters':
      return `Книга не поместилась: во всех ваших сагах стало бы больше ${quota ? chaptersGenitive(quota.account.chapters.limit) : 'глав, чем помещается'}. Удалите ненужное или ${support}.`;
    case 'quota.account_characters':
      return `Книга не поместилась: во всех ваших сагах стало бы больше ${quota ? `${formatCharacters(quota.account.characters.limit)} знаков` : 'знаков, чем помещается'}. Удалите ненужное или ${support}.`;
    case 'quota.projects': {
      const limit = quota?.projects.limit;
      return limit === undefined
        ? `У вас уже столько саг, сколько можно. Удалите ненужную сагу или ${support}.`
        : `У вас уже ${limit} ${pluralize(limit, 'сага', 'саги', 'саг')} — это предел. Удалите ненужную сагу или ${support}.`;
    }
    default:
      return undefined;
  }
}

/** Uploads left today, from 80 % of the daily limit (R5.3). */
export function uploadsQuotaHint(quota: AccountQuota | undefined) {
  if (!quota || !quotaNearLimit(quota.imports)) return '';
  const left = Math.max(0, quota.imports.limit - quota.imports.used);
  return `Сегодня можно загрузить ещё ${files(left)} из ${quota.imports.limit}.`;
}

/** The saga text from 80 % of its limit (R5.3). */
export function projectTextQuotaHint(quota: AccountQuota | undefined, projectId: string | null) {
  const characters = quota?.ownedProjects.find((project) => project.projectId === projectId)?.sources.characters;
  if (!quotaNearLimit(characters)) return '';
  return `Текст саги: ${formatCharacters(characters!.used)} знаков из ${formatCharacters(characters!.limit)}.`;
}
