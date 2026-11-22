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

  // The server takes the shorthand in any case, the key failed as failure, and
  // an exit code as a string of digits.
  it('reads what the server reads', () => {
    expect(readContinueOn(' Failed ')).toEqual({ failure: true });
    expect(readContinueOn({ failed: true })).toEqual({ failure: true });
    expect(readContinueOn({ exit_code: '75' })).toEqual({ exitCodes: [75] });
    expect(readContinueOn('failure')).toBeUndefined();
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
    // The server refuses the key precondition.
    expect(readPreconditions({ precondition: 'test -f x' })).toEqual([]);
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
    expect(edgeCondition(failed, 'verify_1', 'verify_1')).toMatchObject({
      when: 'failed',
      step: 'verify_1',
    });
    const passed = pre({
      preconditions: [{ condition: '${verify_1.exitCode}', expected: '0' }],
    });
    expect(edgeCondition(passed, 'verify_1', 'verify_1')?.when).toBe(
      'succeeded'
    );
  });

  // The server resolves `${id.field}` only: `${steps.id.field}` looks up a
  // step whose id is "steps".
  it('gives nothing for a reference that the server does not resolve', () => {
    const prefixed = pre({
      preconditions: [
        { condition: '${steps.verify_1.exit_code}', expected: '0' },
      ],
    });
    expect(edgeCondition(prefixed, 'verify_1', 'verify_1')).toBeUndefined();
  });

  it('reads another exit code and any other test of the step', () => {
    const exit = pre({
      preconditions: [{ condition: '${a.exit_code}', expected: '75' }],
    });
    expect(edgeCondition(exit, 'a', 'a')).toMatchObject({
      when: 'exit',
      value: '75',
    });
    const match = pre({
      preconditions: [
        { condition: '${a.stdout}', expected: 'ok', negate: true },
      ],
    });
    expect(edgeCondition(match, 'a', 'a')).toMatchObject({
      when: 'match',
      value: 'stdout != ok',
    });
  });

  it('keeps the negation of an exit code test against a pattern', () => {
    const negated = pre({
      preconditions: [
        { condition: '${a.exit_code}', expected: 're:^[12]$', negate: true },
      ],
    });
    expect(edgeCondition(negated, 'a', 'a')).toMatchObject({
      when: 'match',
      value: 'exit_code != re:^[12]$',
    });
  });

  it('gives nothing when no precondition reads the source', () => {
    const other = pre({
      preconditions: [{ condition: '${b.exit_code}', expected: '0' }],
    });
    expect(edgeCondition(other, 'a', 'a')).toBeUndefined();
    expect(
      edgeCondition(pre({ preconditions: 'x' }), 'a', 'a')
    ).toBeUndefined();
  });
});

// A verify loop: a step, its check, and a fix that runs only if the check
// failed.
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
      id: 'verify_1',
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
    // A reference by name: the server leaves it unresolved, so the test is
    // always true and the link carries no condition.
    makeStep({
      key: 'steps[3]',
      explicitName: 'code_3',
      depends: ['code_2'],
      value: {
        run: 'y',
        preconditions: [
          { condition: '${code_2.exit_code}', expected: '0', negate: true },
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
    expect(byId['depends:steps[2]->steps[3]']?.condition).toBeUndefined();
  });
});
