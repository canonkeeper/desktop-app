// SPDX-License-Identifier: AGPL-3.0-only
import { FileUp } from 'lucide-react';
import { useRef, useState } from 'react';
import type { CanonKeeperApiClient } from '../../../shared/api';
import { EntryCard } from '../../../ui/feedback/EntryCard';
import { LongWork } from '../../../ui/feedback/LongWork';
import { LinkStart } from '../account/LinkStart';
import { FileButton } from './FileButton';
import { SagaFilePicker } from './SagaFileSection';
import { NeedLinkLine, TransferButton, useTransferNeedsLink } from './TransferEntries';
import { transferWords } from './transfer-words';
import { useSagaFileOpen } from './use-saga-file-open';
import { sagaFileWords as words } from './words';
import styles from './SagaFile.module.css';

/**
 * «Перенести сагу с сайта или другого компьютера» of the first screen of the app (specs/2026-11-13-desk-sagafile-web.md
 * SW2.1; board 44·2, `Pattern/Desktop/Saga File Card`): the second way to a saga after «Назовите сагу». «Перенести с сайта…»
 * of a linked app is its main button, without a link — the line and «Связать с аккаунтом» in its place
 * (specs/2026-11-16-desk-site-saga.md TR6.1; board 44·77). «Открыть файл саги…» opens the window of files, and a file dropped on
 * the card goes the same way; while it opens, the card says so.
 */
function SagaFileCard({ api }: { api: Pick<CanonKeeperApiClient, 'openSagaArchive'> }) {
  const opening = useSagaFileOpen(api);
  const picker = useRef<HTMLInputElement>(null);
  const [dragging, setDragging] = useState(false);
  const going = opening.opening;
  const needsLink = useTransferNeedsLink();
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
      {needsLink ? <NeedLinkLine /> : null}
      <div className={styles.actions}>
        {needsLink ? <LinkStart account={needsLink} chooseHelper={false} /> : <TransferButton label={transferWords.card} variant="primary" />}
        <FileButton icon={<FileUp aria-hidden="true" size={16} />} label={words.open} onClick={() => picker.current?.click()}
          reason={going ? words.openingReason : null} />
      </div>
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
