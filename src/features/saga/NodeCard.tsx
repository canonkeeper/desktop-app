import { ChevronDown, ChevronUp, EyeOff, MessageCircleQuestion, Network, Pencil, Search, Undo2, Unlink, X } from 'lucide-react';
import { Fragment, useState } from 'react';
import { isApiStatusFailure } from '../../entities/api-errors';
import { authorMaterialKindLabel, type AuthorMaterialsApiClient } from '../../entities/author-materials/api';
import { useSagaMapNodeQuery, type SagaApiClient, type SagaChapterRef, type SagaMap, type SagaMapCategory, type SagaMapNodeCard, type SourceView } from '../../entities/saga/api';
import { Button } from '../../ui/controls/Button';
import { IconButton } from '../../ui/controls/IconButton';
import { LinkAction } from '../../ui/controls/LinkAction';
import { pluralize } from '../../ui/copy';
import { EmptyState } from '../../ui/feedback/EmptyState';
import { countTo } from './map-model';
import { MaterialDeletion, MaterialEditor, type EditedMaterial } from './materials/MaterialEditor';
import materialStyles from './materials/Materials.module.css';
import type { SagaDrawings } from './saga-slot';
import { ChangeList, ChapterLink, LoadError, LoadingRows, type ReaderTarget } from './SagaParts';
import { paragraphsLabel } from '../../shared/text/place';
import { onlyOnMap, type MapDecisions } from './use-map-decisions';
import styles from './SagaMap.module.css';
import { MoreMenu } from '../../ui/overlays/MoreMenu';

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
  /** A new chat with the question in the field, not sent (R5.6). */
  onAsk: (question: string) => void;
  onClose: () => void;
  /** «Найти похожее»: the name goes into the search of the map (MP3.4). */
  onFindLike: (name: string) => void;
  onFocus: (key: string) => void;
  onSelect: (key: string) => void;
}

const chapters = (count: number) => `${count} ${pluralize(count, 'главе', 'главах', 'главах')}`;

/**
 * The question «Спросить в чате» puts into the field, by the kind of the node (specs/2026-11-11-ux8-map.md MP3.4, J-39). The
 * name stands as it is, after a word of its kind, so no case of it is needed (specs/2026-11-11-ux9-shelf-chat.md SC3.5,
 * W-5E): «Расскажи про героя: Мара — …».
 */
export function chatQuestion(name: string, category: SagaMapCategory | null | undefined) {
  switch (category) {
    case 'character': return `Расскажи про героя: ${name} — кто это, главные события и с кем связан по книге.`;
    case 'place': return `Расскажи про место: ${name} — что там происходит и кто там бывает по книге.`;
    case 'faction': return `Расскажи про фракцию: ${name} — кто в неё входит, чего она добивается и что с ней происходит по книге.`;
    case 'item': return `Расскажи про предмет: ${name} — что это, у кого он бывал и что с ним происходило по книге.`;
    default: return `Расскажи по книге: ${name} — что это, главные события и связи.`;
  }
}
const sharedEvents = (count: number) => `${count} ${pluralize(count, 'событии', 'событиях', 'событиях')}`;

/** Why two names are linked, in the words of the author (SC3.5, W-5C). */
function linkWhy(link: SagaMapNodeCard['links'][number]) {
  if (link.fromMaterials) return 'в ваших материалах';
  return link.together ? `Вместе в ${sharedEvents(link.together)}` : 'Оба названы, когда что-то изменилось';
}

/**
 * The card of a node (specs/2026-11-04-saga-map.md, UC5): who it is under all its names, where it first appears,
 * its links, its events with the way to the reader, what changed about it and its drawings — up to the chapter of
 * the map; «Спросить в чате» starts a question in the chat.
 */
export function NodeCard(props: NodeCardProps) {
  const { api, map, nodeKey, onClose, onFindLike, projectId, sourceView, to, toKey } = props;
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
            ? <EmptyState why="Карточка откроется сама, когда карта будет готова." title="Карта обновляется" />
            : <EmptyState action={<Button onClick={() => onFindLike(nodeKey)} variant="primary"><Search aria-hidden="true" size={16} />Найти похожее</Button>}
              title="Этого имени в саге больше нет" why="Возможно, книгу обновили." />)
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
      {/* One line of a node off the map, its one way back (MP3.2). */}
      {data.hiddenId ? (
        <p className={styles.removedLine}>
          <span>Убрано с карты. Поиск и карточка остаются</span>
          {canEdit ? <Button aria-label={`Вернуть на карту: ${data.name}`} disabled={decisions.pending} onClick={() => decisions.restore(data.hiddenId!)} size="sm" variant="ghost"><Undo2 aria-hidden="true" size={15} />Вернуть на карту</Button> : null}
        </p>
      ) : null}
      {data.aliases.length ? <p className={styles.meta}>Также: {data.aliases.join(', ')}</p> : null}
      {data.note ? <p className={styles.note}>{data.note}</p> : null}
      {onlyMaterial ? null : <dl className={styles.facts}>
        <div>
          <dt>Впервые</dt>
          <dd>{data.first ? <>
            <ChapterLink chapter={data.first.chapter} target={target} withBook />
            {data.first.paragraph ? <span className={styles.paragraph}>, {paragraphsLabel(data.first.paragraph)}</span> : null}
          </> : 'позже этой главы'}</dd>
        </div>
        <div><dt>Встречается</dt><dd>в {chapters(data.mentions)}</dd></div>
      </dl>}
      <div className={styles.cardActions}>
        {canEdit ? (
          <Button onClick={() => onAsk(chatQuestion(data.name, data.category))} size="sm" variant="primary">
            <MessageCircleQuestion aria-hidden="true" size={15} />Спросить в чате
          </Button>
        ) : null}
        {onMap ? (
          <Button onClick={() => onFocus(data.key)} size="sm" variant="outline">
            <Network aria-hidden="true" size={15} />Показать, с кем связан
          </Button>
        ) : null}
        {onMap && canEdit ? (
          <Button aria-label={`Убрать с карты: ${data.name}`} disabled={decisions.pending} onClick={() => decisions.hideNode(data.key, data.category, onClose)} size="sm" title={onlyOnMap} variant="ghost">
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
                    {/* A link-action of the controls: under a finger its target is 44 px by the control (SH4.2, W-32). */}
                    <LinkAction className={styles.linkName} onClick={() => onSelect(link.key)}>
                      <span aria-hidden="true" className={styles.dot} data-category={link.category} />{link.name}
                    </LinkAction>
                    <span className={styles.meta}>{linkWhy(link)}</span>
                    <IconButton aria-expanded={expanded} aria-label={`Подробнее: ${link.name}`} onClick={() => setOpen(expanded ? null : link.key)} size="sm">
                      {expanded ? <ChevronUp aria-hidden="true" size={15} /> : <ChevronDown aria-hidden="true" size={15} />}
                    </IconButton>
                  </div>
                  <LinkReason decisions={decisions} canEdit={canEdit} link={link} nodeKey={data.key} target={target} />
                  {expanded ? (
                    <div className={styles.linkDetails}>
                      {link.changes.length ? <ChangeList changes={link.changes} target={target} />
                        : <p className={styles.meta}>Помощник не нашёл в прочитанных главах перемен между ними.</p>}
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
                  {event.paragraph ? <span className={styles.paragraph}>, {paragraphsLabel(event.paragraph)}</span> : null}
                </span>
                <span>{event.event}</span>
              </li>
            ))}
          </ol>
        ) : <p className={styles.meta}>Помощник не нашёл в прочитанных главах событий с его участием.</p>}
        {data.events.length > firstShown ? (
          <Button className={styles.more} onClick={() => setAllEvents((value) => !value)} size="sm" variant="ghost">
            {allEvents ? 'Свернуть' : `Показать все ${data.events.length}`}
          </Button>
        ) : null}
      </section>

      <section aria-label="Что изменилось" className={styles.section}>
        <h3>Что изменилось{data.changesTotal ? <span className={styles.count}>{data.changesTotal}</span> : null}</h3>
        {changes.length ? <ChangeList changes={changes} target={target} /> : <p className={styles.meta}>Помощник не нашёл в прочитанных главах перемен, где он назван.</p>}
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
 * and «Убрать связь» when it is no link for the author (R11.3; specs/2026-11-11-ux8-map.md MP3.1).
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
        <Button aria-label={`Убрать связь: ${link.name}`} className={styles.notLink} disabled={decisions.pending} onClick={() => decisions.hideLink(nodeKey, link.key)} size="sm" title={onlyOnMap} variant="ghost">
          <Unlink aria-hidden="true" size={15} />Убрать связь
        </Button>
      ) : null}
    </div>
  );
}

/** «По вашим материалам» (R9.5; specs/2026-11-10-ux2-memory-page.md MP1.4): what his record of this name says, signed apart from the book. */
function MaterialBlock({ api, canEdit, material, onGone, plan, projectId }: { api: AuthorMaterialsApiClient; canEdit: boolean; material: EditedMaterial; onGone: () => void; plan: boolean; projectId: string }) {
  const [dialog, setDialog] = useState<'edit' | 'delete' | null>(null);
  return (
    <section aria-label="По вашим материалам" className={materialStyles.block}>
      <h3>По вашим материалам</h3>
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
          <Button onClick={() => setDialog('edit')} size="sm" variant="outline"><Pencil aria-hidden="true" size={15} />Изменить</Button>
          {/* The deletion of the record stands in its «⋯», last and apart (PA3.2). */}
          <MoreMenu label="Действия записи" remove={{ label: 'Удалить запись…', run: () => setDialog('delete') }} />
        </div>
      ) : null}
      {dialog === 'edit' ? <MaterialEditor api={api} material={material} onClose={() => setDialog(null)} projectId={projectId} /> : null}
      {dialog === 'delete' ? <MaterialDeletion api={api} material={material} onClose={() => setDialog(null)} {...(plan ? { onDeleted: onGone } : {})} projectId={projectId} /> : null}
    </section>
  );
}
