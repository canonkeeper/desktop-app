// SPDX-License-Identifier: AGPL-3.0-only
import { useQueryClient } from '@tanstack/react-query';
import { ArrowRight } from 'lucide-react';
import { useEffect, useRef } from 'react';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import { Button } from '../../../ui/controls/Button';
import { useToasts } from '../../../ui/feedback/useToastQueue';
import { obsidianExportAt, obsidianPath } from './address';
import { obsidianKeys, type ObsidianVault } from './obsidian-api';
import { useAllExports } from './use-export';
import { exportEndLine, words } from './words';

/**
 * The end of an export to Obsidian on another page (specs/2026-11-16-desk-obsidian-export-web.md XW2.8; board 44·66): when an
 * export of this window ends while the author is not on the page of the bridge of its saga where its part stands — the folder
 * linked —, the area of the toasts says what it created and updated, or what stayed unchanged, and «не закончилась» of a run that
 * did not end or was refused, with «Открыть итог», which opens the part. The exports that ended before this observer are not said.
 */
export function ExportOutcome({ navigate, route }: { navigate: RouteNavigate; route: AppRoute }) {
  const toasts = useToasts();
  const client = useQueryClient();
  const exports = useAllExports();
  const here = useRef({ path: route.path, projectId: route.query.get('projectId') });
  here.current = { path: route.path, projectId: route.query.get('projectId') };
  const ended = exports.filter((each) => each.status === 'success' || each.status === 'error');
  const key = ended.map((each) => `${each.id}:${each.status}`).join(',');
  const said = useRef<Set<number> | null>(null);
  if (said.current === null) said.current = new Set(ended.map((each) => each.id));
  const latest = useRef(ended);
  latest.current = ended;

  useEffect(() => {
    for (const each of latest.current) {
      if (said.current!.has(each.id)) continue;
      said.current!.add(each.id);
      const { projectId } = each.write;
      const stands = client.getQueryData<ObsidianVault>(obsidianKeys.vault(projectId))?.linked === true;
      if (here.current.path === obsidianPath && here.current.projectId === projectId && stands) continue;
      const answer = each.status === 'success' ? each.answer ?? null : null;
      const toastId = toasts.push({
        message: exportEndLine(answer),
        tone: answer?.complete ? 'success' : 'warning',
        action: (
          <Button onClick={() => { toasts.dismiss(toastId); const at = obsidianExportAt(projectId); navigate(at.path, at.query); }} size="sm" variant="ghost">
            <ArrowRight aria-hidden="true" size={14} />{words.openResult}
          </Button>
        ),
      });
    }
  }, [client, key, navigate, toasts]);
  return null;
}
