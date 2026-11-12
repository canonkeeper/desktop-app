import { useEffect, useRef, useState, type DragEvent } from 'react';

/** A drag that carries files from the computer, not a text or a link of the page. */
function carriesFiles(event: { dataTransfer: DataTransfer | null }) {
  return Boolean(event.dataTransfer && [...event.dataTransfer.types].includes('Files'));
}

/**
 * The chat panel takes a chapter file dropped anywhere on it (specs/2026-11-11-ux4-after-answer.md AA4.4; Ч-52): while a
 * file is dragged over it says «Отпустите, чтобы приложить главу». A file dropped anywhere on the page of the chat never
 * opens in the tab instead of the chat; where the field takes no chapter — a reader, a question on its way — the drop is
 * only kept from leaving the page.
 */
export function useChatDrop({ enabled, onFile }: { enabled: boolean; onFile: (file: File) => void }) {
  const [dragging, setDragging] = useState(false);
  const depth = useRef(0);
  const latest = useRef({ enabled, onFile });
  latest.current = { enabled, onFile };

  useEffect(() => {
    // Beside the panel the browser would open the file in place of the page.
    const keep = (event: globalThis.DragEvent) => {
      if (carriesFiles(event)) event.preventDefault();
    };
    window.addEventListener('dragover', keep);
    window.addEventListener('drop', keep);
    return () => {
      window.removeEventListener('dragover', keep);
      window.removeEventListener('drop', keep);
    };
  }, []);

  useEffect(() => {
    if (!enabled) {
      depth.current = 0;
      setDragging(false);
    }
  }, [enabled]);

  return {
    dragging: dragging && enabled,
    handlers: {
      onDragEnter: (event: DragEvent<HTMLElement>) => {
        if (!carriesFiles(event)) return;
        event.preventDefault();
        depth.current += 1;
        if (latest.current.enabled) setDragging(true);
      },
      onDragOver: (event: DragEvent<HTMLElement>) => {
        if (!carriesFiles(event)) return;
        event.preventDefault();
        event.dataTransfer.dropEffect = latest.current.enabled ? 'copy' : 'none';
      },
      onDragLeave: (event: DragEvent<HTMLElement>) => {
        if (!carriesFiles(event)) return;
        depth.current = Math.max(0, depth.current - 1);
        if (depth.current === 0) setDragging(false);
      },
      onDrop: (event: DragEvent<HTMLElement>) => {
        if (!carriesFiles(event)) return;
        event.preventDefault();
        depth.current = 0;
        setDragging(false);
        const file = event.dataTransfer.files[0];
        if (file && latest.current.enabled) latest.current.onFile(file);
      },
    },
  };
}
