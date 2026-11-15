// SPDX-License-Identifier: AGPL-3.0-only
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { ArrowRight, ExternalLink, HardDrive, Package, RefreshCw, Send } from 'lucide-react';
import { useId, useRef, useState } from 'react';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { desktopShell } from '../../../shared/desktop/shell';
import { Button, ButtonLink } from '../../../ui/controls/Button';
import { SkeletonArea } from '../../../ui/feedback/Skeleton';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Dialog } from '../../../ui/overlays/Dialog';
import { external } from '../external';
import { desktopStateQueryKey } from '../settings/desktop-state';
import { appUpdateKey, useAppUpdate } from './app-update';
import { Switch } from '../Switch';
import { releasesUrl, updateWords as words } from './update-words';
import { useWelcomeState } from './welcome-state';
import styles from './Updates.module.css';

type WelcomeClient = Pick<CanonKeeperApiClient, 'getDesktopState' | 'getAppUpdate' | 'dismissWelcome'>;

/** Nothing closes the window but «Начать» (UW1.1). */
const stays = () => undefined;

/**
 * «CanonKeeper на вашем компьютере» (specs/2026-11-15-desk-updates-web.md UW1.1–UW1.3; board 44·1, `Pattern/Desktop/Welcome`):
 * over the whole product while `getDesktopState.welcome` — where the data lie, what goes out, the check of new versions
 * with its switch, and «Начать». Esc, «Назад» and a click beside it do not close it; it closes by the new read of the
 * state after «Начать», not by the memory of the page. A build updated by hand has the way to the releases in the place of
 * the switch and keeps the choice of the server. No word of a log, «Письмо» or the voice: this build has none.
 */
export function Welcome({ api }: { api: WelcomeClient }) {
  const state = useWelcomeState(api);
  if (state.data?.welcome !== true) return null;
  return <WelcomeWindow api={api} checkUpdates={state.data.checkUpdates} dataDir={state.data.dataDir} reading={state.isFetching} />;
}

function WelcomeWindow({ api, checkUpdates, dataDir, reading }: { api: WelcomeClient; checkUpdates: boolean; dataDir: string; reading: boolean }) {
  const queryClient = useQueryClient();
  const toasts = useToasts();
  const update = useAppUpdate(api);
  const start = useRef<HTMLButtonElement>(null);
  const checkWords = useId();
  // The choice of the switch until «Начать»: a draft of the window, the server's value until the author moves it.
  const [choice, setChoice] = useState<boolean | null>(null);
  const checked = choice ?? checkUpdates;
  const manual = update.data?.kind === 'none';
  const dismiss = useMutation({
    mutationFn: (value: boolean) => api.dismissWelcome({ checkUpdates: value }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: desktopStateQueryKey });
      void queryClient.invalidateQueries({ queryKey: appUpdateKey });
    },
  });
  // Busy while the answer goes and while the state is read again after it: the window closes by that read.
  const busy = dismiss.isPending || (dismiss.isSuccess && reading);
  return (
    <Dialog canDismiss={false} initialFocusRef={start} label={words.welcomeTitle} onDismiss={stays} width={560}>
      <section className={styles.welcome}>
        <h2 className={styles.welcomeTitle} data-overlay-heading="">{words.welcomeTitle}</h2>
        <ul className={styles.welcomeItems}>
          <li className={styles.welcomeItem}>
            <HardDrive aria-hidden="true" className={styles.welcomeIcon} size={18} />
            <div className={styles.welcomeText}>
              <span>{words.welcomeData}</span>
              <span className={styles.path}>{dataDir}</span>
            </div>
          </li>
          <li className={styles.welcomeItem}>
            <Send aria-hidden="true" className={styles.welcomeIcon} size={18} />
            <div className={styles.welcomeText}><span>{words.welcomeOut}</span><span className={styles.welcomeNote}>{words.welcomeLog}</span></div>
          </li>
          {/* The place of the switch waits for the kind of the build; a read that failed leaves the switch (UW1.2). */}
          {update.isPending ? (
            <li className={styles.welcomeItem} data-waiting=""><SkeletonArea count={1} shape="lines" /></li>
          ) : manual ? (
            <li className={styles.welcomeItem}>
              <Package aria-hidden="true" className={styles.welcomeIcon} size={18} />
              <div className={styles.welcomeText}>
                <span>{words.manual}</span>
                <div>
                  <ButtonLink {...external(releasesUrl, desktopShell(), toasts)} size="sm" variant="outline">
                    <ExternalLink aria-hidden="true" size={16} />{words.releases}
                  </ButtonLink>
                </div>
              </div>
            </li>
          ) : (
            <li className={styles.welcomeItem}>
              <RefreshCw aria-hidden="true" className={styles.welcomeIcon} size={18} />
              <div className={styles.welcomeText}><span id={checkWords}>{words.welcomeCheck}</span></div>
              <Switch checked={checked} className={styles.welcomeSwitch} labelledBy={checkWords} onChange={setChoice} />
            </li>
          )}
        </ul>
        {/* The window keeps its own refusal: the area of the toasts lies under its shade (UW1.1). */}
        {dismiss.isError ? <p className={styles.welcomeFailed} role="alert">{words.welcomeFailed}</p> : null}
        <footer className={styles.welcomeFooter}>
          <Button loading={busy} onClick={() => {
            if (busy) return;
            // A build updated by hand keeps the choice of the server: it may be an AppImage started not from its file. Before
            // the kind is known no switch stood, and the choice is the server's as well.
            dismiss.mutate(manual ? checkUpdates : checked);
          }} ref={start} variant="primary">
            <ArrowRight aria-hidden="true" size={16} />{words.welcomeStart}
          </Button>
        </footer>
      </section>
    </Dialog>
  );
}
