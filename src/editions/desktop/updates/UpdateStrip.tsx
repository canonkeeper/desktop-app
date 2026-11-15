// SPDX-License-Identifier: AGPL-3.0-only
import { CircleAlert, Download, List, MessageSquare, RotateCw, Settings, X } from 'lucide-react';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Progress } from '../../../ui/feedback/Progress';
import { updatesPartKey } from '../settings/part-props';
import { useAppUpdate, useUpdateActions, type UpdateActionsClient } from './app-update';
import { hideStrip, useHiddenStrips } from './hidden-strips';
import { stripStateOf, updateWords as words, type StripState } from './update-words';
import styles from './Updates.module.css';

interface UpdateStripProps {
  api: UpdateActionsClient;
  navigate: RouteNavigate;
  /** «Что нового» of the strip: the window is held by what does not mount again with the row under the top bar. */
  onWhatsNew: (notes: NonNullable<StripState['whatsNew']>) => void;
  route: AppRoute;
}

/**
 * The strip of an update under the top bar of any page (specs/2026-11-15-desk-updates-web.md UW3.1, UW3.2, UW3.5; board
 * 44·15, `Pattern/Desktop/Update Strip`): a version found, a download, the wait for the answer of the helper, a failure —
 * each in the words of the Data with its buttons, over «Книга прочитана». The part «Обновления» says all of it itself and
 * has no strip. The cross hides a version until the next one and a failure until a new attempt (`hidden-strips.ts`); the
 * strips of a work going on have none — their way out is «Отменить обновление». A screen reader hears a change of the
 * state, not each per cent.
 */
export function UpdateStrip({ api, navigate, onWhatsNew, route }: UpdateStripProps) {
  const update = useAppUpdate(api);
  const hidden = useHiddenStrips();
  const strip = update.data ? stripStateOf(update.data) : null;
  const onThePart = route.surface === 'settings' && route.query.get('part') === updatesPartKey;
  const shown = strip && !onThePart && !(strip.hides && hidden.includes(strip.hides)) ? strip : null;
  return (
    <>
      {/* One place of the announcement for every state, before and after the strip comes. */}
      <p aria-atomic="true" aria-live="polite" className={styles.announcement}>{shown?.announcement ?? ''}</p>
      {shown ? (
        <StripLine api={api} onHide={hideStrip} onOpenUpdates={() => navigate('/settings', { part: updatesPartKey })} onWhatsNew={onWhatsNew} strip={shown} />
      ) : null}
    </>
  );
}

function StripLine({ api, onHide, onOpenUpdates, onWhatsNew, strip }: {
  api: UpdateActionsClient;
  onHide: (key: string) => void;
  onOpenUpdates: () => void;
  onWhatsNew: (notes: NonNullable<StripState['whatsNew']>) => void;
  strip: StripState;
}) {
  const actions = useUpdateActions(api);
  const Icon = strip.kind === 'failure' ? CircleAlert : strip.kind === 'waiting' ? MessageSquare : Download;
  const pending = actions.install.isPending || actions.cancel.isPending;
  return (
    <section aria-label={words.strip} className={styles.strip} data-kind={strip.kind}>
      <Icon aria-hidden="true" className={styles.stripIcon} size={18} />
      <p className={styles.stripMessage}>{strip.message}</p>
      {strip.progress !== null ? <Progress aria-label={strip.announcement} className={styles.stripProgress} value={strip.progress} /> : null}
      <div className={styles.stripActions}>
        {strip.whatsNew ? (
          <Button onClick={() => onWhatsNew(strip.whatsNew!)} size="sm" variant="ghost"><List aria-hidden="true" size={16} />{words.whatsNew}</Button>
        ) : null}
        {strip.install ? (
          <Button loading={actions.install.isPending} onClick={() => {
            if (!pending) actions.install.mutate();
          }} size="sm" variant="primary">
            <RotateCw aria-hidden="true" size={16} />{words.install}
          </Button>
        ) : null}
        {strip.adminAsk ? <span className={styles.stripNote}>{words.stripAdmin}</span> : null}
        {strip.cancel ? (
          <Button loading={actions.cancel.isPending} onClick={() => {
            if (!pending) actions.cancel.mutate();
          }} size="sm" variant="outline">
            <X aria-hidden="true" size={16} />{words.cancel}
          </Button>
        ) : null}
        {strip.openUpdates ? (
          <Button onClick={onOpenUpdates} size="sm" variant="outline"><Settings aria-hidden="true" size={16} />{words.openUpdates}</Button>
        ) : null}
      </div>
      {strip.hides ? (
        // A version is hidden until the next one, a failure until a new attempt: each cross says what it does.
        <IconButton aria-label={strip.kind === 'failure' ? words.hideFailure : words.hide} onClick={() => onHide(strip.hides!)} size="sm">
          <X aria-hidden="true" size={16} />
        </IconButton>
      ) : null}
    </section>
  );
}
