// SPDX-License-Identifier: AGPL-3.0-only
import { Info, RotateCcw } from 'lucide-react';
import { useEffect } from 'react';
import { desktopShell } from '../../shared/desktop/shell';
import { useToasts } from '../../ui/feedback/useToastQueue';

/** The page asked the shell already in this load: a second mount of the root asks nothing. */
let asked = false;

/**
 * The lines of a launch of the app (specs/2026-11-13-desk-settings.md ST4.4; board 44·22): once the window opened, the
 * page asks the shell once what happened — the backend fell and was started again, the address of the window changed —
 * and says it in the one area of the toasts. The shell gives the notes once a launch, so a reload says nothing again.
 */
export function LaunchNotes() {
  const toasts = useToasts();
  useEffect(() => {
    const shell = desktopShell();
    if (!shell || asked) return;
    asked = true;
    void shell.launchNotes().then((notes) => {
      if (notes.restartedAfterCrash) toasts.push({ icon: RotateCcw, message: 'CanonKeeper перезапущен после сбоя', tone: 'info' });
      if (notes.portChanged) toasts.push({ icon: Info, message: 'Адрес окна сменился: место чтения и вид текста сброшены', tone: 'info' });
    }, () => undefined);
  }, [toasts]);
  return null;
}
