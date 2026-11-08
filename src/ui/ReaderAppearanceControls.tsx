import { Maximize2, Minimize2, Settings2 } from 'lucide-react';
import { Button } from './controls/Button';
import { SegmentedControl } from './navigation/SegmentedControl';
import styles from './ReaderAppearanceControls.module.css';

export function ReaderFocusButton({ focusMode, onToggleFocus }: { focusMode: boolean; onToggleFocus: () => void }) {
  return <Button aria-label={focusMode ? 'Выйти из режима фокуса' : 'Режим фокуса'} onClick={onToggleFocus} size="touch" variant="outline">
    {focusMode ? <Minimize2 aria-hidden="true" size={17} /> : <Maximize2 aria-hidden="true" size={17} />}
    {focusMode ? 'Обычный режим' : 'Режим фокуса'}
  </Button>;
}

export function ReaderAppearanceControls({ displaySize, displayTheme, focusMode, onSetDisplaySize, onSetDisplayTheme, onToggleFocus }: {
  displaySize: 'normal' | 'large';
  displayTheme: 'light' | 'sepia' | 'dark';
  focusMode: boolean;
  onSetDisplaySize: (size: 'normal' | 'large') => void;
  onSetDisplayTheme: (theme: 'light' | 'sepia' | 'dark') => void;
  onToggleFocus: () => void;
}) {
  return <div aria-label="Настройки чтения" className={styles.controls}>
    <Button aria-pressed={displaySize === 'large'} onClick={() => onSetDisplaySize(displaySize === 'normal' ? 'large' : 'normal')} variant="outline">
      <Settings2 aria-hidden="true" size={17} />Размер текста
    </Button>
    <SegmentedControl className={styles.theme} label="Тема" value={displayTheme} onValueChange={onSetDisplayTheme}
      options={[{ label: 'Светлая', value: 'light' }, { label: 'Сепия', value: 'sepia' }, { label: 'Тёмная', value: 'dark' }]} />
    <ReaderFocusButton focusMode={focusMode} onToggleFocus={onToggleFocus} />
  </div>;
}
