import type { Element, ElementContent, Root } from 'hast';
import { useMemo, useRef, type ReactNode } from 'react';
import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { attachmentNumber, parseAttachmentCitation, parseSourceCitation, type SourceCitation } from '../shared/navigation/chat-source';
import { missingPlace, paragraphsLabel, placeLabel, withoutDeviceWords, type Places } from '../shared/text/place';
import styles from './MarkdownContent.module.css';

const openingPunctuation = /[([«„“‘"']+$/u;
const closingPunctuation = /^[.,;:!?…)\]»”’"']+/u;

/**
 * Keeps a source citation on one line with the punctuation that touches it, as text keeps a word
 * with its punctuation: a citation renders as a button, and a line may break on either side of a
 * button, which would leave «.» or «).» at the start of the next line.
 */
function rehypeCitationPunctuation() {
  return (tree: Root) => glue(tree);
}

function glue(parent: Root | Element) {
  const children = parent.children;
  for (let index = 0; index < children.length; index += 1) {
    const node = children[index]!;
    if (node.type !== 'element') continue;
    if (node.tagName !== 'a' || !citationLink(String(node.properties.href ?? ''))) {
      glue(node);
      continue;
    }
    const before = children[index - 1];
    const after = children[index + 1];
    const opening = before?.type === 'text' ? openingPunctuation.exec(before.value)?.[0] ?? '' : '';
    const closing = after?.type === 'text' ? closingPunctuation.exec(after.value)?.[0] ?? '' : '';
    if (!opening && !closing) continue;
    const group: ElementContent[] = [node];
    if (opening && before?.type === 'text') {
      before.value = before.value.slice(0, -opening.length);
      group.unshift({ type: 'text', value: opening });
    }
    if (closing && after?.type === 'text') {
      after.value = after.value.slice(closing.length);
      group.push({ type: 'text', value: closing });
    }
    children[index] = { type: 'element', tagName: 'span', properties: { className: [styles.citation ?? ''] }, children: group };
  }
}

/** A citation of the chat: a chapter of the book or a place of the chapter attached to the chat. */
function citationLink(href: string) {
  return href.startsWith('source:') || href.startsWith('attachment:');
}

/** The source a citation names; an attachment of the chat is named `aN` (specs/2026-11-07-chat-attach-chapter.md CA4.4). */
function citationOf(href: string) {
  return href.startsWith('attachment:') ? parseAttachmentCitation(href) : parseSourceCitation(href);
}

function textOf(node: Element | ElementContent): string {
  if (node.type === 'text') return node.value;
  return node.type === 'element' ? node.children.map(textOf).join('') : '';
}

/** The address as a writer reads it: letters decoded, unless that fails or would show invisible or control characters. */
function readableAddress(href: string) {
  try {
    const decoded = decodeURI(href);
    return /[\p{Cc}\p{Cf}]/u.test(decoded) ? href : decoded;
  } catch {
    return href;
  }
}

/**
 * A link in text the model wrote is its label and, unless the label already shows it, its address
 * as plain text: the agent has no network, so such an address comes from a book, a guess or an
 * injected instruction, and following it is never needed (SEC-16, security-hardening UC3). The
 * address is its own element, so noted text taken before it was shown is still found
 * (ChatNoteLanding.quoteRanges).
 */
function addressAsText(node: Element | undefined, href: string | undefined, label: ReactNode) {
  const address = readableAddress(href ?? '');
  const shown = address.replace(/^[a-z][a-z\d+.-]*:(?:\/\/)?/i, '').replace(/\/$/, '');
  if (!shown || (node && textOf(node).includes(shown))) return <>{label}</>;
  return <>{label}<span data-link-address=""> ({address})</span></>;
}

/**
 * The words of a citation as the author reads them (specs/2026-11-10-ux1-names.md N2.3): the place the server named
 * for its chapter with the paragraphs in words — «Книга 2, гл. 12 „Белый порт“, абзац 3». Until the server has named
 * it (a remark being written, a chapter the text lost) the words of the model stand, without «¶» and keys. A place of
 * the chapter attached to the chat is «ваша глава, абзац 2».
 */
function citationWords(source: SourceCitation, written: string, places: Places | undefined) {
  if (attachmentNumber(source) !== null) return ['ваша глава', paragraphsLabel(source.from, source.to)].filter(Boolean).join(', ');
  const place = places?.[source.chapterKey];
  return place ? placeLabel(place, source.from, source.to) : withoutDeviceWords(written).trim() || missingPlace;
}

/** Where a `drawing:` link of the chat leads: the form of a new drawing, or the drawing of version K. */
export type WorkshopLinkTarget = 'new' | number;

/** Parses `drawing:new` and `drawing:42` of a chat answer. */
export function parseWorkshopLink(href: string | undefined): WorkshopLinkTarget | null {
  if (href === 'drawing:new') return 'new';
  const match = href ? /^drawing:([1-9]\d{0,8})$/u.exec(href) : null;
  return match ? Number(match[1]) : null;
}

/** An embedded image of an answer, `![подпись](image:42)`: its address and its caption. */
export interface MarkdownImage {
  src: string;
  alt: string;
}

/** A paragraph that holds an embedded image the host draws as a block. */
function holdsImage(node: Element | undefined) {
  return Boolean(node?.children.some((child) => child.type === 'element' && child.tagName === 'img'
    && String(child.properties.src ?? '').startsWith('image:')));
}

interface MarkdownContentProps {
  children: string;
  className?: string;
  renderSourceLink?: (source: SourceCitation, label: ReactNode) => ReactNode;
  /** Draws an `image:` address of the text (a drawing of the saga); without it the caption stays as text. */
  renderImage?: (image: MarkdownImage) => ReactNode;
  /**
   * Draws a `drawing:new` or `drawing:K` link of the chat into the drawing workshop (specs/2026-11-02-image-workshop.md
   * IW7.3); without it the label stays as text. `scene` is the title of the link — the scene the helper described,
   * `[Нарисовать в «Рисунках»](drawing:new "Мара на причале…")` (specs/2026-11-11-ux9-shelf-chat.md SC3.4).
   */
  renderWorkshopLink?: (target: WorkshopLinkTarget, label: ReactNode, scene?: string) => ReactNode;
  /** The product's own text (legal documents): its links stay links. Elsewhere only source citations are. */
  ownLinks?: boolean;
  /** The places of the chapters the citations of the text name, as the server wrote them beside the keys (N2.3). */
  places?: Places;
}

export function MarkdownContent({ children, className, places, renderImage, renderSourceLink, renderWorkshopLink, ownLinks = false }: MarkdownContentProps) {
  const sourceRenderer = useRef(renderSourceLink);
  sourceRenderer.current = renderSourceLink;
  const placesRef = useRef(places);
  placesRef.current = places;
  const imageRenderer = useRef(renderImage);
  imageRenderer.current = renderImage;
  const workshopRenderer = useRef(renderWorkshopLink);
  workshopRenderer.current = renderWorkshopLink;
  // Stable component identities keep the clicked source link mounted across deltas and navigation.
  const components = useMemo<NonNullable<React.ComponentProps<typeof ReactMarkdown>['components']>>(() => ({
    a: ({ node, href, id, title, children: label }) => {
      // A footnote and its way back are links inside the page, not a way out of it.
      if (ownLinks || href?.startsWith('#')) return <a href={href} id={id}>{label}</a>;
      const workshop = parseWorkshopLink(href);
      const scene = title?.trim() || undefined;
      if (workshop !== null) return workshopRenderer.current ? <>{workshopRenderer.current(workshop, label, scene)}</> : <>{label}</>;
      if (!href || !citationLink(href)) return addressAsText(node, href, label);
      const source = citationOf(href);
      const words = source ? citationWords(source, node ? textOf(node) : '', placesRef.current) : label;
      return source && sourceRenderer.current
        ? sourceRenderer.current(source, words)
        : <span title="Место недоступно">{source ? words : withoutDeviceWords(node ? textOf(node) : '') || label}</span>;
    },
    img: ({ alt, src }) => typeof src === 'string' && src.startsWith('image:') && imageRenderer.current
      ? <>{imageRenderer.current({ src, alt: alt ?? '' })}</>
      : <>{alt}</>,
    // A drawing is a block: the paragraph around it is not a <p>, which may not hold one.
    p: ({ node, children: content }) => holdsImage(node) && imageRenderer.current ? <div className={styles.imageParagraph}>{content}</div> : <p>{content}</p>,
    table: ({ children: tableChildren }) => <div className={styles.tableScroll}><table>{tableChildren}</table></div>,
  }), [ownLinks]);
  return (
    <div className={className}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeCitationPunctuation]}
        urlTransform={(href) => citationLink(href) ? citationOf(href) ? href : 'source:unavailable'
          : /^image:[1-9]\d{0,8}$/u.test(href) || parseWorkshopLink(href) !== null ? href : defaultUrlTransform(href)}
        components={components}
      >
        {children}
      </ReactMarkdown>
    </div>
  );
}
