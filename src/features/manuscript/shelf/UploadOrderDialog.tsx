import { ArrowDown, ArrowUp, FileUp, GripVertical, Upload, X } from 'lucide-react';
import { useRef, useState, type PointerEvent } from 'react';
import { QuotaRefusal } from '../../../entities/account-quota/ui/QuotaRefusal';
import type { Book } from '../../../entities/manuscript/api';
import { Button } from '../../../ui/controls/Button';
import { formatCount } from '../../../ui/copy';
import { Confirm } from '../../../ui/overlays/Confirm';
import { Dialog } from '../../../ui/overlays/Dialog';
import { MoreMenu } from '../../../ui/overlays/MoreMenu';
import { fitWords, loadNumber, moveRow, similarBook, uploadButtonWords, type OrderRow, type RefusedFile, type UploadRoom } from './upload-files';
import styles from './UploadOrderDialog.module.css';

export interface UploadOrderDialogProps {
  rows: readonly OrderRow[];
  refused: readonly RefusedFile[];
  /** The books of the shelf: the rows stand after them, and a row like one of them says so. */
  books: readonly Pick<Book, 'title' | 'place'>[];
  /** What the counters of the account still take (UP1.3); null — the server decides alone. */
  room: UploadRoom | null;
  busy: boolean;
  onRows: (rows: OrderRow[]) => void;
  onReplaceRefused: (key: string, file: File) => void;
  onSend: (rows: readonly OrderRow[]) => void;
  onClose: () => void;
}

/**
 * «Порядок томов» (specs/2026-11-11-ux7-upload.md UP2.3–UP2.5; board 35·21): two files and more. The order of the rows is
 * the order of the volumes — by dragging a row with the mouse or a finger, or «Выше» and «Ниже» of its «⋯»; there is no
 * number to type. Each row says where it will stand on the shelf; all the remarks stand at once, each with its file, and
 * none of them holds the other files back. Esc and a click beside the window ask before a gathered list is dropped.
 */
export function UploadOrderDialog({ books, busy, onClose, onReplaceRefused, onRows, onSend, refused, room, rows }: UploadOrderDialogProps) {
  const [askClose, setAskClose] = useState(false);
  const [dragged, setDragged] = useState<string | null>(null);
  const list = useRef<HTMLOListElement>(null);
  const fits = room === null ? rows.length : Math.min(rows.length, room.room);
  const sent = rows.slice(0, fits);
  const close = () => {
    if (busy) return;
    if (rows.length > 0) setAskClose(true);
    else onClose();
  };
  const remarks = [
    ...rows.filter((row) => row.check).map((row) => ({ key: `check-${row.key}`, text: `«${row.file.name}» — в имени нет номера тома: проверьте его место.` })),
    ...rows.flatMap((row) => {
      const like = similarBook(row.file.name, books);
      return like ? [{
        key: `like-${row.key}`,
        text: `«${row.file.name}» похож на «${like.title}» из этой саги. Если это новая версия той же книги, уберите строку — книгу обновит «Новая версия из файла…» в её «⋯».`,
      }] : [];
    }),
  ];

  function dragOver(event: PointerEvent<HTMLLIElement>) {
    if (!dragged || !list.current) return;
    const target = [...list.current.querySelectorAll<HTMLElement>('[data-row-key]')].find((element) => {
      const rect = element.getBoundingClientRect();
      return event.clientY >= rect.top && event.clientY <= rect.bottom;
    });
    const key = target?.dataset.rowKey;
    if (key && key !== dragged) onRows(moveRow(rows, dragged, key));
  }

  return (
    <>
      <Dialog canDismiss={!busy} dismissOnScrim label="Порядок томов" onDismiss={close} panelClassName={styles.panel} surface="content" width={680}>
        <section className={styles.window}>
          <header className={styles.header}>
            <h2 data-overlay-heading="">Порядок томов</h2>
            <button aria-label="Закрыть окно" className={styles.close} disabled={busy} onClick={close} type="button">
              <X aria-hidden="true" size={20} />
            </button>
          </header>
          <div className={styles.body}>
            <p className={styles.hint}>
              Книги встанут в сагу в порядке строк — после книг, что уже есть. Перетащите строку или выберите «Выше», «Ниже» в «⋯».
            </p>
            {room !== null && fits < rows.length ? (
              <QuotaRefusal code={room.code} place="shelf" words={fitWords(room, rows.length)}
                actions={fits > 0 ? (
                  <Button disabled={busy} onClick={() => onSend(sent)} size="sm" variant="primary">
                    <Upload aria-hidden="true" size={14} />Загрузить {loadNumber(fits)}
                  </Button>
                ) : undefined} />
            ) : null}
            {rows.length > 0 ? (
              <ol aria-label="Тома по порядку" className={styles.rows} ref={list}>
                {rows.map((row, index) => (
                  <li
                    aria-label={row.file.name}
                    className={styles.row}
                    data-dragging={dragged === row.key || undefined}
                    data-outside={index >= fits || undefined}
                    data-row-key={row.key}
                    key={row.key}
                    onPointerMove={dragOver}
                  >
                    <button
                      aria-label={`Перетащить «${row.file.name}»`}
                      className={styles.handle}
                      onPointerCancel={() => setDragged(null)}
                      onPointerDown={(event) => {
                        event.currentTarget.setPointerCapture?.(event.pointerId);
                        setDragged(row.key);
                      }}
                      onPointerMove={(event) => dragged === row.key && dragOver(event as unknown as PointerEvent<HTMLLIElement>)}
                      onPointerUp={() => setDragged(null)}
                      type="button"
                    >
                      <GripVertical aria-hidden="true" size={18} />
                    </button>
                    <span className={styles.name} title={row.file.name}>{row.file.name}</span>
                    {row.check ? <span className={styles.check}>проверьте место</span> : null}
                    <span className={styles.place}>
                      {index < fits ? <><span className={styles.placeVerb}>встанет </span>книгой {books.length + index + 1}</> : 'не поместится'}
                    </span>
                    <MoreMenu
                      items={[
                        ...(index > 0 ? [{ icon: ArrowUp, label: 'Выше', run: () => onRows(moveRow(rows, row.key, rows[index - 1]!.key)) }] : []),
                        ...(index < rows.length - 1 ? [{ icon: ArrowDown, label: 'Ниже', run: () => onRows(moveRow(rows, row.key, rows[index + 1]!.key)) }] : []),
                        { icon: X, label: 'Убрать', run: () => onRows(rows.filter((each) => each.key !== row.key)) },
                      ]}
                      label={`Действия строки «${row.file.name}»`}
                    />
                  </li>
                ))}
              </ol>
            ) : null}
            {remarks.length > 0 || refused.length > 0 ? (
              <ul aria-label="Замечания" className={styles.remarks}>
                {remarks.map((remark) => <li key={remark.key}>{remark.text}</li>)}
                {refused.map((file) => (
                  <li className={styles.refused} key={file.key}>
                    <span>«{file.file.name}» — {file.reason}</span>
                    <label className={styles.choose}>
                      <FileUp aria-hidden="true" size={15} />Выбрать другой
                      <input
                        aria-label={`Выбрать другой файл вместо «${file.file.name}»`}
                        onChange={(event) => {
                          const next = event.target.files?.[0];
                          event.target.value = '';
                          if (next) onReplaceRefused(file.key, next);
                        }}
                        type="file"
                      />
                    </label>
                  </li>
                ))}
              </ul>
            ) : null}
            {refused.length > 0 ? (
              <p className={styles.count}>Подошли {rows.length} из {rows.length + refused.length}</p>
            ) : null}
          </div>
          <footer className={styles.footer}>
            <Button disabled={busy} onClick={close} variant="outline">Отмена</Button>
            <Button disabledReason={fits === 0 ? (rows.length === 0 ? 'Нет файлов, которые можно загрузить.' : 'В саге нет места для новых книг.') : null}
              loading={busy} onClick={() => onSend(sent)} variant="primary">
              <Upload aria-hidden="true" size={16} />{uploadButtonWords(fits || rows.length)}
            </Button>
          </footer>
        </section>
      </Dialog>
      {askClose ? (
        <Confirm
          action="Не загружать"
          irreversible
          onCancel={() => setAskClose(false)}
          onConfirm={() => {
            setAskClose(false);
            onClose();
          }}
          title={`Не загружать ${rows.length === 1 ? 'этот файл' : `эти ${formatCount(rows.length, 'файл', 'файла', 'файлов')}`}?`}
        >
          Список и порядок пропадут — файлы придётся выбрать заново.
        </Confirm>
      ) : null}
    </>
  );
}
