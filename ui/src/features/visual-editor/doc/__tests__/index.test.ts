// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import type { DagEditOp } from '../../contracts';
import {
  yamlArraySteps,
  yamlMultiDoc,
  yamlRouter,
} from '../../testing/fixtures';
import * as doc from '../index';
import { applyTextEdits } from '../patch';

describe('applyEdit', () => {
  it('dispatches each op kind', () => {
    const cases: [string, DagEditOp][] = [
      [yamlArraySteps, { kind: 'addStep', step: { run: 'echo x' } }],
      [yamlArraySteps, { kind: 'deleteStep', step: 'steps[2]', rewire: false }],
      [
        yamlArraySteps,
        { kind: 'renameStep', step: 'steps[2]', newName: 'ship' },
      ],
      [yamlArraySteps, { kind: 'setStepId', step: 'steps[0]', id: 'fetch_id' }],
      [
        yamlArraySteps,
        { kind: 'addDependency', step: 'steps[2]', dependsOn: 'steps[0]' },
      ],
      [
        yamlArraySteps,
        { kind: 'removeDependency', step: 'steps[2]', dependsOn: 'steps[1]' },
      ],
      [
        yamlArraySteps,
        {
          kind: 'setStepField',
          step: 'steps[0]',
          path: ['description'],
          value: 'Get it',
        },
      ],
      [
        yamlArraySteps,
        {
          kind: 'replaceStepYaml',
          step: 'steps[0]',
          yaml: 'name: fetch\nrun: echo hi\n',
        },
      ],
      [
        yamlRouter,
        {
          kind: 'setRouteTargets',
          step: 'steps[0]',
          pattern: 're:.*',
          targets: ['server_error'],
        },
      ],
    ];
    for (const [text, op] of cases) {
      const result = doc.applyEdit(text, op);
      expect(result.ok, op.kind).toBe(true);
      if (result.ok) {
        expect(applyTextEdits(text, result.edits)).toBe(result.text);
      }
    }
  });

  it('gives noop when nothing needs conversion', () => {
    const result = doc.applyEdit(yamlArraySteps, {
      kind: 'convertStepsToExplicitForm',
    });
    expect(result).toMatchObject({ ok: false, code: 'noop' });
  });

  it('gives unknownStep for a missing key', () => {
    const result = doc.applyEdit(yamlArraySteps, {
      kind: 'setStepField',
      step: 'steps[9]',
      path: ['run'],
      value: 'x',
    });
    expect(result).toMatchObject({ ok: false, code: 'unknownStep' });
  });

  // Every op must leave document 1 and later byte for byte.
  it('never changes later documents', () => {
    const tail = yamlMultiDoc.slice(yamlMultiDoc.indexOf('---\n'));
    const ops: DagEditOp[] = [
      {
        kind: 'addStep',
        step: { name: 'next', run: 'echo next' },
        dependsOn: ['steps[0]'],
      },
      { kind: 'renameStep', step: 'steps[0]', newName: 'call-child' },
      { kind: 'setStepId', step: 'steps[0]', id: 'child_run' },
      {
        kind: 'setStepField',
        step: 'steps[0]',
        path: ['with', 'dag'],
        value: 'other',
      },
      {
        kind: 'replaceStepYaml',
        step: 'steps[0]',
        yaml: 'name: run-child\nrun: echo x\n',
      },
      { kind: 'deleteStep', step: 'steps[0]', rewire: true },
    ];
    for (const op of ops) {
      const result = doc.applyEdit(yamlMultiDoc, op);
      expect(result.ok, op.kind).toBe(true);
      if (!result.ok) continue;
      expect(result.text.endsWith(tail), op.kind).toBe(true);
      for (const edit of result.edits) {
        expect(edit.to, op.kind).toBeLessThanOrEqual(
          yamlMultiDoc.length - tail.length
        );
      }
    }
  });
});
