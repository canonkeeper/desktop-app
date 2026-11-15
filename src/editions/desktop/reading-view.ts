// SPDX-License-Identifier: AGPL-3.0-only
import type { PreparationActionInput, PreparationView } from '../../entities/manuscript/preparation-slot';
import { useAccountReadingView } from './account-reading/AccountReadingView';
import { useReadingView as useOwnReadingView } from './reading/ReadingView';

/**
 * The one slot of the line of the reading of the app (specs/2026-11-15-desk-account-words-web.md AW5.1): who reads the books the
 * server says alone (`reading.source`) — the block of the helper of the account or the block of «Свой ИИ» with the helper not
 * connected. The hooks of both run at every render, so a change of the source on an open page keeps the order of the hooks; the
 * block of the other source closes its windows and asks nothing.
 */
export function useDesktopReadingView(input: PreparationActionInput): PreparationView | null {
  const byAccount = input.status?.reading?.source === 'account';
  const own = useOwnReadingView(input, !byAccount);
  const account = useAccountReadingView(input, byAccount);
  return byAccount ? account : own;
}
