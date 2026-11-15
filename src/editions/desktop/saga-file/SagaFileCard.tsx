// SPDX-License-Identifier: AGPL-3.0-only
import { FileUp } from 'lucide-react';
import { useRef, useState } from 'react';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { EntryCard } from '../../../ui/feedback/EntryCard';
import { LongWork } from '../../../ui/feedback/LongWork';
import { FileButton, SagaFilePicker } from './SagaFileSection';
import { useSagaFileOpen } from './use-saga-file-open';
import { sagaFileWords as words } from './words';

/**
 * «Перенести сагу с сайта или другого компьютера» of the first screen of the app (specs/2026-11-13-desk-sagafile-web.md
 * SW2.1; board 44·2, `Pattern/Desktop/Saga File Card`): the second way to a saga after «Назовите сагу». «Открыть файл
 * саги…» opens the window of files, and a file dropped on the card goes the same way; while it opens, the card says so.
 */
function SagaFileCard({ api }: { api: Pick<CanonKeeperApiClient, 'openSagaArchive'> }) {
  const opening = useSagaFileOpen(api);
  const picker = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const going = opening.opening;
  return (
    <EntryCard
      data-dragging={dragging || undefined}
      onDragLeave={() => setDragging(false)}
      onDragOver={(event) => {
        if (going) return;
        event.preventDefault();
        setDragging(true);
      }}
      onDrop={(event) => {
        event.preventDefault();
        setDragging(false);
        const file = event.dataTransfer.files?.[0];
        if (!going && file) opening.open(file);
      }}
      text={words.cardText}
      title={words.card}
    >
      {going ? (
        <LongWork announcement={words.opening(going.file.name)} detail={words.openingLong} label={words.opening(going.file.name)}
          title={words.opening(going.file.name)} />
      ) : null}
      <FileButton icon={<FileUp aria-hidden="true" size={16} />} label={words.open} onClick={() => picker.current?.click()}
        reason={going ? words.openingReason : null} />
      <SagaFilePicker onFile={opening.open} ref={picker} />
    </EntryCard>
  );
}

/** The card of the app for the slot of the first screen, bound to the API of the app. */
export function sagaFileOfferSlot(api: Pick<CanonKeeperApiClient, 'openSagaArchive'>) {
  return function useDesktopSagaFileOffer() {
    return <SagaFileCard api={api} />;
  };
}
