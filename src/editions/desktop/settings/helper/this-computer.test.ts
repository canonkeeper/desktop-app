// SPDX-License-Identifier: AGPL-3.0-only
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { isThisComputer, plainToTheNetwork } from './this-computer';

const table = JSON.parse(readFileSync(path.join(process.cwd(), 'backend/internal/domain/aisettings/testdata/local-hosts.json'), 'utf8')) as {
  hosts: Array<{ host: string; local: boolean; why: string }>;
};

// specs/2026-11-19-desk-trust.md TW4.1: the page says «этот компьютер» by the table of the server.
describe('this computer', () => {
  it.each(table.hosts.map((row) => [row.host, row.local, row.why] as const))('%s — %s (%s)', (host, local) => {
    expect(isThisComputer(host)).toBe(local);
  });
});

// TW4.2: an address of http to a host that is not this computer goes without encryption; https, this computer and no address
// say nothing.
describe('an address without encryption', () => {
  it.each([
    ['http://192.168.1.10:11434/v1', true],
    ['http://nas.local:8080/v1', true],
    ['http://LLM.example.test/v1', true],
    [' http://10.0.0.5/v1 ', true],
    ['http://localhost:11434/v1', false],
    ['http://LOCALHOST:11434/v1', false],
    ['http://127.0.0.1:1234/v1', false],
    ['http://[::1]:11434/v1', false],
    ['https://192.168.1.10/v1', false],
    ['https://llm.example.com/v1', false],
    ['llm.example.com', false],
    ['', false],
  ] as const)('%s — %s', (address, plain) => {
    expect(plainToTheNetwork(address)).toBe(plain);
  });
});
