import { act, fireEvent, render, screen } from '@testing-library/react';
import { StrictMode, useState } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { Button } from '../controls/Button';
import { Overlay } from './Overlay';
import { Dialog } from './Dialog';
import { Portal } from './Portal';
import { usePresence } from './use-presence';

function ConcurrentModals({ parentOpen, childOpen, parentModal = true, onChildDismiss }: {
  parentOpen: boolean;
  childOpen: boolean;
  parentModal?: boolean;
  onChildDismiss?: () => void;
}) {
  const parent = usePresence(parentOpen);
  const child = usePresence(childOpen);
  return (
    <>
      {parent.mounted && (
        <Portal>
          <div data-overlay-scrim="">
            <Overlay kind="sheet" label="Заметки" modal={parentModal} onDismiss={() => undefined} state={parent.status}>
              <button type="button">Закрыть заметки</button>
            </Overlay>
          </div>
        </Portal>
      )}
      {child.mounted && (
        <Dialog label="Отменить изменения" onDismiss={onChildDismiss ?? (() => undefined)} state={child.status}>
          <button type="button">Продолжить редактирование</button>
        </Dialog>
      )}
    </>
  );
}

describe('Overlay', () => {
  it('isolates an inline responsive modal without unmounting its content', () => {
    const view = (modal: boolean) => <main><header data-testid="chrome"><button>Menu</button></header><section><div data-testid="list">Notes</div><Overlay kind="drawer" label="Inline note" modal={modal} onDismiss={() => undefined}><input aria-label="Draft" defaultValue="Keep me" /></Overlay></section></main>;
    const { rerender } = render(view(false));
    fireEvent.change(screen.getByLabelText('Draft'), { target: { value: 'Unsaved' } });
    rerender(view(true));
    expect(screen.getByTestId('chrome').inert).toBe(true);
    expect(screen.getByTestId('list').inert).toBe(true);
    expect(screen.getByRole('dialog').inert).not.toBe(true);
    rerender(view(false));
    expect(screen.getByTestId('chrome').inert).toBe(false);
    expect(screen.getByTestId('list').inert).toBe(false);
    expect((screen.getByLabelText('Draft') as HTMLInputElement).value).toBe('Unsaved');
  });

  it('enters focus, locks scroll, dismisses with Escape and returns focus', () => {
    const onDismiss = vi.fn();

    function Harness() {
      const [open, setOpen] = useState(false);
      return (
        <>
          <button onClick={() => setOpen(true)} type="button">
            Open overlay
          </button>
          {open ? (
            <Overlay
              kind="dialog"
              label="Rename chat"
              onDismiss={() => {
                onDismiss();
                setOpen(false);
              }}
            >
              <button type="button">Cancel</button>
              <button type="button">Save</button>
            </Overlay>
          ) : null}
        </>
      );
    }

    render(<Harness />);

    const trigger = screen.getByRole('button', { name: 'Open overlay' });
    trigger.focus();
    fireEvent.click(trigger);

    const dialog = screen.getByRole('dialog', { name: 'Rename chat' });
    expect(document.body.style.overflow).toBe('hidden');
    const cancel = screen.getByRole('button', { name: 'Cancel' });
    const save = screen.getByRole('button', { name: 'Save' });
    expect(document.activeElement).toBe(cancel);

    save.focus();
    fireEvent.keyDown(save, { key: 'Tab' });
    expect(document.activeElement).toBe(cancel);
    fireEvent.keyDown(cancel, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(save);

    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onDismiss).toHaveBeenCalledTimes(1);
    expect(document.activeElement).toBe(trigger);
    expect(document.body.style.overflow).toBe('');
  });

  // The owner, 12.11: «Версии текста» of «Письмо» could not be closed — the line of the list he pressed gave way to the
  // opened version, the focus fell to <body>, and Escape went past the window.
  it('takes Escape and Tab back when its focused part left the page, the top window only', () => {
    function Swap({ onDismiss }: { onDismiss: () => void }) {
      const [opened, setOpened] = useState(false);
      return (
        <Dialog label="Версии текста" onDismiss={onDismiss}>
          {opened
            ? <p><button type="button">Все версии</button></p>
            : <ul><li><button onClick={() => setOpened(true)} type="button">Версия 2</button></li></ul>}
        </Dialog>
      );
    }
    const onDismiss = vi.fn();
    const view = render(<Swap onDismiss={onDismiss} />);
    fireEvent.click(screen.getByRole('button', { name: 'Версия 2' }));
    expect(document.activeElement).toBe(document.body);
    fireEvent.keyDown(document.body, { key: 'Tab' });
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Все версии' }));
    act(() => (document.activeElement as HTMLElement).blur());
    expect(document.activeElement).toBe(document.body);
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(onDismiss).toHaveBeenCalledTimes(1);
    // With the focus inside, the window takes the key itself, once.
    screen.getByRole('button', { name: 'Все версии' }).focus();
    fireEvent.keyDown(screen.getByRole('button', { name: 'Все версии' }), { key: 'Escape' });
    expect(onDismiss).toHaveBeenCalledTimes(2);
    view.unmount();
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(onDismiss).toHaveBeenCalledTimes(2);
  });

  it('leaves Escape from <body> to the upper window and to nobody while it may not be dismissed', () => {
    const lower = vi.fn();
    const upper = vi.fn();
    function Two({ busy }: { busy: boolean }) {
      return (
        <>
          <Dialog label="Нижнее" onDismiss={lower}><button type="button">Нижняя кнопка</button></Dialog>
          <Dialog canDismiss={!busy} label="Верхнее" onDismiss={upper}><button type="button">Верхняя кнопка</button></Dialog>
        </>
      );
    }
    const view = render(<Two busy />);
    act(() => (document.activeElement as HTMLElement).blur());
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(upper).not.toHaveBeenCalled();
    expect(lower).not.toHaveBeenCalled();
    view.rerender(<Two busy={false} />);
    act(() => (document.activeElement as HTMLElement | null)?.blur());
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(upper).toHaveBeenCalledTimes(1);
    expect(lower).not.toHaveBeenCalled();
  });

  it('makes background inert, focuses a non-destructive action and obeys the dismiss gate', () => {
    const onDismiss = vi.fn();
    const outside = document.createElement('div');
    const alreadyInert = document.createElement('div');
    alreadyInert.inert = true;
    document.body.style.overflow = 'clip';
    document.body.append(outside, alreadyInert);

    const view = render(
      <Overlay canDismiss={false} kind="dialog" label="Удаление" onDismiss={onDismiss}>
        <h2>Удалить книгу?</h2>
        <Button variant="danger">Удалить</Button>
        <Button>Отмена</Button>
      </Overlay>,
    );

    const heading = screen.getByRole('heading', { name: 'Удалить книгу?' });
    expect(document.activeElement).toBe(heading);
    expect(heading.tabIndex).toBe(-1);
    fireEvent.keyDown(heading, { key: 'Tab', shiftKey: true });
    expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Отмена' }));
    expect(outside.inert).toBe(true);
    expect(alreadyInert.inert).toBe(true);
    expect(outside.hasAttribute('aria-hidden')).toBe(false);
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });
    expect(onDismiss).not.toHaveBeenCalled();

    view.unmount();
    expect(outside.inert).toBe(false);
    expect(alreadyInert.inert).toBe(true);
    expect(document.body.style.overflow).toBe('clip');
    outside.remove();
    alreadyInert.remove();
    document.body.style.overflow = '';
  });

  it('stays passive when mounted with modal behavior inactive', () => {
    const onDismiss = vi.fn();
    const onBodyKeyDown = vi.fn();
    const trigger = document.createElement('button');
    const outside = document.createElement('div');
    document.body.style.overflow = 'clip';
    document.body.append(trigger, outside);
    document.body.addEventListener('keydown', onBodyKeyDown);
    trigger.focus();

    const view = render(
      <Overlay modal={false} kind="dialog" label="Writer" onDismiss={onDismiss}>
        <button type="button">Writer action</button>
      </Overlay>,
    );

    const dialog = screen.getByRole('dialog', { name: 'Writer' });
    const action = screen.getByRole('button', { name: 'Writer action' });
    expect(dialog.hasAttribute('aria-modal')).toBe(false);
    expect(document.activeElement).toBe(trigger);
    expect(document.body.style.overflow).toBe('clip');
    expect(Boolean(outside.inert)).toBe(false);
    expect(fireEvent.keyDown(action, { key: 'Tab' })).toBe(true);
    onBodyKeyDown.mockClear();
    fireEvent.keyDown(dialog, { key: 'Escape' });
    expect(onDismiss).not.toHaveBeenCalled();
    expect(onBodyKeyDown).toHaveBeenCalledTimes(1);

    view.unmount();
    document.body.removeEventListener('keydown', onBodyKeyDown);
    trigger.remove();
    outside.remove();
    document.body.style.overflow = '';
  });

  it('deactivates modal behavior without remounting children or returning focus', () => {
    const trigger = document.createElement('button');
    const outside = document.createElement('div');
    const alreadyInert = document.createElement('div');
    alreadyInert.inert = true;
    document.body.style.overflow = 'clip';
    document.body.append(trigger, outside, alreadyInert);
    trigger.focus();

    const view = render(
      <Overlay modal kind="dialog" label="Writer" onDismiss={() => undefined}>
        <button type="button">Writer action</button>
      </Overlay>,
    );
    const action = screen.getByRole('button', { name: 'Writer action' });
    expect(document.activeElement).toBe(action);
    expect(document.body.style.overflow).toBe('hidden');
    expect(outside.inert).toBe(true);

    view.rerender(
      <Overlay modal={false} kind="dialog" label="Writer" onDismiss={() => undefined}>
        <button type="button">Writer action</button>
      </Overlay>,
    );

    expect(screen.getByRole('button', { name: 'Writer action' })).toBe(action);
    expect(document.activeElement).toBe(action);
    expect(document.body.style.overflow).toBe('clip');
    expect(outside.inert).toBe(false);
    expect(alreadyInert.inert).toBe(true);

    view.unmount();
    trigger.remove();
    outside.remove();
    alreadyInert.remove();
    document.body.style.overflow = '';
  });

  it.each([
    { order: 'parent-first', mountTogether: false },
    { order: 'child-first', mountTogether: false },
    { order: 'simultaneous', mountTogether: false },
    { order: 'simultaneous', mountTogether: true },
  ])(
    'restores nested modal locks and focus after $order release (mountTogether=$mountTogether) in StrictMode',
    ({ order, mountTogether }) => {
      vi.useFakeTimers();
      vi.stubGlobal('matchMedia', vi.fn(() => ({ matches: false })));
      const background = document.createElement('main');
      const trigger = document.createElement('button');
      const alreadyInert = document.createElement('aside');
      alreadyInert.inert = true;
      background.append(trigger);
      document.body.append(background, alreadyInert);
      document.body.style.overflow = 'clip';
      trigger.focus();
      const view = render(<StrictMode><ConcurrentModals parentOpen={mountTogether} childOpen={mountTogether} /></StrictMode>);

      try {
        if (!mountTogether) {
          view.rerender(<StrictMode><ConcurrentModals parentOpen childOpen={false} /></StrictMode>);
          expect(document.activeElement).toBe(screen.getByRole('button', { name: 'Закрыть заметки' }));
          view.rerender(<StrictMode><ConcurrentModals parentOpen childOpen /></StrictMode>);
        }
        const parentAction = screen.getByRole('button', { name: 'Закрыть заметки' });
        const childAction = screen.getByRole('button', { name: 'Продолжить редактирование' });
        const parentScrim = screen.getByRole('dialog', { name: 'Заметки' }).parentElement;
        const childScrim = screen.getByRole('dialog', { name: 'Отменить изменения' }).parentElement;
        expect(document.activeElement).toBe(childAction);
        expect(parentScrim?.parentElement).toBe(document.body);
        expect(childScrim?.parentElement).toBe(document.body);
        expect(parentScrim?.inert).toBe(true);
        expect(Boolean(childScrim?.inert)).toBe(false);
        expect(background.inert).toBe(true);
        expect(document.body.style.overflow).toBe('hidden');

        view.rerender(
          <StrictMode>
            <ConcurrentModals parentOpen={order === 'child-first'} childOpen={order === 'parent-first'} />
          </StrictMode>,
        );
        expect(background.inert).toBe(true);
        act(() => vi.advanceTimersByTime(220));

        if (order !== 'simultaneous') {
          const remaining = screen.getByRole('dialog');
          expect(background.inert).toBe(true);
          expect(document.body.style.overflow).toBe('hidden');
          expect(Boolean(remaining.parentElement?.inert)).toBe(false);
          expect(document.activeElement).toBe(order === 'parent-first' ? childAction : parentAction);
          view.rerender(<StrictMode><ConcurrentModals parentOpen={false} childOpen={false} /></StrictMode>);
          act(() => vi.advanceTimersByTime(220));
        }

        expect(screen.queryByRole('dialog')).toBeNull();
        expect(background.inert).toBe(false);
        expect(alreadyInert.inert).toBe(true);
        expect(document.body.style.overflow).toBe('clip');
        expect(document.activeElement).toBe(trigger);
      } finally {
        view.unmount();
        background.remove();
        alreadyInert.remove();
        document.body.style.overflow = '';
        vi.useRealTimers();
        vi.unstubAllGlobals();
      }
    },
  );

  it('keeps the child modal active when its mounted parent becomes passive', () => {
    const background = document.createElement('main');
    const trigger = document.createElement('button');
    background.append(trigger);
    document.body.append(background);
    document.body.style.overflow = 'clip';
    trigger.focus();
    const view = render(<StrictMode><ConcurrentModals parentOpen childOpen={false} /></StrictMode>);

    try {
      const parentAction = screen.getByRole('button', { name: 'Закрыть заметки' });
      view.rerender(<StrictMode><ConcurrentModals parentOpen childOpen /></StrictMode>);
      const childAction = screen.getByRole('button', { name: 'Продолжить редактирование' });
      view.rerender(<StrictMode><ConcurrentModals parentOpen childOpen parentModal={false} /></StrictMode>);

      expect(screen.getByRole('button', { name: 'Закрыть заметки' })).toBe(parentAction);
      expect(document.activeElement).toBe(childAction);
      expect(background.inert).toBe(true);
      expect(document.body.style.overflow).toBe('hidden');
      expect(fireEvent.keyDown(childAction, { key: 'Tab' })).toBe(false);
      expect(document.activeElement).toBe(childAction);

      view.rerender(<StrictMode><ConcurrentModals parentOpen childOpen={false} parentModal={false} /></StrictMode>);
      expect(screen.queryByRole('dialog', { name: 'Отменить изменения' })).toBeNull();
      expect(background.inert).toBe(false);
      expect(document.body.style.overflow).toBe('clip');
      expect(document.activeElement).toBe(parentAction);
      trigger.focus();
      view.unmount();
      expect(document.activeElement).toBe(trigger);
    } finally {
      view.unmount();
      background.remove();
      document.body.style.overflow = '';
    }
  });

  it('keeps the child on top when its mounted passive parent becomes modal', () => {
    const background = document.createElement('main');
    const trigger = document.createElement('button');
    background.append(trigger);
    document.body.append(background);
    document.body.style.overflow = 'clip';
    trigger.focus();
    const onChildDismiss = vi.fn();
    const props = { parentOpen: true, parentModal: false, childOpen: false, onChildDismiss };
    const view = render(<StrictMode><ConcurrentModals {...props} /></StrictMode>);

    try {
      const parentAction = screen.getByRole('button', { name: 'Закрыть заметки' });
      parentAction.focus();
      view.rerender(<StrictMode><ConcurrentModals {...props} childOpen /></StrictMode>);
      const childAction = screen.getByRole('button', { name: 'Продолжить редактирование' });
      const parentScrim = screen.getByRole('dialog', { name: 'Заметки' }).parentElement;
      const childScrim = screen.getByRole('dialog', { name: 'Отменить изменения' }).parentElement;
      view.rerender(<StrictMode><ConcurrentModals {...props} childOpen parentModal /></StrictMode>);

      expect(document.activeElement).toBe(childAction);
      expect(parentScrim?.inert).toBe(true);
      expect(Boolean(childScrim?.inert)).toBe(false);
      expect(background.inert).toBe(true);
      expect(document.body.style.overflow).toBe('hidden');
      expect(fireEvent.keyDown(childAction, { key: 'Tab' })).toBe(false);
      fireEvent.keyDown(childAction, { key: 'Escape' });
      expect(onChildDismiss).toHaveBeenCalledTimes(1);

      view.rerender(<StrictMode><ConcurrentModals {...props} parentModal /></StrictMode>);
      expect(screen.queryByRole('dialog', { name: 'Отменить изменения' })).toBeNull();
      expect(screen.getByRole('button', { name: 'Закрыть заметки' })).toBe(parentAction);
      expect(document.activeElement).toBe(parentAction);
      expect(Boolean(parentScrim?.inert)).toBe(false);
      expect(background.inert).toBe(true);
      expect(document.body.style.overflow).toBe('hidden');
      expect(fireEvent.keyDown(parentAction, { key: 'Tab' })).toBe(false);

      view.unmount();
      expect(background.inert).toBe(false);
      expect(document.body.style.overflow).toBe('clip');
      expect(document.activeElement).toBe(trigger);
    } finally {
      view.unmount();
      background.remove();
      document.body.style.overflow = '';
    }
  });
});
