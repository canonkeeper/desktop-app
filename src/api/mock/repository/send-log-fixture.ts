// SPDX-License-Identifier: AGPL-3.0-only
import type { Schema } from '../../../shared/api';

type SendLogRow = Schema<'SendLogRow'>;
type SendLogSending = Schema<'SendLogSending'>;

/** A row of the journal of the mock with its sendings (specs/2026-11-14-desk-journal.md JR2). */
export interface MockSendLogRow {
  row: SendLogRow;
  sendings: SendLogSending[];
}

const hour = 60 * 60 * 1000;

/**
 * The journal of the computer of the mock (specs/2026-11-14-desk-journal.md JR2.2): a question of the made-up saga «Северный
 * предел» this morning, the reading of its first book yesterday and a check of the connection — at the clock of the mock.
 */
export function mockSendLog(now = Date.now()): MockSendLogRow[] {
  const today = new Date(now);
  today.setHours(9, 40, 0, 0);
  const at = (from: Date, minutes: number) => new Date(from.getTime() + minutes * 60_000).toISOString();
  const yesterday = new Date(today.getTime() - 24 * hour);
  yesterday.setHours(18, 22, 0, 0);
  const question: SendLogRow = {
    key: '0b6a7c8e-1f2a-4b3c-8d9e-0a1b2c3d4e5f|today', kind: 'question', startedAt: at(today, 0), lastAt: at(today, 1),
    saga: { title: 'Северный предел', deleted: false }, chat: { title: 'Кто стоит на заставе?', deleted: false },
    whatWent: 'ваш вопрос; Книга 2, гл. 12 „Белый порт“, абзацы 3–9', recipients: ['provider.example.com'], bytes: 6_200, sendings: 2,
    running: false, firstTime: false, own: { requests: 2, tokens: 48_000, estimated: false, costs: [{ currency: 'RUB', micros: 3_000_000 }] },
  };
  const check: SendLogRow = {
    key: '1c7b8d9f-2a3b-4c4d-9e0f-1b2c3d4e5f60|today', kind: 'check', startedAt: at(today, -10), lastAt: at(today, -9), saga: null, chat: null,
    whatWent: 'без вашего текста', recipients: ['provider.example.com'], bytes: 1_200, sendings: 6, running: false, firstTime: false,
    own: { requests: 6, tokens: 3_100, estimated: false, costs: [] },
  };
  const reading: SendLogRow = {
    key: '2d8c9e0a-3b4c-4d5e-8f1a-2c3d4e5f6071|yesterday', kind: 'reading', startedAt: at(yesterday, 0), lastAt: at(yesterday, 40),
    saga: { title: 'Северный предел', deleted: false }, chat: null, whatWent: 'Книга 1 — 4 главы целиком', recipients: ['provider.example.com'],
    bytes: 48_000, sendings: 8, running: false, firstTime: false, own: { requests: 8, tokens: 41_000, estimated: true, costs: [] },
  };
  return [
    {
      row: question,
      sendings: [
        { at: at(today, 0), recipient: 'provider.example.com', bytes: 2_100, whatWent: 'ваш вопрос', firstTime: false, outcome: null, repeat: false },
        {
          at: at(today, 1), recipient: 'provider.example.com', bytes: 4_100, whatWent: 'Книга 2, гл. 12 „Белый порт“, абзацы 3–9', firstTime: false,
          outcome: null, repeat: false,
        },
      ],
    },
    {
      row: check,
      sendings: Array.from({ length: 6 }, (_, index) => ({
        at: at(today, -10 + index / 10), recipient: 'provider.example.com', bytes: 200, whatWent: index === 0 ? 'определение протокола, без вашего текста' : 'без вашего текста',
        firstTime: false, outcome: null, repeat: false,
      })),
    },
    {
      row: reading,
      sendings: Array.from({ length: 8 }, (_, index) => ({
        at: at(yesterday, index * 5), recipient: 'provider.example.com', bytes: 6_000, whatWent: `Книга 1, гл. ${Math.floor(index / 2) + 1} целиком`,
        firstTime: false, outcome: index === 3 ? 'ответ оборвался' : null, repeat: index === 4,
      })),
    },
  ];
}

/**
 * A full journal (the scenario sendLog: 'full', specs/2026-11-14-desk-journal.md JR3.1): the journal of the computer of the
 * mock, a reading of a second saga that goes on now, a question of it that went to another helper for the first time, the
 * readings of Claude Code, and enough checks of the connection over the days before for a second page.
 */
export function mockFullSendLog(now = Date.now()): MockSendLogRow[] {
  const base = mockSendLog(now);
  const today = new Date(now);
  today.setHours(10, 5, 0, 0);
  const at = (from: Date, minutes: number) => new Date(from.getTime() + minutes * 60_000).toISOString();
  const second = { title: 'Переправа', deleted: false };
  const going: MockSendLogRow = {
    row: {
      key: '3e9d0f1b-4c5d-4e6f-9a2b-3d4e5f607182|today', kind: 'reading', startedAt: at(today, 0), lastAt: at(today, 3), saga: second, chat: null,
      whatWent: 'Книга 1 — 3 главы целиком', recipients: ['provider.example.com'], bytes: 18_000, sendings: 3, running: true, firstTime: false,
      own: { requests: 3, tokens: 15_000, estimated: false, costs: [] },
    },
    sendings: Array.from({ length: 3 }, (_, index) => ({
      at: at(today, index), recipient: 'provider.example.com', bytes: 6_000, whatWent: `Книга 1, гл. ${index + 1} целиком`, firstTime: false,
      outcome: null, repeat: false,
    })),
  };
  const first: MockSendLogRow = {
    row: {
      key: '4fae1a2c-5d6e-4f70-8b3c-4e5f60718293|today', kind: 'question', startedAt: at(today, -5), lastAt: at(today, -4), saga: second,
      chat: { title: 'Где переправа?', deleted: false }, whatWent: 'ваш вопрос', recipients: ['provider.example.com', 'сервер CanonKeeper'],
      bytes: 24_000, sendings: 2, running: false, firstTime: true, own: { requests: 1, tokens: 9_000, estimated: false, costs: [] },
    },
    sendings: [
      { at: at(today, -5), recipient: 'provider.example.com', bytes: 8_000, whatWent: 'ваш вопрос', firstTime: false, outcome: null, repeat: false },
      { at: at(today, -4), recipient: 'сервер CanonKeeper', bytes: 16_000, whatWent: 'прежний разговор этого чата', firstTime: true, outcome: null, repeat: false },
    ],
  };
  const checks: MockSendLogRow[] = Array.from({ length: 60 }, (_, index) => {
    const day = new Date(today.getTime() - (index + 2) * 24 * hour);
    day.setHours(8, 0, 0, 0);
    const id = `5${String(index).padStart(7, '0')}-6e7f-4a81-9c4d-5f6071829304`;
    return {
      row: {
        key: `${id}|${day.toISOString().slice(0, 10)}`, kind: 'check', startedAt: day.toISOString(), lastAt: day.toISOString(), saga: null, chat: null,
        whatWent: 'без вашего текста', recipients: ['provider.example.com'], bytes: 1_200, sendings: 1, running: false, firstTime: false,
        own: { requests: 1, tokens: 500, estimated: false, costs: [] },
      },
      sendings: [{ at: day.toISOString(), recipient: 'provider.example.com', bytes: 1_200, whatWent: 'без вашего текста', firstTime: false, outcome: null, repeat: false }],
    };
  });
  // The readings of a program of the author (specs/2026-11-15-desk-mcp.md MC4.4): a chapter by the six pages of its answer, and
  // the titles of the sagas — a row of its own without a saga.
  const program: MockSendLogRow = {
    row: {
      key: '6abf2b3d-6e7f-4081-9c4d-5f6071829304|today', kind: 'program', startedAt: at(today, -20), lastAt: at(today, -18), saga: second, chat: null,
      whatWent: 'Книга 1, гл. 3 „Переправа“, страницами ответа', recipients: ['Claude Code'], bytes: 216_000, sendings: 6, running: false,
      firstTime: false, own: null,
    },
    sendings: Array.from({ length: 6 }, (_, index) => ({
      at: at(today, -20 + index / 3), recipient: 'Claude Code', bytes: 36_000, whatWent: `Книга 1, гл. 3 „Переправа“ · страница ${index + 1} из 6 ответа`,
      firstTime: false, outcome: null, repeat: false,
    })),
  };
  const sagas: MockSendLogRow = {
    row: {
      key: '7bc03c4e-7f80-4192-8d5e-607182930415|today', kind: 'program', startedAt: at(today, -21), lastAt: at(today, -21), saga: null, chat: null,
      whatWent: 'названия саг: 2', recipients: ['Claude Code'], bytes: 180, sendings: 1, running: false, firstTime: false, own: null,
    },
    sendings: [{ at: at(today, -21), recipient: 'Claude Code', bytes: 180, whatWent: 'названия саг: 2', firstTime: false, outcome: null, repeat: false }],
  };
  return [going, first, program, sagas, ...base, ...checks].sort((a, b) => b.row.startedAt.localeCompare(a.row.startedAt));
}
