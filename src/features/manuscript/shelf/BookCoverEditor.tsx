import { useQueryClient } from '@tanstack/react-query';
import { Check, ImageOff, ImageUp, Images, X } from 'lucide-react';
import { useEffect, useRef, useState } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import { applyCoverBookResponse, type Book, type ManuscriptApiClient, type UploadProgress } from '../../../entities/manuscript/api';
import { formatFileSize } from '../../../ui/copy';
import { Button } from '../../../ui/controls/Button';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import { Progress } from '../../../ui/feedback/Progress';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { Confirm, deletionLines } from '../../../ui/overlays/Confirm';
import { Dialog } from '../../../ui/overlays/Dialog';
import { useDeleteBookCoverMutation, useUpdateBookMutation, useUploadBookCoverMutation } from '../api';
import { BookCover } from './BookCover';
import { coverColours, defaultCoverColour } from './cover-colours';
import styles from './BookCoverEditor.module.css';
import { useShelfSlot, type CoverDrawing } from './shelf-slot';

const coverMimeTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);
const maxCoverBytes = 5 * 1024 * 1024;

const nameOf = (color: string) => coverColours.find((entry) => entry.color.toLowerCase() === color.toLowerCase())?.name ?? 'Свой цвет';

/**
 * «Обложка…» of a book (SF4.1): a window with the cover of this book — the chosen colour stands on it at once, before
 * anything is saved; the colours have names; a picture of the author stands over the colour and goes by «Убрать
 * картинку»; a drawing of the saga is chosen in the same window by «Выбрать из рисунков саги» of a build with drawings
 * (specs/2026-11-11-ux9-shelf-chat.md SC1.6), as it looks now. One «Сохранить» saves what was chosen; the result is a toast
 * of the root.
 */
export function BookCoverEditor({ api, book, onClose }: { api: ManuscriptApiClient; book: Book; onClose: () => void }) {
  const toasts = useToasts();
  const [file, setFile] = useState<File | null>(null);
  const [dimensions, setDimensions] = useState<{ width: number; height: number } | null>(null);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [color, setColor] = useState(book.coverColor ?? defaultCoverColour);
  const [progress, setProgress] = useState<UploadProgress | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  // A drawing of the saga for the cover, and its choice open (SC1.6).
  const CoverDrawings = useShelfSlot()?.CoverDrawings;
  const [drawing, setDrawing] = useState<CoverDrawing | null>(null);
  const [choosing, setChoosing] = useState(false);
  const [fromDrawing, setFromDrawing] = useState(false);
  const queryClient = useQueryClient();
  const fileInput = useRef<HTMLInputElement>(null);
  const uploadMutation = useUploadBookCoverMutation(api);
  const deleteMutation = useDeleteBookCoverMutation(api);
  const updateMutation = useUpdateBookMutation(api);
  const pending = uploadMutation.isPending || deleteMutation.isPending || updateMutation.isPending || fromDrawing;
  const colourChanged = color.toLowerCase() !== (book.coverColor ?? defaultCoverColour).toLowerCase();
  const picture = previewUrl ?? drawing?.picture ?? book.coverUrl;

  useEffect(() => {
    if (!file || typeof URL.createObjectURL !== 'function') {
      setPreviewUrl(null);
      return;
    }
    const url = URL.createObjectURL(file);
    setPreviewUrl(url);
    return () => URL.revokeObjectURL(url);
  }, [file]);

  async function selectFile(selected: File) {
    setFile(null);
    setDimensions(null);
    setError(null);
    if (selected.type && !coverMimeTypes.has(selected.type.toLowerCase())) {
      setError('Подходят картинки JPG, PNG и WEBP.');
      return;
    }
    if (selected.size > maxCoverBytes) {
      setError('Картинка больше 5 МБ.');
      return;
    }
    try {
      const bitmap = await createImageBitmap(selected);
      try {
        if (bitmap.width < 600 || bitmap.height < 900) {
          setError('Картинка должна быть не меньше 600×900.');
          return;
        }
        setDimensions({ width: bitmap.width, height: bitmap.height });
        setFile(selected);
      } finally {
        bitmap.close();
      }
    } catch {
      setError('Не удалось прочитать картинку.');
    }
  }

  async function save() {
    if (pending) return;
    if (!colourChanged && !file && !drawing) {
      onClose();
      return;
    }
    setError(null);
    setProgress(null);
    try {
      if (colourChanged) await updateMutation.mutateAsync({ bookId: book.id, body: { coverColor: color } });
      if (file) await uploadMutation.mutateAsync({ bookId: book.id, file, onUploadProgress: setProgress });
      if (drawing && !file) {
        setFromDrawing(true);
        try {
          applyCoverBookResponse(queryClient, await api.setBookCoverFromImage(book.id, drawing.number));
        } finally {
          setFromDrawing(false);
        }
      }
      toasts.push({ message: `Обложка «${book.title}» сохранена`, tone: 'success' });
      onClose();
    } catch (failure) {
      setError(publicApiErrorMessage(failure, file ? 'Не удалось загрузить картинку.' : drawing ? 'Не удалось сделать обложку из рисунка.' : 'Не удалось сохранить цвет обложки.', {
        codes: { 'cover.too_small': 'Рисунок слишком маленький для обложки: нужно хотя бы 600 × 900 точек. Сделайте его крупнее в «Рисунках».' },
      }));
    }
  }

  async function removePicture() {
    if (deleteMutation.isPending) return;
    setError(null);
    try {
      await deleteMutation.mutateAsync(book.id);
      setConfirmRemove(false);
      toasts.push({ message: `Картинка обложки «${book.title}» убрана`, tone: 'success' });
    } catch (failure) {
      setConfirmRemove(false);
      setError(publicApiErrorMessage(failure, 'Не удалось убрать картинку.'));
    }
  }

  const knownTotal = progress?.totalBytes != null && progress.totalBytes > 0;
  return (
    <Dialog canDismiss={!pending && !confirmRemove} label={`Обложка «${book.title}»`} onDismiss={onClose} width={480}>
      <div className={styles.editor}>
        <header className={styles.header}>
          <h2>Обложка «{book.title}»</h2>
          <IconButton aria-label="Закрыть окно обложки" disabled={pending} onClick={onClose}><X aria-hidden="true" size={18} /></IconButton>
        </header>
        <div className={styles.body}>
          <div className={styles.sample} data-cover-sample="">
            <BookCover book={{ title: book.title, coverColor: color, coverUrl: picture ?? null }} decorative />
          </div>
          <div className={styles.choice}>
            <fieldset className={styles.colours}>
              <legend>Цвет</legend>
              <div className={styles.palette}>
                {coverColours.map((entry) => (
                  <button aria-label={entry.name} aria-pressed={color.toLowerCase() === entry.color.toLowerCase()} className={styles.swatch}
                    disabled={pending} key={entry.color} onClick={() => setColor(entry.color)} style={{ backgroundColor: entry.color }} title={entry.name}
                    type="button">
                    {color.toLowerCase() === entry.color.toLowerCase() ? <Check aria-hidden="true" size={16} /> : null}
                  </button>
                ))}
              </div>
              <p className={styles.colourName}>{nameOf(color)}</p>
            </fieldset>
            <div className={styles.picture}>
              <p className={styles.label}>Картинка</p>
              <input accept="image/jpeg,image/png,image/webp" aria-label="Картинка обложки" className={styles.fileInput} disabled={pending}
                onChange={(event) => {
                  const selected = event.target.files?.[0];
                  if (selected) {
                    setDrawing(null);
                    void selectFile(selected);
                  }
                  event.target.value = '';
                }} ref={fileInput} type="file" />
              <Button disabled={pending} onClick={() => fileInput.current?.click()} size="sm" variant="outline">
                <ImageUp aria-hidden="true" size={15} />{picture ? 'Заменить картинку' : 'Загрузить картинку'}
              </Button>
              {CoverDrawings ? (
                <Button aria-expanded={choosing} disabled={pending} onClick={() => setChoosing((open) => !open)} size="sm" variant="outline">
                  <Images aria-hidden="true" size={15} />Выбрать из рисунков саги
                </Button>
              ) : null}
              {file ? <p className={styles.fileMeta}>{file.name} · {formatFileSize(file.size)}{dimensions ? ` · ${dimensions.width}×${dimensions.height}` : ''}</p> : null}
              {drawing ? <p className={styles.fileMeta}>Рисунок «{drawing.title}» станет обложкой таким, какой он сейчас.</p> : null}
              {picture ? <>
                <p className={styles.note}>Картинка стоит поверх цвета.</p>
                <Button disabled={pending} onClick={() => (file ? setFile(null) : drawing ? setDrawing(null) : setConfirmRemove(true))} size="sm" variant="ghost">
                  <ImageOff aria-hidden="true" size={15} />Убрать картинку
                </Button>
              </> : <p className={styles.note}>JPG, PNG или WEBP · до 5 МБ · не меньше 600×900</p>}
            </div>
          </div>
        </div>
        {CoverDrawings && choosing ? (
          <section aria-label="Рисунки саги" className={styles.drawings}>
            <CoverDrawings chosen={drawing?.number ?? null} onChoose={(chosen) => {
              setFile(null);
              setDrawing(chosen);
              setChoosing(false);
            }} projectId={book.projectId} />
          </section>
        ) : null}
        {uploadMutation.isPending ? (
          <div className={styles.progress}>
            <Progress aria-label="Загрузка картинки" value={knownTotal ? progress!.loadedBytes / progress!.totalBytes! : undefined} />
            {knownTotal ? <span>{formatFileSize(progress!.loadedBytes)} / {formatFileSize(progress!.totalBytes!)}</span> : null}
          </div>
        ) : null}
        {error ? <Alert tone="error">{error}</Alert> : null}
        <footer className={styles.actions}>
          <Button disabled={pending} onClick={onClose} variant="outline">Отмена</Button>
          <Button loading={updateMutation.isPending || uploadMutation.isPending} onClick={() => void save()} variant="primary">
            <Check aria-hidden="true" size={15} />Сохранить
          </Button>
        </footer>
      </div>
      {confirmRemove ? (
        <Confirm action="Убрать картинку" also="Обложка останется цветной." busy={deleteMutation.isPending} irreversible
          onCancel={() => setConfirmRemove(false)} onConfirm={() => void removePicture()} title="Убрать картинку обложки?">
          {deletionLines.gone}
        </Confirm>
      ) : null}
    </Dialog>
  );
}
