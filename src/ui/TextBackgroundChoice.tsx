import { useSiteTheme } from '../shared/theme/site-theme';
import { LinkAction } from './controls/LinkAction';
import { SegmentedControl } from './navigation/SegmentedControl';
import type { TextBackground } from './text-background';
import styles from './TextBackgroundChoice.module.css';

const backgrounds = [{ label: 'Светлый', value: 'light' }, { label: 'Сепия', value: 'sepia' }, { label: 'Тёмный', value: 'dark' }] as const;

/**
 * «Фон текста: Светлый · Сепия · Тёмный» (specs/2026-11-12-ux11-dark-core.md TH1.3; Pattern/Reader/Appearance,
 * Pattern/Writing/Look Sheet): the background of the sheet of text in the reader and in «Письмо». Beside a dark sheet on a
 * light site — «Сделать тёмным весь сайт». Labelled, the words «Фон текста» stand before it where nothing else names it
 * (specs/2026-11-12-ux12-writing.md WR4.1; Pattern/Writing/Chapter Foot).
 */
export function TextBackgroundChoice({ fullWidth = false, labelled = false, onValue, value }: {
  fullWidth?: boolean;
  labelled?: boolean;
  onValue: (background: TextBackground) => void;
  value: TextBackground;
}) {
  const site = useSiteTheme();
  return (
    <div className={styles.choice} data-labelled={labelled ? '' : undefined}>
      {labelled ? <span aria-hidden="true" className={styles.label}>Фон текста</span> : null}
      <SegmentedControl fullWidth={fullWidth} label="Фон текста" onValueChange={onValue} options={backgrounds} value={value} />
      {value === 'dark' && site.theme === 'light' ? <LinkAction className={styles.whole} onClick={() => site.choose('dark')}>Сделать тёмным весь сайт</LinkAction> : null}
    </div>
  );
}
