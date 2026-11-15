// SPDX-License-Identifier: AGPL-3.0-only
import { Gauge } from 'lucide-react';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { accountPartKey } from '../settings/part-props';
import { useDesktopAccount } from './account-api';
import { useAccountLink, type AccountLinkClient } from './AccountLink';

/** «Тариф «Писатель»» — the name of the mark for a reader of the screen; the mark itself says the name of the tier. */
export function tierMarkLabel(title: string) {
  return `Тариф «${title}»`;
}

/**
 * The tier of the linked app in the top bar beside «Настройки» (specs/2026-11-15-desk-account-words-web.md AW4.7; board 44·7):
 * its name the server's (`getAccount.account.tier.title`), its press «Настройки → Аккаунт». None without a link, while the site
 * has told nothing, with a broken link.
 */
export function TierMark({ navigate }: { navigate: RouteNavigate }) {
  const link = useAccountLink();
  // Without the link of the edition the query never runs: its client is not asked.
  const account = useDesktopAccount(link?.api as AccountLinkClient, link !== null).data;
  const tier = account?.state === 'linked' ? account.account?.tier : null;
  if (!tier) return null;
  return (
    <Button aria-label={tierMarkLabel(tier.title)} onClick={() => navigate('/settings', { part: accountPartKey })} size="sm" variant="outline">
      <Gauge aria-hidden="true" size={14} />{tier.title}
    </Button>
  );
}
