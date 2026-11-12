import { useEffect, useId, useState, type KeyboardEvent } from 'react';
import { textSourceView } from '../../entities/manuscript/api';
import { useSagaMapSearchQuery, type SagaApiClient } from '../../entities/saga/api';
import { Field } from '../../ui/controls/Field';
import styles from './AuthorMemory.module.css';

const searchDelay = 300;
const shownNames = 8;

const nameKey = (name: string) => name.trim().toLocaleLowerCase('ru').replaceAll('ё', 'е');

/**
 * «Имя» of a record (specs/2026-11-10-ux8-memory-records.md MR1.3): the names of the saga as the map finds them, at most eight,
 * asked not oftener than every 300 ms and chosen by the arrows and Enter. A name the book does not know stays allowed and is
 * marked under the field; a saga without a map has nothing to suggest or to mark.
 */
export function NameField({ api, onChange, projectId, value }: { api: SagaApiClient; onChange: (name: string) => void; projectId: string; value: string }) {
  const listId = useId();
  const [settled, setSettled] = useState(value.trim());
  const [open, setOpen] = useState(false);
  const [active, setActive] = useState(-1);
  useEffect(() => {
    const timer = window.setTimeout(() => setSettled(value.trim()), searchDelay);
    return () => window.clearTimeout(timer);
  }, [value]);
  // The most mentioned names say whether the saga has a map at all.
  const map = useSagaMapSearchQuery(api, projectId, { sourceView: textSourceView, limit: shownNames });
  const search = useSagaMapSearchQuery(api, projectId, { sourceView: textSourceView, q: settled, limit: shownNames }, settled.length > 0);
  const hasMap = (map.data?.entries.length ?? 0) > 0;
  const found = settled && search.data ? search.data.entries.slice(0, shownNames) : [];
  const shown = open && hasMap ? found.filter((entry) => nameKey(entry.name) !== nameKey(value) || found.length > 1) : [];
  const known = found.some((entry) => nameKey(entry.name) === nameKey(value));
  const unknown = hasMap && value.trim() !== '' && settled === value.trim() && search.isSuccess && !search.isPlaceholderData && !known;
  const pick = (name: string) => {
    onChange(name);
    setOpen(false);
    setActive(-1);
  };
  const onKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
      if (!shown.length) return;
      event.preventDefault();
      setOpen(true);
      const step = event.key === 'ArrowDown' ? 1 : -1;
      setActive((active + step + shown.length) % shown.length);
    } else if (event.key === 'Enter' && active >= 0 && shown[active]) {
      event.preventDefault();
      pick(shown[active]!.name);
    } else if (event.key === 'Escape' && shown.length) {
      // The list closes first; the dialog stays.
      event.preventDefault();
      event.stopPropagation();
      setOpen(false);
      setActive(-1);
    }
  };
  return (
    <div className={styles.nameField}>
      <Field
        aria-activedescendant={active >= 0 && shown[active] ? `${listId}-${active}` : undefined}
        aria-autocomplete="list"
        aria-controls={shown.length ? listId : undefined}
        aria-expanded={shown.length > 0}
        autoComplete="off"
        hint={unknown ? 'В книге такого имени нет — запись сработает, когда оно появится в тексте или в ваших материалах.' : 'Необязательно. Герой или название, о котором запись: помощник найдёт запись по этому имени.'}
        label="Имя"
        maxLength={200}
        onBlur={() => setOpen(false)}
        onChange={(event) => { onChange(event.target.value); setOpen(true); setActive(-1); }}
        onFocus={() => setOpen(true)}
        onKeyDown={onKeyDown}
        role="combobox"
        value={value}
      />
      {shown.length ? (
        <ul aria-label="Имена саги" className={styles.names} id={listId} role="listbox">
          {shown.map((entry, index) => (
            <li aria-selected={index === active} className={styles.nameOption} id={`${listId}-${index}`} key={entry.key}
              onMouseDown={(event) => { event.preventDefault(); pick(entry.name); }} role="option">
              <span>{entry.name}</span>
              <span className={styles.nameType}>{entry.type}</span>
            </li>
          ))}
        </ul>
      ) : null}
    </div>
  );
}
