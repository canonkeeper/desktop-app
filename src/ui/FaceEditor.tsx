import 'prosemirror-view/style/prosemirror.css';
import { baseKeymap, chainCommands, deleteSelection, joinBackward, joinForward, selectNodeBackward, selectNodeForward, toggleMark } from 'prosemirror-commands';
import { history, redo, undo } from 'prosemirror-history';
import { keymap } from 'prosemirror-keymap';
import { Fragment, Slice, type Node } from 'prosemirror-model';
import { EditorState, Plugin, TextSelection, type Command } from 'prosemirror-state';
import { Decoration, DecorationSet, EditorView } from 'prosemirror-view';
import { forwardRef, useImperativeHandle, useLayoutEffect, useRef } from 'react';
import {
  bodyOf, docOf, faceMark, faceSchema, facesIn, hardBreak, literalText, openAfter, paragraph, plainAt, plainOf, plainSlice,
  positionAt, writtenMark, type Face,
} from '../shared/text/face-doc';
import { plain, projectedAt, sourceRange } from '../shared/text/faces';
import { registerRichField } from './rich-field';

/** What the author selected in the field: offsets in its piece of the working text, where it stands, its faces. */
export interface FaceSelection {
  start: number;
  end: number;
  rect: DOMRect | null;
  faces: Record<Face, boolean>;
}

export interface FaceEditorHandle {
  /** Puts the caret at an offset of the piece. */
  focusAt: (at: number) => void;
  /** Selects a stretch of the piece, by its offsets, and says so as the author's selection. */
  select: (start: number, end: number) => void;
  /** Turns a face on or off over the selection, as Ctrl+I or Ctrl+B does. */
  toggle: (face: Face) => void;
}

interface FaceEditorProps {
  /** The accessible name of the field. */
  label: string;
  /** The piece of the working text the field holds, its marks as signs. */
  value: string;
  /** The marks open at the start of the piece in the whole text, the outermost first. */
  open?: readonly string[];
  readOnly?: boolean;
  placeholder?: string;
  /** A stretch of the piece lit and brought into view, anew with each `at`. */
  shown?: { start: number; end: number; at: number };
  /**
   * Stretches of the piece lit quietly, nothing brought into view: what the voice assistant wrote in this conversation
   * (specs/2026-11-10-ux5-voice.md VO1.4). Presentation only — nothing of it goes into the text.
   */
  lit?: readonly { start: number; end: number }[];
  /**
   * Stretches of the piece marked by the page with data attributes, nothing brought into view: the places of the findings
   * of «Сверка», the tail «Переписать отсюда до конца» replaces (specs/2026-11-11-ux5-notes-in-text.md NT1.1, NT2.3).
   * Presentation only — nothing of it goes into the text.
   */
  spans?: readonly { start: number; end: number; attrs: Readonly<Record<string, string>> }[];
  /**
   * Words shown at places of the piece and not part of it, marked by the page with data attributes: what a correction of the
   * proofreading puts in (specs/2026-11-11-ux5-proofread-mode.md PM2.1). They cannot be edited, copied or saved.
   */
  inserts?: readonly { at: number; text: string; attrs: Readonly<Record<string, string>> }[];
  className?: string;
  /** The longest text, in signs of its plain words. */
  maxLength?: number;
  onChange: (value: string) => void;
  onBlur?: () => void;
  onSelection?: (selection: FaceSelection | null) => void;
}

const none: readonly string[] = [];
const fromProps = 'face-editor-props';

/** A line break; a second one in a row makes a blank line — the next paragraph (the tidying plugin parts them). */
const breakLine: Command = (state, dispatch) => {
  dispatch?.(state.tr.replaceSelectionWith(hardBreak.create(), true).scrollIntoView());
  return true;
};

/** Backspace at the start of a paragraph or Delete at its end takes one sign of the blank line, as in a plain field. */
function joinAsBreak(direction: -1 | 1): Command {
  return (state, dispatch) => {
    const { $from, empty } = state.selection;
    if (!empty || !$from.parent.isTextblock) return false;
    if (direction < 0 ? $from.parentOffset > 0 : $from.parentOffset < $from.parent.content.size) return false;
    const index = $from.index(0);
    if (direction < 0 ? index === 0 : index >= state.doc.childCount - 1) return false;
    const boundary = direction < 0 ? $from.before() : $from.after();
    dispatch?.(state.tr.replaceWith(boundary - 1, boundary + 1, hardBreak.create()).scrollIntoView());
    return true;
  };
}

/** Text without the mark of the model's text: what comes in by the clipboard or a drop is the author's. */
function withoutWritten(fragment: Fragment): Fragment {
  const nodes: Node[] = [];
  fragment.forEach((node) => nodes.push(node.isInline ? node.mark(writtenMark.removeFromSet(node.marks)) : node.copy(withoutWritten(node.content))));
  return Fragment.from(nodes);
}

/**
 * Keeps the document as the working text reads it: signs of marks typed or pasted become brackets (TF1.5), and two line
 * breaks in a row become a blank line between two paragraphs.
 */
const tidy = new Plugin({
  appendTransaction(_, before, after) {
    if (before.doc === after.doc) return null;
    const start = before.doc.content.findDiffStart(after.doc.content);
    if (start === null) return null;
    const end = before.doc.content.findDiffEnd(after.doc.content);
    const from = Math.max(0, start - 2);
    const to = Math.min(after.doc.content.size, Math.max(end?.b ?? after.doc.content.size, start) + 2);
    const tr = after.tr;
    after.doc.nodesBetween(from, to, (node, pos) => {
      if (node.isText && /[⟦⟧⟨⟩⟪⟫]/u.test(node.text!)) tr.replaceWith(pos, pos + node.nodeSize, faceSchema.text(literalText(node.text!), node.marks));
    });
    const pairs: number[] = [];
    tr.doc.nodesBetween(from, Math.min(tr.doc.content.size, to), (node, pos) => {
      if (node.type !== paragraph) return true;
      let previous = false;
      node.forEach((child, offset) => {
        const isBreak = child.type === hardBreak;
        // The first of the two: the break before this one, at the position before it.
        if (isBreak && previous) pairs.push(pos + 1 + offset - 1);
        previous = isBreak && !previous;
      });
      return false;
    });
    for (const at of pairs.reverse()) tr.delete(at, at + 2).split(at);
    return tr.docChanged ? tr : null;
  },
});

const plugins = [
  history(),
  keymap({
    'Mod-i': toggleMark(faceMark('italic')),
    'Mod-b': toggleMark(faceMark('bold')),
    'Mod-z': undo,
    'Shift-Mod-z': redo,
    'Mod-y': redo,
    Enter: breakLine,
    'Shift-Enter': breakLine,
    Backspace: chainCommands(deleteSelection, joinAsBreak(-1), joinBackward, selectNodeBackward),
    Delete: chainCommands(deleteSelection, joinAsBreak(1), joinForward, selectNodeForward),
  }),
  keymap(baseKeymap),
  tidy,
];

/** Where a stretch of the document stands on screen: the place a menu of the selection goes to. */
function rectOf(view: EditorView, from: number, to: number): DOMRect | null {
  try {
    const start = view.domAtPos(from);
    const end = view.domAtPos(to);
    const range = document.createRange();
    range.setStart(start.node, start.offset);
    range.setEnd(end.node, end.offset);
    return typeof range.getBoundingClientRect === 'function' ? range.getBoundingClientRect() : null;
  } catch {
    return null;
  }
}

/**
 * The field of a piece of the working text with its faces (specs/2026-11-08-italic-bold-in-text.md TF6): a ProseMirror
 * editor whose document is the piece read by `docOf` and written back by `bodyOf`, so the page keeps its offsets in the
 * working text and everything that works by them goes on as with a plain field. Ctrl/Cmd+I and Ctrl/Cmd+B turn a face
 * on and off, undo is the editor's history; the clipboard brings only italic and bold and takes the plain words with
 * them. The model's text is the mark `written`, lit by the page; a shown place and the placeholder are decorations.
 */
export const FaceEditor = forwardRef<FaceEditorHandle, FaceEditorProps>(function FaceEditor(props, ref) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<EditorView | null>(null);
  const latest = useRef(props);
  latest.current = props;
  const open = props.open ?? none;
  const openKey = open.join('');
  // The piece the document stands for now, with the marks around it: what offsets are counted in.
  const synced = useRef({ value: props.value, openKey, open, end: openAfter(props.value, open) });
  // A hand selecting with the mouse or a finger: its selection is said when it lets go, not at every step of the drag.
  const pressing = useRef(false);

  function report() {
    const view = editor.current;
    const onSelection = latest.current.onSelection;
    if (!view || !onSelection) return;
    const { doc, selection } = view.state;
    const { from, to } = selection;
    if (selection.empty || !doc.textBetween(from, to, '\n\n', '\n').trim()) {
      onSelection(null);
      return;
    }
    const [start, end] = sourceRange(plain(synced.current.value), plainAt(doc, from), plainAt(doc, to));
    onSelection({ start, end, rect: rectOf(view, from, to), faces: facesIn(doc, from, to, null) });
  }

  function decorations(state: EditorState) {
    const { lit, placeholder, shown, spans } = latest.current;
    const items: Decoration[] = [];
    const { doc } = state;
    for (const stretch of [...(lit ?? []).map((each) => ({ ...each, attrs: { 'data-voice': '' } })), ...(spans ?? [])]) {
      const view = plain(synced.current.value);
      const from = positionAt(doc, projectedAt(view, stretch.start));
      const to = positionAt(doc, projectedAt(view, stretch.end));
      if (to > from) items.push(Decoration.inline(from, to, { ...stretch.attrs }));
    }
    for (const insert of latest.current.inserts ?? []) {
      const view = plain(synced.current.value);
      const at = positionAt(doc, projectedAt(view, insert.at));
      const key = `insert:${insert.at}:${insert.text}:${Object.entries(insert.attrs).join(';')}`;
      items.push(Decoration.widget(at, () => {
        const shown = document.createElement('ins');
        shown.textContent = insert.text;
        shown.contentEditable = 'false';
        for (const [name, value] of Object.entries(insert.attrs)) shown.setAttribute(name, value);
        return shown;
      }, { side: -1, key, ignoreSelection: true }));
    }
    if (shown && shown.end > shown.start) {
      const view = plain(synced.current.value);
      const from = positionAt(doc, projectedAt(view, shown.start));
      const to = positionAt(doc, projectedAt(view, shown.end));
      if (to > from) items.push(Decoration.inline(from, to, { 'data-shown': '' }));
    }
    if (placeholder && doc.childCount === 1 && doc.firstChild!.childCount === 0) {
      items.push(Decoration.node(0, doc.firstChild!.nodeSize, { 'data-placeholder': placeholder }));
    }
    return items.length ? DecorationSet.create(doc, items) : DecorationSet.empty;
  }

  function attributes(): Record<string, string> {
    const { className, label, placeholder, readOnly } = latest.current;
    return {
      role: 'textbox',
      'aria-multiline': 'true',
      'aria-label': label,
      'aria-readonly': readOnly ? 'true' : 'false',
      'data-voice-authored': '',
      spellcheck: 'true',
      // Reached by Tab like a field also when it cannot be typed in, and named focusable by checkers of a scrolled column.
      tabindex: '0',
      ...(className ? { class: className } : {}),
      ...(placeholder ? { 'aria-placeholder': placeholder } : {}),
    };
  }

  /** The field takes the voice's words as its text: only what differs is replaced, the faces around it stay (TF6.5). */
  function fill(text: string) {
    const view = editor.current;
    if (!view || latest.current.readOnly) return false;
    const { doc } = view.state;
    const current = plainOf(doc);
    const wanted = literalText(text);
    let start = 0;
    while (start < current.length && start < wanted.length && current[start] === wanted[start]) start += 1;
    let same = 0;
    while (same < current.length - start && same < wanted.length - start && current[current.length - 1 - same] === wanted[wanted.length - 1 - same]) same += 1;
    let end = current.length - same;
    let tail = wanted.length - same;
    // An end in the middle of a blank line takes the whole blank line: a position there stands between paragraphs.
    if (!doc.resolve(positionAt(doc, start)).parent.inlineContent) start -= 1;
    if (!doc.resolve(positionAt(doc, end)).parent.inlineContent) {
      end += 1;
      tail += 1;
    }
    const from = positionAt(doc, start);
    const to = positionAt(doc, end);
    const words = wanted.slice(start, tail);
    const marks = doc.resolve(from).marks().filter((mark) => mark.type !== writtenMark);
    const tr = view.state.tr;
    if (words) tr.replace(from, to, plainSlice(words, marks));
    else tr.delete(from, to);
    view.dispatch(tr);
    return true;
  }

  /** Selects a stretch of the plain words, as the author's selection. */
  function selectPlain(start: number, end: number) {
    const view = editor.current;
    if (!view) return;
    const { doc } = view.state;
    // The selection is said by the transaction itself, as one made by the keyboard.
    view.dispatch(view.state.tr.setSelection(TextSelection.between(doc.resolve(positionAt(doc, start)), doc.resolve(positionAt(doc, end)))));
    view.focus();
  }

  useLayoutEffect(() => {
    const view = new EditorView(host.current!, {
      state: EditorState.create({ doc: docOf(props.value, open), plugins: [...plugins, new Plugin({
        // Nothing grows the text past its longest: a change that would is not made, one that shortens it always is.
        filterTransaction: (tr) => !tr.docChanged || tr.doc.content.size - 2 <= (latest.current.maxLength ?? Infinity) || tr.doc.content.size <= tr.before.content.size,
      })] }),
      dispatchTransaction(tr) {
        const before = view.state;
        const next = before.apply(tr);
        view.updateState(next);
        if (next.doc !== before.doc && !tr.getMeta(fromProps)) {
          const value = bodyOf(next.doc, synced.current.open, synced.current.end);
          synced.current = { ...synced.current, value };
          latest.current.onChange(value);
        }
        if (tr.selectionSet && !tr.getMeta(fromProps)) {
          if (next.selection.empty) latest.current.onSelection?.(null);
          // A selection by the keyboard (Shift with arrows, select all) is said as soon as the editor reads it.
          else if (next.doc === before.doc && !pressing.current) report();
        }
      },
      editable: () => !latest.current.readOnly,
      attributes,
      decorations,
      clipboardTextParser: (text, $context) => plainSlice(text, $context.marks().filter((mark) => mark.type !== writtenMark)),
      transformPasted: (slice) => new Slice(withoutWritten(slice.content), slice.openStart, slice.openEnd),
      clipboardTextSerializer: (slice) => slice.content.textBetween(0, slice.content.size, '\n\n', '\n'),
      handleDOMEvents: {
        mousedown: () => {
          pressing.current = true;
          // The hand may let go outside the field.
          document.addEventListener('mouseup', () => {
            pressing.current = false;
            setTimeout(report, 0);
          }, { once: true });
          return false;
        },
        // A selection that ends here without a press seen in the field — made by a script, or the same one again.
        mouseup: () => {
          if (!pressing.current) setTimeout(report, 0);
          return false;
        },
        touchstart: () => {
          pressing.current = true;
          return false;
        },
        touchend: () => {
          pressing.current = false;
          setTimeout(report, 0);
          return false;
        },
        blur: () => {
          latest.current.onBlur?.();
          return false;
        },
      },
    });
    editor.current = view;
    const forget = registerRichField(view.dom, {
      text: () => plainOf(view.state.doc),
      fill,
      select: (start, end) => selectPlain(start, end),
    });
    return () => {
      forget();
      view.destroy();
      editor.current = null;
    };
    // The view lives as long as the field; the props reach it through `latest` and the effects below.
  }, []);

  // A piece that came otherwise than the author typed it — from the server, a new layout — becomes the document anew;
  // the caret stays where it stood in the words.
  useLayoutEffect(() => {
    const view = editor.current;
    if (!view || (props.value === synced.current.value && openKey === synced.current.openKey)) return;
    synced.current = { value: props.value, openKey, open, end: openAfter(props.value, open) };
    const doc = docOf(props.value, open);
    if (doc.eq(view.state.doc)) return;
    const { from, to } = view.state.selection;
    const anchor = plainAt(view.state.doc, from);
    const head = plainAt(view.state.doc, to);
    const tr = view.state.tr.replaceWith(0, view.state.doc.content.size, doc.content);
    tr.setSelection(TextSelection.between(tr.doc.resolve(positionAt(tr.doc, anchor)), tr.doc.resolve(positionAt(tr.doc, head))));
    view.dispatch(tr.setMeta('addToHistory', false).setMeta(fromProps, true));
  }, [props.value, openKey]);

  // Whether it can be typed in, its name and what it lights follow the props.
  useLayoutEffect(() => {
    editor.current?.setProps({ attributes, decorations, editable: () => !latest.current.readOnly });
  }, [props.readOnly, props.label, props.placeholder, props.className, props.shown?.start, props.shown?.end, props.shown?.at,
    props.lit?.map((stretch) => `${stretch.start}-${stretch.end}`).join(','),
    props.spans?.map((stretch) => `${stretch.start}-${stretch.end}:${Object.entries(stretch.attrs).join('=')}`).join(','),
    props.inserts?.map((insert) => `${insert.at}:${insert.text}:${Object.entries(insert.attrs).join('=')}`).join(',')]);

  // A shown place comes into view, anew with each request.
  useLayoutEffect(() => {
    if (!props.shown) return;
    editor.current?.dom.querySelector<HTMLElement>('[data-shown]')?.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
  }, [props.shown?.at]);

  useImperativeHandle(ref, () => ({
    focusAt(at: number) {
      const view = editor.current;
      if (!view) return;
      const pos = positionAt(view.state.doc, projectedAt(plain(synced.current.value), at));
      view.dispatch(view.state.tr.setSelection(TextSelection.near(view.state.doc.resolve(pos))));
      view.focus();
    },
    select(start: number, end: number) {
      const words = plain(synced.current.value);
      selectPlain(projectedAt(words, start), projectedAt(words, end));
    },
    toggle(face: Face) {
      const view = editor.current;
      if (!view || latest.current.readOnly) return;
      toggleMark(faceMark(face))(view.state, view.dispatch);
      view.focus();
      report();
    },
  }));

  return <div ref={host} />;
});
