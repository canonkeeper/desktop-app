// SPDX-License-Identifier: AGPL-3.0-only
import { useQueryClient } from '@tanstack/react-query';
import { ArrowRight } from 'lucide-react';
import { useEffect, useRef } from 'react';
import { useImportQueuesQuery } from '../../../features/manuscript/api';
import type { CanonKeeperApiClient } from '../../../shared/api';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { obsidianKeys } from './obsidian-api';
import { obsidianPath } from './address';
import { useAllLoads } from './use-load';
import { outcomeLine, words } from './words';

/** A run of «Загрузить» as the cache of the mutations keeps its two writes. */
interface Run {
  projectId: string;
  pending: boolean;
  records: { saved: number } | 'failed' | null;
  books: { loading: number } | 'failed' | null;
}

/**
 * The end of «Загрузить» on another page (specs/2026-11-15-desk-obsidian-web.md OW4.1; board 44·53): when both writes of a run
 * ended while the author is not on the step «что получится» of its saga, the area of the toasts says what each part did — both,
 * one, a part that failed — with «Открыть итог», which opens the step «что получится» where the result waits. And once the load
 * of «Книги» a run started ends, the folder of the saga is read again: a new book holds its folder only then (OW4.5).
 */
export function LoadOutcome({ api, navigate, route }: { api: Pick<CanonKeeperApiClient, 'listImportQueues'>; navigate: RouteNavigate; route: AppRoute }) {
  const toasts = useToasts();
  const loads = useAllLoads();
  const where = () => ({ path: route.path, projectId: route.query.get('projectId'), step: route.query.get('step') });
  const here = useRef(where());
  here.current = where();

  const runs = new Map<string, Run & { parts: { materials: boolean; books: boolean }; seen: { materials: boolean; books: boolean } }>();
  const runOf = (write: { run: string; projectId: string; parts: { materials: boolean; books: boolean } }) => {
    const run = runs.get(write.run) ?? { projectId: write.projectId, pending: false, records: null, books: null, parts: write.parts, seen: { materials: false, books: false } };
    runs.set(write.run, run);
    return run;
  };
  // The latest write of each part of a run: a repeat with the same key or «Загрузить книги ещё раз» takes the place of the one before.
  for (const write of [...loads.materials].sort((left, right) => left.submittedAt - right.submittedAt)) {
    if (!write.write) continue;
    const run = runOf(write.write);
    run.seen.materials = true;
    run.records = write.status === 'pending' || write.status === 'idle' ? null
      : write.answer ? { saved: write.answer.created.count + write.answer.updated.count + write.answer.replaced.count } : 'failed';
  }
  for (const write of [...loads.books].sort((left, right) => left.submittedAt - right.submittedAt)) {
    if (!write.write) continue;
    const run = runOf(write.write);
    run.seen.books = true;
    run.books = write.status === 'pending' || write.status === 'idle' ? null
      : write.answer ? { loading: write.answer.folders.filter((folder) => folder.outcome === 'queued').length } : 'failed';
  }
  // A run has ended once every write it has came back: the books go after the records.
  const ended = [...runs.entries()].filter(([, run]) => (!run.parts.materials || (run.seen.materials && run.records !== null))
    && (!run.parts.books || (run.seen.books && run.books !== null)));
  const key = ended.map(([id, run]) => `${id}:${JSON.stringify([run.records, run.books])}`).join(',');
  // The runs that ended before this observer are not said again.
  const said = useRef<Set<string> | null>(null);
  if (said.current === null) said.current = new Set(ended.map(([id]) => id));
  const latest = useRef(ended);
  latest.current = ended;

  useEffect(() => {
    for (const [id, run] of latest.current) {
      if (said.current!.has(id)) continue;
      said.current!.add(id);
      // Silent only for the author on the step «что получится» of its saga, where the result stands.
      if (here.current.path === obsidianPath && here.current.projectId === run.projectId && here.current.step === 'preview') continue;
      const toastId = toasts.push({
        message: outcomeLine(run.records, run.books),
        tone: run.records === 'failed' || run.books === 'failed' ? 'warning' : 'success',
        action: (
          <Button onClick={() => { toasts.dismiss(toastId); navigate(obsidianPath, { projectId: run.projectId, step: 'preview' }); }} size="sm" variant="ghost">
            <ArrowRight aria-hidden="true" size={14} />{words.openResult}
          </Button>
        ),
      });
    }
  }, [key, navigate, toasts]);

  const started = [...new Set(loads.books.filter((write) => write.answer?.queue).map((write) => write.write.projectId))];
  return <>{started.map((projectId) => <LoadEnd api={api} key={projectId} projectId={projectId} />)}</>;
}

/** The load of «Книги» of a saga a run started: its end reads the folder of the saga again (OW4.5). */
function LoadEnd({ api, projectId }: { api: Pick<CanonKeeperApiClient, 'listImportQueues'>; projectId: string }) {
  const client = useQueryClient();
  const queues = useImportQueuesQuery(api, projectId);
  const live = (queues.data?.data ?? []).some((queue) => ['uploading', 'draft', 'running'].includes(queue.status));
  const was = useRef(live);
  useEffect(() => {
    if (was.current && !live) void client.invalidateQueries({ queryKey: obsidianKeys.vault(projectId) });
    was.current = live;
  }, [client, live, projectId]);
  return null;
}
