import { Check, ImageUp, Trash2, X } from 'lucide-react';
import { useEffect, useState } from 'react';
import { publicApiErrorMessage } from '../../../entities/api-errors';
import type { Book, ManuscriptApiClient, UploadProgress } from '../../../entities/manuscript/api';
import { formatFileSize } from '../../../ui/copy';
import { Button } from '../../../ui/controls/Button';
import { Dropzone } from '../../../ui/controls/Dropzone';
import { IconButton } from '../../../ui/controls/IconButton';
import { Alert } from '../../../ui/feedback/Alert';
import { Progress } from '../../../ui/feedback/Progress';
import { Tab, Tabs } from '../../../ui/navigation/Tabs';
import { Popover } from '../../../ui/overlays/Popover';
import { useDeleteBookCoverMutation, useUpdateBookMutation, useUploadBookCoverMutation } from '../api';
import { BookCover } from './BookCover';
import styles from './BookCoverEditor.module.css';

const coverMimeTypes = new Set(['image/jpeg', 'image/png', 'image/webp']);
const maxCoverBytes = 5 * 1024 * 1024;
const coverPalette = ['#4F46E5', '#285f58', '#4c3a5d', '#30472b', '#8a6500'];

export function BookCoverEditor({ api, book, onClose, onNotice, onUpdated }: {
  api: ManuscriptApiClient;
  book: Book;
  onClose: () => void;
  onNotice: (message: string) => void;
  onUpdated: (book: Book) => void;
}) {
  const [tab, setTab] = useState<'color' | 'image'>(book.coverUrl ? 'image' : 'color');
  const [file, setFile] = useState<File | null>(null);
  const [dimensions, setDimensions] = useState<{ width: number; height: number } | null>(null);
  const [previewUrl, setPreviewUrl] = useState<string | null>(null);
  const [selectedColor, setSelectedColor] = useState(book.coverColor ?? coverPalette[0]!);
  const [progress, setProgress] = useState<UploadProgress | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmRemove, setConfirmRemove] = useState(false);
  const uploadMutation = useUploadBookCoverMutation(api);
  const deleteMutation = useDeleteBookCoverMutation(api);
  const updateMutation = useUpdateBookMutation(api);
  const pending = uploadMutation.isPending || deleteMutation.isPending || updateMutation.isPending;

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
    setConfirmRemove(false);
    if (selected.type && !coverMimeTypes.has(selected.type.toLowerCase())) {
      setError('Поддерживаются только JPG, PNG и WEBP.');
      return;
    }
    if (selected.size > maxCoverBytes) {
      setError('Файл больше 5 МБ.');
      return;
    }
    try {
      const bitmap = await createImageBitmap(selected);
      try {
        if (bitmap.width < 600 || bitmap.height < 900) {
          setError('Изображение должно быть не меньше 600×900.');
          return;
        }
        setDimensions({ width: bitmap.width, height: bitmap.height });
        setFile(selected);
      } finally {
        bitmap.close();
      }
    } catch {
      setError('Не удалось прочитать изображение.');
    }
  }

  async function saveImage() {
    if (!file || uploadMutation.isPending) return;
    setError(null);
    setProgress(null);
    try {
      const updated = await uploadMutation.mutateAsync({ bookId: book.id, file, onUploadProgress: setProgress });
      onUpdated(updated);
      onNotice('Обложка книги обновлена');
      onClose();
    } catch (error) {
      setError(publicApiErrorMessage(error, 'Не удалось загрузить обложку.'));
    }
  }

  async function saveColor() {
    if (updateMutation.isPending) return;
    setError(null);
    try {
      const updated = await updateMutation.mutateAsync({ bookId: book.id, body: { coverColor: selectedColor } });
      onUpdated(updated);
      onNotice('Обложка книги обновлена');
      onClose();
    } catch (error) {
      setError(publicApiErrorMessage(error, 'Не удалось обновить цвет обложки.'));
    }
  }

  async function removeImage() {
    if (deleteMutation.isPending) return;
    setError(null);
    try {
      const updated = await deleteMutation.mutateAsync(book.id);
      onUpdated(updated);
      onNotice('Изображение обложки удалено');
      onClose();
    } catch (error) {
      setConfirmRemove(false);
      setError(publicApiErrorMessage(error, 'Не удалось удалить изображение обложки.'));
    }
  }

  const knownTotal = progress?.totalBytes != null && progress.totalBytes > 0;
  return (
    <Popover canDismiss={!pending} className={styles.editor} label="Обложка книги" onDismiss={onClose} open role="dialog">
      <header className={styles.header}>
        <div><h2>Обложка книги</h2><p>{book.title}</p></div>
        <IconButton aria-label="Закрыть редактор обложки" disabled={pending} onClick={onClose}><X aria-hidden="true" size={18} /></IconButton>
      </header>
      <Tabs label="Тип обложки">
        <Tab onClick={() => setTab('color')} selected={tab === 'color'}>Цвет</Tab>
        <Tab onClick={() => setTab('image')} selected={tab === 'image'}>Изображение</Tab>
      </Tabs>

      {tab === 'color' ? (
        <div className={styles.body} role="tabpanel">
          <div className={styles.palette}>
            {coverPalette.map((color) => (
              <button
                aria-label={`Цвет ${color}`}
                aria-pressed={selectedColor === color}
                className={styles.swatch}
                key={color}
                onClick={() => setSelectedColor(color)}
                style={{ backgroundColor: color }}
                type="button"
              />
            ))}
          </div>
          <p className={styles.status}>Изображение имеет приоритет над цветом, пока не удалено.</p>
          {error ? <Alert tone="error">{error}</Alert> : null}
          <div className={styles.actions}>
            <Button disabled={pending} onClick={onClose} variant="outline">Отмена</Button>
            <Button loading={updateMutation.isPending} onClick={() => void saveColor()} variant="primary"><Check aria-hidden="true" size={15} />Сохранить цвет</Button>
          </div>
        </div>
      ) : (
        <div className={styles.body} role="tabpanel">
          {previewUrl ? (
            <div className={styles.localPreview} style={{ backgroundColor: book.coverColor ?? '#4F46E5' }}>
              <img alt="Локальный предпросмотр обложки" src={previewUrl} />
            </div>
          ) : book.coverUrl ? <BookCover book={book} /> : null}
          <Dropzone
            accept="image/jpeg,image/png,image/webp"
            aria-label="Изображение обложки"
            description="JPG, PNG или WEBP · до 5 МБ · минимум 600×900"
            disabled={pending}
            label={<><ImageUp aria-hidden="true" size={17} /> Перетащите обложку или выберите файл</>}
            onChange={(event) => {
              const selected = event.target.files?.[0];
              if (selected) void selectFile(selected);
              event.target.value = '';
            }}
            onFilesDrop={(files) => { if (files[0]) void selectFile(files[0]); }}
          />
          {file ? <p className={styles.fileMeta}>{file.name} · {formatFileSize(file.size)}{dimensions ? ` · ${dimensions.width}×${dimensions.height}` : ''}</p> : null}
          {uploadMutation.isPending ? (
            <div className={styles.progress}>
              <Progress aria-label="Передача обложки" value={knownTotal ? progress!.loadedBytes / progress!.totalBytes! : undefined} />
              {knownTotal ? <span>{formatFileSize(progress!.loadedBytes)} / {formatFileSize(progress!.totalBytes!)}</span> : null}
            </div>
          ) : null}
          {error ? <Alert tone="error">{error}</Alert> : null}
          {confirmRemove ? (
            <Alert className={styles.confirm} tone="warning">
              <strong>Убрать изображение?</strong>
              <span>Изображение будет удалено без возможности восстановления.</span>
              <div className={styles.actions}>
                <Button disabled={pending} onClick={() => setConfirmRemove(false)} variant="outline">Отмена</Button>
                <Button loading={deleteMutation.isPending} onClick={() => void removeImage()} variant="danger">Удалить изображение</Button>
              </div>
            </Alert>
          ) : null}
          <div className={styles.actions}>
            <Button disabled={pending || (!file && !book.coverUrl)} onClick={() => file ? setFile(null) : setConfirmRemove(true)} variant="outline">
              <Trash2 aria-hidden="true" size={15} />Убрать
            </Button>
            <Button disabled={!file} loading={uploadMutation.isPending} onClick={() => void saveImage()} variant="primary">
              {error && file ? 'Повторить' : 'Сохранить'}
            </Button>
          </div>
        </div>
      )}
    </Popover>
  );
}
