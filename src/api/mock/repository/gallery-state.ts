import type { Schema } from '../../../shared/api';
import { now } from './common';
import { projectId as whitePortProjectId } from './fixtures';

/** A drawing of the mock workshop: its versions are the images of its branch (specs/2026-11-02-image-workshop.md IW5). */
export type MockDrawing = {
  id: string;
  projectId: string;
  idea: string;
  brief: Schema<'DrawingBrief'>;
  revision: number;
  branch: number | null;
  current: number | null;
  chatId: string | null;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
  /** «Нарисовать» sent the turn: the reads of the page left until the turn ends and starts the drawing (D3.7). */
  drawAfterReads?: number;
};

type SagaImage = Schema<'SagaImage'>;
type SagaImageLayer = Schema<'SagaImageLayer'>;
type SagaImageCheck = Schema<'SagaImageCheck'>;

/** What the start of a drawing or a layer spent of its author's right, until it is given back (R13.6). */
export type MockCharge = 'trial' | 'quota' | null;

/** A region edit of a mock drawing with its author (specs/2026-10-29-saga-illustrations.md R8.15). */
export type MockImageLayer = Omit<SagaImageLayer, 'canDelete' | 'partialUrl'> & {
  createdBy: string;
  partialFrame: number;
  /** The facts the render with this layer is checked against (R12.1). */
  facts?: string[];
  charge?: MockCharge;
};

/** Drawing K of a project in the mock gallery, with its author; addresses and rights are derived. */
export type MockSagaImage = Omit<SagaImage, 'references' | 'canDelete' | 'layers' | 'fileUrl' | 'screenUrl' | 'thumbUrl' | 'partialUrl'> & {
  projectId: string;
  createdBy: string;
  /** The chat turn whose saga.draw started the drawing (R8.1); null outside a turn. */
  turnId?: string | null;
  partialFrame: number;
  layers: MockImageLayer[];
  /** The facts of the text the drawing is checked against and the render its check judged (R12.1–R12.3). */
  facts?: string[];
  checkRevision?: number;
  charge?: MockCharge;
  /** The size an enlarged version comes out with (R17.1). */
  enlargedSize?: { width: number; height: number };
  /** A variant after the first of its group: the group is paid for once, by the first (R21.4). */
  variantPaid?: boolean;
};

/** A drawing or a layer that ended draw_limited or draw_failed gave its place in images_per_day back (R13.6). */
export function spendsImage(item: Pick<MockSagaImage | MockImageLayer, 'errorCode'> & Pick<Partial<MockSagaImage>, 'variantPaid'>) {
  return item.errorCode !== 'draw_limited' && item.errorCode !== 'draw_failed' && !item.variantPaid;
}

/** The check the mock Job starts on a ready render: the facts without verdicts (R12.2). */
export function pendingCheck(facts: string[]): SagaImageCheck {
  return {
    status: 'pending', total: facts.length, ok: 0, notVisible: 0, contradicts: 0, defects: 0,
    facts: facts.map((text) => ({ text, verdict: null, note: null })), defectsList: [],
  };
}

/** The mock critic sees every fact on the render and no defect. */
export function settledCheck(check: SagaImageCheck): SagaImageCheck {
  if (check.status !== 'pending') return check;
  return {
    ...check, status: 'done', ok: check.total,
    facts: check.facts.map((fact) => ({ ...fact, verdict: 'ok', note: 'видно на рисунке' })),
  };
}

export type MockImageReference = Schema<'SagaImageReference'> & {
  projectId: string;
  number: number;
};

/** The right of a mock account as the administrator left it and the trial drawings left. */
export type MockIllustrationRight = {
  granted: 'none' | 'active' | 'blocked';
  trialLeft: number;
  /** The last Moscow day of active access, inclusive; null — without end. */
  activeUntil: string | null;
};

export type MockAccessRequest = Schema<'IllustrationAccessRequest'> & { userId: string };

export type MockIllustrationAccessState = {
  rights: Record<string, MockIllustrationRight>;
  requests: MockAccessRequest[];
  /** Projects whose free first style sketches were taken (R11.2, decision 29.10). */
  freeSketches: Record<string, true>;
  nextRequest: number;
};

export type MockGalleryState = {
  images: MockSagaImage[];
  /** The drawings of the workshop, one per branch with a drawing (specs/2026-11-02-image-workshop.md IW5). */
  drawings: MockDrawing[];
  references: MockImageReference[];
  styles: Record<string, string | null>;
  /** The style sample of each project, a number of its gallery (R19.1). */
  styleImages: Record<string, number | null>;
  lastNumber: Record<string, number>;
  /**
   * The chat turns whose question asks to draw: the chat draws nothing and answers them with a link to the drawing
   * workshop (specs/2026-11-02-image-workshop.md IW7.3).
   */
  drawingRequests: Record<string, true>;
  nextLayer: number;
  /** The right of each account to illustrations and the requests for it (R11). */
  access: MockIllustrationAccessState;
  /**
   * The author's own images (R15.4, R15.5): whether the backend switches uploads on and the edition
   * of the documents each account confirmed the rights to its images for.
   */
  uploads: { enabled: boolean; consents: Record<string, string> };
  /** The images attached to the question of each chat turn, for its question in the timeline (R15.9). */
  turnQuestions: Record<string, { text: string; images: number[]; attachment?: { number: number; title: string; words: number } }>;
  /** The corrections of each chat turn, for the timeline (specs/2026-10-30-chat-steering.md). */
  turnSteers: Record<string, { steerId: string; text: string; images: number[]; firstPhoto?: number }[]>;
  /** The revision of the description each turn of a drawing's helper wrote, for its `drawing.describe` in the timeline (D6.7). */
  turnDescribes: Record<string, number>;
};

const maraFacts = ['волосы Мары рыжие, до плеч', 'на Маре синий плащ', 'родинка на левой щеке Мары'];
const [onThePier, whiteSails, evening] = ['Мара стоит на пристани', 'за Марой корабль с белыми парусами', 'вечер, закатный свет'] as const;
const pierFacts = [onThePier, whiteSails, evening];

/** What the agent asked the image model to draw — technical, unlike the captions the author sees (R8.17). */
const maraPortrait = 'Портрет по пояс, ракурс в три четверти, камера на высоте глаз. Мара: рыжие волосы до плеч, синий плащ, родинка на левой щеке. Нейтральный фон, мягкий свет.';
const pierScene = 'Общий план, камера чуть ниже уровня глаз. Мара стоит на краю пристани, за ней корабль с белыми парусами. Вечер, закатный свет.';

/** The pier scene contradicts the text in one place and has one defect, for the web to show both. */
const pierCheck: SagaImageCheck = {
  status: 'done', total: 3, ok: 2, notVisible: 0, contradicts: 1, defects: 1,
  facts: [
    { text: onThePier, verdict: 'ok', note: 'Мара на краю пристани' },
    { text: whiteSails, verdict: 'contradicts', note: 'паруса корабля тёмные', region: { x: 0.58, y: 0.08, w: 0.3, h: 0.4 } },
    { text: evening, verdict: 'ok', note: 'низкое солнце, тёплый свет' },
  ],
  defectsList: [{ note: 'шесть пальцев на правой руке Мары', region: { x: 0.36, y: 0.55, w: 0.08, h: 0.1 } }],
};

/** A drawing made of a branch before the workshop: no idea, its description one helper piece. */
function mockMigratedDrawing(authorId: string, id: string, number: number, title: string, kind: MockDrawing['brief']['kind'], prompt: string): MockDrawing {
  return {
    id, projectId: whitePortProjectId, idea: '', revision: 1, branch: number, current: number, chatId: null,
    brief: { title, kind, aspect: null, chapter: null, subjects: ['Мара'], segments: [{ text: prompt, origin: 'helper' }], facts: [], staging: [], references: null, images: [] },
    createdBy: authorId, createdAt: now, updatedAt: now,
  };
}

/** The gallery of the synthetic saga: Мара's portrait is her reference, the second drawing a scene from it. */
export function createInitialGallery(authorId: string): MockGalleryState {
  const base = {
    projectId: whitePortProjectId, createdBy: authorId, aspect: 'square' as const, style: 'акварель, тёплый свет',
    revisedPrompt: null, baseImage: null, chatId: null, width: 1024, height: 1024, errorCode: null,
    errorMessage: null, retryAt: null, createdAt: now, readyAt: now, phase: 'ready' as const, partialFrame: 0, sources: [],
    sketch: false, renderRevision: 0,
  };
  return {
    images: [
      {
        ...base, layers: [], number: 1, branch: 1, status: 'ready', kind: 'character', chapter: 'b1c1',
        subjects: [{ key: 'мара', name: 'Мара' }], referenceImages: [],
        title: 'Портрет Мары',
        prompt: maraPortrait,
        facts: maraFacts, checkRevision: 0, check: settledCheck(pendingCheck(maraFacts)),
      },
      {
        ...base, layers: [], number: 2, branch: 2, status: 'ready', kind: 'scene', aspect: 'landscape',
        chapter: 'b2c12', subjects: [{ key: 'мара', name: 'Мара' }], referenceImages: [1], width: 1536,
        sources: ['b2c12/3-5'],
        title: 'Мара у пристани',
        prompt: pierScene,
        facts: pierFacts, checkRevision: 0, check: pierCheck,
      },
    ],
    // The drawings of the two branches as the migration to the workshop made them: current their only version.
    drawings: [
      mockMigratedDrawing(authorId, '00000000-0000-4000-9000-000000000001', 1, 'Портрет Мары', 'character', maraPortrait),
      mockMigratedDrawing(authorId, '00000000-0000-4000-9000-000000000002', 2, 'Мара у пристани', 'scene', pierScene),
    ],
    references: [{ projectId: whitePortProjectId, number: 1, subjectKey: 'мара', subjectName: 'Мара', fromChapter: null }],
    styles: { [whitePortProjectId]: 'акварель, тёплый свет' },
    styleImages: {},
    lastNumber: { [whitePortProjectId]: 2 },
    nextLayer: 1,
    access: {
      rights: {},
      // A pending request of the new author of the admin panel's writers, for the tab «Запросы».
      requests: [{
        id: 'illustration-request-1', userId: '11111111-0000-4000-8000-000000000002', status: 'pending',
        message: 'Хочу иллюстрировать главы для читателей', createdAt: now, sentAt: now, decidedAt: null,
      }],
      freeSketches: {},
      nextRequest: 2,
    },
    uploads: { enabled: true, consents: {} },
    turnQuestions: {},
    drawingRequests: {},
    turnSteers: {},
    turnDescribes: {},
  };
}

export const mockAspectSize: Record<SagaImage['aspect'], readonly [number, number]> = {
  square: [1024, 1024], portrait: [1024, 1536], landscape: [1536, 1024], tall: [941, 1672], wide: [1672, 941], panorama: [1916, 821], banner: [2172, 724],
};

/** A 1×1 PNG served as every mock drawing file. */
export const mockImagePNG = Uint8Array.from(atob(
  'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
), (character) => character.charCodeAt(0));
