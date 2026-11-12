import { quotaFailureCode, type AccountQuota } from '../../../entities/account-quota/api';
import { refusalWords } from '../../../entities/account-quota/refusal-words';
import { sourceQuotaMessage } from './quota-messages';

/**
 * A return refused by a quota of the plan (specs/2026-11-10-ux1-shelf.md UXS4.2, specs/2026-11-11-ux7-trash.md TB1.3), one
 * for «Недавно удалённые» and for the line after a deletion: the title, the limit it hit and its code for the note of the
 * tier.
 */
export function quotaRefusal(error: unknown, quota: AccountQuota | undefined) {
  const quotaCode = quotaFailureCode(error);
  if (!quotaCode) return null;
  return { title: 'Не удалось вернуть.', detail: sourceQuotaMessage(quotaCode, quota), quotaCode, words: refusalWords(quotaCode, quota) };
}
