// SPDX-License-Identifier: AGPL-3.0-only
import { ExternalLink, KeyRound, Link2, Settings } from 'lucide-react';
import { useState } from 'react';
import { desktopShell } from '../../shared/desktop/shell';
import type { RouteNavigate } from '../../shared/navigation/app-route';
import { Button, ButtonLink } from '../../ui/controls/Button';
import { useToasts } from '../../ui/feedback/useToastQueue';
import { useDesktopAccount } from './account/account-api';
import { relinkHelper, useAccountLink, type AccountLinkClient } from './account/AccountLink';
import { accountWords } from './account/account-words';
import { useHelperReasonSince } from './account/helper-reason';
import { external } from './external';
import { readingButtons } from './reading/reading-line';
import { ConnectWindow } from './settings/helper/ConnectWindow';
import { helperWords } from './settings/helper/helper-words';
import { helperPartKey, updatesPartKey } from './settings/part-props';
import { updateWords } from './updates/update-words';

/**
 * «Тарифы» (specs/2026-11-15-desk-account-words-web.md AW4.1, AW5.3): the page of the tiers of the site in the browser of the
 * system — its address only the server's (`getAccount.pages.tariffs`); none until the link is read.
 */
export function TariffsLink() {
  const link = useAccountLink();
  const toasts = useToasts();
  // Without the link of the edition the query never runs: its client is not asked.
  const pages = useDesktopAccount(link?.api as AccountLinkClient, link !== null).data?.pages;
  if (!pages) return null;
  return (
    <ButtonLink {...external(pages.tariffs, desktopShell(), toasts)} size="sm" variant="outline">
      <ExternalLink aria-hidden="true" size={14} />{accountWords.tariffs}
    </ButtonLink>
  );
}

/**
 * «Связать снова» (AW4.3, AW5.2): the one start of a link of the edition and its window (specs/2026-11-15-desk-account-link-web.md
 * AW1.1), with the intent to bring the helper of the account back (`relinkHelper`); a window it stands in closes once the window
 * of the waiting opens — one window over the page.
 */
export function RelinkButton({ onShown }: { onShown?: () => void }) {
  const link = useAccountLink();
  if (!link) return null;
  return (
    <Button loading={link.starting} onClick={() => relinkHelper(link, onShown)} size="sm" variant="outline">
      <Link2 aria-hidden="true" size={14} />{accountWords.relink}
    </Button>
  );
}

/** «Открыть обновления» (AW4.5): «Настройки → Обновления» — the same name of the way to the part everywhere (UW2). */
export function OpenUpdatesButton({ navigate }: { navigate: RouteNavigate }) {
  return (
    <Button onClick={() => navigate('/settings', { part: updatesPartKey })} size="sm" variant="outline">
      <Settings aria-hidden="true" size={14} />{updateWords.openUpdates}
    </Button>
  );
}

/** «Подключить свой ИИ» (AW4.1; DT10.6): «Настройки → Помощник», where the author chooses «Свой ИИ». */
export function ConnectOwnButton({ navigate }: { navigate: RouteNavigate }) {
  return (
    <Button onClick={() => navigate('/settings', { part: helperPartKey })} size="sm" variant="outline">
      <KeyRound aria-hidden="true" size={14} />{helperWords.connectOwn}
    </Button>
  );
}

/** What stays in the field after a refusal at the start (AW4.2). */
const keptInField = { start: 'Вопрос остался в поле', steer: 'Уточнение осталось в поле' } as const;

/**
 * The words of a question or a correction the server refused because the helper is not connected (409 `helper.not_connected`;
 * AW4.2): the one reason «помощник не подключён» of the edition (specs/2026-11-15-desk-account-link-web.md AW3.4) over the
 * answers read again after the refusal — the link may have broken on the site at that very moment — and what stays in the
 * field; until they came, only that.
 */
export function HelperRefusalWords({ operation, since }: { operation: 'start' | 'steer'; since: number }) {
  const reason = useHelperReasonSince(since);
  return <>{reason ? `${reason.title}. ${keptInField[operation]}` : keptInField[operation]}</>;
}

/**
 * The way out of that refusal — the button of the reason, by the same answers: «Связать снова» for a broken link, «Открыть окно
 * связи» for a link that waits, «Подключить помощника» with its window of the choice.
 */
export function HelperRefusalWay({ navigate, since }: { navigate: RouteNavigate; since: number }) {
  const reason = useHelperReasonSince(since);
  const link = useAccountLink();
  const [choosing, setChoosing] = useState(false);
  if (!reason || !link) return null;
  if (reason.action === 'relink') return <RelinkButton />;
  if (reason.action === 'linkWindow') {
    return <Button onClick={link.openWindow} size="sm" variant="outline"><Link2 aria-hidden="true" size={14} />{readingButtons.linkWindow}</Button>;
  }
  return (
    <>
      <Button onClick={() => setChoosing(true)} size="sm" variant="outline"><KeyRound aria-hidden="true" size={14} />{readingButtons.connect}</Button>
      {choosing ? <ConnectWindow navigate={navigate} onClose={() => setChoosing(false)} /> : null}
    </>
  );
}
