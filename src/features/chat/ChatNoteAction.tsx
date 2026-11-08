import { useEffect, useState, type RefObject } from 'react';
import { StickyNote } from 'lucide-react';
import type { CanonicalAgentTimelineEvent } from './feed/types';
import { Button } from '../../ui/controls/Button';
import { TextSelectionMenu } from '../../ui/TextSelectionMenu';
import { captureMessageText } from '../../shared/notes/note-capture';
import { useNotes } from '../../shared/notes/notes-context';

export function ChatNoteAction({ chatId, event, contentRef }: {
  chatId: string;
  event: CanonicalAgentTimelineEvent;
  contentRef: RefObject<HTMLElement | null>;
}) {
  const notes = useNotes();
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
  if (!notes || !chatId || !event.providerTurnId || !event.providerItemId || !event.text?.trim()) return null;

  function capture({ body, quote }: { body: string; quote: string }) {
    if (body) notes!.capture({ body, source: {
      kind: 'chat', chatId, providerTurnId: event.providerTurnId!, providerItemId: event.providerItemId!, quote,
    } });
  }

  function finishSelection(run: () => void) {
    contentRef.current?.parentElement?.focus({ preventScroll: true });
    window.getSelection()?.removeAllRanges();
    setSelection(null);
    run();
  }

  return <><Button variant="ghost" size="touch" onPointerDown={(event) => event.preventDefault()} onClick={() => {
    const content = contentRef.current;
    if (!content) return;
    const text = captureMessageText(content);
    finishSelection(() => capture(text));
  }}><StickyNote aria-hidden="true" size={16} />В заметки</Button>
    {selection ? <TextSelectionMenu rect={selection.rect} onDismiss={() => setSelection(null)}
      onAddNote={() => finishSelection(() => capture(selection))}
      onCopy={() => finishSelection(() => { void navigator.clipboard?.writeText(selection.body).catch(() => undefined); })} /> : null}
  </>;
}
