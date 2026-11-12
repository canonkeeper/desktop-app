import { type AccountQuota, quotaNearLimit } from '../../../entities/account-quota/api';
import { formatCharacters, refusalLine } from '../../../entities/account-quota/refusal-words';
import { pluralize } from '../../../ui/copy';


function books(value: number) {
  return `${value} ${pluralize(value, 'книга', 'книги', 'книг')}`;
}

function files(value: number) {
  return `${value} ${pluralize(value, 'файл', 'файла', 'файлов')}`;
}

/** The codes of a refusal of an upload, a book, a saga or a restore by a limit (quota spec R5.3, R5.5). */
const shelfCodes = new Set([
  'quota.imports_daily', 'quota.project_books', 'quota.project_chapters', 'quota.project_characters',
  'quota.account_books', 'quota.account_chapters', 'quota.account_characters', 'quota.projects',
]);

/**
 * The refusal of an upload, a book, a saga or a restore by its code in one line, in the words of the one table of
 * refusals (specs/2026-11-10-ux2-patterns-refusals.md PR2.1); undefined for any other code. `volumes` is the size of the
 * upload being started: more books than the day still takes is said by their number.
 */
export function sourceQuotaMessage(code: string | null | undefined, quota: AccountQuota | undefined, options: { volumes?: number } = {}) {
  if (!code || !shelfCodes.has(code)) return undefined;
  if (code === 'quota.imports_daily' && quota && options.volumes) {
    const left = Math.max(0, quota.imports.limit - quota.imports.used);
    if (options.volumes > left) return `Выбрано ${books(options.volumes)}, а сегодня можно загрузить ещё ${left}. Уберите лишние книги или загрузите их завтра.`;
  }
  return refusalLine(code, quota);
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
