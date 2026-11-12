import { ChevronDown, ChevronUp, Library, List, X } from 'lucide-react';
import type { RefObject } from 'react';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { useNarrowScreen } from '../../../ui/use-narrow-screen';
import { minFindLength } from './reader-find';
import styles from './ReaderFindBar.module.css';

export type FindBarState =
  | { kind: 'typing' }
  | { kind: 'searching' }
  | { kind: 'failed'; retry: () => void }
  | { kind: 'none' }
  | { kind: 'found'; current: number; total: number; complete: boolean };

/**
 * «Найти» in the book (specs/2026-11-10-ux7-reader.md RD2.1–RD2.4; Pattern/Reader/Find Bar, board 40·29): a bar over the
 * text — the field, «3 из 17», «‹» and «›» (Shift+Enter and Enter), «Все места», «×». Nothing found — «В этой книге такого
 * нет» and «Искать во всех книгах саги», the search of the shell with the same words.
 */
export function ReaderFindBar({ fieldRef, onAll, onClose, onEverywhere, onNext, onPrevious, onValue, state, value }: {
  value: string;
  onValue: (value: string) => void;
  fieldRef: RefObject<HTMLInputElement | null>;
  state: FindBarState;
  onNext: () => void;
  onPrevious: () => void;
  onAll: () => void;
  onClose: () => void;
  onEverywhere: () => void;
}) {
  const phone = useNarrowScreen(767);
  const found = state.kind === 'found';
  return (
    <div aria-label="Найти в книге" className={styles.bar} role="search">
      <input aria-label="Найти в книге" autoComplete="off" className={styles.field} enterKeyHint="search"
        onChange={(event) => onValue(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
            event.preventDefault();
            if (event.shiftKey) onPrevious(); else onNext();
          }
          if (event.key === 'Escape') {
            event.preventDefault();
            event.stopPropagation();
            onClose();
          }
        }}
        placeholder="Слово или фраза из книги" ref={fieldRef} type="search" value={value} />
      <span aria-live="polite" className={styles.count} role="status">{countWords(state)}</span>
      {state.kind === 'failed' ? <Button onClick={state.retry} size="sm" variant="ghost">Повторить</Button> : null}
      {state.kind === 'none' ? (
        <Button onClick={onEverywhere} size="sm" variant="ghost"><Library aria-hidden="true" size={15} />Искать во всех книгах саги</Button>
      ) : (
        <>
          {/* On a phone the steps say their word under the arrow (specs/2026-11-11-ux9-shelf-chat.md SC2.2, П-15). */}
          {phone ? <>
            <Button aria-keyshortcuts="Shift+Enter" aria-label="Предыдущее место" caption disabledReason={found && state.total ? null : 'Мест пока нет.'} onClick={onPrevious} variant="ghost">
              <ChevronUp aria-hidden="true" size={18} />Назад
            </Button>
            <Button aria-keyshortcuts="Enter" aria-label="Следующее место" caption disabledReason={found && state.total ? null : 'Мест пока нет.'} onClick={onNext} variant="ghost">
              <ChevronDown aria-hidden="true" size={18} />Дальше
            </Button>
          </> : <>
            <IconButton aria-keyshortcuts="Shift+Enter" aria-label="Предыдущее место" disabledReason={found && state.total ? null : 'Мест пока нет.'} onClick={onPrevious} size="touch">
              <ChevronUp aria-hidden="true" size={18} />
            </IconButton>
            <IconButton aria-keyshortcuts="Enter" aria-label="Следующее место" disabledReason={found && state.total ? null : 'Мест пока нет.'} onClick={onNext} size="touch">
              <ChevronDown aria-hidden="true" size={18} />
            </IconButton>
          </>}
          <Button aria-label="Все места" className={styles.all} disabledReason={found && state.total ? null : 'Мест пока нет.'} onClick={onAll} size="sm" variant="ghost">
            <List aria-hidden="true" size={15} /><span>Все места</span>
          </Button>
        </>
      )}
      <IconButton aria-label="Закрыть поиск" onClick={onClose} size="touch"><X aria-hidden="true" size={18} /></IconButton>
    </div>
  );
}

function countWords(state: FindBarState) {
  switch (state.kind) {
    case 'typing': return `Не меньше ${minFindLength} букв`;
    case 'searching': return 'Ищу…';
    case 'failed': return 'Не получилось найти';
    case 'none': return 'В этой книге такого нет';
    case 'found': return `${state.current} из ${state.total}${state.complete ? '' : '…'}`;
  }
}
