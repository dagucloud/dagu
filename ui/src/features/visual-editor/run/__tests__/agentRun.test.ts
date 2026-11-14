// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { components } from '@/api/v1/schema';
import { buildAgentRunInfo } from '../agentRun';

type Details = components['schemas']['DAGRunDetails'];

function run(extra: Partial<Details>): Details {
  return { name: 'agent', dagRunId: 'r1', ...extra } as Details;
}

describe('buildAgentRunInfo', () => {
  it('is null without a run and for a run that is not an agent run', () => {
    expect(buildAgentRunInfo(undefined)).toBeNull();
    expect(buildAgentRunInfo(null)).toBeNull();
    expect(buildAgentRunInfo(run({}))).toBeNull();
  });

  it('counts the turns, the calls of each tool and the task states', () => {
    const info = buildAgentRunInfo(
      run({
        agentEvents: [
          { turn: 1, kind: 'action', name: 'plan', status: 'succeeded' },
          { turn: 2, kind: 'action', name: 'build', status: 'failed' },
          { turn: 3, kind: 'action', name: 'build', status: 'succeeded' },
          { turn: 3, kind: 'task_status', name: 'built', status: 'completed' },
          { turn: 4, kind: 'rejected', name: 'nope' },
          { turn: 4, kind: 'ask_user' },
        ] as never,
        agentTasks: [
          { name: 'planned', status: 'completed' },
          { name: 'built', status: 'open' },
          { name: 'shipped', status: 'failed' },
        ] as never,
      })
    );
    expect(info).toEqual({
      iterations: 4,
      // A task event and a rejected call are no tool call.
      calls: { plan: 1, build: 2 },
      tasks: { planned: 'completed', built: 'open', shipped: 'failed' },
    });
  });

  it('counts a tool and a task named as a property of an object', () => {
    const info = buildAgentRunInfo(
      run({
        agentEvents: [
          { turn: 1, kind: 'action', name: 'toString', status: 'succeeded' },
        ] as never,
        agentTasks: [] as never,
      })
    );
    expect(info?.calls['toString']).toBe(1);
    expect(info?.calls['constructor']).toBeUndefined();
    expect(info?.tasks['constructor']).toBeUndefined();
  });

  it('gives zero turns for a run that has not decided yet', () => {
    expect(buildAgentRunInfo(run({ agentTasks: [] as never }))).toEqual({
      iterations: 0,
      calls: {},
      tasks: {},
    });
  });

  // The server omits both lists for an agent run with no task and no decision.
  it('shows an agent DAG run that has no task, with the calls it made', () => {
    const events = [
      { turn: 1, kind: 'action', name: 'plan', status: 'succeeded' },
    ] as never;
    expect(buildAgentRunInfo(run({ agentEvents: events }))).toEqual({
      iterations: 1,
      calls: { plan: 1 },
      tasks: {},
    });
    expect(buildAgentRunInfo(run({}), true)).toEqual({
      iterations: 0,
      calls: {},
      tasks: {},
    });
    expect(buildAgentRunInfo(null, true)).toBeNull();
  });
});
