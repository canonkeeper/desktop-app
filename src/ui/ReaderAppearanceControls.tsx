import { useId } from 'react';
import { Button } from './controls/Button';
import { SegmentedControl } from './navigation/SegmentedControl';
import styles from './ReaderAppearanceControls.module.css';
import type { TextBackground } from './text-background';
import { TextBackgroundChoice } from './TextBackgroundChoice';
import { useKeyboardAtHand } from './use-narrow-screen';

const widths = [{ label: 'Узкая', value: 'narrow' }, { label: 'Обычная', value: 'normal' }, { label: 'Широкая', value: 'wide' }] as const;

type Width = 'narrow' | 'normal' | 'wide';

/**
 * «Аа» — the one window of the look of the text (specs/2026-11-10-ux7-reader.md RD1.4, board 40·27): the size in seven
 * steps with the current one said, «Фон текста» (specs/2026-11-12-ux11-dark-core.md TH1.3), the width of the column and
 * «Только текст». The values are the browser's (display-preferences-store); «Письмо» reads the same size and background.
 */
export function ReaderAppearanceControls({ columnWidth, onColumnWidth, onOnlyText, onStepSize, onTheme, onlyText, step, steps, textSize, theme }: {
  /** The size in pixels and its place among the steps, from one. */
  textSize: number;
  step: number;
  steps: number;
  onStepSize: (step: -1 | 1) => void;
  theme: TextBackground;
  onTheme: (theme: TextBackground) => void;
  columnWidth: Width;
  onColumnWidth: (width: Width) => void;
  onlyText: boolean;
  onOnlyText: () => void;
}) {
  const sizeLabel = useId();
  const onlyHint = useId();
  // Esc is named only where a keyboard is at hand: under a finger «Только текст» is undone by its own button (SH4.1, П-19).
  const keyboard = useKeyboardAtHand();
  return (
    <div className={styles.controls}>
      <section aria-labelledby={sizeLabel} className={styles.group}>
        <h3 className={styles.label} id={sizeLabel}>Размер текста</h3>
        <div className={styles.size}>
          <Button aria-label="А− мельче" disabledReason={step === 1 ? 'Это самый мелкий размер.' : null} onClick={() => onStepSize(-1)} size="sm" variant="outline">А−</Button>
          <output aria-live="polite" className={styles.current}>
            <strong>{textSize} px</strong>
            <span>{step} из {steps}</span>
          </output>
          <Button aria-label="А+ крупнее" disabledReason={step === steps ? 'Это самый крупный размер.' : null} onClick={() => onStepSize(1)} size="sm" variant="outline">А+</Button>
        </div>
      </section>
      <div className={styles.group}>
        <span aria-hidden="true" className={styles.label}>Фон текста</span>
        <TextBackgroundChoice fullWidth onValue={onTheme} value={theme} />
      </div>
      <div className={`${styles.group} ${styles.width}`}>
        <span aria-hidden="true" className={styles.label}>Ширина колонки</span>
        <SegmentedControl fullWidth label="Ширина колонки" onValueChange={onColumnWidth} options={widths} value={columnWidth} />
      </div>
      <div className={styles.only}>
        <span className={styles.onlyWords}>
          <strong>Только текст</strong>
          <span id={onlyHint}>{keyboard ? 'Шапка и панели уходят. Вернуть — Esc или эта же кнопка' : 'Шапка и панели уходят. Вернуть — эта же кнопка'}</span>
        </span>
        <button aria-checked={onlyText} aria-describedby={onlyHint} className={styles.switch} onClick={onOnlyText} role="switch" type="button">
          <span aria-hidden="true" className={styles.knob} />
          <span className={styles.visuallyHidden}>Только текст</span>
        </button>
      </div>
    </div>
  );
}
