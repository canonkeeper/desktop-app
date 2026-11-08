import { ChevronDown, ChevronUp, EyeOff, MessageCircleQuestion, Network, Pencil, Trash2, Undo2, X } from 'lucide-react';
import { Fragment, useState } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import { authorMaterialKindLabel, type AuthorMaterialsApiClient } from '../../entities/author-materials/api';
import { useSagaMapNodeQuery, type SagaApiClient, type SagaChapterRef, type SagaMap, type SagaMapNodeCard, type SourceView } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { Alert } from '../../ui/feedback/Alert';
import { IconButton } from '../../ui/controls/IconButton';
import { pluralize } from '../../ui/copy';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { countTo } from './map-model';
import { MaterialDeletion, MaterialEditor, type EditedMaterial } from './materials/MaterialEditor';
import materialStyles from './materials/Materials.module.css';
import type { SagaDrawings } from './saga-slot';
import { ChangeList, ChapterLink, LoadError, LoadingRows, type ReaderTarget } from './SagaParts';
import type { MapDecisions } from './use-map-decisions';
import styles from './SagaMap.module.css';

const firstShown = 6;
const shownLinkChapters = 30;

export interface NodeCardProps {
  api: SagaApiClient & AuthorMaterialsApiClient;
  canEdit: boolean;
  decisions: MapDecisions;
  drawings: SagaDrawings;
  map: SagaMap;
  nodeKey: string;
  /** The chapter index the map shows and its key, absent for the whole saga. */
  to: number;
  toKey: string | undefined;
  projectId: string;
  sourceView: SourceView;
  target: ReaderTarget;
  onAsk: (name: string) => void;
  onClose: () => void;
  onFocus: (key: string) => void;
  onSelect: (key: string) => void;
}

const chapters = (count: number) => `${count} ${pluralize(count, 'главе', 'главах', 'главах')}`;
const sharedEvents = (count: number) => `${count} ${pluralize(count, 'событии', 'событиях', 'событиях')}`;

/**
 * The card of a node (specs/2026-11-04-saga-map.md, UC5): who it is under all its names, where it first appears,
 * its links, its events with the way to the reader, what changed about it and its drawings — up to the chapter of
 * the map; «Спросить о нём» starts a question in the chat.
 */
export function NodeCard(props: NodeCardProps) {
  const { api, map, nodeKey, onClose, projectId, sourceView, to, toKey } = props;
  const card = useSagaMapNodeQuery(api, projectId, { sourceView, key: nodeKey, ...(toKey ? { to: toKey } : {}) }, map.revision);
  const data = card.data;
  // What the map knows is there at once; the rest of the card comes after it, in place (R10.5).
  const known = map.nodes.find((node) => node.key === nodeKey);
  const type = data?.type ?? known?.type;
  const category = data ? data.category : known?.category;
  return (
    <>
      <header className={styles.cardHeader}>
        <div className={styles.cardTitle}>
          <h2>{data?.name ?? known?.name ?? 'Карточка'}</h2>
          {data || known ? <p className={styles.cardType}><span aria-hidden="true" className={styles.dot} data-category={category ?? undefined} />{type || 'без типа'}</p> : null}
        </div>
        <IconButton aria-label="Закрыть карточку" onClick={onClose}><X aria-hidden="true" size={18} /></IconButton>
      </header>
      {data ? <CardBody {...props} data={data} /> : card.isPending ? <>
        {known ? <dl className={`${styles.facts} ${styles.loadingFacts}`}><div><dt>Встречается</dt><dd>в {chapters(countTo(known.chapters, to))}</dd></div></dl> : null}
        <LoadingRows />
      </> : (
        isApiStatusFailure(card.error) && card.error.status === 404
          ? (map.building
            ? <EmptyState description="Карточка откроется сама, когда карта будет готова." title="Карта обновляется" />
            : <EmptyState description="Карточки глав могли быть перестроены." title="Такого героя в саге больше нет" />)
          : <LoadError error={card.error} onRetry={() => void card.refetch()} />
      )}
    </>
  );
}

function CardBody({ api, canEdit, data, decisions, drawings, map, onAsk, onClose, onFocus, onSelect, projectId, target, to }: NodeCardProps & { data: SagaMapNodeCard }) {
  const [allEvents, setAllEvents] = useState(false);
  const [allChanges, setAllChanges] = useState(false);
  const [open, setOpen] = useState<string | null>(null);
  const index = map.nodes.findIndex((node) => node.key === data.key);
  const onMap = index >= 0;
  const nodeChapters: SagaChapterRef[] = onMap
    ? map.nodes[index]!.chapters.map((chapter) => map.chapters[chapter]!)
    : data.first ? [data.first.chapter] : [];
  const onlyMaterial = Boolean(data.material) && !data.first && data.mentions === 0;
  const events = allEvents ? data.events : data.events.slice(-firstShown);
  const changes = allChanges ? data.changes : data.changes.slice(-firstShown);
  /** The chapters of shared events of the node and a neighbor up to the chapter, from the graph (R5.4). */
  const together = (key: string) => {
    const other = map.nodes.findIndex((node) => node.key === key);
    const link = map.links.find((item) => (item.source === index && item.target === other) || (item.source === other && item.target === index));
    return link ? [...new Set(link.events.slice(0, countTo(link.events, to)))].map((chapter) => map.chapters[chapter]!) : [];
  };
  return (
    <div className={styles.cardBody}>
      {data.hiddenId ? (
        <Alert tone="info">
          <p>Вы убрали это с карты. Поиск и карточка остаются.</p>
          {canEdit ? <Button aria-label={`Вернуть на карту: ${data.name}`} disabled={decisions.pending} onClick={() => decisions.restore(data.hiddenId!)} size="sm" variant="outline"><Undo2 aria-hidden="true" size={15} />Вернуть на карту</Button> : null}
        </Alert>
      ) : null}
      {data.aliases.length ? <p className={styles.meta}>Также: {data.aliases.join(', ')}</p> : null}
      {data.note ? <p className={styles.note}>{data.note}</p> : null}
      {onlyMaterial ? null : <dl className={styles.facts}>
        <div>
          <dt>Впервые</dt>
          <dd>{data.first ? <>
            <ChapterLink chapter={data.first.chapter} target={target} withBook />
            {data.first.paragraph ? <span className={styles.paragraph}> · ¶ {data.first.paragraph}</span> : null}
          </> : 'позже этой главы'}</dd>
        </div>
        <div><dt>Встречается</dt><dd>в {chapters(data.mentions)}</dd></div>
      </dl>}
      <div className={styles.cardActions}>
        {canEdit ? (
          <Button onClick={() => onAsk(data.name)} size="sm" variant="primary">
            <MessageCircleQuestion aria-hidden="true" size={15} />Спросить о нём
          </Button>
        ) : null}
        {onMap ? (
          <Button onClick={() => onFocus(data.key)} size="sm" variant="outline">
            <Network aria-hidden="true" size={15} />Показать окружение
          </Button>
        ) : null}
        {onMap && canEdit ? (
          <Button aria-label={`Убрать с карты: ${data.name}`} disabled={decisions.pending} onClick={() => decisions.hideNode(data.key)} size="sm" variant="ghost">
            <EyeOff aria-hidden="true" size={15} />Убрать с карты
          </Button>
        ) : null}
      </div>

      {data.material ? <MaterialBlock api={api} canEdit={canEdit} material={data.material} onGone={onClose} plan={onlyMaterial} projectId={projectId} /> : null}

      <section aria-label="Связи" className={styles.section}>
        <h3>Связи</h3>
        {data.links.length ? (
          <ul className={styles.links}>
            {data.links.map((link) => {
              const expanded = open === link.key;
              const shared = expanded ? together(link.key) : [];
              return (
                <li key={link.key}>
                  <div className={styles.linkRow}>
                    <button className={styles.linkName} onClick={() => onSelect(link.key)} type="button">
                      <span aria-hidden="true" className={styles.dot} data-category={link.category} />{link.name}
                    </button>
                    <span className={styles.meta}>{link.fromMaterials ? 'в ваших материалах' : link.together ? `вместе в ${sharedEvents(link.together)}` : 'названы в перемене'}</span>
                    <IconButton aria-expanded={expanded} aria-label={`Подробнее: ${link.name}`} onClick={() => setOpen(expanded ? null : link.key)} size="sm">
                      {expanded ? <ChevronUp aria-hidden="true" size={15} /> : <ChevronDown aria-hidden="true" size={15} />}
                    </IconButton>
                  </div>
                  <LinkReason decisions={decisions} canEdit={canEdit} link={link} nodeKey={data.key} target={target} />
                  {expanded ? (
                    <div className={styles.linkDetails}>
                      {link.changes.length ? <ChangeList changes={link.changes} target={target} />
                        : <p className={styles.meta}>Карточки не записали перемен между ними.</p>}
                      {shared.length ? <>
                        <p className={styles.meta}>Вместе в событиях глав:</p>
                        <ul aria-label={`Главы: ${link.name}`} className={styles.chapterCloud}>
                          {shared.slice(-shownLinkChapters).map((chapter) => (
                            <li key={chapter.key}><ChapterLink chapter={chapter} compact target={target} /></li>
                          ))}
                        </ul>
                      </> : null}
                    </div>
                  ) : null}
                </li>
              );
            })}
          </ul>
        ) : <p className={styles.meta}>{onMap || data.hiddenId ? 'Связей до этой главы нет.' : 'Этого героя нет на карте: он встречается редко.'}</p>}
        {data.beside?.length ? (
          <p className={styles.beside}>Упоминаются в одних главах: {data.beside.map((neighbor, position) => (
            <Fragment key={neighbor.key}>
              {position ? ', ' : ''}
              <button onClick={() => onSelect(neighbor.key)} type="button">{neighbor.name}</button>
            </Fragment>
          ))}</p>
        ) : null}
      </section>

      {data.namedIn.length ? (
        <section aria-label="В ваших материалах" className={styles.section}>
          <h3>В ваших материалах</h3>
          <ul className={materialStyles.named}>
            {data.namedIn.map((record) => (
              <li key={record.id}>
                <Button onClick={() => onSelect(record.name)} size="sm" variant="outline">{record.name} · {authorMaterialKindLabel(record.kind)}</Button>
              </li>
            ))}
          </ul>
        </section>
      ) : null}

      {onlyMaterial ? null : <>
      <section aria-label="События" className={styles.section}>
        <h3>События{data.eventsTotal ? <span className={styles.count}>{data.eventsTotal}</span> : null}</h3>
        {events.length ? (
          <ol className={styles.events}>
            {events.map((event, position) => (
              <li data-key={event.key || undefined} key={`${event.chapter.key}:${position}`}>
                <span>
                  <ChapterLink chapter={event.chapter} target={target} withBook />
                  {event.paragraph ? <span className={styles.paragraph}> · ¶ {event.paragraph}</span> : null}
                </span>
                <span>{event.event}</span>
              </li>
            ))}
          </ol>
        ) : <p className={styles.meta}>Карточки не записали событий с его участием.</p>}
        {data.events.length > firstShown ? (
          <Button className={styles.more} onClick={() => setAllEvents((value) => !value)} size="sm" variant="ghost">
            {allEvents ? 'Свернуть' : `Показать все ${data.events.length}`}
          </Button>
        ) : null}
      </section>

      <section aria-label="Что менялось" className={styles.section}>
        <h3>Что менялось{data.changesTotal ? <span className={styles.count}>{data.changesTotal}</span> : null}</h3>
        {changes.length ? <ChangeList changes={changes} target={target} /> : <p className={styles.meta}>Карточки не записали перемен, где он назван.</p>}
        {data.changes.length > firstShown ? (
          <Button className={styles.more} onClick={() => setAllChanges((value) => !value)} size="sm" variant="ghost">
            {allChanges ? 'Свернуть' : `Показать все ${data.changes.length}`}
          </Button>
        ) : null}
      </section>

      </>}

      {drawings.likeness({ chapters: nodeChapters, entry: data })}
    </div>
  );
}

/**
 * Why two names are linked (R11.2): the latest events they share, or the latest change naming both, each with the way to its chapter,
 * and the way out when it is no link for the author (R11.3).
 */
function LinkReason({ canEdit, decisions, link, nodeKey, target }: { canEdit: boolean; decisions: MapDecisions; link: SagaMapNodeCard['links'][number]; nodeKey: string; target: ReaderTarget }) {
  return (
    <div className={styles.linkDetails}>
      {link.shared?.length ? (
        <ul aria-label={`Вместе: ${link.name}`} className={styles.linkEvents}>
          {link.shared.map((event, position) => (
            <li key={`${event.chapter.key}:${position}`}>
              {event.chapter.number === undefined ? null : 'гл. '}<ChapterLink chapter={event.chapter} compact target={target} /> — {event.event}
            </li>
          ))}
        </ul>
      ) : link.changes.length ? <ChangeList changes={link.changes.slice(-1)} target={target} /> : null}
      {canEdit ? (
        <Button aria-label={`Это не связь: ${link.name}`} className={styles.notLink} disabled={decisions.pending} onClick={() => decisions.hideLink(nodeKey, link.key)} size="sm" variant="ghost">
          Это не связь
        </Button>
      ) : null}
    </div>
  );
}

/** «По материалам автора» (R9.5): what his record of this name says, signed apart from the book. */
function MaterialBlock({ api, canEdit, material, onGone, plan, projectId }: { api: AuthorMaterialsApiClient; canEdit: boolean; material: EditedMaterial; onGone: () => void; plan: boolean; projectId: string }) {
  const [dialog, setDialog] = useState<'edit' | 'delete' | null>(null);
  return (
    <section aria-label="По материалам автора" className={materialStyles.block}>
      <h3>По материалам автора</h3>
      <p className={styles.meta}>{plan ? 'План автора, в книге ещё нет' : 'Запись автора; при расхождении прав текст книги'} · {authorMaterialKindLabel(material.kind)}</p>
      {material.description ? <p>{material.description}</p> : null}
      {material.properties.length ? (
        <dl className={materialStyles.properties}>
          {material.properties.map((property) => <div key={`${property.key}:${property.value}`} style={{ display: 'contents' }}><dt>{property.key}</dt><dd>{property.value}</dd></div>)}
        </dl>
      ) : null}
      {material.sourceName ? <p className={styles.meta}>Из файла «{material.sourceName}»{material.section ? `, ${material.section}` : ''}</p> : null}
      {canEdit ? (
        <div className={styles.cardActions}>
          <Button onClick={() => setDialog('edit')} size="sm" variant="outline"><Pencil aria-hidden="true" size={15} />Править</Button>
          <Button onClick={() => setDialog('delete')} size="sm" variant="ghost"><Trash2 aria-hidden="true" size={15} />Удалить</Button>
        </div>
      ) : null}
      {dialog === 'edit' ? <MaterialEditor api={api} material={material} onClose={() => setDialog(null)} projectId={projectId} /> : null}
      {dialog === 'delete' ? <MaterialDeletion api={api} material={material} onClose={() => setDialog(null)} {...(plan ? { onDeleted: onGone } : {})} projectId={projectId} /> : null}
    </section>
  );
}
