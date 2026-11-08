import type { SheetProps } from './Sheet';
import { Sheet } from './Sheet';
import styles from './overlays.module.css';

export type ActionSheetProps = SheetProps;

export function ActionSheet({ children, ...props }: ActionSheetProps) {
  return (
    <Sheet {...props}>
      <div className={styles.actionSheet}>{children}</div>
    </Sheet>
  );
}
