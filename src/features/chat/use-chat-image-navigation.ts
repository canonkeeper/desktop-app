import { chatSourceQuery, type SourceCitation } from '../../shared/navigation/chat-source';
import type { AppRoute, RouteNavigate } from '../../shared/navigation/app-route';

export type ChatImagePanel = 'info' | 'layers';

/**
 * The drawing open in the lightbox over the chat and its panel (illustrations R8.5): the URL
 * keeps them (`image=42&panel=layers`), so a reload or a shared link opens the same view.
 */
export function useChatImageNavigation(route: AppRoute, navigate: RouteNavigate) {
  const raw = route.query.get('image') ?? '';
  const number = /^[1-9]\d{0,8}$/u.test(raw) ? Number(raw) : 0;
  const panel: ChatImagePanel = route.query.get('panel') === 'layers' ? 'layers' : 'info';
  const baseQuery = Object.fromEntries([...route.query.entries()].filter(([key]) => key !== 'image' && key !== 'panel'));

  return {
    number,
    panel,
    open(next: number, nextPanel: ChatImagePanel = 'info') {
      navigate('/chat', { ...baseQuery, image: String(next), panel: nextPanel === 'layers' ? 'layers' : undefined }, { replace: number > 0 });
    },
    show(next: number) {
      navigate('/chat', { ...baseQuery, image: String(next), panel: panel === 'layers' ? 'layers' : undefined }, { replace: true });
    },
    setPanel(nextPanel: ChatImagePanel) {
      if (number) navigate('/chat', { ...baseQuery, image: String(number), panel: nextPanel === 'layers' ? 'layers' : undefined }, { replace: true });
    },
    close() {
      navigate('/chat', baseQuery, { replace: true });
    },
    /** «По тексту» of the lightbox: the lightbox closes and the chapter opens beside the chat. */
    openSource(source: SourceCitation) {
      const withoutSource = Object.fromEntries(Object.entries(baseQuery).filter(([key]) => key !== 'source' && key !== 'sourceMode'));
      navigate('/chat', { ...withoutSource, ...chatSourceQuery(source) }, { replace: true });
    },
  };
}

export type ChatImageNavigation = ReturnType<typeof useChatImageNavigation>;
