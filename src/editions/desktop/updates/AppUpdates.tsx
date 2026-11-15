// SPDX-License-Identifier: AGPL-3.0-only
import { useQueryClient } from '@tanstack/react-query';
import { useMemo, useState, type ReactNode } from 'react';
import { ChatSlotProvider, type ChatSlot } from '../../../features/chat/chat-slot';
import type { AppRoute, RouteNavigate } from '../../../shared/navigation/app-route';
import { TopBarStripProvider } from '../../../ui/navigation/TopBarStrip';
import { useNotConnectedReason } from '../account/helper-reason';
import { desktopChatSlot } from '../chat-words';
import { useAppUpdate, type UpdateActionsClient } from './app-update';
import { askReasonOf, type StripState } from './update-words';
import { UpdateStrip } from './UpdateStrip';
import { WhatsNew } from './WhatsNew';

/**
 * The reason of «Спросить» of the edition, after the reason of the server of a waiting: the update being installed, then the
 * helper not connected by the answers of the settings and the link (decision of the coordinator 15.11) — read where the chat is.
 */
function askReasonHook(api: UpdateActionsClient) {
  return function useAskReasonOfTheApp() {
    const installing = askReasonOf(useAppUpdate(api).data);
    const helper = useNotConnectedReason();
    return installing ?? helper?.title ?? null;
  };
}

interface AppUpdatesProps {
  api: UpdateActionsClient;
  navigate: RouteNavigate;
  route: AppRoute;
  children?: ReactNode;
}

/**
 * The update of the app around the pages (specs/2026-11-15-desk-updates-web.md UW2.4, UW3.1, UW3.4): the one reader that
 * follows the update by its times, its strip in the row under the top bar (`TopBarStripProvider`) and the reason of
 * «Спросить» while the app installs — the field `useAskReason` of the slot of the chat, beside the words of the app for the
 * refusals and the failed turns of its helper (specs/2026-11-15-desk-account-words-web.md AW4;
 * specs/2026-11-15-desk-interrupted-turn.md IT1.4): the app has one slot of the chat. The shell of the pages, the
 * edition and the site are the same: these are the seams they already have.
 */
export function AppUpdates({ api, children, navigate, route }: AppUpdatesProps) {
  useAppUpdate(api, { follow: true });
  // «Что нового» opened from the strip (UW2.2) is held here: the row under the top bar builds its strip anew when «Книга
  // прочитана» comes or goes, and a window of the strip would close with it.
  const [notes, setNotes] = useState<StripState['whatsNew']>(null);
  // The one slot of the chat of the app: a second provider would hide the fields of the first.
  const client = useQueryClient();
  const chat = useMemo<ChatSlot>(() => ({ ...desktopChatSlot(navigate, client), useAskReason: askReasonHook(api) }), [api, client, navigate]);
  return (
    <TopBarStripProvider value={<UpdateStrip api={api} navigate={navigate} onWhatsNew={setNotes} route={route} />}>
      <ChatSlotProvider value={chat}>{children}</ChatSlotProvider>
      {notes ? <WhatsNew notes={notes.notes} onClose={() => setNotes(null)} version={notes.version} /> : null}
    </TopBarStripProvider>
  );
}
