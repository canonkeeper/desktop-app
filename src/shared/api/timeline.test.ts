import { describe, expect, it, vi } from 'vitest';
import {
  agentTimelineWebSocketUrl,
  parseAgentTimelineStreamEnvelope,
  streamAgentTimelineUrl,
} from './timeline';
import { OpenApiCanonKeeperClient } from './client';

describe('canonical timeline transport', () => {
  it('builds one exact Job stream URL without a role query', () => {
    const url = agentTimelineWebSocketUrl('project/one', 'job/one', '/api/v1', 'https://canon.test/app');

		expect(url).toBe('wss://canon.test/api/v1/projects/project%2Fone/jobs/job%2Fone/agent-timeline/stream');
    expect(url).not.toContain('role=');
    expect(streamAgentTimelineUrl).toBe(agentTimelineWebSocketUrl);
  });

  it('parses protocol envelopes without projecting Job state', () => {
    const parsed = parseAgentTimelineStreamEnvelope(JSON.stringify({
      cursor: 'tl_NA',
      event: {
        cursor: 'tl_NA',
        id: 'event-4',
        jobId: 'job-1',
        kind: 'turn_outcome',
        logicalSection: 0,
        outcome: 'failed',
        role: 'main',
      },
      type: 'event',
    }));

    expect(parsed.success).toBe(true);
    if (parsed.success) expect(parsed.data.type).toBe('event');
  });

  it('parses an incremental assistant delta envelope', () => {
    const parsed = parseAgentTimelineStreamEnvelope(JSON.stringify({
      cursor: 'tl_MQ',
      delta: {
        id: 'assistant-item-0000000000001',
        kind: 'assistant',
        jobId: 'job-1',
        logicalSection: 0,
        providerItemId: 'item-1',
        providerTurnId: 'turn-1',
        role: 'main',
        text: 'Перв',
      },
      type: 'delta',
    }));

    expect(parsed.success).toBe(true);
    if (parsed.success && parsed.data.type === 'delta') {
      expect(parsed.data.delta.text).toBe('Перв');
    }
  });

  it('parses live context-window usage without treating cumulative tokens as occupancy', () => {
    const parsed = parseAgentTimelineStreamEnvelope(JSON.stringify({
      context: {
        compacted: false,
        contextWindow: 128_000,
        providerTurnId: 'turn-1',
        role: 'main',
        usedTokens: 96_000,
      },
      type: 'context',
    }));

    expect(parsed.success).toBe(true);
    if (parsed.success && parsed.data.type === 'context') {
      expect(parsed.data.context.usedTokens).toBe(96_000);
      expect(parsed.data.context.contextWindow).toBe(128_000);
    }
  });

  it('uses the generated snapshot operation with the exact project and Job path', async () => {
    const requests: Request[] = [];
    const fetch = vi.fn(async (request: Request) => {
      requests.push(request);
      return new Response(JSON.stringify({
        events: [],
        needsReconcile: false,
			nextCursor: null,
			eof: true,
      }), { headers: { 'content-type': 'application/json' }, status: 200 });
    });
    const client = new OpenApiCanonKeeperClient({ baseUrl: 'https://canon.test/api/v1', fetch });

    await client.getAgentTimeline('project-1', 'job-1', { cursor: 'tl_MQ' });

	expect(requests[0]?.url).toContain('/api/v1/projects/project-1/jobs/job-1/agent-timeline?cursor=tl_MQ');
		expect(requests[0]?.url).not.toContain('role=');
	});

	it('rejects numeric cursors at the public boundary', () => {
		const parsed = parseAgentTimelineStreamEnvelope(JSON.stringify({
			cursor: 4,
			event: {
				cursor: 4,
				id: 'event-4',
				jobId: 'job-1',
				kind: 'assistant',
				logicalSection: 0,
				role: 'main',
			},
			type: 'event',
		}));

		expect(parsed.success).toBe(false);
	});

  it('accepts native subagent and terminal events in server cursor order', () => {
		const parsed = parseAgentTimelineStreamEnvelope(JSON.stringify({
			snapshot: {
				eof: true,
				events: [
					{
						agentPath: 'reviewer-sources',
						agentThreadId: 'thread-child',
						cursor: 'tl_MQ',
						id: 'subagent-1:0',
						jobId: 'job-1',
						kind: 'subagent',
						logicalSection: 0,
						outcome: 'in_progress',
						providerTurnId: 'turn-1',
						role: 'main',
					},
					{
						cursor: 'tl_Mg',
						id: 'final-1:0',
						jobId: 'job-1',
						kind: 'turn_outcome',
						logicalSection: 0,
						outcome: 'completed',
						phase: 'final_answer',
						providerTurnId: 'turn-1',
						role: 'main',
						text: 'Готово',
					},
				],
				needsReconcile: false,
				nextCursor: null,
			},
			type: 'snapshot',
		}));

		expect(parsed.success).toBe(true);
		if (parsed.success && parsed.data.type === 'snapshot') {
			expect(parsed.data.snapshot.events.map((event) => event.kind)).toEqual(['subagent', 'turn_outcome']);
    }
  });

  it('accepts saga tool arguments and a result summary but not their wrong shapes', () => {
    const event = {
      cursor: 'tl_MQ',
      id: 'tool-1:0',
      jobId: 'job-1',
      kind: 'tool_result',
      logicalSection: 0,
      role: 'main',
      tool: { name: 'search', namespace: 'saga', status: 'completed', arguments: { queries: ['Шорох'] }, summary: '«Шорох» (any): 56 абзацев в 23 главах' },
    };
    const parsed = parseAgentTimelineStreamEnvelope(JSON.stringify({ cursor: 'tl_MQ', event, type: 'event' }));
    expect(parsed.success).toBe(true);
    if (parsed.success && parsed.data.type === 'event') {
      expect(parsed.data.event.tool).toMatchObject({ arguments: { queries: ['Шорох'] }, summary: '«Шорох» (any): 56 абзацев в 23 главах' });
    }
    const emoji = { ...event, tool: { ...event.tool, summary: '📖'.repeat(240) } };
    expect(parseAgentTimelineStreamEnvelope(JSON.stringify({ cursor: 'tl_MQ', event: emoji, type: 'event' })).success).toBe(true);
    for (const tool of [{ ...event.tool, summary: 3 }, { ...event.tool, summary: 'а'.repeat(241) }, { ...event.tool, arguments: ['Шорох'] }]) {
      expect(parseAgentTimelineStreamEnvelope(JSON.stringify({ cursor: 'tl_MQ', event: { ...event, tool }, type: 'event' })).success).toBe(false);
    }
  });

  it('rejects removed reference fields, event kinds and raw tool payloads', () => {
    const base = {
      cursor: 'tl_MQ',
      id: 'event-1',
      jobId: 'job-1',
      kind: 'assistant',
      logicalSection: 0,
      role: 'main',
    };
    for (const event of [
      { ...base, references: [] },
      { ...base, kind: 'job_outcome' },
      { ...base, kind: 'tool_result', tool: { name: 'read', namespace: 'saga', status: 'completed', arguments: '{}' } },
      { ...base, kind: 'tool_result', tool: { name: 'read', namespace: 'saga', status: 'completed', content: [{ type: 'inputText', text: 'raw' }] } },
    ]) {
      expect(parseAgentTimelineStreamEnvelope(JSON.stringify({ cursor: 'tl_MQ', event, type: 'event' })).success).toBe(false);
    }
  });
});

// specs/2026-10-30-chat-steering.md R3.1.
describe('a correction in the timeline', () => {
  it('reads the steer event with its images, anchor and id', () => {
    const envelope = parseAgentTimelineStreamEnvelope(JSON.stringify({
      type: 'snapshot', cursor: 'tl_1',
      snapshot: { eof: true, needsReconcile: false, nextCursor: null, events: [{
        id: 'steer-event', cursor: 'tl_1', kind: 'steer', jobId: 'job-1', role: 'main', logicalSection: 0,
        text: 'Без дракона', images: [90], firstPhoto: 3, steerId: 'steer-1',
      }] },
    }));
    expect(envelope).toMatchObject({ data: { snapshot: { events: [{ kind: 'steer', firstPhoto: 3, steerId: 'steer-1' }] } } });
  });
});
