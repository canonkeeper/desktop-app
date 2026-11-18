import type { OperationRequest, Schema } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';

type Step = Schema<'ReadingTestStep'>;
type Voted = Schema<'ReadingTestVoteResult'>;
type Operation = 'takeReadingTestPair' | 'voteReadingTestPair';

/**
 * The tests of the mock (specs/2026-11-05-reading-test.md). All their text is invented. The open test has no labels and
 * goes straight on after a choice; the test of the reveal has the same pairs with labels, the author against the model of
 * the product (RT5.4), and reveals each voted pair.
 */
export const mockReadingTestSlug = 'mock-compare-open-0001';
export const mockRevealReadingTestSlug = 'mock-compare-reveal-01';
export const mockClosedReadingTestSlug = 'mock-compare-closed-01';

/** The sketch of a chapter and the items both pieces cover (RT1.10); a pair without one shows only its context. */
const sketches: Record<number, { sketch: string[]; shown: number[] }> = {
  1: {
    sketch: ['Лодочник ждёт у переправы в тумане.', 'Приходит последний пассажир и просит перевезти.', 'На середине реки пассажир называет своё имя.'],
    shown: [1, 2],
  },
};

type MockSide = { condition: string; label?: string; paragraphs: string[] };
type MockPair = { number: number; context: string; a: MockSide; b: MockSide };

const pairs: MockPair[] = [
  {
    number: 1,
    context: 'Вечер у переправы. Лодочник ждёт последнего пассажира.',
    a: { condition: 'C', paragraphs: [
      'Туман лёг на реку раньше обычного, и лодочник перестал различать дальний берег. Он сел на борт, вынул из кармана кусок хлеба и стал ждать, хотя ждать было уже некого.',
      'Шаги он услышал прежде, чем увидел человека. Кто-то шёл по мосткам неторопливо, как ходят те, кому некуда опаздывать.',
      '— Перевезёшь? — спросил голос из тумана.',
      'Лодочник доел хлеб, стряхнул крошки в воду и только тогда кивнул.',
    ] },
    b: { condition: 'O', paragraphs: [
      'Над рекой рано сгустился туман, дальний берег исчез. Лодочник присел на борт и достал хлеб. Пассажиров больше не предвиделось, но он всё равно ждал.',
      'Сначала послышались шаги. Человек шёл по мосткам спокойно и никуда не спешил.',
      '— Перевезёшь? — раздалось из тумана.',
      'Лодочник кивнул, когда покончил с хлебом.',
    ] },
  },
  {
    number: 2,
    context: 'Рынок в день ярмарки. Девочка ищет брата.',
    a: { condition: 'O', paragraphs: [
      'На ярмарке было тесно. Девочка протискивалась между корзинами и звала брата, но её голос тонул в чужих голосах.',
      'У лавки с медными котлами она остановилась. Брат всегда любил смотреть, как в них отражается небо.',
    ] },
    b: { condition: 'C', paragraphs: [
      'Ярмарка шумела так, что собственного голоса было не слышно. Девочка лезла между корзинами, цеплялась платьем за прутья и кричала одно и то же имя.',
      'Возле медных котлов она встала как вкопанная: брат любил эти котлы, любил небо в них, круглое и маленькое.',
    ] },
  },
  {
    number: 3,
    context: 'Ночь на маяке. Смотритель слышит стук.',
    a: { condition: 'C', paragraphs: [
      'Стук повторился, когда смотритель уже решил, что ему почудилось. Три удара и тишина, будто кто-то внизу ждал ответа.',
    ] },
    b: { condition: 'P', paragraphs: [
      'Смотритель решил, что стук ему почудился, но тот повторился: три удара и пауза. Внизу явно ждали ответа.',
    ] },
  },
  {
    number: 4,
    context: 'Утро в горной деревне. Приходит письмо.',
    a: { condition: 'P', paragraphs: [
      'Письмо принёс мальчишка с соседнего склона. Старуха взяла его, не вытирая рук, и положила на подоконник рядом с луком.',
      'Прочла она его только к вечеру, когда ушли все, кто мог спросить, что там.',
    ] },
    b: { condition: 'O', paragraphs: [
      'Мальчик с соседнего склона принёс письмо. Старуха, не вытирая рук, положила его на подоконник.',
      'Вечером, когда никого не осталось, она его прочитала.',
    ] },
  },
  {
    number: 5,
    context: 'Постоялый двор. Двое спорят о дороге.',
    a: { condition: 'O', paragraphs: [
      '— По перевалу быстрее, — сказал первый.',
      '— По перевалу дольше живут те, кто туда не ходит, — ответил второй и подвинул к себе кувшин.',
    ] },
    b: { condition: 'C', paragraphs: [
      '— Через перевал короче.',
      'Второй подвинул к себе кувшин и усмехнулся:',
      '— Короче. Только дольше живут те, кто туда не ходит.',
    ] },
  },
];

/** RT5.4: in the test of the reveal side a is the author's and side b the model's, whatever the order a voter sees. */
const revealPairs: MockPair[] = pairs.map((pair) => ({
  ...pair,
  a: { condition: 'C', label: 'Автор', paragraphs: pair.a.paragraphs },
  b: { condition: 'O', label: 'Модель gpt-6.2-astra', paragraphs: pair.b.paragraphs },
}));

const tests: Record<string, MockPair[]> = { [mockReadingTestSlug]: pairs, [mockRevealReadingTestSlug]: revealPairs };

function hashOf(text: string) {
  let hash = 2166136261;
  for (const character of text) {
    hash ^= character.codePointAt(0) ?? 0;
    hash = Math.imul(hash, 16777619);
  }
  return hash >>> 0;
}

/** The public page of the blind comparison: the same order, choice and answers as the backend, on a simpler hash. */
export function createReadingTestOperations(context: MockRepositoryContext) {
  function open(operation: Operation, slug: string) {
    const test = tests[slug];
    if (!test) throw new MockApiError(404, 'this test is not available', operation, 'reading_test.unavailable');
    return test;
  }

  function checkVoter(operation: Operation, voterId: string) {
    if (!/^[0-9a-f]{32}$/.test(voterId)) throw new MockApiError(400, 'validation failed', operation, 'request.validation');
  }

  const turned = (voterId: string, number: number) => hashOf(`${voterId}:${number}`) % 2 === 1;

  function stepOf(slug: string, test: MockPair[], voterId: string, keep?: number): Step {
    const votes = context.state.readingTestVotes;
    const mine = new Set(votes.filter((vote) => vote.slug === slug && vote.voterId === voterId).map((vote) => vote.number));
    const unread = test.filter((pair) => !mine.has(pair.number));
    const step = { read: mine.size, total: test.length };
    if (unread.length === 0) return { ...step, pair: null };
    // RT2.5: each voter reads in a random order of his own, the order of the hashes of him and the pairs.
    const kept = unread.find((pair) => pair.number === keep);
    const chosen = kept ?? [...unread].sort((one, other) =>
      hashOf(`${voterId}:${one.number}`) - hashOf(`${voterId}:${other.number}`))[0]!;
    const [first, second] = turned(voterId, chosen.number) ? [chosen.b, chosen.a] : [chosen.a, chosen.b];
    const { sketch, shown } = sketches[chosen.number] ?? { sketch: [], shown: [] };
    return { ...step, pair: { number: chosen.number, context: chosen.context, sketch, shown, first: first.paragraphs, second: second.paragraphs } };
  }

  /** RT5.2: the labels in the order this voter saw the sides and the side the kept vote chose; nothing without labels. */
  function revealOf(pair: MockPair, voterId: string, kept: string): Voted['reveal'] {
    if (!pair.a.label || !pair.b.label) return undefined;
    const isTurned = turned(voterId, pair.number);
    const [first, second] = isTurned ? [pair.b, pair.a] : [pair.a, pair.b];
    return { first: first.label!, second: second.label!, chosen: first.condition === kept ? 'first' : 'second' };
  }

  async function takeReadingTestPair(slug: string, body: OperationRequest<'takeReadingTestPair'>): Promise<Step> {
    checkVoter('takeReadingTestPair', body.voterId);
    const test = open('takeReadingTestPair', slug);
    return stepOf(slug, test, body.voterId, body.keep);
  }

  async function voteReadingTestPair(slug: string, body: OperationRequest<'voteReadingTestPair'>): Promise<Voted> {
    checkVoter('voteReadingTestPair', body.voterId);
    const test = open('voteReadingTestPair', slug);
    const pair = test.find((candidate) => candidate.number === body.pairNumber);
    if (!pair) throw new MockApiError(400, 'validation failed', 'voteReadingTestPair', 'request.validation');
    const votes = context.state.readingTestVotes;
    // RT2.6: the first vote stays, and a repeat reveals what it chose.
    let kept = votes.find((vote) => vote.slug === slug && vote.number === pair.number && vote.voterId === body.voterId);
    if (!kept) {
      const chosen = (body.choice === 'first') !== turned(body.voterId, pair.number) ? pair.a : pair.b;
      kept = { slug, number: pair.number, voterId: body.voterId, chosen: chosen.condition, note: body.note ?? '' };
      votes.push(kept);
    }
    const reveal = revealOf(pair, body.voterId, kept.chosen);
    return { ...stepOf(slug, test, body.voterId), ...(reveal ? { reveal } : {}) };
  }

  return { takeReadingTestPair, voteReadingTestPair };
}
