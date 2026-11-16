import type { ParsedLocation } from '@tanstack/react-router';
import type { ReactNode } from 'react';
import type { AuthFailure, CurrentUser } from '../entities/session/api';
import type { CanonKeeperApiClient } from '../shared/api';
import type { AppRoute, RouteNavigate, Surface } from '../shared/navigation/app-route';
import type { ToastInput } from '../ui/feedback/useToastQueue';
import type { SagaMenuItem } from '../ui/navigation/SagaMenu';
import type { SagaSection } from '../ui/saga-sections';

/**
 * A build of the app (specs/2026-11-07-desktop-app.md DA16.4): the site or the desktop app. One router and one shell read
 * its tables; the open code gets the edition only through `@edition`, and the closed features and entities are imported
 * by `src/editions/site` alone.
 */
export interface Edition {
  /** The routes of the router: a path, the search it keeps, what it does before the page. */
  routes: readonly EditionRoute[];
  /**
   * The pages of a signed-in author by surface; a surface without one is the page «Страница не найдена». A page of the
   * site provides its closed blocks to the open section (the slots of chat, reader and saga map); without them the block
   * is not shown.
   */
  pages: Partial<Record<Surface, EditionPage>>;
  /** The menu «Разделы саги». */
  sections: readonly SagaSection[];
  /** The account of the edition: who the author is, the pages before the product and what stands around it. */
  useAccount: (input: EditionAccountInput) => EditionAccount;
  /**
   * The marks of the closed sections of the edition in the menu, by the mechanism of the mark of «Чат»: «Рисунки» of the
   * site (specs/2026-11-11-ux6-drawing-flow.md DF2.2). An edition without such a section has none.
   */
  useSectionMarks?: (route: AppRoute, saga: SectionMarksSaga) => SectionMarks;
  /**
   * The block of the saga of the edition around the shell: «Скачать сагу файлом…» of «Настроить сагу» of the site
   * (specs/2026-11-13-desk-sagafile-web.md SW4), the transfer of a saga from the site of the app — its line and its item of the
   * menu of sagas (specs/2026-11-16-desk-site-saga.md TR6). An edition without one has nothing there.
   */
  useSagaSettingsBlock?: (input: SagaSettingsBlockInput) => SagaSettingsBlock;
}

/** The saga «Настроить сагу» is about and whether its window is open. */
export interface SagaSettingsBlockInput {
  api: CanonKeeperApiClient;
  saga: { id: string; title: string; role: string } | null;
  /** The window is open: what the block reads for it is read now. */
  open: boolean;
}

/**
 * What a block of the saga puts on the page: its item in the window «Настроить сагу», its line under the top bar, its windows
 * and its item of the menu of sagas.
 */
export interface SagaSettingsBlock {
  /** An item of the window before «Удалить сагу…»; `close` closes the window. Null — none for this author. */
  item: ((close: () => void) => ReactNode) | null;
  /** A line under the top bar while its work goes, on every page. */
  notice: ReactNode;
  /** Its windows over the page. */
  overlay: ReactNode;
  /** An item of the menu of sagas under «Новая сага»; none — the menu has no item of the block. */
  menuItem?: SagaMenuItem | null;
}

/** The saga of the page the marks are read for, and whether its author may change it (specs/2026-11-11-ux9-shelf-chat.md SC3.6). */
export interface SectionMarksSaga {
  api: CanonKeeperApiClient;
  projectId: string | null;
  canEdit: boolean;
}

/** What a section of the menu has while the author is elsewhere: one on its way, the ready ones not opened. */
export type SectionMark = { preparing: boolean; ready: number } | null;
export type SectionMarks = Partial<Record<SagaSection, SectionMark>>;

export interface EditionRoute {
  path: string;
  validateSearch?: (search: Record<string, unknown>) => unknown;
  beforeLoad?: (context: { location: ParsedLocation }) => void;
}

export interface EditionPageProps {
  api: CanonKeeperApiClient;
  navigate: RouteNavigate;
  route: AppRoute;
  /**
   * Signing out, where the edition signs in; `returnTo` — the page the next sign-in comes back to: «Войти в другой аккаунт» of
   * /transfer (specs/2026-11-16-desk-site-saga.md TR3.2).
   */
  onLogout?: (returnTo?: string) => void;
  user: CurrentUser;
}

export interface EditionPage {
  /** The page; null when the address names nothing here. */
  render: (props: EditionPageProps) => ReactNode;
  /** Loads the code of the page ahead, once the author is known. */
  preload?: () => Promise<unknown>;
  /** Who may open it; anyone else lands on the shelf without a message. */
  allows?: (user: CurrentUser) => boolean;
}

export interface EditionAccountInput {
  api: CanonKeeperApiClient;
  navigate: RouteNavigate;
  route: AppRoute;
  /** The address asked for, to come back to after a sign-in. */
  requestedHref: string;
  status: 'checking' | 'anonymous' | 'authenticated' | 'error';
  user: CurrentUser | null;
  /** The sign-in check or an active query answered 401: the sign-in ended. */
  unauthorizedFailure: AuthFailure | null;
  serviceOutage: boolean;
  toasts: { push: (toast: ToastInput) => number; dismissAll: () => void };
}

export interface EditionAccount {
  /** A page that stands instead of everything, before the state of the service is known: public pages of the site. */
  publicPage: ReactNode;
  /** A page that stands instead of the product until the author is known: sign-in and its link pages. */
  signInPage: ReactNode;
  onLogout?: (returnTo?: string) => void;
  /** Around the product of the author: the account and what follows it. */
  wrapAccount: (product: ReactNode) => ReactNode;
  /** Around the pages: what lives above them and follows the author from page to page. */
  wrapPages: (pages: ReactNode) => ReactNode;
  /** After the pages in the product: notices of the account. */
  notices: ReactNode;
  /** Over the whole product: dialogs of the account. */
  overlay: ReactNode;
}
