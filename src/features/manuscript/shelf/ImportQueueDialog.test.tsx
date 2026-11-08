import { fireEvent, render, screen, within } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { ImportQueueRow, ImportQueueValidation } from './import-queue';
import { ImportQueueDialog } from './ImportQueueDialog';
import { useImportQueue } from './useImportQueue';

type Queue = ReturnType<typeof useImportQueue>;
type QueueProps = Parameters<typeof ImportQueueDialog>[0];
type OptionalQueueProps = Partial<Omit<QueueProps, 'queue'>>;

const valid: ImportQueueValidation = { valid: true, issues: [], message: null };

function row(name: string, volume: number, overrides: Partial<ImportQueueRow> = {}): ImportQueueRow {
  return {
    detectedVolume: volume,
    fileName: name,
    id: name,
    locked: false,
    sizeBytes: 6,
    status: 'pending',
    totalBytes: null,
    transferState: null,
    uploadedBytes: 0,
    volume,
    volumeSource: 'file',
    ...overrides,
  };
}

function controller(overrides: Partial<Queue> = {}): Queue {
  return {
    beginTransfer: vi.fn(),
    canEdit: true,
    canMutate: true,
    canReopen: false,
    canRetry: false,
    cancel: vi.fn(),
    constraintsRefreshing: false,
    constraintsStatus: 'ready',
    dismiss: vi.fn(),
    errorMessage: null,
    errorSupportCode: null,
    hide: vi.fn(),
    isDialogVisible: true,
    isOpen: true,
    localSelection: false,
    move: vi.fn(),
    nextBookOrder: 4,
    openFiles: vi.fn(),
    operationPending: false,
    quotaMessage: () => undefined,
    uploadsHint: '',
    rejectedFiles: [],
    canCancelTransfer: false,
    cancelPending: false,
    transferStopRequested: false,
    remove: vi.fn(),
    replaceFile: vi.fn(),
    replaceQueue: vi.fn(),
    retry: vi.fn(),
    rows: [row('Том 4.epub', 4)],
    show: vi.fn(),
    setVolume: vi.fn(),
    status: 'editing',
    transferInterrupted: false,
    validation: valid,
    ...overrides,
  };
}

function renderDialog(queue: Queue, props: OptionalQueueProps = {}) {
  const { onOpenItem = vi.fn(), ...rest } = props;
  return render(<ImportQueueDialog {...rest} onOpenItem={onOpenItem} queue={queue} />);
}

describe('ImportQueueDialog', () => {
  it('keeps the local selection editable until the actor starts transfer', () => {
    const beginTransfer = vi.fn();
    const remove = vi.fn();
    renderDialog(controller({
      beginTransfer,
      remove,
      rows: [row('Том 4.epub', 4, {
        file: new File(['book'], 'Том 4.epub'),
        transferState: 'local',
      })],
      localSelection: true,
    }));

    expect(screen.queryByRole('checkbox')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Удалить Том 4.epub' }));
    fireEvent.click(screen.getByRole('button', { name: 'Загрузить' }));

    expect(remove).toHaveBeenCalledWith('Том 4.epub');
    expect(beginTransfer).toHaveBeenCalledTimes(1);
  });

  it('shows determinate byte progress only when total bytes are known', () => {
    renderDialog(controller({
      rows: [
        row('Том 4.epub', 4, { status: 'uploading', transferState: 'uploading', uploadedBytes: 4 }),
        row('Том 5.epub', 5, { status: 'uploading', transferState: 'uploading', uploadedBytes: 5, totalBytes: 10 }),
      ],
      status: 'uploading',
    }));

    expect(screen.getByLabelText('Передача Том 4.epub').getAttribute('value')).toBeNull();
    expect(screen.getByLabelText('Передача Том 5.epub').getAttribute('value')).toBe('0.5');
  });

  it('distinguishes transferred, active and waiting rows', () => {
    renderDialog(controller({
      rows: [
        row('Том 4.epub', 4, { status: 'pending', transferState: 'uploaded' }),
        row('Том 5.epub', 5, { status: 'uploading', transferState: 'uploading' }),
        row('Том 6.epub', 6, { status: 'pending', transferState: 'local' }),
      ],
      status: 'uploading',
    }));

    expect(screen.getByText('Передан · проверен')).toBeTruthy();
    expect(screen.getByText('Передача')).toBeTruthy();
    expect(screen.getByText('Ожидает')).toBeTruthy();
  });

  it('renders a restored server row without requiring a browser File', () => {
    renderDialog(controller());

    expect(screen.getByText('Том 4.epub')).toBeTruthy();
    expect(screen.getByText('Из имени файла')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Подробнее/ })).toBeNull();
  });

  it('keeps an uploading projection read-only and closes by hiding only', () => {
    const cancel = vi.fn();
    const dismiss = vi.fn();
    const hide = vi.fn();
    renderDialog(controller({
      cancel,
      dismiss,
      hide,
      rows: [row('Том 4.epub', 4, { status: 'uploading' })],
      status: 'uploading',
    }));

    expect(screen.queryByLabelText('Номер книги для Том 4.epub')).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Скрыть очередь' }));
    expect(hide).toHaveBeenCalledTimes(1);
    expect(dismiss).not.toHaveBeenCalled();
    expect(cancel).not.toHaveBeenCalled();
  });

  it('saves edits through the controller actions', () => {
    const move = vi.fn();
    const setVolume = vi.fn();
    renderDialog(controller({
      move,
      rows: [row('Том 4.epub', 4), row('Том 5.epub', 5)],
      setVolume,
    }));

    fireEvent.change(screen.getByLabelText('Номер книги для Том 4.epub'), { target: { value: '5' } });
    fireEvent.click(screen.getByRole('button', { name: 'Переместить Том 5.epub выше' }));

    expect(setVolume).toHaveBeenCalledWith('Том 4.epub', 5);
    expect(move).toHaveBeenCalledWith('Том 5.epub', 'Том 4.epub');
  });

  it('describes a valid draft as an automatic start instead of a correction', () => {
    renderDialog(controller());

    const alert = screen.getByRole('alert', { name: 'Очередь готова к автозапуску' });
    expect(alert.textContent).toContain('Проверка пройдена');
    expect(alert.textContent).not.toContain('Исправьте номера');
  });

  it('renders a controller error in the accessible state alert', () => {
    const errorMessage = 'Не удалось сохранить очередь.';
    renderDialog(controller({ errorMessage }));

    const alert = screen.getByRole('alert', { name: errorMessage });

    expect(alert).toBeTruthy();
    expect(alert.textContent).toContain(errorMessage);
  });

  it('does not promise an automatic start after a refused start', () => {
    const errorMessage = 'В очереди 13 книг, а сегодня можно загрузить ещё 5. Уберите лишние книги или загрузите их завтра.';
    renderDialog(controller({ errorMessage }));

    const alert = screen.getByRole('alert', { name: errorMessage });
    expect(alert.textContent).toContain('Очередь сохранена и не запущена.');
    expect(alert.textContent).not.toContain('Проверка пройдена');
  });

  it('separates hiding from explicit cancellation for an editable server draft', () => {
    const cancel = vi.fn();
    const hide = vi.fn();
    renderDialog(controller({ cancel, hide, status: 'editing' }));

    fireEvent.click(screen.getByRole('button', { name: 'Скрыть очередь' }));
    fireEvent.click(screen.getByRole('button', { name: 'Отменить очередь' }));

    expect(hide).toHaveBeenCalledTimes(1);
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('renders an accepted sequence gap as a locked recovery state', () => {
    renderDialog(controller({
      canEdit: false,
      rows: [row('Том 6.epub', 6)],
      status: 'blocked',
      validation: {
        valid: false,
        issues: [{ code: 'missing_volume', itemId: 'Том 6.epub', message: 'Не хватает книги №5: номера должны идти подряд.' }],
        message: 'Не хватает книги №5: номера должны идти подряд.',
      },
    }));

    expect(screen.getByRole('alert', { name: 'Не хватает книги в серии' }).textContent)
      .toContain('Не хватает книги №5');
    expect(screen.queryByLabelText('Номер книги для Том 6.epub')).toBeNull();
    expect(screen.getByRole('button', { name: 'Скрыть' })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Отменить очередь' })).toBeTruthy();
  });

  it('offers an exact-file replacement for a blocked preflight failure', () => {
    const replaceFile = vi.fn();
    renderDialog(controller({
      canEdit: false,
      replaceFile,
      rows: [row('broken.epub', 4, { errorCode: 'invalid_epub', status: 'failed' })],
      status: 'blocked',
      validation: { valid: false, issues: [], message: null },
    }));
    const replacement = new File(['replacement'], 'Том 4.epub');

    fireEvent.change(screen.getByLabelText('Заменить broken.epub'), { target: { files: [replacement] } });

    expect(screen.getByRole('alert', { name: 'Файл не прошёл проверку' })).toBeTruthy();
    expect(replaceFile).toHaveBeenCalledWith('broken.epub', replacement);
  });

  it('opens the exact restored item through the status callback', () => {
    const onOpenItem = vi.fn();
    renderDialog(controller({
      rows: [row('Том 4.epub', 4, {
        bookId: 'book-4',
        status: 'indexing',
      })],
      status: 'processing',
    }), { onOpenItem });

    fireEvent.click(screen.getByRole('button', { name: 'Подробнее: Том 4.epub' }));

    expect(onOpenItem).toHaveBeenCalledWith(expect.objectContaining({ bookId: 'book-4' }));
  });

  it('offers Retry, Replace and Dismiss for a failed accepted queue', () => {
    const retry = vi.fn();
    const replaceQueue = vi.fn();
    const dismiss = vi.fn();
    renderDialog(controller({
      canEdit: false,
      canRetry: true,
      dismiss,
      replaceQueue,
      retry,
      rows: [row('Том 4.epub', 4, { locked: true, status: 'failed' })],
      status: 'failed',
    }));

    fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
    fireEvent.change(screen.getByLabelText('Заменить очередь'), {
      target: { files: [new File(['next'], 'Том 5.epub')] },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Скрыть' }));

    expect(retry).toHaveBeenCalledTimes(1);
    expect(replaceQueue).toHaveBeenCalledWith([expect.objectContaining({ name: 'Том 5.epub' })]);
    expect(dismiss).toHaveBeenCalledTimes(1);
  });

  it('keeps persisted queue mutations unavailable to a viewer', () => {
    renderDialog(controller({
      canEdit: false,
      canMutate: false,
      canRetry: true,
      rows: [row('Том 4.epub', 4, { status: 'indexing' })],
      status: 'processing',
    }));

    expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    expect(screen.getByRole('button', { name: 'Отменить очередь' })).toHaveProperty('disabled', true);
    expect(screen.queryByLabelText('Заменить очередь')).toBeNull();
  });

  it('keeps the authoritative running state while the current item is canceling', () => {
    renderDialog(controller({
      rows: [row('Том 4.epub', 4, { status: 'canceling' })],
      status: 'processing',
    }));

    expect(screen.getByText('Готовим книги по порядку')).toBeTruthy();
    expect(screen.getByText('Останавливаем')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Повторить/ })).toBeNull();
  });

  it('keeps processing rows read-only', () => {
    renderDialog(controller({
      rows: [row('Том 4.epub', 4, { status: 'indexing' })],
      status: 'processing',
    }));
    const dialog = screen.getByRole('dialog', { name: 'Загрузка книг' });

    expect(within(dialog).getByText('Текущий файл')).toBeTruthy();
    expect(screen.queryByLabelText('Номер книги для Том 4.epub')).toBeNull();
  });

  it('uses the recognized book title and a safe failure reason in status rows', () => {
    renderDialog(controller({
      rows: [row('chapter-16.docx', 4, {
        bookId: 'book-4',
        errorCode: 'invalid_epub_container',
        errorMessage: 'EPUB container is invalid.',
        status: 'failed',
      })],
      status: 'failed',
    }), {
      books: [{ id: 'book-4', title: 'Книга IV. Северный суд' }],
    });

    expect(screen.getByText('Книга IV. Северный суд')).toBeTruthy();
    expect(screen.getByText('chapter-16.docx · DOCX · 6 Б')).toBeTruthy();
    expect(screen.getByText('EPUB не читается: файл повреждён. Сохраните книгу заново или загрузите DOCX.')).toBeTruthy();
  });

  it.each([
    ['import.unsupported_encoding', 'Том 4.fb2', 'Файл в кодировке, которую мы не читаем. Сохраните его в UTF-8 или загрузите DOCX.'],
    ['invalid_fb2_document', 'Том 4.fb2', 'FB2 не читается: в разметке файла ошибка. Пересохраните его в редакторе FB2 или загрузите DOCX.'],
    ['invalid_epub_document', 'Том 4.epub', 'EPUB не читается: файл повреждён. Сохраните книгу заново или загрузите DOCX.'],
    ['invalid_docx_document', 'Том 4.docx', 'DOCX не читается. Откройте файл в Word и сохраните заново.'],
    ['empty_import_content', 'Том 4.docx', 'В файле не нашлось текста глав.'],
    ['import.content_mismatch', 'Том 4.epub', 'Содержимое файла не совпадает с его расширением. Проверьте, что это действительно FB2, EPUB или DOCX.'],
    ['import.unsupported_media_type', 'Том 4.fb2', 'Формат файла не поддерживается. Подходят FB2, EPUB и DOCX.'],
  ])('says how to fix a file the import cannot read, without promising a retry (%s)', (errorCode, fileName, message) => {
    renderDialog(controller({
      rows: [row(fileName, 4, { errorCode, failedStep: 'import', locked: true, status: 'failed' })],
      status: 'failed',
    }));
    expect(screen.getByText(message)).toBeTruthy();
    expect(screen.queryByText(/можно повторить/)).toBeNull();
  });

  it.each([undefined, 'worker.retry_exhausted'])('does not invent file corruption for an import failure (%s)', (errorCode) => {
    renderDialog(controller({
      rows: [row('volume-4.epub', 4, { errorCode, failedStep: 'import', status: 'failed' })],
      status: 'failed',
    }));
    expect(screen.queryByText(/Файл повреждён/)).toBeNull();
    expect(screen.getByText('Не удалось загрузить книгу. Повторите — если не получится, напишите в поддержку.')).toBeTruthy();
  });

  it.each([
    ['import.too_many_chapters', 'В файле больше 2000 глав — столько в одну книгу не поместить. Разделите его на тома.'],
    ['import.too_large', 'В файле больше 10 миллионов знаков — столько в одну книгу не поместить. Разделите его на тома.'],
  ])('explains an import over the book limits (%s)', (errorCode, message) => {
    renderDialog(controller({
      rows: [row('Том 4.fb2', 4, { errorCode, failedStep: 'import', locked: true, status: 'failed' })],
      status: 'failed',
    }));
    expect(screen.getByText(message)).toBeTruthy();
    expect(screen.queryByText(/не прошёл предпроверку/)).toBeNull();
  });

  it('places cancellation on the left and hiding on the right', () => {
    renderDialog(controller({ status: 'processing' }));
    const footer = screen.getByRole('dialog', { name: 'Загрузка книг' }).querySelector('footer');
    const buttons = footer?.querySelectorAll('button') ?? [];

    expect(footer?.className).toContain('footer');
    expect(buttons[0]?.textContent).toContain('Отменить очередь');
    expect(buttons[buttons.length - 1]?.textContent).toContain('Скрыть');
  });

  // R3.2.1, R3.2.4: the local list already shows the book number and where it came from.
  it('shows the local list with the book number, its source and the saga rule', () => {
    renderDialog(controller({
      localSelection: true,
      nextBookOrder: 1,
      rows: [row('Пламя Севера.docx', 1, { file: new File(['book'], 'Пламя Севера.docx'), sizeBytes: 1_258_291, volumeSource: 'order' })],
    }));

    const alert = screen.getByRole('alert', { name: 'Проверьте порядок книг' });
    expect(alert.textContent).toContain('Файлы ещё не отправлены. Порядок строк — порядок книг в серии.');
    expect(screen.getByRole('heading', { name: 'Загрузка книг' })).toBeTruthy();
    expect(screen.getByText('FB2, EPUB или DOCX · до 50 МБ на файл')).toBeTruthy();
    expect(screen.getByText('Порядок книг нужен для хронологии саги. В саге пока нет книг — первая получит номер 1.')).toBeTruthy();
    expect(screen.getByText('Книга №')).toBeTruthy();
    expect(screen.getByLabelText('Номер книги для Пламя Севера.docx')).toHaveProperty('value', '1');
    expect(screen.getByText('По порядку')).toBeTruthy();
    expect(screen.getByText('DOCX · 1,2 МБ')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Загрузить' })).toHaveProperty('disabled', false);
  });

  it('marks a missing number and a typed one', () => {
    const setVolume = vi.fn();
    const { rerender } = renderDialog(controller({
      localSelection: true,
      nextBookOrder: 2,
      rows: [row('Том 2.fb2', 2), row('черновик.docx', null as unknown as number, { detectedVolume: null })],
      setVolume,
      validation: {
        valid: false,
        issues: [{ code: 'unknown_volume', itemId: 'черновик.docx', message: 'Укажите номер книги в серии.' }],
        message: 'Укажите номер книги в серии.',
      },
    }));

    expect(screen.getByRole('alert', { name: 'Проверьте номера книг' })).toBeTruthy();
    expect(screen.getByText('Нет номера')).toBeTruthy();
    expect(screen.getAllByText('Укажите номер книги в серии.').length).toBeGreaterThan(0);
    expect(screen.getByRole('button', { name: 'Загрузить' })).toHaveProperty('disabled', true);
    fireEvent.change(screen.getByLabelText('Номер книги для черновик.docx'), { target: { value: '3' } });
    expect(setVolume).toHaveBeenCalledWith('черновик.docx', 3);

    rerender(<ImportQueueDialog onOpenItem={vi.fn()} queue={controller({
      localSelection: true,
      nextBookOrder: 2,
      rows: [row('Том 2.fb2', 2), row('черновик.docx', 3, { detectedVolume: null, volumeSource: 'manual' })],
    })} />);
    expect(screen.getByText('Указан вручную')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Загрузить' })).toHaveProperty('disabled', false);
  });

  // R3.1.2: no unchecked path.
  it.each([
    ['pending', 'Проверяем файлы…'],
    ['error', 'Не удалось проверить файлы'],
  ] as const)('keeps «Загрузить» unavailable while the files are not checked (%s)', (constraintsStatus, title) => {
    const retry = vi.fn();
    renderDialog(controller({
      canRetry: constraintsStatus === 'error',
      constraintsStatus,
      localSelection: true,
      nextBookOrder: null,
      retry,
      rows: [],
      validation: { valid: false, issues: [], message: null },
    }));

    expect(screen.getByRole('alert', { name: title })).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Загрузить' })).toHaveProperty('disabled', true);
    if (constraintsStatus === 'error') {
      fireEvent.click(screen.getByRole('button', { name: 'Повторить' }));
      expect(retry).toHaveBeenCalledTimes(1);
    } else {
      expect(screen.queryByRole('button', { name: 'Повторить' })).toBeNull();
    }
  });

  it('lists the files it did not take and why', () => {
    renderDialog(controller({
      localSelection: true,
      rejectedFiles: [
        { fileName: 'rukopis.pdf', message: 'PDF не поддерживается: из него нельзя надёжно достать главы. Сохраните рукопись как DOCX (Word: Файл → Сохранить как → Документ Word) или загрузите FB2/EPUB.' },
        { fileName: 'chernovik.doc', message: 'Этот формат не поддерживается. Откройте файл в Word или LibreOffice и сохраните как DOCX.' },
      ],
    }));

    const rejected = screen.getByRole('alert', { name: 'Не добавлены: 2' });
    expect(rejected.textContent).toContain('rukopis.pdf');
    expect(rejected.textContent).toContain('Сохраните рукопись как DOCX');
  });

  // R3.4.3
  it('asks to replace a file the author fixes and offers no retry', () => {
    renderDialog(controller({
      canEdit: false,
      rows: [row('Том 4.docx', 4, { errorCode: 'invalid_docx_document', status: 'failed' })],
      status: 'blocked',
      validation: { valid: false, issues: [], message: null },
    }));

    expect(screen.getByText('DOCX не читается. Откройте файл в Word и сохраните заново.')).toBeTruthy();
    expect(screen.getByText('Заменить файл')).toBeTruthy();
    expect(screen.getByLabelText('Заменить Том 4.docx')).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Повторить/ })).toBeNull();
    expect(screen.queryByText(/можно повторить/)).toBeNull();
  });

  it('offers a retry and the support code for a transient import failure', () => {
    renderDialog(controller({
      canEdit: false,
      canRetry: true,
      rows: [row('Том 4.docx', 4, { errorCode: 'worker.handler_failed', failedStep: 'import', locked: true, status: 'failed', supportCode: 'req-1' })],
      status: 'failed',
    }));

    expect(screen.getByText('Не удалось загрузить книгу. Повторите — если не получится, напишите в поддержку.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Повторить' })).toBeTruthy();
    expect(screen.getByText('req-1')).toBeTruthy();
    expect(screen.getByRole('link', { name: 'Написать в поддержку' })).toBeTruthy();
  });

  // R3.5.3
  it('asks to keep the tab open while files transfer', () => {
    renderDialog(controller({
      rows: [row('Том 4.epub', 4, { transferState: 'uploaded' }), row('Том 5.epub', 5, { status: 'uploading', transferState: 'uploading' })],
      status: 'uploading',
    }));

    const alert = screen.getByRole('alert', { name: 'Передаём файлы · 1 из 2' });
    expect(alert.textContent).toContain('Не закрывайте вкладку, пока файлы передаются — потом подготовка пойдёт сама.');
    expect(screen.getByRole('heading', { name: 'Загрузка книг · передаём файлы' })).toBeTruthy();
  });

  // R3.5.1, R3.5.2
  it('shows an interrupted transfer with the choice of files and the cancellation', () => {
    const cancel = vi.fn();
    const openFiles = vi.fn();
    renderDialog(controller({
      cancel,
      openFiles,
      rows: [row('Том 4.epub', 4, { transferState: 'uploaded' }), row('Том 5.epub', 5)],
      status: 'uploading',
      transferInterrupted: true,
    }));

    const alert = screen.getByRole('alert', { name: 'Передача прервалась' });
    expect(alert.textContent).toContain('Передано 1 из 2. Файлы передаются из этой вкладки, поэтому после перезагрузки выберите оставшиеся файлы снова.');
    const files = [new File(['4'], 'Том 4.epub'), new File(['5'], 'Том 5.epub')];
    fireEvent.change(screen.getByLabelText('Выбрать файлы'), { target: { files } });
    fireEvent.click(screen.getByRole('button', { name: 'Отменить загрузку' }));

    expect(openFiles).toHaveBeenCalledWith(files);
    expect(cancel).toHaveBeenCalledTimes(1);
  });

  it('reports a finished queue in the writer words', () => {
    renderDialog(controller({ rows: [row('Пламя Севера.docx', 1, { status: 'succeeded' })], status: 'complete' }));

    expect(screen.getByRole('alert', { name: 'Книги загружены' }).textContent).toContain('1 книга добавлена и подготовлена.');
  });
});
