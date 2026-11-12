import { fireEvent, render, screen, within } from '@testing-library/react';
import { useState } from 'react';
import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { UploadOrderDialog } from './UploadOrderDialog';
import { localFiles, orderRows, screenFiles, type OrderRow, type UploadRoom } from './upload-files';

const constraints = { allowedExtensions: ['.fb2', '.epub', '.docx', '.txt', '.md', '.rtf', '.odt', '.fb2.zip'], maxFileSizeBytes: 52_428_800 };
const books = [{ title: 'Туманный берег', place: 1 }, { title: 'Белый порт', place: 2 }];

function chosen(names: string[]) {
  const screened = screenFiles(localFiles(names.map((name) => new File(['book'], name))), constraints);
  return { rows: orderRows(screened.chosen), refused: screened.refused };
}

function Window({ names, onClose = vi.fn(), onSend = vi.fn(), room = null }: {
  names: string[];
  onClose?: () => void;
  onSend?: (rows: readonly OrderRow[]) => void;
  room?: UploadRoom | null;
}) {
  const [state] = useState(() => chosen(names));
  const [rows, setRows] = useState(state.rows);
  return (
    <UploadOrderDialog books={books} busy={false} onClose={onClose} onReplaceRefused={vi.fn()} onRows={setRows} onSend={onSend}
      refused={state.refused} room={room} rows={rows} />
  );
}

const places = (dialog: HTMLElement) => within(within(dialog).getByRole('list', { name: 'Тома по порядку' })).getAllByRole('listitem')
  .map((row) => `${row.getAttribute('aria-label')} → ${row.querySelector('[class*="place"]')?.textContent}`);

afterEach(() => vi.restoreAllMocks());

// The test browser has no pointer events: a mouse event with its pointer stands for a finger or a mouse.
const realPointerEvent = (window as { PointerEvent?: unknown }).PointerEvent;
beforeAll(() => {
  class TestPointerEvent extends MouseEvent {
    pointerId: number;
    constructor(type: string, init: MouseEventInit & { pointerId?: number } = {}) {
      super(type, init);
      this.pointerId = init.pointerId ?? 1;
    }
  }
  (window as { PointerEvent?: unknown }).PointerEvent = TestPointerEvent;
});
afterAll(() => {
  (window as { PointerEvent?: unknown }).PointerEvent = realPointerEvent;
});

// specs/2026-11-11-ux7-upload.md UP2.3, UP2.4: the order is the order of the rows — by dragging or «Выше/Ниже»; there is
// no number field; every remark stands at once with its file.
describe('«Порядок томов»', () => {
  it('moves a row by dragging it and by «Выше», and «встанет книгой N» follows; there is no number to type', () => {
    render(<Window names={['Том 2.fb2', 'Том 1.epub', 'черновик финала.docx']} />);
    const dialog = screen.getByRole('dialog', { name: 'Порядок томов' });
    expect(within(dialog).queryByRole('spinbutton')).toBeNull();
    expect(within(dialog).queryByText(/Из имени файла|По порядку|Указан вручную|Книга №/u)).toBeNull();
    expect(places(dialog)).toEqual([
      'Том 1.epub → встанет книгой 3', 'Том 2.fb2 → встанет книгой 4', 'черновик финала.docx → встанет книгой 5',
    ]);

    // The rows stand one under another: a finger or a mouse that carries the handle of the last row over the first one.
    const rows = within(dialog).getAllByRole('listitem').filter((row) => row.dataset.rowKey);
    rows.forEach((row, index) => {
      vi.spyOn(row, 'getBoundingClientRect').mockReturnValue({ top: index * 50, bottom: index * 50 + 44, left: 0, right: 600, width: 600, height: 44, x: 0, y: index * 50, toJSON: () => ({}) });
    });
    const handle = within(dialog).getByRole('button', { name: 'Перетащить «черновик финала.docx»' });
    fireEvent.pointerDown(handle, { pointerId: 1, clientY: 120 });
    fireEvent.pointerMove(handle, { pointerId: 1, clientY: 10 });
    fireEvent.pointerUp(handle, { pointerId: 1, clientY: 10 });
    expect(places(dialog)).toEqual([
      'черновик финала.docx → встанет книгой 3', 'Том 1.epub → встанет книгой 4', 'Том 2.fb2 → встанет книгой 5',
    ]);

    fireEvent.click(within(dialog).getByRole('button', { name: 'Действия строки «Том 2.fb2»' }));
    fireEvent.click(screen.getByRole('menuitem', { name: 'Выше' }));
    expect(places(dialog)[1]).toBe('Том 2.fb2 → встанет книгой 4');
  });

  it('says every remark at once, each with its file, and the files that fit go', () => {
    const onSend = vi.fn();
    render(<Window names={['Том 3.fb2', 'рукопись.pdf', 'черновик.docx', 'Белый порт.epub']} onSend={onSend} />);
    const dialog = screen.getByRole('dialog', { name: 'Порядок томов' });
    const remarks = within(within(dialog).getByRole('list', { name: 'Замечания' })).getAllByRole('listitem').map((item) => item.textContent);
    expect(remarks).toEqual([
      '«черновик.docx» — в имени нет номера тома: проверьте его место.',
      expect.stringMatching(/^«Белый порт\.epub» — в имени нет номера тома/u),
      expect.stringMatching(/^«Белый порт\.epub» похож на «Белый порт» из этой саги/u),
      expect.stringMatching(/^«рукопись\.pdf» — PDF не поддерживается.*Выбрать другой$/u),
    ]);
    expect(within(dialog).getByText('Подошли 3 из 4')).toBeTruthy();
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить 3 книги' }));
    expect(onSend.mock.calls[0]![0].map((row: OrderRow) => row.file.name)).toEqual(['Том 3.fb2', 'черновик.docx', 'Белый порт.epub']);
  });

  // UP1.3: what the tier takes is known before any transfer.
  it('says what fits the saga before any transfer and loads the first ones', () => {
    const onSend = vi.fn();
    render(<Window names={['1.fb2', '2.fb2', '3.fb2', '4.fb2', '5.fb2']} onSend={onSend} room={{ room: 2, code: 'quota.project_books' }} />);
    const dialog = screen.getByRole('dialog', { name: 'Порядок томов' });
    expect(within(dialog).getByText('В этой саге поместится 2 из 5 книг')).toBeTruthy();
    expect(places(dialog).slice(1, 3)).toEqual(['2.fb2 → встанет книгой 4', '3.fb2 → не поместится']);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Загрузить две' }));
    expect(onSend.mock.calls[0]![0].map((row: OrderRow) => row.file.name)).toEqual(['1.fb2', '2.fb2']);
  });

  // UP2.4: Esc over a gathered list asks; «Отмена» keeps the list, «Не загружать» drops it.
  it('asks before Esc drops the gathered list', () => {
    const onClose = vi.fn();
    render(<Window names={['Том 1.fb2', 'Том 2.fb2', 'Том 3.fb2']} onClose={onClose} />);
    fireEvent.keyDown(screen.getByRole('dialog', { name: 'Порядок томов' }), { key: 'Escape' });
    let question = screen.getByRole('dialog', { name: 'Не загружать эти 3 файла?' });
    fireEvent.click(within(question).getByRole('button', { name: 'Отмена' }));
    expect(screen.queryByRole('dialog', { name: 'Не загружать эти 3 файла?' })).toBeNull();
    expect(screen.getByRole('dialog', { name: 'Порядок томов' })).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();

    fireEvent.click(within(screen.getByRole('dialog', { name: 'Порядок томов' })).getByRole('button', { name: 'Закрыть окно' }));
    question = screen.getByRole('dialog', { name: 'Не загружать эти 3 файла?' });
    fireEvent.click(within(question).getByRole('button', { name: 'Не загружать' }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });
});
