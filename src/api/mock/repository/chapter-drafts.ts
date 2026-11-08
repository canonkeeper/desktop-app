import type { CanonKeeperApiClient, ChapterFileInput, ListChapterDraftsOptions, OperationRequest, RequiredIdempotencyOptions, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { createChatOperations } from './chat';
import type { createJobOperations } from './jobs';
import { advanceAgentRun, helperPlan, planQuestions, startAgentRun, startSeams, wordsOf, writtenSpansOf } from './chapter-draft-agent';
import { approvePlan, authorPlan, choicesOf, plansOf, setChoices } from './chapter-draft-plans';
import { advanceProofread, batchCategories, keepOriginal, originalOf, readChapterFile } from './chapter-draft-proofread';
import { keepVersion, listVersions, takenText, versionOf } from './chapter-draft-versions';
import { clone, rejectUnknownKeys } from './common';
import type { MockRepositoryContext } from './context';

type ChapterDraftOperations = Pick<CanonKeeperApiClient,
  'listChapterDrafts' | 'createChapterDraft' | 'getChapterDraft' | 'getChapterDraftPlan' | 'getChapterDraftMoment' | 'updateChapterDraft' | 'deleteChapterDraft' | 'startChapterDraftJob' | 'addChapterDraftToBook'
  | 'applyChapterDraftNote' | 'updateChapterDraftNote' | 'submitDraftTurn' | 'cancelJob'
  | 'openChapterEdit' | 'replaceChapterFromDraft' | 'revertChapterFromDraft' | 'reopenChapterFromBook'
  | 'listChapterDraftVersions' | 'getChapterDraftVersion' | 'takeChapterDraftVersion' | 'updateChapterDraftPlan' | 'updateChapterDraftMoment'
  | 'startChapterProofread' | 'loadChapterDraftFile' | 'applyChapterDraftNotes' | 'getChapterDraftExport'>;
type Draft = Schema<'ChapterDraft'>;
type Note = Schema<'ChapterDraftNote'>;
type WriteOperation = 'updateChapterDraft' | 'deleteChapterDraft' | 'startChapterDraftJob' | 'addChapterDraftToBook' | 'applyChapterDraftNote' | 'updateChapterDraftNote'
  | 'submitDraftTurn' | 'replaceChapterFromDraft' | 'revertChapterFromDraft' | 'reopenChapterFromBook' | 'takeChapterDraftVersion'
  | 'updateChapterDraftPlan' | 'updateChapterDraftMoment' | 'loadChapterDraftFile' | 'applyChapterDraftNotes';

const writers = ['owner', 'admin', 'editor'] as const;
const activeStatuses = new Set(['queued', 'running', 'canceling']);
/** The usual length of a chapter of the mock saga at any place. */
const mockTypicalWords = 2800;
/** The kinds of the findings of a check: a new check replaces the open ones of them (WA6.7). */
const checkKinds = new Set<Note['kind']>(['canon', 'new', 'style', 'craft', 'seam', 'plan_follow']);

/** The marks of what the author did not write are closed and not nested (W2.3). */
function validMarks(text: string) {
  let inside = false;
  for (const sign of text) {
    if (sign === '⟦' ? inside : sign === '⟧' && !inside) return false;
    if (sign === '⟦' || sign === '⟧') inside = sign === '⟦';
  }
  return !inside;
}
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
function caseOf(draft: Draft): Draft['case'] {
  if (draft.caseChoice) return draft.caseChoice;
  const words = draft.text.replace(/⟦[^⟧]*⟧/g, ' ').split(/[^\p{L}\p{M}\p{N}]+/u).filter(Boolean).length;
  const typical = draft.typicalWords ?? 2500;
  if (words < Math.max(150, 0.1 * typical)) return 'scratch';
  return words >= 0.8 * typical ? 'nearly_done' : 'continue';
}

/** The plan of the draft is stale once the idea changed since its latest version was made (WA4). */
const planIdeas = new WeakMap<Draft, string>();

function shown(draft: Draft): Draft {
  const plan = draft.plan ? { ...draft.plan, stale: planIdeas.get(draft) !== undefined && planIdeas.get(draft) !== draft.idea } : undefined;
  return clone({ ...draft, case: caseOf(draft), ...(plan ? { plan } : {}) });
}

// helperKey keys the session of the helper the first message opens, apart from the key of the message.
function helperKey(key: string) {
  return `draft-helper-${key}`;
}

/** The chapters in writing of the mock saga; every project starts with none. */
export function createChapterDraftOperations(
  context: MockRepositoryContext, chat: ReturnType<typeof createChatOperations>, jobs: Pick<ReturnType<typeof createJobOperations>, 'cancelJob'>,
) {
  const drafts = new Map<string, Draft[]>();
  let nextId = 1;
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
    planned(draft);
    draft.plan = { number: plan!.number, approved: false, stale: false };
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
      planned(draft);
      // A new version takes the approval away (WA4.2).
      draft.plan = { number: plan.number, approved: false, stale: false };
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
  /** The edit as the page sees it: the chapter changed in the book, deleted from it, or one step to give back. */
  const refreshEdit = (draft: Draft) => {
    const track = edits.get(draft);
    if (!draft.edits || !track) return;
    const chapter = editedChapter(draft);
    const changed = Boolean(chapter) && chapter!.publishedRevision !== track.revision;
    draft.edits = {
      chapterId: draft.edits.chapterId, bookWords: chapter ? wordsOf(paragraphsOf(chapter).join(' ')).length : 0, changed, gone: !chapter,
      canRevert: Boolean(track.previous) && Boolean(chapter) && !changed,
      ...(chapter ? { chapterTitle: chapter.title, bookId: chapter.bookId } : {}),
    };
  };
  /** The text of a draft takes the place of its chapter in the book, as a new revision (A8.7). */
  const putInBook = (draft: Draft, operationId: 'replaceChapterFromDraft' | 'revertChapterFromDraft', title: string, paragraphs: string[]) => {
    const chapter = editedChapter(draft);
    const track = edits.get(draft);
    if (!chapter || !track) throw new MockApiError(409, 'The chapter is gone.', operationId, 'chapter_draft.chapter_gone');
    if (chapter.publishedRevision !== track.revision) throw new MockApiError(409, 'The chapter changed.', operationId, 'chapter_draft.chapter_changed');
    const before = paragraphsOf(chapter);
    const revision = (chapter.publishedRevision ?? 0) + 1;
    chapter.title = title;
    chapter.paragraphs = paragraphs.map((text, index) => ({ id: `${chapter.id}-r${revision}-${index + 1}`, order: index + 1, kind: 'paragraph', text, markdown: text }));
    chapter.publishedRevision = revision;
    chapter.draftRevision = revision;
    edits.set(draft, { revision, ...(operationId === 'replaceChapterFromDraft' ? { previous: { paragraphs: before } } : {}) });
    draft.title = title;
    draft.text = paragraphs.join('\n\n');
    draft.revision += 1;
    draft.updatedAt = new Date().toISOString();
    refreshEdit(draft);
    return shown(draft);
  };
  /** A note applied to the working text: the text kept before it (WA7.2); a correction of the proofreading keeps none (PR4.3). */
  const applyTo = (draft: Draft, note: Note, text: string) => {
    if (note.kind !== 'proofread') keepVersion(draft, 'before_apply', draft.text);
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

  const operations: ChapterDraftOperations = {
    // A chapter of the author from a file or a text with its proofreading started (specs/2026-11-07-writing-proofread.md, PR2.1).
    async startChapterProofread(projectId, input: ChapterFileInput & { title?: string; bookId?: string; afterChapterId?: string }) {
      context.requireProjectAccess('startChapterProofread', projectId, [...writers]);
      const original = await readChapterFile(input, 'startChapterProofread');
      const at = new Date().toISOString();
      const created: Draft = {
        id: `draft-${nextId++}`, title: (input.title?.trim() || original.title).slice(0, 200), idea: '', kind: 'sketch', stage: 'text', text: original.text,
        place: placeOf(input.bookId, input.afterChapterId), typicalWords: mockTypicalWords, notes: [], revision: 1, case: 'nearly_done', caseChoice: 'nearly_done',
        author: context.state.user.displayName, createdAt: at, updatedAt: at,
      };
      keepOriginal(created, original);
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
      startProofread(draft, `draft-job-${nextId++}`);
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
      rejectUnknownKeys('listChapterDrafts', options, ['limit', 'cursor', 'signal']);
      const data = ofProject(projectId).map(({ id, title, kind, stage, job, revision, createdAt, updatedAt, text, edits: edit }) => ({
        id, title, kind, stage, words: wordsOf(text).length, ...(job ? { jobStatus: job.status } : {}), revision, ...(edit ? { edits: true } : {}), createdAt, updatedAt,
      }));
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
      return shown(draft);
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
        { name: 'Кира', why: 'герой точки зрения: первая в замысле' },
        { name: 'Тарен', aliases: ['мальчишка из гильдии'], why: 'в замысле' },
        { name: 'Ольм', aliases: ['стражник'], why: 'в последних событиях прошлой главы' },
        ...choices.filter((choice) => choice.action === 'include').map((choice) => ({ name: choice.entry, why: 'добавил автор' })),
      ].filter((entry) => !out.has(entry.name.toLowerCase()));
      return clone({
        inFrame: frame,
        sections: [
          { name: 'состояние на момент', text: 'Кира: в городе, при ней фонарь (гл. 1 ¶4). Ольм: стражник северных ворот, шрам на щеке (гл. 1 ¶8).' },
          { name: 'кто что знает', text: 'Кира не знает, куда ушёл Тарен (гл. 1 ¶6).' },
          { name: 'место', text: `Новая глава встаёт ${draft.place.afterChapterTitle ? `после «${draft.place.afterChapterTitle}»` : 'в конец саги'}.` },
        ],
        gaps: [{ kind: 'unknown_name', name: 'Гильдия', text: '«Гильдия» из замысла до места не названа ни в карточках, ни в материалах.' }],
        log: ['взято: 3 героя в кадре', ...[...out].map((name) => `убрал автор: ${name}`)],
        typicalWords: draft.typicalWords ?? 0, choices,
      });
    },
    async updateChapterDraft(projectId, draftId, body) {
      context.requireProjectAccess('updateChapterDraft', projectId, [...writers]);
      const draft = found(projectId, draftId, 'updateChapterDraft');
      open(draft, body.expectedRevision, 'updateChapterDraft');
      if (body.title !== undefined) draft.title = body.title.trim();
      if (body.idea !== undefined) draft.idea = body.idea;
      if (body.kind !== undefined) draft.kind = body.kind;
      if (body.text !== undefined && !validMarks(body.text)) {
        throw new MockApiError(400, 'Broken marks.', 'updateChapterDraft', 'chapter_draft.invalid_marks');
      }
      if (body.keepVersion && body.text === undefined) {
        throw new MockApiError(400, 'A version is kept only of an edit of the text.', 'updateChapterDraft', 'request.validation');
      }
      if (body.keepVersion) keepVersion(draft, 'before_delete', draft.text);
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
      rejectUnknownKeys('startChapterDraftJob', body, ['expectedRevision', 'mode', 'plan', 'scenes', 'from', 'tail', 'place', 'instruction']);
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
      const text = paragraphsOf(chapter).join('\n\n');
      const existing = ofProject(projectId).find((item) => item.edits?.chapterId === chapterId);
      if (existing) {
        refreshEdit(existing);
        return shown(existing);
      }
      const previous = context.state.chapters.filter((item) => item.bookId === chapter.bookId && item.order < chapter.order).sort((left, right) => right.order - left.order)[0];
      const at = new Date().toISOString();
      const created: Draft = {
        id: `draft-${nextId++}`, title: chapter.title, idea: '', kind: 'sketch', stage: 'text', text,
        place: placeOf(chapter.bookId, previous?.id), typicalWords: mockTypicalWords, notes: [], revision: 1, case: 'nearly_done',
        edits: { chapterId, bookWords: 0, changed: false, gone: false, canRevert: false },
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
      const paragraphs = draft.text.replace(/[⟦⟧]/gu, '').split('\n').map((line) => line.trim()).filter(Boolean);
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
      Object.assign(draft, { title: chapter.title, text: paragraphsOf(chapter).join('\n\n'), revision: draft.revision + 1 });
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
      if (!draft.title || !draft.text.replace(/[⟦⟧]/gu, '').trim()) throw new MockApiError(422, 'Nothing to add.', 'addChapterDraftToBook', 'chapter_draft.no_text');
      draft.added = { chapterId: `chapter-from-${draft.id}`, title: draft.title };
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
      keepVersion(draft, body.whole ? 'before_restore' : 'before_take', draft.text);
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
      } else if (body.status) {
        note.status = body.status;
      }
      note.updatedAt = new Date().toISOString();
      return clone(note);
    },
    // «Остановить» of the work on a chapter (WA5.10): the Job of a draft stops at once, what it wrote stays; any other
    // Job is stopped the way the mock stops Jobs.
    async cancelJob(jobId) {
      const owner = [...drafts.entries()].find(([, list]) => list.some((item) => item.job?.id === jobId));
      const draft = owner?.[1].find((item) => item.job?.id === jobId);
      if (!owner || !draft?.job) return jobs.cancelJob(jobId);
      context.requireProjectAccess('cancelJob', owner[0], [...writers]);
      if (activeStatuses.has(draft.job.status)) draft.job.status = 'canceled';
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
      context.state.gallery.turnQuestions[subject.turnId] = { text: body.text.trim(), images: [] };
      helperPass(draft, body.text, subject.turnId, body.place, Boolean(body.button));
      return { chatId: draft.chatId, turnId: subject.turnId, jobId: started.jobId };
    },
  };
  return { ...operations, helperNoteDraft, helperPlanDraft };
}
