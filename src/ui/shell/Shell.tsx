import { ChevronDown, Plus, X } from 'lucide-react';
import { useState, type ComponentPropsWithRef, type ReactNode } from 'react';
import { useNotes } from '../../shared/notes/notes-context';
import { Button } from '../controls/Button';
import { IconButton } from '../controls/IconButton';
import { Badge } from '../feedback/Badge';
import { TopBarStrip } from '../navigation/TopBarStrip';
import { Drawer } from '../overlays/Drawer';
import type { SagaSection } from '../saga-sections';
import { useLowScreen, useNarrowScreen } from '../use-narrow-screen';
import { SectionNav, type ChatMark } from './SectionNav';
import { AccountBlock, useShellHelp, useShellHelpReplies, type ShellAccountProps } from './ShellAccount';
import type { ContentWidth } from './shell-routes';
import { ShellTopBar, type ShellSaga, type ShellSettings, type ShellWidth } from './ShellTopBar';
import styles from './Shell.module.css';

export interface ShellSections {
  /** The sections of the edition. */
  items: readonly SagaSection[];
  active: SagaSection | null;
  onOpen: (section: SagaSection, trigger: HTMLElement) => void;
}

export interface ShellProps {
  /**
   * The word in place of a saga that is not there: «Первая сага» of an author without one (board 14·04). The section is not
   * named by the top bar: the menu and the heading of the page name it (specs/2026-11-11-ux2-polish.md PL1.1).
   */
  place?: string | undefined;
  saga: ShellSaga | null;
  /** The column of the sections; none without a saga (S1.4). */
  sections: ShellSections | null;
  chat?: ChatMark;
  /** The mark of «Рисунки» by the same mechanism (specs/2026-11-11-ux6-drawing-flow.md DF2.2). */
  drawing?: ChatMark;
  /** The mark of «Что помнит помощник»: a file of the materials read, its records waiting (specs/2026-11-11-ux9-shelf-chat.md SC3.6). */
  memory?: ChatMark;
  /** The sections stand in the narrow column at any width: an open chapter, an open drawing, the reader (SC1.2). */
  narrow?: boolean;
  /**
   * «Только текст» of the reader (specs/2026-11-10-ux7-reader.md RD1.6): the top bar, the line under it and the sections
   * step aside and come back while the pointer is at the top of the screen, on a touch there and on Tab.
   */
  bare?: boolean;
  onOpenSearch?: () => void;
  searchRef?: ComponentPropsWithRef<'button'>['ref'];
  /** «Новая сага» of the drawer of a phone (S1.3). */
  onNewSaga?: () => void;
  /** The quiet line under «Новая сага» when the sagas take their place (specs/2026-11-10-ux2-patterns-refusals.md PR2.7). */
  newSagaNote?: ReactNode;
  account?: ShellAccountProps;
  /** «Настройки» in the place of the account of an edition without one (specs/2026-11-13-desk-settings.md ST1.1). */
  settings?: ShellSettings;
  /** The line under the top bar of a reader of the saga (S1.4, П-22). */
  notice?: ReactNode;
  /** The name of the content for the reader of the screen. */
  label: string;
  /** What stands over the page: the menu of the saga, the search, the windows of the shell. */
  overlays?: ReactNode;
  /** The width of the content (PS1.2): «чтение и формы» or «списки и рабочие места». */
  width?: ContentWidth;
  /** «← К <откуда>» over the content, when the author came by a link of another section (PS4.1). */
  back?: ReactNode;
  children: ReactNode;
}

/** The width of the window as the shell takes it (S1.3): from 1100 px, 768–1099 px, below 768 px. */
export function useShellWidth(): ShellWidth {
  const phone = useNarrowScreen(767);
  const compact = useNarrowScreen(1099);
  return phone ? 'phone' : compact ? 'compact' : 'wide';
}

/**
 * The one shell of a signed-in author (specs/2026-11-10-ux2-shell-core.md UC1; board 14, `Template/Shell/*`): the top bar,
 * the strips under it, the column of the sections, the content of the page and the drawer of a phone. The root puts it
 * around every page of the author once; a page gives only its content. A low window (to 760 px high) and a workplace keep
 * the narrow column; a phone has the drawer.
 */
export function Shell({ account, back, bare = false, chat = null, children, drawing = null, label, memory = null, narrow = false, newSagaNote, notice, onNewSaga, onOpenSearch, overlays, place, saga, searchRef, sections, settings, width: contentWidth = 'wide' }: ShellProps) {
  const width = useShellWidth();
  const low = useLowScreen(760);
  const notes = useNotes();
  const help = useShellHelp();
  const helpReplies = useShellHelpReplies();
  const [drawer, setDrawer] = useState(false);
  const [revealed, setRevealed] = useState(false);
  const phone = width === 'phone';
  // The pointer at the top edge brings the top bar back; it stays while the pointer is on it or on the sections.
  const reveal = (event: { clientX: number; clientY: number }, touch = false) => {
    const near = event.clientY < (revealed ? 72 : touch ? 40 : 12) || (revealed && !phone && event.clientX < 100);
    if (near !== revealed) setRevealed(near);
  };
  const rail = narrow || low || width === 'compact';
  // Without a saga there is nothing to write a note to or to search in (board 14·04).
  const onNote = saga && notes ? () => notes.capture({ body: '' }) : undefined;
  const close = (then?: () => void) => {
    setDrawer(false);
    then?.();
  };
  return (
    <div className={styles.shell} data-bare={bare || undefined} data-column={sections && !phone ? (rail ? 'rail' : 'list') : 'none'} data-revealed={(bare && revealed) || undefined}
      data-shell="" data-width={width} onPointerDown={bare ? (event) => reveal(event, event.pointerType !== 'mouse') : undefined}
      onPointerMove={bare ? (event) => { if (event.pointerType === 'mouse') reveal(event); } : undefined}>
      <ShellTopBar account={account} menuMark={chat !== null || drawing !== null || memory !== null || helpReplies > 0} onNote={onNote} onOpenMenu={() => setDrawer(true)}
        onOpenSearch={saga ? onOpenSearch : undefined} place={place} saga={saga} searchRef={searchRef} settings={settings} talk width={width} />
      <TopBarStrip />
      {bare ? null : notice}
      <div className={styles.body}>
        {sections && !phone ? (
          <SectionNav active={sections.active} chat={chat} drawing={drawing} helpReplies={helpReplies} memory={memory} onHelp={help} onOpen={sections.onOpen}
            sections={sections.items} variant={rail ? 'rail' : 'list'} />
        ) : null}
        <main aria-label={label} className={styles.content} data-page-content="" id="page-content" tabIndex={-1}>
          <div className={styles.page} data-content-width={contentWidth}>
            {back}
            {children}
          </div>
        </main>
      </div>
      {overlays}
      {drawer && phone ? (
        <Drawer canDismiss className={styles.drawer} label="Меню" onDismiss={() => close()}>
          <div className={styles.drawerHead}>
            {saga ? (
              <button aria-haspopup="menu" className={styles.drawerSaga} onClick={() => close(() => saga.onOpenMenu())} type="button">
                <span className={styles.sagaName}><span className={styles.visuallyHidden}>Сага: </span>{saga.name}</span>
                {saga.demo ? <Badge>Пример</Badge> : null}
                <ChevronDown aria-hidden="true" size={16} />
              </button>
            ) : <p className={styles.drawerTitle}>{place ?? label}</p>}
            <IconButton aria-label="Закрыть меню" onClick={() => close()} size="touch"><X aria-hidden="true" size={18} /></IconButton>
          </div>
          {onNewSaga ? (
            <Button className={styles.drawerNew} onClick={() => close(onNewSaga)} size="touch" variant="ghost">
              <Plus aria-hidden="true" size={16} />Новая сага
            </Button>
          ) : null}
          {onNewSaga && newSagaNote ? <div className={styles.drawerNote}>{newSagaNote}</div> : null}
          {sections ? (
            <SectionNav active={sections.active} chat={chat} drawing={drawing} helpReplies={helpReplies} memory={memory} onHelp={help ? () => close(help) : null}
              onOpen={(section, trigger) => close(() => sections.onOpen(section, trigger))} sections={sections.items} variant="drawer" />
          ) : help ? (
            <SectionNav active={null} chat={null} helpReplies={helpReplies} onHelp={() => close(help)} onOpen={() => undefined} sections={[]} variant="drawer" />
          ) : null}
          {account ? (
            <div className={styles.drawerAccount}>
              <AccountBlock {...account} onDone={() => close()} support={!help} theme={false} />
            </div>
          ) : null}
        </Drawer>
      ) : null}
    </div>
  );
}
