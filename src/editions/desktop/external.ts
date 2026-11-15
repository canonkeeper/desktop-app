// SPDX-License-Identifier: AGPL-3.0-only
import type { MouseEvent } from 'react';
import type { DesktopShell } from '../../shared/desktop/shell';
import type { Toasts } from '../../ui/feedback/useToastQueue';

/**
 * A link out of the app (specs/2026-11-13-desk-settings.md ST4.1): the shell opens it in the browser of the system
 * (`open_external`); in a browser without the shell it is an ordinary link to a new tab.
 */
export function external(href: string, shell: DesktopShell | null, toasts: Pick<Toasts, 'push'>) {
  return {
    href, target: '_blank', rel: 'noopener noreferrer',
    onClick: shell ? (event: MouseEvent<HTMLAnchorElement>) => {
      event.preventDefault();
      void shell.openExternal(href).catch(() => toasts.push({ message: 'Не удалось открыть ссылку', tone: 'error' }));
    } : undefined,
  };
}
