import { Check, Download, Library, Plus, Settings } from 'lucide-react';
import { useId, useState, type CSSProperties, type ReactNode } from 'react';
import { Alert } from '../feedback/Alert';
import { Badge } from '../feedback/Badge';
import { Popover } from '../overlays/Popover';
import type { ShellSaga } from '../shell/ShellTopBar';
import styles from './SagaMenu.module.css';

export interface SagaChoiceItem {
  id: string;
  title: string;
  /** The saga-example of the site (specs/2026-11-09-demo-saga.md DS4.1). */
  demo?: boolean;
}

interface SagaChoiceInput {
  /** The saga of the page, once the server has named it. */
  current: SagaChoiceItem | null;
  sagas: readonly SagaChoiceItem[];
  /** Moves the page to the saga chosen; the page closes the list once it is there. */
  onChoose: (projectId: string) => void;
  choosing: boolean;
  failed: boolean;
  /** «Новая сага»: the window of the name of a new saga. */
  onCreate?: () => void;
  /**
   * The quiet line under «Новая сага» when the sagas take their place: «Саг 1 из 1 · больше — на „Художнике“»
   * (specs/2026-11-10-ux2-patterns-refusals.md PR2.7).
   */
  createNote?: ReactNode;
  /** «Настроить сагу»: the window of the saga of the page. */
  onSettings?: () => void;
  /** «Скачать всю сагу…»: the window of the kind of the files of its books (specs/2026-11-11-ux9-shelf-chat.md SC1.5). */
  onDownload?: (() => void) | undefined;
  /** The name of the button when the address names a saga the author has not: the menu still lets him choose. */
  missing?: string;
}

/** Under the control that opened the list, where it is; a list opened from a menu of the phone stands as a sheet. */
export function sagaMenuAnchor(trigger: HTMLElement): CSSProperties {
  const rect = trigger.getBoundingClientRect();
  return { '--saga-menu-top': `${Math.round(rect.bottom + 6)}px`, '--saga-menu-left': `${Math.round(Math.max(8, rect.left))}px` } as CSSProperties;
}

/**
 * The one menu of the saga on every page (specs/2026-11-10-ux2-shell.md S3.1; `Pattern/Shell/Saga Menu`): «Ваши саги» —
 * the current one marked, the example with «Пример» — then «Новая сага», «Скачать всю сагу…» for whoever may take its files
 * (specs/2026-11-11-ux9-shelf-chat.md SC1.5) and «Настроить сагу». The name of the saga in the
 * top bar and its line in the drawer of a phone open it; choosing a saga moves the page to it.
 */
export function useSagaChoice({ choosing, createNote, current, failed, missing, onChoose, onCreate, onDownload, onSettings, sagas }: SagaChoiceInput) {
  const [at, setAt] = useState<{ anchor?: CSSProperties } | null>(null);
  const labelId = useId();
  const close = () => setAt(null);
  const open = (trigger?: HTMLElement) => setAt(trigger ? { anchor: sagaMenuAnchor(trigger) } : {});
  const named = current ? { name: current.title, demo: current.demo === true } : missing ? { name: missing, demo: false } : null;
  const saga: ShellSaga | null = named
    ? { ...named, menuOpen: Boolean(at), onOpenMenu: (trigger) => (at ? close() : open(trigger)) }
    : null;
  const act = (then: () => void) => () => {
    close();
    then();
  };
  const menu = (
    <Popover className={styles.menu} label="Выбор саги" onDismiss={close} open={Boolean(at)} role="menu" style={at?.anchor}>
      <div aria-labelledby={labelId} className={styles.group} role="group">
        <p aria-hidden="true" className={styles.label} id={labelId}>Ваши саги</p>
        {sagas.map((item) => {
          const here = item.id === current?.id;
          return (
            <button aria-current={here ? 'true' : undefined} className={styles.item} disabled={choosing} key={item.id}
              onClick={() => (here ? close() : onChoose(item.id))} role="menuitem" type="button">
              <Library aria-hidden="true" className={styles.icon} size={16} />
              <span className={styles.title}>{item.title}</span>
              {item.demo ? <Badge>Пример</Badge> : null}
              <span aria-hidden="true" className={styles.mark}>{here ? <Check size={16} /> : null}</span>
            </button>
          );
        })}
      </div>
      {failed ? <Alert tone="error">Не удалось сменить сагу.</Alert> : null}
      {onCreate || (onSettings && current) ? <div aria-hidden="true" className={styles.separator} /> : null}
      {onCreate ? (
        <button className={styles.item} onClick={act(onCreate)} role="menuitem" type="button">
          <Plus aria-hidden="true" className={styles.icon} size={16} />
          <span className={styles.title}>Новая сага{createNote ? <span className={styles.note}>{createNote}</span> : null}</span>
        </button>
      ) : null}
      {onDownload && current ? (
        <button className={styles.item} onClick={act(onDownload)} role="menuitem" type="button">
          <Download aria-hidden="true" className={styles.icon} size={16} />
          <span className={styles.title}>Скачать всю сагу…</span>
        </button>
      ) : null}
      {onSettings && current ? (
        <button className={styles.item} onClick={act(onSettings)} role="menuitem" type="button">
          <Settings aria-hidden="true" className={styles.icon} size={16} />
          <span className={styles.title}>Настроить сагу</span>
        </button>
      ) : null}
    </Popover>
  );
  return { close, menu, open, saga };
}
