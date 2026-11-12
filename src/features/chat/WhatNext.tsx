import { Mic } from 'lucide-react';
import { useId } from 'react';
import { useSagaMapQuery, type SagaApiClient, type SourceView } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { useTalkAction } from '../../ui/navigation/TopBarStrip';
import { sectionEntry, useSagaSections } from '../../ui/saga-sections';
import { useNarrowScreen } from '../../ui/use-narrow-screen';
import styles from './WhatNext.module.css';

/** Where «Что дальше» leads: the pages the chat page opens. */
export interface WhatNextRoutes {
  openMap: () => void;
  /** «Рисование» with the idea in «Что нарисовать?»: nothing is drawn until the author presses «Нарисовать». */
  draw: (idea: string) => void;
  write: () => void;
}

/** The hero of the saga as the map names him (specs/2026-11-10-first-questions.md FQ1.2): a character of the book. */
function useHero(api: SagaApiClient, projectId: string, sourceView: SourceView, wanted: boolean) {
  const map = useSagaMapQuery(api, wanted ? projectId : '', sourceView).data;
  const hero = map?.nodes.find((node) => node.key === map.hero && node.category === 'character' && !node.material);
  return hero?.name ?? '';
}

/**
 * «Что дальше» under the first answer of the saga (specs/2026-11-10-ux3-first-screen.md FS3.1, FS3.2; board 20·44): the map,
 * the portrait of the hero, a chapter and the voice — the place where the author learns that the saga is more than a chat.
 * A reader of the saga gets the map only; a build without a section or the voice has no button of it.
 */
export function WhatNext({ api, canEdit, projectId, routes, sourceView }: {
  api: SagaApiClient;
  /** An editor of the saga: he may draw, write and talk in it. */
  canEdit: boolean;
  projectId: string;
  routes: WhatNextRoutes;
  sourceView: SourceView;
}) {
  const labelId = useId();
  const sections = useSagaSections();
  const talk = useTalkAction();
  const phone = useNarrowScreen(767);
  const drawing = canEdit && sections.includes('drawing');
  const hero = useHero(api, projectId, sourceView, drawing);
  const size = phone ? 'touch' : 'sm';
  const MapIcon = sectionEntry('saga').Icon;
  const DrawIcon = sectionEntry('drawing').Icon;
  const WriteIcon = sectionEntry('writing').Icon;
  return (
    <div aria-labelledby={labelId} className={styles.next} role="group">
      <p className={styles.label} id={labelId}>Что дальше</p>
      <div className={styles.actions}>
        <Button onClick={routes.openMap} size={size} variant="outline"><MapIcon aria-hidden="true" size={14} />Посмотреть карту саги</Button>
        {drawing ? (
          <Button onClick={() => routes.draw(hero ? `Портрет: ${hero}` : '')} size={size} variant="outline">
            <DrawIcon aria-hidden="true" size={14} />Нарисовать героя
          </Button>
        ) : null}
        {canEdit && sections.includes('writing') ? (
          <Button onClick={routes.write} size={size} variant="outline"><WriteIcon aria-hidden="true" size={14} />Написать главу</Button>
        ) : null}
        {canEdit && talk ? <Button onClick={talk} size={size} variant="outline"><Mic aria-hidden="true" size={14} />Поговорить</Button> : null}
      </div>
    </div>
  );
}
