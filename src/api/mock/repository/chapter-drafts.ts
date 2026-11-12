import type { CanonKeeperApiClient, ChapterFileInput, ListChapterDraftsOptions, OperationRequest, ProofreadChapterInput, RequiredIdempotencyOptions, Schema } from '../../../shared/api';
import { book, faceless, fromBook, markdown, markdownPlain, plain, valid } from '../../../shared/text/faces';
import { MockApiError } from '../scenarios';
import type { createChatOperations } from './chat';
import type { createJobOperations } from './jobs';
import { advanceAgentRun, helperPlan, planQuestions, startAgentRun, startSeams, wordsOf, writtenSpansOf } from './chapter-draft-agent';
import { approvePlan, authorPlan, choicesOf, plansOf, setChoices, withdrawTail } from './chapter-draft-plans';
import { advanceProofread, attachmentOf, batchCategories, keepOriginal, mockRevisions, originalOf, readChapterFile, tieToAttachment } from './chapter-draft-proofread';
import { keepVersion, listVersions, takenText, versionOf } from './chapter-draft-versions';
import { placedBook } from './books';
import { clone, createJob, rejectUnknownKeys } from './common';
import type { MockRepositoryContext } from './context';
import { momentTitle } from './moment-sections';
import { chapterKey, chapterPlace, chapterRefOf } from './places';
import type { MockState } from './state';
import { mockChargeNewChapter, mockDraftChapterCounts, mockSettleChapter } from './tiers';

type ChapterDraftOperations = Pick<CanonKeeperApiClient,
  'listChapterDrafts' | 'createChapterDraft' | 'getChapterDraft' | 'getChapterDraftPlan' | 'getChapterDraftMoment' | 'updateChapterDraft' | 'deleteChapterDraft' | 'startChapterDraftJob' | 'addChapterDraftToBook'
  | 'applyChapterDraftNote' | 'updateChapterDraftNote' | 'submitDraftTurn' | 'cancelJob' | 'getJob'
  | 'openChapterEdit' | 'replaceChapterFromDraft' | 'revertChapterFromDraft' | 'reopenChapterFromBook'
  | 'listChapterDraftVersions' | 'getChapterDraftVersion' | 'takeChapterDraftVersion' | 'updateChapterDraftPlan' | 'withdrawChapterDraftPlan' | 'updateChapterDraftMoment'
  | 'startChapterProofread' | 'loadChapterDraftFile' | 'takeChapterDraftOriginalLook' | 'applyChapterDraftNotes' | 'getChapterDraftExport'>;
type Draft = Schema<'ChapterDraft'>;
type Note = Schema<'ChapterDraftNote'>;
type WriteOperation = 'updateChapterDraft' | 'deleteChapterDraft' | 'startChapterDraftJob' | 'addChapterDraftToBook' | 'applyChapterDraftNote' | 'updateChapterDraftNote'
  | 'submitDraftTurn' | 'replaceChapterFromDraft' | 'revertChapterFromDraft' | 'reopenChapterFromBook' | 'takeChapterDraftVersion'
  | 'updateChapterDraftPlan' | 'withdrawChapterDraftPlan' | 'updateChapterDraftMoment' | 'loadChapterDraftFile' | 'takeChapterDraftOriginalLook' | 'applyChapterDraftNotes';

const writers = ['owner', 'admin', 'editor'] as const;

/** The words of a working text as the list of the server counts them: by the rule of the head of the chapter, runs of
 * letters, marks and digits of the text without the marks of faces (specs/2026-11-12-ux12-writing.md WR2.1). */
const listedWords = (text: string) => faceless(text).text.match(/[\p{L}\p{M}\p{N}]+/gu)?.length ?? 0;

/** A Job as the list and a reader of the saga read it: without the scene in writing (ux10-scene-stream ST1.4). */
function withoutWriting<T extends { writing?: unknown }>(job: T): T {
  const { writing: _writing, ...rest } = job;
  return rest as T;
}
const activeStatuses = new Set(['queued', 'running', 'canceling']);
/** The usual length of a chapter of the mock saga at any place. */
const mockTypicalWords = 2800;
/** The kinds of the findings of a check: a new check replaces the open ones of them (WA6.7). */
const checkKinds = new Set<Note['kind']>(['canon', 'new', 'style', 'craft', 'seam', 'plan_follow']);

/** The marks of what the author did not write and of his italic and bold make a tree (W2.3, specs/2026-11-08-italic-bold-in-text.md TF1.1). */
const validMarks = valid;
/** The chapter of the book an edit stands on (A8.7): its revision then and the text before the last replacement. */
const edits = new WeakMap<Draft, { revision: number; previous?: { paragraphs: string[] } }>();
/** The last number a note of a draft took: a number is never given twice. */
const lastNumbers = new WeakMap<Draft, number>();
/** What stood right around a place the helper replaced: its note finds it again by them (P5.6). */
const replacedAnchors = new WeakMap<Note, { before: string; after: string }>();
const replacementOfTheMock = `Капитан встретил её у склада.\n\n${'Они долго шли вдоль причала и молчали.\n\n'.repeat(40)}― Ключ у тебя, ― сказала Кира.`;

function stageOf(draft: Draft): Draft['stage'] {
  return draft.added ? 'added' : 'text';
}

/** The case of the chapter by the author's words, the server's thresholds (specs/2026-11-06-writing-agent.md, WA1). */
function caseByWords(draft: Draft): Draft['case'] {
  const words = draft.text.replace(/⟦[^⟧]*⟧/g, ' ').split(/[^\p{L}\p{M}\p{N}]+/u).filter(Boolean).length;
  const typical = draft.typicalWords ?? 2500;
  if (words < Math.max(150, 0.1 * typical)) return 'scratch';
  return words >= 0.8 * typical ? 'nearly_done' : 'continue';
}

/**
 * The case of the chapter: the author's own choice wins (WA1.2); an edit of a chapter of the book is nearly done at any
 * length (specs/2026-11-11-ux9-writing-a.md WA4.1).
 */
function caseOf(draft: Draft): Draft['case'] {
  if (draft.edits && !draft.edits.gone) return 'nearly_done';
  return draft.caseChoice ?? caseByWords(draft);
}

/**
 * The name of a draft without a title, by the rule of the server (sagadraft.Opening, WA1.1): the first words of the idea,
 * else of the plain text, up to six words, 48 signs or the end of the first sentence; an ellipsis when more follows.
 */
export function mockOpening(idea: string, text: string) {
  let source = idea.split(/\s+/u).filter(Boolean);
  if (!source.length) source = plain(text).text.split(/\s+/u).filter(Boolean);
  while (source.length && !source[0]!.replace(/[—–\-«»"]/gu, '')) source = source.slice(1);
  if (!source.length) return '';
  const kept: string[] = [];
  let signs = 0;
  for (const word of source) {
    const size = [...word].length;
    if (kept.length === 6 || (kept.length > 0 && signs + 1 + size > 48)) break;
    kept.push(word);
    signs += 1 + size;
    if (/[.!?…]$/u.test(word)) break;
  }
  const joined = kept.join(' ').replace(/^[—–\-«" ]+/u, '');
  const opening = [...joined].slice(0, 48).join('').replace(/[ .,;:!?…—–\-»"]+$/u, '');
  return kept.length < source.length || [...kept.join(' ')].length > 48 ? `${opening}…` : opening;
}

/**
 * The scenes of a draft are a new chapter of the writer's tier (specs/2026-11-08-subscription-tiers.md TR7.1): it edits
 * no chapter of the book and is not nearly done by the author's own words — not by the case he chose
 * (specs/2026-11-09-quota-accounting.md Q3.3). The start of the scenes and what a draft says of itself read this one.
 */
function newChapterDraft(draft: Draft) {
  return !draft.edits && caseByWords(draft) !== 'nearly_done';
}

/** The kinds of the findings of «Сверка» (WS7.1), apart from the corrections of the proofreading. */
const findingKinds: ReadonlySet<string> = new Set(['canon', 'new', 'style', 'craft', 'seam', 'plan_follow', 'meaning', 'repeat', 'machine']);

/** The plan of the draft is stale once the idea changed since its latest version was made (WA4). */
const planIdeas = new WeakMap<Draft, string>();

/**
 * A draft as every operation answers it: with whether its first scenes would count as a new chapter of the tier, until
 * the chapter is in the book (specs/2026-11-10-ux1-writing.md UW1.1).
 */
function answered(draft: Draft, state: MockState): Draft {
  const plan = draft.plan ? { ...draft.plan, stale: planIdeas.get(draft) !== undefined && planIdeas.get(draft) !== draft.idea } : undefined;
  const opening = mockOpening(draft.idea, draft.text);
  return clone({
    ...draft, case: caseOf(draft), ...(plan ? { plan } : {}), ...(opening ? { opening } : {}),
    ...(draft.added ? {} : { newChapter: newChapterDraft(draft) && !mockDraftChapterCounts(state, draft.id) }),
    // The revisions of the file with corrections now (specs/2026-11-11-ux5-proofread-mode.md PM1.4).
    ...(draft.original ? { original: { ...draft.original, revisions: mockRevisions(draft) } } : {}),
  });
}

// helperKey keys the session of the helper the first message opens, apart from the key of the message.
function helperKey(key: string) {
  return `draft-helper-${key}`;
}

/** The chapters in writing of the mock saga; every project starts with none. */
export function createChapterDraftOperations(
  context: MockRepositoryContext, chat: ReturnType<typeof createChatOperations>, jobs: Pick<ReturnType<typeof createJobOperations>, 'cancelJob' | 'getJob'>,
) {
  const drafts = new Map<string, Draft[]>();
  let nextId = 1;
  // Whether the book holds an edit is read with every answer: the text of the draft changes by every save (WA4.1).
  const shown = (draft: Draft) => answered(sameOf(draft), context.state);
  // A new scenario of the mock starts with a clean saga, so with no drafts of it either.
  let draftsOwner = context.state;
  const ofProject = (projectId: string) => {
    if (draftsOwner !== context.state) {
      draftsOwner = context.state;
      drafts.clear();
    }
    return drafts.get(projectId) ?? [];
  };
  const found = (projectId: string, draftId: string, operationId: WriteOperation | 'getChapterDraft' | 'getChapterDraftPlan' | 'getChapterDraftMoment'
    | 'listChapterDraftVersions' | 'getChapterDraftVersion' | 'getChapterDraftExport') => {
    const draft = ofProject(projectId).find((item) => item.id === draftId);
    if (!draft) throw new MockApiError(404, 'Draft not found.', operationId);
    return draft;
  };
  const placeOf = (bookId?: string, afterChapterId?: string): Draft['place'] => {
    if (!bookId) return {};
    const book = context.state.books.find((item) => item.id === bookId);
    const chapter = context.state.chapters.find((item) => item.id === afterChapterId);
    return {
      bookId, ...(book ? { bookTitle: book.title } : {}),
      ...(afterChapterId ? { afterChapterId, ...(chapter ? { afterChapterTitle: chapter.title } : {}) } : {}),
    };
  };
  const open = (draft: Draft, revision: number, operationId: WriteOperation) => {
    if (draft.added) throw new MockApiError(409, 'Already in the book.', operationId, 'chapter_draft.added');
    if (draft.job && activeStatuses.has(draft.job.status)) throw new MockApiError(409, 'A run is working.', operationId, 'chapter_draft.busy');
    if (revision !== draft.revision) throw new MockApiError(409, 'Stale revision.', operationId, 'conflict');
  };
  const numbered = (draft: Draft, note: Omit<Note, 'id' | 'number' | 'status' | 'createdAt' | 'updatedAt'>): Note => {
    const number = (lastNumbers.get(draft) ?? 0) + 1;
    lastNumbers.set(draft, number);
    const at = new Date().toISOString();
    return { id: `${draft.id}-note-${number}`, number, status: 'open', createdAt: at, updatedAt: at, ...note };
  };
  /** A chapter without a title takes the one the helper proposed with the plan (WA3.5): a title the author gave stays. */
  const named = (draft: Draft, plan: ReturnType<typeof helperPlan>) => {
    if (!plan?.understanding.title || draft.title.trim()) return;
    draft.title = plan.understanding.title;
    draft.revision += 1;
  };
  /** A new version of the plan remembers the idea it was made for (stale once it changes). */
  const planned = (draft: Draft) => planIdeas.set(draft, draft.idea);

  /** One step of the Job of a draft on each read, as workers would; a check replaces the open findings of its kinds. */
  const advance = (draft: Draft) => {
    const job = draft.job;
    if (!job || !activeStatuses.has(job.status)) return;
    // A proofreading replaces the open corrections of the one before when it ends (specs/2026-11-07-writing-proofread.md, PR3.8).
    const proofread = advanceProofread(draft);
    if (proofread !== null) {
      if (job.status === 'succeeded') {
        draft.notes = [...draft.notes.filter((note) => !(note.kind === 'proofread' && note.status === 'open')), ...proofread.map((note) => numbered(draft, note))];
      }
      return;
    }
    const added = advanceAgentRun(draft);
    mockSettleChapter(context.state, job);
    if (!added.length) return;
    const checking = added.some((note) => checkKinds.has(note.kind) && note.kind !== 'new') || job.mode === 'check';
    draft.notes = [
      ...draft.notes.filter((note) => !(checking && note.origin === 'job' && note.status === 'open' && checkKinds.has(note.kind))),
      ...added.map((note) => numbered(draft, note)),
    ];
  };

  /**
   * What the mock helper writes with draft.notes (specs/2026-11-02-writer-workshop.md, W6.2): a divergence from the
   * canon at an exact quote, with a short replacement. It is no operation of the API.
   */
  async function helperNoteDraft(projectId: string, draftId: string, body: { place: string; body: string; replacement?: string }) {
    const draft = found(projectId, draftId, 'getChapterDraft');
    const note = numbered(draft, {
      kind: 'canon', target: 'draft', origin: 'helper', quote: body.place, body: body.body, level: 'warning',
      ...(body.replacement ? { replacement: body.replacement } : {}), refs: ['b1c1/2'],
    });
    draft.notes.push(note);
    return clone(note);
  }

  /** What the mock helper writes with draft.plan for «Написать главу» (WA3.7). It is no operation of the API. */
  async function helperPlanDraft(projectId: string, draftId: string) {
    const draft = found(projectId, draftId, 'getChapterDraft');
    draft.case = caseOf(draft);
    const plan = helperPlan(draft, 'Написать главу', undefined, new Map());
    named(draft, plan);
    planned(draft);
    draft.plan = { number: plan!.number, approved: false, stale: false, reason: plan!.reason };
    return plan!;
  }

  /**
   * What the mock helper writes in a turn (WS12.1): a plan and its questions by the words of the button that opened
   * it; a place the author asked to replace in his own words (P5.6). The turn itself goes the way the mock chat's go.
   */
  const helperPass = (draft: Draft, words: string, turnId: string, place: string | undefined, button: boolean) => {
    const before = plansOf(draft).at(-1)?.number ?? 0;
    const spans = before ? writtenSpansOf(draft, before) : new Map<number, { from: string; to: string }>();
    draft.case = caseOf(draft);
    const plan = helperPlan(draft, words, place, spans);
    if (plan) {
      named(draft, plan);
      planned(draft);
      // A new version takes the approval away (WA4.2).
      draft.plan = { number: plan.number, approved: false, stale: false, reason: plan.reason };
      if (/^(Написать главу|Дописать главу)/u.test(words.trim()) && !draft.notes.some((note) => note.kind === 'question' && note.origin === 'helper')) {
        draft.notes = [...draft.notes, ...planQuestions().map((note) => numbered(draft, { ...note, origin: 'helper', turnId }))];
      }
    }
    const asked = words.toLowerCase();
    const taken = asked.includes('убери');
    if (button || !(asked.includes('замени') || taken)) return;
    const scene = place && draft.text.includes(place) ? place : draft.text.split('\n').map((line) => line.trim()).find(Boolean) ?? '';
    const at = scene ? draft.text.indexOf(scene) : -1;
    if (at < 0) return;
    const head = draft.text.slice(0, at);
    const tail = draft.text.slice(at + scene.length);
    const inside = head.lastIndexOf('⟦') > head.lastIndexOf('⟧');
    const put = taken ? '' : inside ? replacementOfTheMock : `⟦${replacementOfTheMock}⟧`;
    draft.text = `${head}${put}${tail}`;
    draft.revision += 1;
    draft.updatedAt = new Date().toISOString();
    const note = numbered(draft, {
      kind: 'replaced', target: 'draft', origin: 'helper', ...(put ? { quote: put } : {}),
      body: scene.split(/\s+/u).slice(0, 6).join(' '), replacement: scene, refs: [], turnId,
    });
    replacedAnchors.set(note, { before: head.slice(-120), after: tail.slice(0, 120) });
    draft.notes = [...draft.notes, note];
  };

  /** The chapter of the book a draft edits, if it is still there (A8.7). */
  const editedChapter = (draft: Draft) => context.state.chapters.find((item) => item.id === draft.edits?.chapterId);
  const paragraphsOf = (chapter: { paragraphs: { text: string }[] }) => chapter.paragraphs.map((paragraph) => paragraph.text.trim()).filter(Boolean);
  /**
   * The paragraphs of a chapter of the book in the Markdown subset with their italic and bold, as «Править главу» takes
   * them (specs/2026-11-08-italic-bold-in-text.md TF3.2); a heading of the mock saga is its text.
   */
  const markdownOf = (chapter: { paragraphs: { text: string; kind?: string; markdown?: string }[] }) => chapter.paragraphs
    .filter((paragraph) => paragraph.text.trim())
    .map((paragraph) => (paragraph.kind === 'paragraph' && paragraph.markdown !== undefined ? paragraph.markdown.trim() : markdown([{ t: paragraph.text.trim() }])));
  /**
   * An edit with whether the book holds its text and title (sagadraft.HeldByBook, specs/2026-11-11-ux9-writing-a.md WA4.1):
   * paragraph for paragraph with their faces, a title left empty being the chapter's.
   */
  function sameOf(draft: Draft): Draft {
    if (!draft.edits) return draft;
    const chapter = editedChapter(draft);
    const held = Boolean(chapter) && !draft.edits.changed && (!draft.title.trim() || draft.title.trim() === chapter!.title.trim())
      && JSON.stringify(book(draft.text)) === JSON.stringify(markdownOf(chapter!));
    return { ...draft, edits: { ...draft.edits, same: held } };
  }
  /** The edit as the page sees it: the chapter changed in the book, deleted from it, or one step to give back. */
  const refreshEdit = (draft: Draft) => {
    const track = edits.get(draft);
    if (!draft.edits || !track) return;
    const chapter = editedChapter(draft);
    const changed = Boolean(chapter) && chapter!.publishedRevision !== track.revision;
    draft.edits = {
      chapterId: draft.edits.chapterId, bookWords: chapter ? wordsOf(paragraphsOf(chapter).join(' ')).length : 0, changed, gone: !chapter,
      canRevert: Boolean(track.previous) && Boolean(chapter) && !changed, same: false,
      ...(chapter ? { chapterTitle: chapter.title, bookId: chapter.bookId } : {}),
    };
  };
  /** The text of a draft takes the place of its chapter in the book, as a new revision (A8.7): paragraphs in the Markdown subset. */
  const putInBook = (draft: Draft, operationId: 'replaceChapterFromDraft' | 'revertChapterFromDraft', title: string, paragraphs: string[]) => {
    const chapter = editedChapter(draft);
    const track = edits.get(draft);
    if (!chapter || !track) throw new MockApiError(409, 'The chapter is gone.', operationId, 'chapter_draft.chapter_gone');
    if (chapter.publishedRevision !== track.revision) throw new MockApiError(409, 'The chapter changed.', operationId, 'chapter_draft.chapter_changed');
    const before = markdownOf(chapter);
    const revision = (chapter.publishedRevision ?? 0) + 1;
    chapter.title = title;
    chapter.paragraphs = paragraphs.map((source, index) => ({
      id: `${chapter.id}-r${revision}-${index + 1}`, order: index + 1, kind: 'paragraph', text: markdownPlain(source), markdown: source,
    }));
    chapter.publishedRevision = revision;
    chapter.draftRevision = revision;
    edits.set(draft, { revision, ...(operationId === 'replaceChapterFromDraft' ? { previous: { paragraphs: before } } : {}) });
    draft.title = title;
    draft.text = fromBook(paragraphs);
    draft.revision += 1;
    draft.updatedAt = new Date().toISOString();
    refreshEdit(draft);
    return shown(draft);
  };
  /**
   * The chapter of a draft in its book (R7.5): after the chapter it follows, the ones after it move down; its text in the
   * Markdown subset of the book, at the first revision. The reader opens it as any chapter.
   */
  const addToBook = (draft: Draft) => {
    const id = `chapter-from-${draft.id}`;
    const inBook = context.state.chapters.filter((item) => item.bookId === draft.place.bookId).sort((left, right) => left.order - right.order);
    const template = inBook[0] ?? context.state.chapters[0];
    const target = context.state.books.find((item) => item.id === draft.place.bookId);
    if (!template || !target) return id;
    const after = inBook.find((item) => item.id === draft.place.afterChapterId);
    const order = after ? after.order + 1 : 1;
    for (const item of inBook) if (item.order >= order) item.order += 1;
    const paragraphs = book(draft.text);
    const at = new Date().toISOString();
    context.state.chapters.push({
      ...clone(template), id, bookId: target.id, title: draft.title, order, status: 'published', publishedAt: at, createdAt: at, updatedAt: at,
      publishedRevision: 1, draftRevision: 1, wordCount: wordsOf(paragraphs.join(' ')).length,
      paragraphs: paragraphs.map((source, index) => ({ id: `${id}-p${index + 1}`, order: index + 1, kind: 'paragraph', text: markdownPlain(source), markdown: source })),
      navigation: { ...clone(template.navigation), total: inBook.length + 1 },
    });
    target.chapterCount += 1;
    return id;
  };
  /** A note applied to the working text: the text kept before it (WA7.2); a correction of the proofreading keeps none (PR4.3). */
  const applyTo = (draft: Draft, note: Note, text: string) => {
    if (note.kind !== 'proofread') keepVersion(draft, 'before_apply', draft.text, null, undefined, text);
    draft.text = text;
    note.status = 'applied';
    note.updatedAt = new Date().toISOString();
    draft.revision += 1;
    return shown(draft);
  };

  /** The proofreading of a draft (PR3): the version it reads, the Job one step a read. */
  const startProofread = (draft: Draft, jobId: string) => {
    keepVersion(draft, 'before_proofread', draft.text);
    draft.job = { id: jobId, mode: 'proofread', status: 'queued', done: 0, total: 0 };
  };

  /** A chapter attached in a chat of the project (CA5.5): the mock keeps its paragraphs and the kind of its file. */
  const chatChapter = (projectId: string, chatId: string, number: number) => {
    const chat = context.state.chats.find((item) => item.id === chatId && item.projectId === projectId);
    const attachment = chat ? context.state.chatAttachments[chatId]?.find((item) => item.number === number) : undefined;
    if (!attachment) throw new MockApiError(404, 'Attachment not found.', 'startChapterProofread', 'not_found');
    return {
      fileName: attachment.fileName || `${attachment.title || 'Глава'}.txt`, format: attachment.format, title: attachment.title,
      text: attachment.paragraphs.join('\n\n'), uploadedAt: new Date().toISOString(),
    };
  };

  const operations: ChapterDraftOperations = {
    // A chapter of the author from a file or a text with its proofreading started (specs/2026-11-07-writing-proofread.md, PR2.1).
    async startChapterProofread(projectId, input: ProofreadChapterInput & { title?: string; bookId?: string; afterChapterId?: string }) {
      context.requireProjectAccess('startChapterProofread', projectId, [...writers]);
      const fromChat = 'chatId' in input && input.chatId ? { chatId: input.chatId, number: input.attachment } : null;
      // The same chapter of the chat again answers its draft: no second proofreading (specs/2026-11-11-ux5-proofread-mode.md PM3.3).
      const before = fromChat && ofProject(projectId).find((item) => attachmentOf(item)?.chatId === fromChat.chatId && attachmentOf(item)?.number === fromChat.number);
      if (before) return shown(before);
      const original = fromChat ? chatChapter(projectId, fromChat.chatId, fromChat.number) : await readChapterFile(input as ChapterFileInput, 'startChapterProofread');
      const at = new Date().toISOString();
      const created: Draft = {
        id: `draft-${nextId++}`, title: (input.title?.trim() || original.title).slice(0, 200), idea: '', kind: 'sketch', stage: 'text', text: original.text,
        place: placeOf(input.bookId, input.afterChapterId), typicalWords: mockTypicalWords, notes: [], revision: 1, case: 'nearly_done', caseChoice: 'nearly_done',
        author: context.state.user.displayName, createdAt: at, updatedAt: at,
      };
      keepOriginal(created, original);
      tieToAttachment(created, fromChat);
      startProofread(created, `draft-job-${nextId++}`);
      drafts.set(projectId, [created, ...ofProject(projectId)]);
      return shown(created);
    },
    // A file in place of the working text of an open draft (PR2.2).
    async loadChapterDraftFile(projectId, draftId, input: ChapterFileInput & { expectedRevision: number }) {
      context.requireProjectAccess('loadChapterDraftFile', projectId, [...writers]);
      const draft = found(projectId, draftId, 'loadChapterDraftFile');
      open(draft, input.expectedRevision, 'loadChapterDraftFile');
      const original = await readChapterFile(input, 'loadChapterDraftFile');
      if (wordsOf(draft.text.replace(/[⟦⟧]/gu, '')).length) keepVersion(draft, 'before_upload', draft.text);
      Object.assign(draft, { text: original.text, caseChoice: 'nearly_done', revision: draft.revision + 1, updatedAt: new Date().toISOString() });
      draft.notes = draft.notes.filter((note) => !(note.kind === 'proofread' && note.status === 'open'));
      keepOriginal(draft, original);
      // A file loaded later is another chapter: the tie to the chat goes (PM3.3).
      tieToAttachment(draft, null);
      startProofread(draft, `draft-job-${nextId++}`);
      return shown(draft);
    },
    // «Взять оформление из файла Word» (PR9): the Word file of the same text becomes the original, the text and the notes stay.
    async takeChapterDraftOriginalLook(projectId, draftId, input: ChapterFileInput) {
      context.requireProjectAccess('takeChapterDraftOriginalLook', projectId, [...writers]);
      const draft = found(projectId, draftId, 'takeChapterDraftOriginalLook');
      const original = originalOf(draft);
      if (!original) throw new MockApiError(422, 'No chapter was loaded as a file.', 'takeChapterDraftOriginalLook', 'chapter_draft.no_original');
      if (!input.file || !/\.docx$/iu.test(input.file.name)) {
        throw new MockApiError(422, 'The look is taken from a Word file.', 'takeChapterDraftOriginalLook', 'chapter_draft.no_look');
      }
      const file = await readChapterFile(input, 'takeChapterDraftOriginalLook');
      const spaced = (text: string) => text.split(/\n\n/u).map((line) => line.split(/\s+/u).filter(Boolean).join(' ')).filter(Boolean).join('\n');
      if (spaced(file.text) !== spaced(original.text)) {
        throw new MockApiError(422, 'The file is another text than the original.', 'takeChapterDraftOriginalLook', 'chapter_draft.original_differs');
      }
      keepOriginal(draft, file);
      return shown(draft);
    },
    // «Принять все» of typos, spelling or punctuation (PR4.4): one version before them all; a place gone stays open.
    async applyChapterDraftNotes(projectId, draftId, body) {
      context.requireProjectAccess('applyChapterDraftNotes', projectId, [...writers]);
      rejectUnknownKeys('applyChapterDraftNotes', body, ['expectedRevision', 'notes']);
      const draft = found(projectId, draftId, 'applyChapterDraftNotes');
      open(draft, body.expectedRevision, 'applyChapterDraftNotes');
      const notes = body.notes.map((id) => draft.notes.find((note) => note.id === id));
      if (!notes.length || notes.some((note) => !note || note.kind !== 'proofread' || !batchCategories.has(note.category ?? ''))) {
        throw new MockApiError(400, 'Only typos, spelling and punctuation are taken at once.', 'applyChapterDraftNotes', 'request.validation');
      }
      let text = draft.text;
      let applied = 0;
      let stale = 0;
      const taken: Note[] = [];
      for (const note of [...notes as Note[]].sort((left, right) => left.number - right.number)) {
        const at = note.status === 'open' && note.quote ? text.indexOf(note.quote) : -1;
        if (at < 0 || note.replacement === undefined) {
          stale += 1;
          continue;
        }
        const end = at + note.quote!.length;
        text = text.slice(0, at) + note.replacement + text.slice(end);
        taken.push(note);
        applied += 1;
      }
      if (applied) {
        keepVersion(draft, 'before_apply', draft.text);
        draft.text = text;
        draft.revision += 1;
        const at = new Date().toISOString();
        for (const note of taken) Object.assign(note, { status: 'applied', updatedAt: at });
      }
      return { draft: shown(draft), applied, stale };
    },
    // The Word file with revisions (PR6): the mock gives a small file under the name the server gives.
    async getChapterDraftExport(projectId, draftId) {
      context.requireProjectAccess('getChapterDraftExport', projectId);
      const draft = found(projectId, draftId, 'getChapterDraftExport');
      const original = originalOf(draft);
      if (!original) throw new MockApiError(422, 'No chapter was loaded as a file.', 'getChapterDraftExport', 'chapter_draft.no_original');
      const stem = original.fileName.replace(/\.[^.]+$/u, '') || 'Глава';
      return {
        blob: new Blob([`Правки к «${original.fileName}»\n\n${draft.text.replace(/[⟦⟧]/gu, '')}`], { type: 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' }),
        fileName: `${stem} — правки.docx`,
      };
    },
    async listChapterDrafts(projectId, options: ListChapterDraftsOptions = {}) {
      context.requireProjectAccess('listChapterDrafts', projectId);
      rejectUnknownKeys('listChapterDrafts', options, ['limit', 'cursor', 'signal', 'chat']);
      // A row names the step of its draft by what the draft says of itself (specs/2026-11-11-ux5-writing-path.md WP1.2);
      // a chat reads the drafts proofread from its chapters (specs/2026-11-11-ux5-proofread-mode.md PM3.3).
      const data = ofProject(projectId).filter((draft) => !options.chat || attachmentOf(draft)?.chatId === options.chat).map((draft) => {
        refreshEdit(draft);
        const { id, title, opening, kind, stage, job, plan, revision, createdAt, updatedAt, text, idea, notes, edits: edit } = shown(draft);
        const open = notes.filter((note) => note.status === 'open');
        const attachment = attachmentOf(draft);
        // Where an edit stands against its chapter, by the one rule of the draft (WA1.3).
        const editState = !edit ? undefined : edit.gone ? 'gone' as const : edit.changed ? 'changed' as const : edit.same ? 'same' as const : 'edited' as const;
        return {
          id, title, ...(opening ? { opening } : {}), kind, stage, words: listedWords(text), revision, ...(edit ? { edits: true, editState } : {}), createdAt, updatedAt,
          idea: idea.trim() !== '', ...(plan ? { plan } : {}), ...(job ? { job: withoutWriting(job) } : {}),
          findings: open.filter((note) => findingKinds.has(note.kind)).length, corrections: open.filter((note) => note.kind === 'proofread').length,
          ...(attachment ? { attachment: { ...attachment } } : {}),
        };
      });
      return clone({ data, meta: { hasMore: false, nextCursor: null, totalCount: data.length } });
    },
    async createChapterDraft(projectId, body: OperationRequest<'createChapterDraft'>) {
      context.requireProjectAccess('createChapterDraft', projectId, [...writers]);
      const at = new Date().toISOString();
      const created: Draft = {
        id: `draft-${nextId++}`, title: body.title?.trim() ?? '', idea: body.idea ?? '', kind: body.kind, stage: 'text', text: body.text ?? '',
        place: placeOf(body.place?.bookId, body.place?.afterChapterId), typicalWords: mockTypicalWords, notes: [], revision: 1, case: 'scratch',
        author: context.state.user.displayName, createdAt: at, updatedAt: at,
      };
      drafts.set(projectId, [created, ...ofProject(projectId)]);
      return shown(created);
    },
    async getChapterDraft(projectId, draftId) {
      context.requireProjectAccess('getChapterDraft', projectId);
      const draft = found(projectId, draftId, 'getChapterDraft');
      refreshEdit(draft);
      advance(draft);
      draft.stage = stageOf(draft);
      const answer = shown(draft);
      // The scene in writing is the editor's work in progress: a reader of the saga reads none (ux10-scene-stream ST1.4).
      return writers.some((role) => role === context.actorRole(projectId)) || !answer.job ? answer : { ...answer, job: withoutWriting(answer.job) };
    },
    async getChapterDraftPlan(projectId, draftId, options = {}) {
      context.requireProjectAccess('getChapterDraftPlan', projectId);
      const draft = found(projectId, draftId, 'getChapterDraftPlan');
      const versions = plansOf(draft);
      const plan = options.number ? versions.find((item) => item.number === options.number) : versions.at(-1);
      if (!plan) throw new MockApiError(404, 'No plan yet.', 'getChapterDraftPlan');
      const latest = versions.at(-1)?.number === plan.number;
      return clone({ ...plan, approved: Boolean(latest && draft.plan?.approved), stale: Boolean(latest && shown(draft).plan?.stale) });
    },
    // The author's version of the plan and his choices about the pack (specs/2026-11-06-writing-agent.md, WA4.2, WA2.5).
    async updateChapterDraftPlan(projectId, draftId, body) {
      context.requireProjectAccess('updateChapterDraftPlan', projectId, [...writers]);
      rejectUnknownKeys('updateChapterDraftPlan', body, ['expectedNumber', 'understanding', 'scenes']);
      const draft = found(projectId, draftId, 'updateChapterDraftPlan');
      if (draft.added) throw new MockApiError(409, 'Already in the book.', 'updateChapterDraftPlan', 'chapter_draft.added');
      if (draft.job && activeStatuses.has(draft.job.status)) throw new MockApiError(409, 'A run is working.', 'updateChapterDraftPlan', 'chapter_draft.busy');
      const plan = authorPlan(draft, body);
      planned(draft);
      return plan;
    },
    async withdrawChapterDraftPlan(projectId, draftId, body) {
      context.requireProjectAccess('withdrawChapterDraftPlan', projectId, [...writers]);
      rejectUnknownKeys('withdrawChapterDraftPlan', body, ['expectedNumber']);
      const draft = found(projectId, draftId, 'withdrawChapterDraftPlan');
      if (draft.added) throw new MockApiError(409, 'Already in the book.', 'withdrawChapterDraftPlan', 'chapter_draft.added');
      if (draft.job && activeStatuses.has(draft.job.status)) throw new MockApiError(409, 'A run is working.', 'withdrawChapterDraftPlan', 'chapter_draft.busy');
      withdrawTail(draft, body.expectedNumber);
      return shown(draft);
    },
    async updateChapterDraftMoment(projectId, draftId, body) {
      context.requireProjectAccess('updateChapterDraftMoment', projectId, [...writers]);
      rejectUnknownKeys('updateChapterDraftMoment', body, ['expectedRevision', 'choices']);
      const draft = found(projectId, draftId, 'updateChapterDraftMoment');
      open(draft, body.expectedRevision, 'updateChapterDraftMoment');
      setChoices(draft, body.choices);
      return shown(draft);
    },
    // The pack of the mock saga on the place of the chapter (WA2): who is in frame by the idea, less whom the author left out.
    async getChapterDraftMoment(projectId, draftId) {
      context.requireProjectAccess('getChapterDraftMoment', projectId);
      const draft = found(projectId, draftId, 'getChapterDraftMoment');
      const choices = choicesOf(draft);
      const out = new Set(choices.filter((choice) => choice.action === 'exclude').map((choice) => choice.entry.toLowerCase()));
      const frame = [
        // Why each is in frame, as the server says it to the author (chaptermoment view.go).
        { name: 'Кира', why: 'первое имя в вашем замысле: глава идёт глазами этого героя' },
        { name: 'Тарен', aliases: ['мальчишка из гильдии'], why: 'из вашего замысла' },
        { name: 'Ольм', aliases: ['стражник'], why: 'из конца прошлой главы' },
        ...choices.filter((choice) => choice.action === 'include').map((choice) => ({ name: choice.entry, why: 'вы добавили в главу' })),
      ].filter((entry) => !out.has(entry.name.toLowerCase()));
      // The sections as the server names them for the author (specs/2026-11-12-ux12-writing.md WR1.4): its names, places
      // of the book in words, no key and nothing said to the model.
      const first = chapterRefOf(context, projectId, chapterKey(1, 1))?.label ?? 'Книга 1, гл. 1';
      const after = context.state.chapters.find((chapter) => chapter.id === draft.place.afterChapterId);
      const afterBook = after ? context.state.books.find((item) => item.id === after.bookId) : undefined;
      const where = after && afterBook ? `Новая глава встанет следом за главой: ${chapterPlace(placedBook(context.state.books, afterBook).place ?? 1, after.order, after.title)}.`
        : 'Новая глава встанет в конец саги.';
      return clone({
        inFrame: frame,
        sections: [
          { name: momentTitle('состояние_на_момент'), text: `Кира:\n  Кира — местонахождение: в городе, при ней фонарь (${first}, абзац 4)\nОльм:\n  Ольм — должность: стражник северных ворот (${first}, абзац 8)` },
          { name: momentTitle('кто_что_знает'), text: `Кира (новое сверху):\n  Кира не знает: куда ушёл Тарен (${first}, абзац 6)` },
          { name: momentTitle('стиль'), text: `Измерено по 3 последним главам (${first} — ${first}):\n- повествование в прошедшем времени, от третьего лица\n`
            + '- обычная глава — 2\u00a0400 слов, обычная фраза — около 11 слов, обычный абзац — 38 слов\n'
            + '- реплики — примерно в 3 из 10 абзацев, каждая с новой строки со знака «—»\n- при репликах чаще всего: «сказал», «спросила», «ответил»\n'
            + '- сравнения «словно», «будто» встречаются редко\n- усилители вроде «очень», «невероятно» почти не встречаются' },
          { name: momentTitle('место'), text: where },
        ],
        gaps: [{ kind: 'unknown_name', name: 'Гильдия', text: '«Гильдия» до этой главы в книге не встречается: новое имя или другое написание?' }],
        typicalWords: draft.typicalWords ?? 0, choices,
      });
    },
    async updateChapterDraft(projectId, draftId, body) {
      context.requireProjectAccess('updateChapterDraft', projectId, [...writers]);
      const draft = found(projectId, draftId, 'updateChapterDraft');
      open(draft, body.expectedRevision, 'updateChapterDraft');
      // The author's variant the draft could not take goes into a version alone (specs/2026-11-11-ux5-writing-path.md WP5.4).
      if (body.variant !== undefined) {
        const { variant } = body;
        if (Object.keys(body).some((key) => key !== 'expectedRevision' && key !== 'variant') || (variant.text === undefined && variant.idea === undefined)) {
          throw new MockApiError(400, 'A variant is kept alone.', 'updateChapterDraft', 'request.validation');
        }
        if (variant.text !== undefined && !validMarks(variant.text)) throw new MockApiError(400, 'Broken marks.', 'updateChapterDraft', 'chapter_draft.invalid_marks');
        keepVersion(draft, 'author_variant', variant.text ?? draft.text, null, variant.idea);
        return shown(draft);
      }
      if (body.title !== undefined) draft.title = body.title.trim();
      if (body.idea !== undefined) draft.idea = body.idea;
      if (body.kind !== undefined) draft.kind = body.kind;
      if (body.text !== undefined && !validMarks(body.text)) {
        throw new MockApiError(400, 'Broken marks.', 'updateChapterDraft', 'chapter_draft.invalid_marks');
      }
      if (body.keepVersion && body.text === undefined) {
        throw new MockApiError(400, 'A version is kept only of an edit of the text.', 'updateChapterDraft', 'request.validation');
      }
      if (body.keepVersion) keepVersion(draft, 'before_delete', draft.text, null, undefined, body.text);
      if (body.caseChoice !== undefined) {
        if (body.caseChoice) draft.caseChoice = body.caseChoice;
        else delete draft.caseChoice;
      }
      if (body.text !== undefined) draft.text = body.text;
      if (body.place !== undefined) draft.place = placeOf(body.place?.bookId, body.place?.afterChapterId);
      draft.revision += 1;
      draft.stage = stageOf(draft);
      draft.updatedAt = new Date().toISOString();
      return shown(draft);
    },
    async deleteChapterDraft(projectId, draftId, expectedRevision) {
      context.requireProjectAccess('deleteChapterDraft', projectId, [...writers]);
      const draft = found(projectId, draftId, 'deleteChapterDraft');
      if (draft.job && activeStatuses.has(draft.job.status)) throw new MockApiError(409, 'A run is working.', 'deleteChapterDraft', 'chapter_draft.busy');
      if (expectedRevision !== draft.revision) throw new MockApiError(409, 'Stale revision.', 'deleteChapterDraft', 'conflict');
      // The session of the helper goes with its draft (W7.5).
      if (draft.chatId) context.state.chats = context.state.chats.filter((item) => item.id !== draft.chatId);
      drafts.set(projectId, ofProject(projectId).filter((item) => item.id !== draftId));
    },
    async startChapterDraftJob(projectId, draftId, body) {
      context.requireProjectAccess('startChapterDraftJob', projectId, [...writers]);
      const draft = found(projectId, draftId, 'startChapterDraftJob');
      rejectUnknownKeys('startChapterDraftJob', body, ['expectedRevision', 'mode', 'plan', 'scenes', 'from', 'tail', 'place', 'instruction', 'release']);
      open(draft, body.expectedRevision, 'startChapterDraftJob');
      const jobId = `draft-job-${nextId++}`;
      // The writing agent (specs/2026-11-06-writing-agent.md, WA8): «Писать» approves the version it names.
      if ((body.mode === 'scenes') !== Boolean(body.plan) || (body.mode === 'selection') !== Boolean(body.place)) {
        throw new MockApiError(400, 'The work of the mode is not named.', 'startChapterDraftJob', 'request.validation');
      }
      if ((body.mode === 'check' || body.mode === 'selection' || body.mode === 'proofread') && !draft.text.replace(/[⟦⟧]/gu, '').trim()) {
        throw new MockApiError(422, 'Nothing to work on.', 'startChapterDraftJob', 'chapter_draft.no_text');
      }
      if (body.mode === 'proofread') {
        startProofread(draft, jobId);
        return shown(draft);
      }
      // The first scenes of a draft the writing agent writes are a new chapter of the writer's tier, however they were
      // started, decided before anything changes (specs/2026-11-08-subscription-tiers.md TR7.1, TR7.2).
      if (body.mode === 'scenes' && newChapterDraft(draft)) {
        mockChargeNewChapter(context.state, context.currentPrincipalId(), draft.id, jobId);
      }
      if (body.mode === 'scenes') approvePlan(draft, body.plan!);
      draft.case = caseOf(draft);
      startAgentRun(draft, body, jobId);
      return shown(draft);
    },
    // «Править главу» (A8.7): the one draft of a chapter of the book, with its text; a changed chapter is read again.
    async openChapterEdit(projectId, chapterId) {
      context.requireProjectAccess('openChapterEdit', projectId, [...writers]);
      const chapter = context.chapterFor(chapterId, 'openChapterEdit');
      const book = context.state.books.find((item) => item.id === chapter.bookId);
      if (!book || book.projectId !== projectId || chapter.publishedRevision === null) throw new MockApiError(404, 'Resource not found.', 'openChapterEdit', 'not_found');
      const text = fromBook(markdownOf(chapter));
      const existing = ofProject(projectId).find((item) => item.edits?.chapterId === chapterId);
      if (existing) {
        refreshEdit(existing);
        return shown(existing);
      }
      // The chapter a draft added is edited in that draft (specs/2026-11-11-ux5-writing-path.md WP4.3).
      const adder = ofProject(projectId).find((item) => item.added?.chapterId === chapterId);
      if (adder) {
        delete adder.added;
        adder.edits = { chapterId, bookWords: 0, changed: false, gone: false, canRevert: false, same: false };
        edits.set(adder, { revision: chapter.publishedRevision });
        Object.assign(adder, { title: chapter.title, stage: 'text', revision: adder.revision + 1, updatedAt: new Date().toISOString() });
        refreshEdit(adder);
        return shown(adder);
      }
      const previous = context.state.chapters.filter((item) => item.bookId === chapter.bookId && item.order < chapter.order).sort((left, right) => right.order - left.order)[0];
      const at = new Date().toISOString();
      const created: Draft = {
        id: `draft-${nextId++}`, title: chapter.title, idea: '', kind: 'sketch', stage: 'text', text,
        place: placeOf(chapter.bookId, previous?.id), typicalWords: mockTypicalWords, notes: [], revision: 1, case: 'nearly_done',
        edits: { chapterId, bookWords: 0, changed: false, gone: false, canRevert: false, same: false },
        author: context.state.user.displayName, createdAt: at, updatedAt: at,
      };
      edits.set(created, { revision: chapter.publishedRevision });
      refreshEdit(created);
      drafts.set(projectId, [created, ...ofProject(projectId)]);
      return shown(created);
    },
    async replaceChapterFromDraft(projectId, draftId, body) {
      context.requireProjectAccess('replaceChapterFromDraft', projectId, [...writers]);
      rejectUnknownKeys('replaceChapterFromDraft', body, ['expectedRevision', 'title']);
      const draft = found(projectId, draftId, 'replaceChapterFromDraft');
      open(draft, body.expectedRevision, 'replaceChapterFromDraft');
      const title = body.title.trim();
      const paragraphs = book(draft.text);
      if (!title) throw new MockApiError(400, 'A title is required.', 'replaceChapterFromDraft', 'request.validation');
      if (!paragraphs.length) throw new MockApiError(422, 'Nothing to put in the book.', 'replaceChapterFromDraft', 'chapter_draft.no_text');
      return putInBook(draft, 'replaceChapterFromDraft', title, paragraphs);
    },
    async reopenChapterFromBook(projectId, draftId, body) {
      context.requireProjectAccess('reopenChapterFromBook', projectId, [...writers]);
      rejectUnknownKeys('reopenChapterFromBook', body, ['expectedRevision']);
      const draft = found(projectId, draftId, 'reopenChapterFromBook');
      open(draft, body.expectedRevision, 'reopenChapterFromBook');
      const chapter = editedChapter(draft);
      if (!chapter || chapter.publishedRevision === null) throw new MockApiError(409, 'The chapter is gone.', 'reopenChapterFromBook', 'chapter_draft.chapter_gone');
      edits.set(draft, { revision: chapter.publishedRevision });
      Object.assign(draft, { title: chapter.title, text: fromBook(markdownOf(chapter)), revision: draft.revision + 1 });
      refreshEdit(draft);
      return shown(draft);
    },
    async revertChapterFromDraft(projectId, draftId, body) {
      context.requireProjectAccess('revertChapterFromDraft', projectId, [...writers]);
      rejectUnknownKeys('revertChapterFromDraft', body, ['expectedRevision']);
      const draft = found(projectId, draftId, 'revertChapterFromDraft');
      open(draft, body.expectedRevision, 'revertChapterFromDraft');
      const previous = edits.get(draft)?.previous;
      if (!previous) throw new MockApiError(400, 'Nothing to give back.', 'revertChapterFromDraft', 'request.validation');
      return putInBook(draft, 'revertChapterFromDraft', editedChapter(draft)?.title ?? draft.title, previous.paragraphs);
    },
    async addChapterDraftToBook(projectId, draftId, body) {
      context.requireProjectAccess('addChapterDraftToBook', projectId, [...writers]);
      const draft = found(projectId, draftId, 'addChapterDraftToBook');
      if (draft.added) return shown(draft);
      if (draft.edits && editedChapter(draft)) throw new MockApiError(400, 'The chapter is in the book: replace it.', 'addChapterDraftToBook', 'request.validation');
      open(draft, body.expectedRevision, 'addChapterDraftToBook');
      if (!draft.place.bookId) throw new MockApiError(422, 'No place.', 'addChapterDraftToBook', 'chapter_draft.no_destination');
      if (!draft.title || !plain(draft.text).text.trim()) throw new MockApiError(422, 'Nothing to add.', 'addChapterDraftToBook', 'chapter_draft.no_text');
      draft.added = { chapterId: addToBook(draft), title: draft.title };
      draft.revision += 1;
      draft.stage = 'added';
      return shown(draft);
    },
    async applyChapterDraftNote(projectId, draftId, noteId, body) {
      context.requireProjectAccess('applyChapterDraftNote', projectId, [...writers]);
      const draft = found(projectId, draftId, 'applyChapterDraftNote');
      open(draft, body.expectedRevision, 'applyChapterDraftNote');
      const note = draft.notes.find((item) => item.id === noteId);
      if (!note) throw new MockApiError(404, 'Note not found.', 'applyChapterDraftNote');
      // «Вернуть как было» (P5.6): what stood in the place comes back, whatever the author did to the new text in it.
      if (note.kind === 'replaced' && note.status === 'open') {
        const anchors = replacedAnchors.get(note);
        const start = anchors ? draft.text.indexOf(anchors.before) : -1;
        const from = start + (anchors?.before.length ?? 0);
        const end = anchors && start >= 0 ? (anchors.after ? draft.text.indexOf(anchors.after, from) : draft.text.length) : -1;
        if (start < 0 || end < 0) throw new MockApiError(409, 'The place is gone.', 'applyChapterDraftNote', 'chapter_draft.note_stale');
        return applyTo(draft, note, `${draft.text.slice(0, from)}${note.replacement ?? ''}${draft.text.slice(end)}`);
      }
      if (note.status !== 'open' || !note.quote || note.replacement === undefined) throw new MockApiError(400, 'Nothing to apply.', 'applyChapterDraftNote', 'request.validation');
      const at = draft.text.indexOf(note.quote);
      if (at < 0) throw new MockApiError(409, 'The place is gone.', 'applyChapterDraftNote', 'chapter_draft.note_stale');
      // A variant of a place goes between the marks, unless the place is inside a block already (WA7.2); its group goes.
      const head = draft.text.slice(0, at);
      const inside = head.lastIndexOf('⟦') > head.lastIndexOf('⟧');
      const put = note.kind === 'variant' && !inside ? `⟦${note.replacement}⟧` : note.replacement;
      if (note.kind === 'variant') {
        for (const other of draft.notes) if (other.variantGroup === note.variantGroup && other.id !== note.id && other.status === 'open') other.status = 'dismissed';
      }
      return applyTo(draft, note, `${head}${put}${draft.text.slice(at + note.quote.length)}`);
    },
    // The versions of the text (specs/2026-11-06-writing-agent.md, WA7.5).
    async listChapterDraftVersions(projectId, draftId) {
      context.requireProjectAccess('listChapterDraftVersions', projectId);
      return listVersions(found(projectId, draftId, 'listChapterDraftVersions'));
    },
    async getChapterDraftVersion(projectId, draftId, number) {
      context.requireProjectAccess('getChapterDraftVersion', projectId);
      return versionOf(found(projectId, draftId, 'getChapterDraftVersion'), number, 'getChapterDraftVersion');
    },
    async takeChapterDraftVersion(projectId, draftId, number, body) {
      context.requireProjectAccess('takeChapterDraftVersion', projectId, [...writers]);
      rejectUnknownKeys('takeChapterDraftVersion', body, ['expectedRevision', 'whole', 'from', 'to', 'place', 'after']);
      const draft = found(projectId, draftId, 'takeChapterDraftVersion');
      open(draft, body.expectedRevision, 'takeChapterDraftVersion');
      const text = takenText(draft.text, versionOf(draft, number, 'takeChapterDraftVersion'), body);
      keepVersion(draft, body.whole ? 'before_restore' : 'before_take', draft.text, null, undefined, body.whole ? undefined : text);
      draft.text = text;
      draft.revision += 1;
      // A piece taken in checks its seams (WA7.5).
      if (!body.whole) startSeams(draft, `draft-job-${nextId++}`);
      return shown(draft);
    },
    async updateChapterDraftNote(projectId, draftId, noteId, body) {
      context.requireProjectAccess('updateChapterDraftNote', projectId, [...writers]);
      rejectUnknownKeys('updateChapterDraftNote', body, ['status', 'answer']);
      const draft = found(projectId, draftId, 'updateChapterDraftNote');
      if (draft.added) throw new MockApiError(409, 'Already in the book.', 'updateChapterDraftNote', 'chapter_draft.added');
      const note = draft.notes.find((item) => item.id === noteId);
      if (!note) throw new MockApiError(404, 'Note not found.', 'updateChapterDraftNote');
      if ((body.status === undefined) === (body.answer === undefined)) throw new MockApiError(400, 'Dismiss or answer.', 'updateChapterDraftNote', 'request.validation');
      if (body.answer !== undefined) {
        const answer = body.answer.trim();
        if (note.kind !== 'question' || !answer) throw new MockApiError(400, 'Only a question takes an answer.', 'updateChapterDraftNote', 'request.validation');
        Object.assign(note, { answer, status: 'answered' });
      } else if (body.status === 'open') {
        // «Отменить» of a replacement (specs/2026-11-10-ux1-writing.md UW4.2): an applied finding whose place is back in
        // the text stands open again; an open one answers as it is.
        if (note.status !== 'open') {
          if (draft.job && activeStatuses.has(draft.job.status)) throw new MockApiError(409, 'A run is working.', 'updateChapterDraftNote', 'chapter_draft.busy');
          // writingagent.Reopenable: an applied finding by «Отменить» (UW4.2), a dismissed one by «Вернуть замечание» (NT1.4).
          const reopenable = note.status === 'applied' ? note.kind !== 'variant' && note.kind !== 'replaced'
            : note.status === 'dismissed' && (findingKinds.has(note.kind) || note.kind === 'proofread');
          if (!reopenable || !note.quote) throw new MockApiError(400, 'This note does not open again.', 'updateChapterDraftNote', 'request.validation');
          if (!draft.text.includes(note.quote)) throw new MockApiError(409, 'The place is not in the text.', 'updateChapterDraftNote', 'chapter_draft.note_stale');
          note.status = 'open';
        }
      } else if (body.status) {
        note.status = body.status;
      }
      note.updatedAt = new Date().toISOString();
      return clone(note);
    },
    // «Остановить» of the work on a chapter (WA5.10): the Job of a draft stops at once, what it wrote stays; any other
    // Job is stopped the way the mock stops Jobs.
    // getJob of a work on a draft (specs/2026-11-11-ux5-writing-path.md WP3.4): the common Job resource, one step a read.
    async getJob(jobId) {
      const owner = [...drafts.entries()].find(([, list]) => list.some((item) => item.job?.id === jobId));
      const draft = owner?.[1].find((item) => item.job?.id === jobId);
      if (!owner || !draft?.job) return jobs.getJob(jobId);
      context.requireProjectAccess('getJob', owner[0]);
      advance(draft);
      const job = draft.job;
      return clone(createJob({
        id: job.id, kind: 'chapter_draft', status: job.status, progress: job.total ? job.done / job.total : null,
        subject: { type: 'chapter_draft', projectId: owner[0], mode: job.mode },
      }));
    },
    async cancelJob(jobId) {
      const owner = [...drafts.entries()].find(([, list]) => list.some((item) => item.job?.id === jobId));
      const draft = owner?.[1].find((item) => item.job?.id === jobId);
      if (!owner || !draft?.job) return jobs.cancelJob(jobId);
      context.requireProjectAccess('cancelJob', owner[0], [...writers]);
      if (activeStatuses.has(draft.job.status)) draft.job.status = 'canceled';
      // A stopped writing keeps no scene in writing: the scene not finished is not kept (ux10-scene-stream ST2.3).
      delete draft.job.writing;
      mockSettleChapter(context.state, draft.job);
      return undefined;
    },
    async submitDraftTurn(projectId, draftId, body: OperationRequest<'submitDraftTurn'>, options: RequiredIdempotencyOptions) {
      context.requireProjectAccess('submitDraftTurn', projectId, [...writers]);
      rejectUnknownKeys('submitDraftTurn', body, ['text', 'place', 'button']);
      const draft = found(projectId, draftId, 'submitDraftTurn');
      if (draft.added) throw new MockApiError(409, 'Already in the book.', 'submitDraftTurn', 'chapter_draft.added');
      if (!body.text.trim()) throw new MockApiError(400, 'Write to the helper.', 'submitDraftTurn', 'request.validation');
      if (body.place && !draft.text.includes(body.place)) throw new MockApiError(400, 'The place is not in the draft.', 'submitDraftTurn', 'request.validation');
      if (!draft.chatId) {
        const session = await chat.createChatSession(projectId, {
          sourceView: 'published', scope: 'project', title: `Письмо: ${draft.title || 'черновик'}`.slice(0, 180),
        }, { idempotencyKey: helperKey(options.idempotencyKey) });
        context.state.draftHelperChatIds.push(session.id);
        draft.chatId = session.id;
      }
      const started = await chat.createChatTurn(draft.chatId, { body: body.text }, options);
      const subject = started.job.subject;
      if (subject.type !== 'research_chat_turn') throw new MockApiError(500, 'The turn has no subject.', 'submitDraftTurn');
      // The question of the helper's turn opens its timeline, as the backend reads it from the native thread.
      context.state.gallery.turnQuestions[subject.turnId] = { text: body.text.trim(), images: [], ...(body.button ? { button: true } : {}) };
      helperPass(draft, body.text, subject.turnId, body.place, Boolean(body.button));
      return { chatId: draft.chatId, turnId: subject.turnId, jobId: started.jobId };
    },
  };
  return { ...operations, helperNoteDraft, helperPlanDraft };
}
