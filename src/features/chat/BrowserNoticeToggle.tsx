import { Check } from 'lucide-react';
import { Button } from '../../ui/controls/Button';
import { toggleBrowserNotice, useBrowserNotice } from './answer-ready';
import styles from './BrowserNoticeToggle.module.css';

/** Why the browser cannot say the answer is ready, and what the author can do instead (PR1.1). */
const reasons = {
  denied: 'Уведомления для этого сайта запрещены в настройках браузера — разрешите их там. Ответ сохранится и так.',
  unsupported: 'Этот браузер не умеет сообщать о готовом ответе. Можно уйти — ответ сохранится в этом чате.',
} as const;

/**
 * «Сообщить, когда будет готово» beside the time of a running turn: a browser notification when the answer is ready
 * (R6.4). A switch of words, not a bell (specs/2026-11-10-ux4-chat-waiting.md CW2.6; W-76); where the browser forbids or
 * has no notifications it stays and says why by a press (specs/2026-11-10-ux2-patterns-refusals.md PR1.1).
 */
export function BrowserNoticeToggle() {
  const notice = useBrowserNotice();
  const on = notice === 'on';
  return (
    <Button aria-pressed={on} className={styles.toggle} data-on={on || undefined}
      disabledReason={notice === 'denied' || notice === 'unsupported' ? reasons[notice] : null}
      onClick={() => void toggleBrowserNotice()} size="sm" variant="ghost">
      {on ? <Check aria-hidden="true" size={15} /> : null}Сообщить, когда будет готово
    </Button>
  );
}
