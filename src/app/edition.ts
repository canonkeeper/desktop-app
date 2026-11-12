import type { ParsedLocation } from '@tanstack/react-router';
import type { ReactNode } from 'react';
import type { AuthFailure, CurrentUser } from '../entities/session/api';
import type { CanonKeeperApiClient } from '../shared/api';
import type { AppRoute, RouteNavigate, Surface } from '../shared/navigation/app-route';
import type { ToastInput } from '../ui/feedback/useToastQueue';
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
  /** Signing out, where the edition signs in. */
  onLogout?: () => void;
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
  onLogout?: () => void;
  /** Around the product of the author: the account and what follows it. */
  wrapAccount: (product: ReactNode) => ReactNode;
  /** Around the pages: what lives above them and follows the author from page to page. */
  wrapPages: (pages: ReactNode) => ReactNode;
  /** After the pages in the product: notices of the account. */
  notices: ReactNode;
  /** Over the whole product: dialogs of the account. */
  overlay: ReactNode;
}
