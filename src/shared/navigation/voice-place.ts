import { createContext, useContext, useEffect, useMemo, useRef } from 'react';
import type { VoicePlace } from '../api';

export type { VoicePlace };

/** A press of the page's own button at the agent's word (specs/2026-11-03-voice-assistant.md V3.3). */
export type VoicePageAction =
  | { action: 'new_drawing'; idea: string }
  | { action: 'choose'; number: number };

/** What a page does for the call: save before it, read a draft or a drawing the agent changed, press its own button. */
export interface VoicePlaceHandlers {
  beforeStart?: () => Promise<unknown>;
  onDraft?: (draftId: string, revision: number) => void;
  onDrawing?: (drawingId: string) => void;
  onAction?: (action: VoicePageAction) => void;
}

/** The saga and the place a page names. */
export interface VoicePlaceRegistration {
  projectId: string;
  place: VoicePlace;
}

/**
 * The call above the pages, as a page sees it (V2.4): where the author is goes to it, and whether it goes on. The call of
 * the edition provides it; without one a page names its place to nobody.
 */
export interface VoicePlaceRegistry {
  register: (next: VoicePlaceRegistration | null, handlers: VoicePlaceHandlers) => void;
  /** The page of an author without a saga says how to found the first one. */
  registerFirstSaga: (create: (() => Promise<unknown>) | null) => void;
  live: boolean;
  /**
   * The versions «до разговора» of the drafts the conversation changed, by draft, while it goes on and while its summary
   * stands (specs/2026-11-10-ux5-voice.md VO1.4): what the voice added is lit against them.
   */
  kept: Readonly<Record<string, number>>;
  /** The author types in a field of his chapter himself: the voice waits for him there (VO2.1). */
  typing: (field: 'idea' | 'text') => void;
}

const VoicePlaceContext = createContext<VoicePlaceRegistry | null>(null);

export const VoicePlaceProvider = VoicePlaceContext.Provider;

/** The page where an author without a saga stands: «Поговорить» founds the first one by this. */
export function useVoiceFirstSaga(create: (() => Promise<unknown>) | null) {
  const register = useContext(VoicePlaceContext)?.registerFirstSaga;
  const latest = useRef(create);
  latest.current = create;
  const needed = Boolean(create);
  useEffect(() => {
    if (!register || !needed) return undefined;
    register(() => latest.current?.() ?? Promise.resolve());
    return () => register(null);
  }, [needed, register]);
}

/**
 * A page names where the author is and what it does for the call (V0.8, V2.4): null where it names no place of its own.
 * Such a page registers nothing, so the workspace above «Письмо» and «Рисование» does not erase the place they name. It
 * answers whether a call goes on here — on a chapter of «Письмо» the fields are the agent's then (V1.3).
 */
export function useVoicePlace(projectId: string | null | undefined, place: VoicePlace | null, handlers: VoicePlaceHandlers = {}) {
  const call = useContext(VoicePlaceContext);
  const latest = useRef(handlers);
  latest.current = handlers;
  const register = call?.register;
  const page = place?.page;
  const { draftId, chapterKey, drawingId, chatId, entry } = place ?? {};
  const named = useMemo<VoicePlaceRegistration | null>(() => (projectId && page ? {
    projectId,
    place: { page, ...(draftId ? { draftId } : {}), ...(chapterKey ? { chapterKey } : {}), ...(drawingId ? { drawingId } : {}),
      ...(chatId ? { chatId } : {}), ...(entry ? { entry } : {}) },
  } : null), [projectId, page, draftId, chapterKey, drawingId, chatId, entry]);
  useEffect(() => {
    if (!register || !named) return undefined;
    const proxy: VoicePlaceHandlers = {
      beforeStart: () => latest.current.beforeStart?.() ?? Promise.resolve(),
      onDraft: (id, revision) => latest.current.onDraft?.(id, revision),
      onDrawing: (id) => latest.current.onDrawing?.(id),
      onAction: (action) => latest.current.onAction?.(action),
    };
    register(named, proxy);
    return () => register(null, {});
  }, [register, named]);
  return {
    live: Boolean(call?.live),
    /** The version «до разговора» of the draft of this place, or null. */
    before: (draftId && call?.kept[draftId]) || null,
    typing: call?.typing ?? noTyping,
  };
}

const noTyping = () => undefined;
