import { useEffect, useState, type RefObject } from 'react';
import { StickyNote } from 'lucide-react';
import type { CanonicalAgentTimelineEvent } from './feed/types';
import { Button } from '../../ui/controls/Button';
import { TextSelectionMenu } from '../../ui/TextSelectionMenu';
import { captureMessageText } from '../../shared/notes/note-capture';
import { useNotes } from '../../shared/notes/notes-context';

/**
 * The selection the author made in the words of a message, while it lies wholly in them: what it says and where it stands,
 * for the menu of a selection. An answer of a chat and an answer of a chat of a saga file read it alike.
 */
export function useMessageSelection(contentRef: RefObject<HTMLElement | null>) {
  const [selection, setSelection] = useState<{ body: string; quote: string; rect: DOMRect } | null>(null);
  useEffect(() => {
    function updateSelection() {
      const content = contentRef.current;
      const current = window.getSelection();
      const range = current?.rangeCount ? current.getRangeAt(0) : null;
      if (!content || !current || current.isCollapsed || !range
        || !content.contains(range.startContainer) || !content.contains(range.endContainer)
        || !current.toString().trim()) {
        setSelection(null);
        return;
      }
      setSelection({ ...captureMessageText(content), rect: range.getBoundingClientRect() });
    }
    document.addEventListener('selectionchange', updateSelection);
    return () => document.removeEventListener('selectionchange', updateSelection);
  }, [contentRef]);
  return { selection, clear: () => setSelection(null) };
}

export function ChatNoteAction({ chatId, event, contentRef }: {
  chatId: string;
  event: CanonicalAgentTimelineEvent;
  contentRef: RefObject<HTMLElement | null>;
}) {
  const notes = useNotes();
  const { selection, clear } = useMessageSelection(contentRef);
  if (!notes || !chatId || !event.providerTurnId || !event.providerItemId || !event.text?.trim()) return null;

  function capture({ body, quote }: { body: string; quote: string }) {
    if (body) notes!.capture({ body, source: {
      kind: 'chat', chatId, providerTurnId: event.providerTurnId!, providerItemId: event.providerItemId!, quote,
    } });
  }

  function finishSelection(run: () => void) {
    contentRef.current?.parentElement?.focus({ preventScroll: true });
    window.getSelection()?.removeAllRanges();
    clear();
    run();
  }

  return <><Button variant="ghost" size="touch" onPointerDown={(event) => event.preventDefault()} onClick={() => {
    const content = contentRef.current;
    if (!content) return;
    const text = captureMessageText(content);
    finishSelection(() => capture(text));
  }}><StickyNote aria-hidden="true" size={16} />В заметки</Button>
    {selection ? <TextSelectionMenu rect={selection.rect} onDismiss={clear}
      onAddNote={() => finishSelection(() => capture(selection))}
      onCopy={() => finishSelection(() => { void navigator.clipboard?.writeText(selection.body).catch(() => undefined); })} /> : null}
  </>;
}
