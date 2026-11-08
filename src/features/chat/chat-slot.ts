import type { QueryClient } from '@tanstack/react-query';
import { createContext, useContext, type ReactNode } from 'react';
import type { ChatApiClient, ChatScope } from '../../entities/chat/api';
import type { ChatSourceTarget, SourceCitation } from '../../shared/navigation/chat-source';
import type { ToastInput } from '../../ui/feedback/useToastQueue';
import type { MarkdownImage, WorkshopLinkTarget } from '../../ui/MarkdownContent';
import type { FeedAction } from './feed/feed-model';
import type { ChatImageNavigation } from './use-chat-image-navigation';

/** Where the drawings of the chat lead beyond it: the lightbox in the URL, the workshop, the feature page, the gallery. */
export interface ChatIllustrationRoutes {
  imageNavigation?: ChatImageNavigation;
  /** A drawing of the workshop: the one a version belongs to, or one just created (image-workshop IW6.3, IW1.1). */
  onOpenDrawing?: (drawingId: string) => void;
  /** `drawing:new` and `drawing:K` of an answer and «Открыть в мастерской» of a card (image-workshop IW7.3). */
  onOpenWorkshop?: (target: 'new' | number, idea?: string) => void;
  onOpenFeaturePage?: () => void;
  onOpenGallery?: () => void;
}

export interface ChatDrawingsOptions extends ChatIllustrationRoutes {
  scope: ChatScope;
  /** An editor of the saga who may send questions now. */
  canEdit: boolean;
  onOpenSource: (source: SourceCitation, trigger: HTMLElement) => void;
  toasts: { push: (toast: ToastInput) => number; dismiss: (id: number) => void };
}

/** The drawings of the chat surface on the site (specs/2026-11-02-image-workshop.md IW7.2): the chat only shows them. */
export interface ChatDrawingsSlot {
  /** The panel: the host of the drawing cards around the conversation and the overlays the URL opens. */
  useDrawings: (options: ChatDrawingsOptions) => { provide: (conversation: ReactNode) => ReactNode; overlays: ReactNode };
  renderImage: (image: MarkdownImage) => ReactNode;
  renderActionDrawing: (action: FeedAction) => ReactNode;
  renderWorkshopLink: (target: WorkshopLinkTarget, label: ReactNode, question: string) => ReactNode;
  renderQuestionImages: (numbers: readonly number[]) => ReactNode;
  renderSteerImages: (numbers: readonly number[], first: number) => ReactNode;
  /** A turn ended: what the drawings depend on is read again. */
  afterTurn: (queryClient: QueryClient) => void;
}

/** What the card of an attached chapter needs of the panel (specs/2026-11-07-chat-attach-chapter.md CA4.2, CA5). */
export interface ChatAttachmentHost {
  api: Pick<ChatApiClient, 'getChatAttachment'>;
  projectId: string;
  chatId: string;
  /** An editor or above: the proofreading is his. */
  canEdit: boolean;
  openSource: (source: ChatSourceTarget, trigger: HTMLElement) => void;
  openDraft?: (draftId: string) => void;
}

/** «Вычитать и вернуть .docx» of an attached chapter on the site (specs/2026-11-07-chat-attach-chapter.md CA5). */
export interface ChatProofreadSlot {
  /** The button among the actions of the card and the problem under them. */
  useProofread: (host: ChatAttachmentHost, number: number) => { action: ReactNode; problem: ReactNode };
}

/** The closed blocks of the chat (specs/2026-11-07-desktop-app.md DA16.4); an absent one is not shown. */
export interface ChatSlot {
  drawings?: ChatDrawingsSlot;
  proofread?: ChatProofreadSlot;
}

const ChatSlotContext = createContext<ChatSlot | null>(null);

export const ChatSlotProvider = ChatSlotContext.Provider;

export function useChatSlot() {
  return useContext(ChatSlotContext);
}
