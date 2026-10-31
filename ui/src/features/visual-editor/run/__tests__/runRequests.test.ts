// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import {
  buildDraftBody,
  buildStartBody,
  EDITOR_RUN_LABEL,
  outputsFromRunId,
} from '../runRequests';

describe('runRequests', () => {
  it('uses the editor source label', () => {
    expect(EDITOR_RUN_LABEL).toBe('dagu.source=editor');
  });

  it('builds a whole-DAG start body with only the label', () => {
    expect(buildStartBody()).toEqual({ labels: [EDITOR_RUN_LABEL] });
  });

  it('builds a step start body with reused outputs', () => {
    expect(buildStartBody({ steps: ['b'], outputsFromRunId: 'r1' })).toEqual({
      steps: ['b'],
      outputsFromRunId: 'r1',
      labels: [EDITOR_RUN_LABEL, 'dagu.outputs-from=r1'],
    });
  });

  // A run id can start with '_' or '-', and the server refuses such a label
  // value. The body still names the run.
  it('writes no outputs-from label that the server would refuse', () => {
    expect(
      buildStartBody({ steps: ['b'], outputsFromRunId: '_source' })
    ).toEqual({
      steps: ['b'],
      outputsFromRunId: '_source',
      labels: [EDITOR_RUN_LABEL],
    });
  });

  it('leaves no outputsFromRunId key when no run is given', () => {
    const body = buildStartBody({ steps: ['b'] });
    expect(body).toEqual({ steps: ['b'], labels: [EDITOR_RUN_LABEL] });
    expect('outputsFromRunId' in body).toBe(false);
  });

  it('builds a draft body with spec, name and label', () => {
    const body = buildDraftBody('steps: []', 'my-dag');
    expect(body).toEqual({
      spec: 'steps: []',
      name: 'my-dag',
      labels: [EDITOR_RUN_LABEL],
    });
    expect('steps' in body).toBe(false);
  });

  it('builds a draft step body', () => {
    expect(
      buildDraftBody('s', 'n', { steps: ['a'], outputsFromRunId: 'r2' })
    ).toEqual({
      spec: 's',
      name: 'n',
      steps: ['a'],
      outputsFromRunId: 'r2',
      labels: [EDITOR_RUN_LABEL, 'dagu.outputs-from=r2'],
    });
    expect(
      'outputsFromRunId' in buildDraftBody('s', 'n', { steps: ['a'] })
    ).toBe(false);
  });

  // The server runs the listed steps and no other step.
  it('lists the given steps in order', () => {
    expect(buildStartBody({ steps: ['a', 'b'] })).toEqual({
      steps: ['a', 'b'],
      labels: [EDITOR_RUN_LABEL],
    });
    expect(buildDraftBody('s', 'n', { steps: ['a', 'b'] })).toMatchObject({
      steps: ['a', 'b'],
    });
  });

  it('reads the run that fed a step run from its labels', () => {
    expect(
      outputsFromRunId([EDITOR_RUN_LABEL, 'dagu.outputs-from=r9', 'team=a'])
    ).toBe('r9');
    expect(outputsFromRunId([EDITOR_RUN_LABEL])).toBeNull();
    expect(outputsFromRunId(undefined)).toBeNull();
    expect(outputsFromRunId(['dagu.outputs-from='])).toBeNull();
  });

  // The browser names runs and never sends a value: the server reads the
  // outputs from the run of each step.
  it('names the run of each step next to the source run, and alone', () => {
    const outputsFromRuns = { a: 'RunA', 'Step B': 'RunB' };
    expect(
      buildStartBody({ steps: ['c'], outputsFromRunId: 'r1', outputsFromRuns })
    ).toEqual({
      steps: ['c'],
      outputsFromRunId: 'r1',
      outputsFromRuns,
      labels: [EDITOR_RUN_LABEL, 'dagu.outputs-from=r1'],
    });
    const alone = buildDraftBody('s', 'n', { steps: ['c'], outputsFromRuns });
    expect(alone).toMatchObject({ steps: ['c'], outputsFromRuns });
    expect('outputsFromRunId' in alone).toBe(false);
  });

  it('never sends a value: no outputs key, whatever the options', () => {
    const bodies = [
      buildStartBody({ steps: ['c'], outputsFromRuns: { a: 'RunA' } }),
      buildStartBody({ steps: ['c'], outputsFromRunId: 'r1' }),
      buildDraftBody('s', 'n', { steps: ['c'], ephemeral: true }),
      buildStartBody(),
    ];
    for (const body of bodies) {
      expect('outputs' in body).toBe(false);
      expect('outputsSources' in body).toBe(false);
    }
    // A caller cannot pass a value: the option does not exist.
    const options = { steps: ['c'], outputs: { a: { x: '1' } } };
    const body = buildStartBody(options as never);
    expect('outputs' in body).toBe(false);
  });

  it('leaves no outputsFromRuns key for an empty set', () => {
    const body = buildStartBody({ steps: ['b'], outputsFromRuns: {} });
    expect('outputsFromRuns' in body).toBe(false);
  });

  // The server records and flags a test run: no label is needed.
  it('marks an ephemeral step run with the flag only', () => {
    expect(buildDraftBody('s', 'n', { steps: ['a'], ephemeral: true })).toEqual(
      {
        spec: 's',
        name: 'n',
        steps: ['a'],
        ephemeral: true,
        labels: [EDITOR_RUN_LABEL],
      }
    );
    expect('ephemeral' in buildStartBody({ steps: ['a'] })).toBe(false);
    expect('ephemeral' in buildStartBody()).toBe(false);
  });
});

describe('the labels of the reused inputs', () => {
  // The server record names the source of each reused step. The label is
  // written only for what the record lacks: a step that changed since its run.
  it('writes a dagu.input label only for a step that changed since its run', () => {
    const body = buildStartBody({
      steps: ['use'],
      outputsFromRunId: 'r1',
      ephemeral: true,
      reused: [
        {
          step: 'slow',
          source: 'cache',
          runId: 'RunA',
          stale: false,
          capturedAt: 5000,
        },
        {
          step: 'Mid Step',
          source: 'run',
          runId: 'r1',
          stale: true,
          capturedAt: 7000,
        },
      ],
    });
    expect(body.labels).toEqual([
      EDITOR_RUN_LABEL,
      'dagu.outputs-from=r1',
      'dagu.input=run-stale/7/.4did.20.53tep/r1',
    ]);
  });

  it('writes a label for a draft body too, and none without reused steps', () => {
    expect(
      buildDraftBody('s', 'n', {
        steps: ['b'],
        reused: [
          {
            step: 'a',
            source: 'run',
            runId: 'AbC1',
            stale: true,
            capturedAt: null,
          },
        ],
      }).labels
    ).toEqual([EDITOR_RUN_LABEL, 'dagu.input=run-stale/0/a/.41b.431']);
    expect(buildStartBody({ steps: ['b'], reused: [] }).labels).toEqual([
      EDITOR_RUN_LABEL,
    ]);
  });

  // A name that the label cannot hold is left out, not cut.
  it('leaves out the label of a step with a name that is too long', () => {
    const labels = buildStartBody({
      steps: ['b'],
      reused: [
        {
          step: 'é'.repeat(60),
          source: 'run',
          runId: 'r1',
          stale: true,
          capturedAt: 1000,
        },
      ],
    }).labels;
    expect(labels).toEqual([EDITOR_RUN_LABEL]);
  });
});
