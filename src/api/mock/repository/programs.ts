// SPDX-License-Identifier: AGPL-3.0-only
import type { OperationResponse } from '../../../shared/api';
import { MockApiError } from '../scenarios';
import type { MockRepositoryContext } from './context';

type ProgramAccess = OperationResponse<'getProgramAccess', '200'>;

/** The program of the bridge and the data directory of the computer of the mock (specs/2026-11-15-desk-mcp.md MC3.2). */
const program = '/usr/bin/canonkeeper-mcp';
const dataDir = '/home/author/.local/share/canonkeeper';
const entry = { command: program, args: [dataDir] };

/** The commands the server builds on Linux (domain/desktop.Commands). */
const commands: NonNullable<ProgramAccess['commands']> = {
  claudeAdd: `claude mcp add --scope user canonkeeper '${program}' '${dataDir}'`,
  claudeRemove: 'claude mcp remove --scope user canonkeeper',
  codexAdd: `codex mcp add canonkeeper -- '${program}' '${dataDir}'`,
  codexRemove: 'codex mcp remove canonkeeper',
  desktopEntry: JSON.stringify({ canonkeeper: entry }, null, 2).replace(/^\{\n|\n\}$/gu, '').replace(/^ {2}/gmu, ''),
  desktopFile: JSON.stringify({ mcpServers: { canonkeeper: entry } }, null, 2),
};

/** The reading of the sagas by the programs of the author on the computer of the mock: off until the author lets them. */
export function createProgramOperations(context: MockRepositoryContext) {
  function signedIn(operationId: 'getProgramAccess' | 'updateProgramAccess' | 'getClaudeDesktopExtension') {
    context.applyOperation(operationId);
    if (!context.state.session) throw new MockApiError(401, 'Сессия отсутствует.', operationId, 'auth.session_required');
  }

  function stateOf(): ProgramAccess {
    const programs = context.state.programs;
    return {
      enabled: programs.enabled, ever: programs.ever, open: programs.enabled, failed: false, system: 'linux', commands,
      extensionFile: null, programs: [], lastRead: null,
    };
  }

  async function getProgramAccess(): Promise<ProgramAccess> {
    signedIn('getProgramAccess');
    return stateOf();
  }

  async function updateProgramAccess(body: { enabled?: unknown } | undefined): Promise<ProgramAccess> {
    signedIn('updateProgramAccess');
    if (typeof body?.enabled !== 'boolean') throw new MockApiError(400, 'invalid request body', 'updateProgramAccess', 'validation.failed');
    context.state.programs = { enabled: body.enabled, ever: context.state.programs.ever || body.enabled };
    return stateOf();
  }

  /**
   * The file of the extension as the server of Windows builds it; the computer of the mock is Linux, and its page never asks
   * for it (extensionFile is null).
   */
  async function getClaudeDesktopExtension(): Promise<{ blob: Blob; fileName: string }> {
    signedIn('getClaudeDesktopExtension');
    return { blob: new Blob(['PK the package of the extension'], { type: 'application/zip' }), fileName: 'CanonKeeper-1.2.0.mcpb' };
  }

  return { getProgramAccess, updateProgramAccess, getClaudeDesktopExtension };
}
