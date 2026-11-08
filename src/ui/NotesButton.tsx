import { StickyNote } from 'lucide-react';
import { IconButton } from './controls/IconButton';
import { useNotes } from '../shared/notes/notes-context';

export function NotesButton({ onOpen }: { onOpen?: () => void }) {
  const notes = useNotes();
  return notes ? <IconButton aria-label="Заметки" title="Заметки" onClick={onOpen ?? notes.open} size="touch"><StickyNote aria-hidden="true" size={18} /></IconButton> : null;
}
