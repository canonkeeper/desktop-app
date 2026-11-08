import { Bell, BellOff, BellRing } from 'lucide-react';
import { IconButton } from '../../ui/controls/IconButton';
import { toggleBrowserNotice, useBrowserNotice } from './answer-ready';
import styles from './BrowserNoticeToggle.module.css';

/** The bell of a running turn: a browser notification when the answer is ready (R6.4). */
export function BrowserNoticeToggle() {
  const notice = useBrowserNotice();
  if (notice === 'unsupported') return null;
  const label = notice === 'denied'
    ? 'Уведомления запрещены в настройках браузера для этого сайта'
    : 'Сообщить в браузере, когда ответ будет готов';
  const Icon = notice === 'on' ? BellRing : notice === 'denied' ? BellOff : Bell;
  return (
    <IconButton aria-label={label} aria-pressed={notice === 'on'} className={styles.toggle} data-on={notice === 'on' || undefined}
      disabled={notice === 'denied'} onClick={() => void toggleBrowserNotice()} size="sm" title={label}>
      <Icon aria-hidden="true" size={16} />
    </IconButton>
  );
}
