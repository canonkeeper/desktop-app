// SPDX-License-Identifier: AGPL-3.0-only
import { useMutationState } from '@tanstack/react-query';
import { FolderInput } from 'lucide-react';
import { lazy, useMemo } from 'react';
import type { EditionPage, EditionPageProps } from '../../../app/edition';
import { openPages } from '../../../app/pages';
import type { ShelfSlot } from '../../../features/manuscript/shelf/shelf-slot';
import { SagaSlotProvider, type MaterialsImport, type SagaSlot } from '../../../features/saga/saga-slot';
import type { RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { obsidianAt } from './address';
import { obsidianKeys } from './obsidian-api';
import type { LoadWrite } from './use-load';
import { words } from './words';

const importObsidianPage = () => import('./ObsidianPage');
const ObsidianPage = lazy(() => importObsidianPage().then((module) => ({ default: module.ObsidianPage })));

/** The page of the bridge at its address of «Книги» (OW1.1): the code comes when the author opens it. */
export function ObsidianPageAt(props: EditionPageProps) {
  return <ObsidianPage {...props} />;
}

/** The books of a saga go from an Obsidian folder now: the write of the books of «Загрузить» has not answered yet (OW4.6). */
function useLoadingFromObsidian(projectId: string) {
  return useMutationState({
    filters: {
      mutationKey: obsidianKeys.loadBooks, status: 'pending',
      predicate: (mutation) => (mutation.state.variables as LoadWrite<unknown> | undefined)?.projectId === projectId,
    },
  }).length > 0;
}

/**
 * The members of «Книги» the bridge gives (specs/2026-11-15-desk-obsidian-web.md OW1.2, OW4.6): «Из Obsidian…» beside «Добавить
 * книги» with its caption in the first steps, and the volumes of a load from a folder, which this tab does not send, said as
 * loaded while the write of the books goes.
 */
export const obsidianShelf: Pick<ShelfSlot, 'bookSource' | 'useLoadingElsewhere'> = {
  bookSource: { label: words.from, caption: words.fromCaption, at: obsidianAt },
  useLoadingElsewhere: useLoadingFromObsidian,
};

/** «Ваши материалы» of the app: «Из Obsidian…» beside «Записать самому», quieter than it (OW1.2). */
function useObsidianMaterials({ canEdit, navigate, projectId }: { canEdit: boolean; navigate: RouteNavigate; projectId: string }): MaterialsImport {
  return {
    step: null, saved: null, add: null,
    more: canEdit ? (
      <Button onClick={() => { const at = obsidianAt(projectId); navigate(at.path, at.query, { from: { section: 'memory' } }); }} variant="outline">
        <FolderInput aria-hidden="true" size={16} />{words.from}
      </Button>
    ) : null,
  };
}

/** Nothing of the bridge waits for the check of the author: the tab of the part says nothing. */
function useNothingWaits(_options: { canEdit: boolean; projectId: string }) {
  return { running: false, waiting: 0 };
}

const memorySlot: SagaSlot = { materialsImport: { useImport: useObsidianMaterials, useWaiting: useNothingWaits } };

function DesktopMemoryPage(props: EditionPageProps) {
  const slot = useMemo(() => memorySlot, []);
  return <SagaSlotProvider value={slot}>{openPages.memory.render(props)}</SagaSlotProvider>;
}

/** «Что помнит помощник» of the app: the open page with the entry of the bridge in «Ваши материалы» (OW1.2). */
export const desktopMemoryPage: EditionPage = {
  render: (props) => <DesktopMemoryPage {...props} />,
  preload: openPages.memory.preload,
};
