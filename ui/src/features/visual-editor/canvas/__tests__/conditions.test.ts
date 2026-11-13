// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { buildCanvasGraph } from '../graph';
import {
  edgeCondition,
  readContinueOn,
  readPreconditions,
} from '../conditions';
import type { CanvasNodeWithRoutes } from '../types';
import { makeModel, makeNames, makeStep } from './fixtures';

describe('readContinueOn', () => {
  it('reads the shorthand and the map', () => {
    expect(readContinueOn('failed')).toEqual({ failure: true });
    expect(readContinueOn('skipped')).toEqual({ skipped: true });
    expect(
      readContinueOn({
        failure: true,
        exit_code: [1, 2],
        output: 'x',
        mark_success: true,
      })
    ).toEqual({
      failure: true,
      exitCodes: [1, 2],
      output: true,
      markSuccess: true,
    });
    expect(readContinueOn({ exit_code: 3 })).toEqual({ exitCodes: [3] });
  });

  it('gives nothing for a value that continues after no cause', () => {
    expect(readContinueOn(undefined)).toBeUndefined();
    expect(readContinueOn({ failure: false })).toBeUndefined();
    expect(readContinueOn({ mark_success: true })).toBeUndefined();
    expect(readContinueOn(42)).toBeUndefined();
  });
});

describe('readPreconditions', () => {
  it('reads a command, a map and a list of them', () => {
    expect(readPreconditions({ preconditions: 'test -f x' })).toEqual([
      { text: 'test -f x', negate: false },
    ]);
    expect(
      readPreconditions({
        preconditions: [
          { condition: '${A}', expected: 'yes' },
          { condition: '${B}', expected: '0', negate: true },
          { condition: 'test -d y' },
          7,
        ],
      }).map((pre) => pre.text)
    ).toEqual(['${A} = yes', '${B} != 0', 'test -d y']);
    expect(readPreconditions({})).toEqual([]);
  });
});

describe('edgeCondition', () => {
  const pre = (value: Record<string, unknown>) => readPreconditions(value);

  it('reads a test of the exit code against 0 as failed or succeeded', () => {
    const failed = pre({
      preconditions: [
        { condition: '${verify_1.exit_code}', expected: '0', negate: true },
      ],
    });
    expect(edgeCondition(failed, ['verify_1'], 'verify_1')).toMatchObject({
      when: 'failed',
      step: 'verify_1',
    });
    const passed = pre({
      preconditions: [
        { condition: '${steps.verify_1.exit_code}', expected: '0' },
      ],
    });
    expect(edgeCondition(passed, ['verify_1'], 'verify_1')?.when).toBe(
      'succeeded'
    );
  });

  it('reads another exit code and any other test of the step', () => {
    const exit = pre({
      preconditions: [{ condition: '${a.exit_code}', expected: '75' }],
    });
    expect(edgeCondition(exit, ['a'], 'a')).toMatchObject({
      when: 'exit',
      value: '75',
    });
    const match = pre({
      preconditions: [
        { condition: '${a.stdout}', expected: 'ok', negate: true },
      ],
    });
    expect(edgeCondition(match, ['a'], 'a')).toMatchObject({
      when: 'match',
      value: 'stdout != ok',
    });
  });

  it('gives nothing when no precondition reads the source', () => {
    const other = pre({
      preconditions: [{ condition: '${b.exit_code}', expected: '0' }],
    });
    expect(edgeCondition(other, ['a'], 'a')).toBeUndefined();
    expect(
      edgeCondition(pre({ preconditions: 'x' }), ['a'], 'a')
    ).toBeUndefined();
  });
});

// The verify loop of examples/agent-worker-demo/dags/agent-dev-loop.yaml.
describe('the verify loop on the canvas', () => {
  const model = makeModel([
    makeStep({
      key: 'steps[0]',
      explicitName: 'code_1',
      value: { action: 'agent.run' },
    }),
    makeStep({
      key: 'steps[1]',
      explicitName: 'verify_1',
      depends: ['code_1'],
      value: { run: 'x', continue_on: { failure: true } },
    }),
    makeStep({
      key: 'steps[2]',
      explicitName: 'code_2',
      depends: ['verify_1'],
      value: {
        action: 'agent.run',
        preconditions: [
          { condition: '${verify_1.exit_code}', expected: '0', negate: true },
        ],
      },
    }),
  ]);
  const graph = buildCanvasGraph({
    model,
    names: makeNames(model),
    issues: { byStep: {}, dag: [] },
  });

  it('marks the step that continues and the step that has preconditions', () => {
    const nodes = graph.nodes as CanvasNodeWithRoutes[];
    expect(nodes[1]?.continueOn).toEqual({ failure: true });
    expect(nodes[2]?.preconditions).toEqual(['${verify_1.exit_code} != 0']);
    expect(nodes[0]?.continueOn).toBeUndefined();
    expect(nodes[0]?.preconditions).toBeUndefined();
  });

  it('puts "failed" on the link that the precondition reads, and on no other', () => {
    const byId = Object.fromEntries(graph.edges.map((edge) => [edge.id, edge]));
    expect(byId['depends:steps[1]->steps[2]']?.condition).toMatchObject({
      when: 'failed',
      step: 'verify_1',
    });
    expect(byId['depends:steps[0]->steps[1]']?.condition).toBeUndefined();
  });
});
