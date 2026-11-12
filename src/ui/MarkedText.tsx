import type { ReactNode } from 'react';
import type { Mark, Node } from 'prosemirror-model';
import { docOf, faceMark, writtenMark } from '../shared/text/face-doc';

function wrapped(text: string, marks: readonly Mark[], key: number): ReactNode {
  let node: ReactNode = text;
  if (faceMark('bold').isInSet(marks)) node = <strong>{node}</strong>;
  if (faceMark('italic').isInSet(marks)) node = <em>{node}</em>;
  return writtenMark.isInSet(marks) ? <span data-written="" key={key}>{node}</span> : <span key={key}>{node}</span>;
}

/**
 * A piece of a working text shown as it reads (specs/2026-11-08-italic-bold-in-text.md TF6.4): its plain words with their
 * italic and bold, and what the model wrote marked `data-written` for the page to light. It is read by the same `docOf`
 * as the editor, and its text, node by node, is the plain text of the piece: offsets of a selection in it are offsets of
 * `plain`.
 */
export function MarkedText({ open, text }: { open?: readonly string[]; text: string }) {
  const doc: Node = docOf(text, open);
  const parts: ReactNode[] = [];
  doc.forEach((paragraph, _, index) => {
    if (index > 0) parts.push('\n\n');
    paragraph.forEach((child) => parts.push(wrapped(child.isText ? child.text! : '\n', child.marks, parts.length)));
  });
  return <>{parts}</>;
}
