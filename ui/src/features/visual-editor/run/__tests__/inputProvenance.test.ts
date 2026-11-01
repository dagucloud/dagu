// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { describe, expect, it } from 'vitest';
import { NodeStatus, ReusedInputVia, type components } from '@/api/v1/schema';
import {
  decodeLabelName,
  encodeLabelName,
  fromDetails,
  inputLabel,
  INPUT_LABEL_KEY,
  isEphemeralRun,
  parseInputLabel,
  parseInputLabels,
  reusedDurationMs,
  reusedInputs,
  reusedTimeMs,
  runInputProvenance,
  type InputProvenance,
} from '../inputProvenance';

type Node = components['schemas']['Node'];

// What the server accepts in a label value: it keeps a-z, 0-9, '-', '_',
// '.', '/', starts with a letter or a digit, and writes it in lower case.
const SERVER_VALUE = /^[a-z0-9][a-z0-9_./-]*$/;

const record = (extra: Partial<InputProvenance> = {}): InputProvenance => ({
  step: 'slow',
  source: 'cache',
  stale: false,
  capturedAt: 1_790_812_800_000,
  ...extra,
});

function node(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: { name },
    status: NodeStatus.Skipped,
    statusLabel: 'skipped',
    stdout: '',
    stderr: '',
    startedAt: '-',
    finishedAt: '-',
    retryCount: 0,
    doneCount: 0,
    ...extra,
  } as unknown as Node;
}

describe('encodeLabelName and decodeLabelName', () => {
  it('keeps a-z, 0-9, - and _ and writes the other bytes as .xx', () => {
    expect(encodeLabelName('extract_users-2')).toBe('extract_users-2');
    expect(encodeLabelName('Extract users')).toBe('.45xtract.20users');
    expect(encodeLabelName('a.b/c')).toBe('a.2eb.2fc');
  });

  it('round-trips capitals, spaces, dots and non-latin names', () => {
    for (const name of ['Build Docs', 'a.b', 'étape 1', '步骤', 'x/y?z', '.']) {
      const encoded = encodeLabelName(name);
      expect(encoded).toMatch(/^[a-z0-9_.-]+$/);
      expect(decodeLabelName(encoded)).toBe(name);
    }
  });

  it('survives the lower case of the server', () => {
    const name = 'Build Docs';
    expect(decodeLabelName(encodeLabelName(name).toLowerCase())).toBe(name);
  });

  it('refuses a text that is not a written name', () => {
    expect(decodeLabelName('')).toBeNull();
    expect(decodeLabelName('Upper')).toBeNull();
    expect(decodeLabelName('a.zz')).toBeNull();
    expect(decodeLabelName('a.2')).toBeNull();
    // Two bytes that are not UTF-8.
    expect(decodeLabelName('.ff.fe')).toBeNull();
  });
});

describe('inputLabel and parseInputLabel', () => {
  it('writes the kind, the capture time in seconds and the step', () => {
    expect(inputLabel(record())).toBe(
      `${INPUT_LABEL_KEY}=cache/1790812800/slow`
    );
    expect(inputLabel(record({ stale: true }))).toBe(
      'dagu.input=cache-stale/1790812800/slow'
    );
  });

  it('writes the run id after the step for a run source', () => {
    expect(inputLabel(record({ source: 'run', runId: '034mhevD9Yk' }))).toBe(
      'dagu.input=run/1790812800/slow/034mhev.449.59k'
    );
  });

  it('writes a value that the server accepts, whatever the step name', () => {
    for (const step of ['Extract Users', '步骤', '.hidden', 'a/b']) {
      const label = inputLabel(record({ step }));
      expect(label).not.toBeNull();
      const value = (label as string).slice(INPUT_LABEL_KEY.length + 1);
      expect(value).toMatch(SERVER_VALUE);
      expect(value.length).toBeLessThanOrEqual(255);
    }
  });

  it('gives null when the value is too long or a run has no id', () => {
    expect(inputLabel(record({ step: 'é'.repeat(60) }))).toBeNull();
    expect(inputLabel(record({ source: 'run' }))).toBeNull();
  });

  it('reads every kind back', () => {
    const cases: InputProvenance[] = [
      record(),
      record({ step: 'Merge Step' }),
      record({ stale: true, step: 'x' }),
      record({ source: 'run', runId: '034mhevD9YkYv6w6ItyvKO', step: 'b' }),
      record({
        source: 'run',
        runId: 'AbC',
        stale: true,
        step: 'c',
        capturedAt: null,
      }),
    ];
    for (const original of cases) {
      const label = inputLabel(original) as string;
      const lowered = label.toLowerCase();
      // Seconds only: the milliseconds are not kept.
      const expected = {
        ...original,
        capturedAt:
          original.capturedAt === null
            ? null
            : Math.floor(original.capturedAt / 1000) * 1000,
      };
      expect(parseInputLabel(lowered)).toEqual(expected);
    }
  });

  it('ignores a label that is not a known record', () => {
    for (const label of [
      'dagu.source=editor',
      'dagu.input=',
      'dagu.input=cache',
      'dagu.input=cache/abc/slow',
      'dagu.input=other/1/slow',
      'dagu.input=cache/1/Slow',
      'dagu.input=cache/1/slow/extra',
      'dagu.input=run/1/slow',
      'dagu.input=run/1/slow/r1/extra',
      // Names of the prototype of an object are no kind.
      'dagu.input=constructor/1/slow',
      'dagu.input=__proto__/1/slow',
      'team=a',
    ]) {
      expect(parseInputLabel(label)).toBeNull();
    }
  });

  it('collects the records of a list of labels by step', () => {
    const found = parseInputLabels([
      'dagu.source=editor',
      'dagu.input=cache/5/a',
      'dagu.input=run-stale/6/b/r1',
      'dagu.input=broken',
    ]);
    expect([...found.keys()]).toEqual(['a', 'b']);
    expect(found.get('b')).toMatchObject({ source: 'run', stale: true });
    expect(parseInputLabels(undefined).size).toBe(0);
  });
});

describe('runInputProvenance', () => {
  it('reads the labels of a run that has no record of the server', () => {
    const found = runInputProvenance({
      labels: ['dagu.input=cache/5/slow', 'dagu.input=pinned/6/middle'],
      nodes: [
        node('slow'),
        node('middle'),
        node('use', { status: NodeStatus.Success }),
      ],
    });
    expect(found.get('slow')).toMatchObject({ source: 'cache' });
    // A value that an earlier version let the user type reads as cached.
    expect(found.get('middle')).toMatchObject({ source: 'cache' });
    expect(found.has('use')).toBe(false);
  });

  it('reads the record of the server before the labels', () => {
    const found = runInputProvenance({
      labels: ['dagu.input=cache/5/slow', 'dagu.outputs-from=other'],
      reusedInputs: [
        {
          step: 'slow',
          runId: 'RunA',
          capturedAt: '2026-10-01T00:00:05Z',
          via: ReusedInputVia.run,
        },
      ],
      nodes: [node('slow'), node('use', { status: NodeStatus.Success })],
    });
    expect(found.get('slow')).toEqual({
      step: 'slow',
      source: 'run',
      runId: 'RunA',
      stale: false,
      capturedAt: Date.parse('2026-10-01T00:00:05Z'),
      startedAt: null,
      finishedAt: null,
    });
  });

  // A run that was started before the labels has only dagu.outputs-from.
  it('derives a run source from outputs-from for the steps that carry copied outputs', () => {
    const found = runInputProvenance({
      labels: ['dagu.source=editor', 'dagu.outputs-from=r1'],
      nodes: [
        node('slow', { doneCount: 1, finishedAt: '2026-10-01T00:00:05Z' }),
        node('never'),
        node('use', { status: NodeStatus.Success }),
      ],
    });
    expect([...found.keys()]).toEqual(['slow']);
    expect(found.get('slow')).toEqual({
      step: 'slow',
      source: 'run',
      runId: 'r1',
      stale: false,
      capturedAt: Date.parse('2026-10-01T00:00:05Z'),
      startedAt: null,
      finishedAt: Date.parse('2026-10-01T00:00:05Z'),
    });
  });

  it('reads the zero time of the server as no time', () => {
    const found = runInputProvenance({
      labels: ['dagu.outputs-from=r1'],
      nodes: [
        node('slow', { doneCount: 1, finishedAt: '0001-01-01T00:00:00Z' }),
      ],
    });
    expect(found.get('slow')).toMatchObject({
      capturedAt: null,
      finishedAt: null,
    });
  });

  it('keeps the label of a step before the derived source', () => {
    const found = runInputProvenance({
      labels: ['dagu.outputs-from=r1', 'dagu.input=cache/9/slow'],
      nodes: [node('slow', { doneCount: 1 })],
    });
    expect(found.get('slow')?.source).toBe('cache');
  });

  it('drops a step that the run executed after all', () => {
    const found = runInputProvenance({
      labels: ['dagu.input=cache/5/slow'],
      nodes: [node('slow', { status: NodeStatus.Success })],
    });
    expect(found.size).toBe(0);
  });

  it('gives nothing for no run, no label or a plain run', () => {
    expect(runInputProvenance(null).size).toBe(0);
    expect(runInputProvenance({ labels: ['dagu.source=editor'] }).size).toBe(0);
    expect(
      runInputProvenance({ labels: [], nodes: [node('a', { doneCount: 1 })] })
        .size
    ).toBe(0);
  });
});

describe('fromDetails', () => {
  const run = (via: ReusedInputVia, runId = 'RunA') => ({
    step: 'slow',
    runId,
    capturedAt: '2026-10-01T00:00:05Z',
    startedAt: '2026-10-01T00:00:01Z',
    finishedAt: '2026-10-01T00:00:05Z',
    via,
  });

  it('gives null for a run with no record', () => {
    expect(fromDetails(null)).toBeNull();
    expect(fromDetails({})).toBeNull();
    expect(fromDetails({ reusedInputs: [] })).toBeNull();
  });

  it('maps via run and via cache, with the run id as the server wrote it', () => {
    const found = fromDetails({
      reusedInputs: [
        run(ReusedInputVia.run),
        { ...run(ReusedInputVia.cache, 'RunB'), step: 'middle' },
        { step: 'last', runId: 'RunC', via: ReusedInputVia.cache },
      ],
    });
    expect(found?.get('slow')).toEqual({
      step: 'slow',
      source: 'run',
      runId: 'RunA',
      stale: false,
      capturedAt: Date.parse('2026-10-01T00:00:05Z'),
      startedAt: Date.parse('2026-10-01T00:00:01Z'),
      finishedAt: Date.parse('2026-10-01T00:00:05Z'),
    });
    expect(found?.get('middle')).toMatchObject({
      source: 'cache',
      runId: 'RunB',
    });
    expect(found?.get('last')?.capturedAt).toBeNull();
  });

  // The record has no stale flag: the label of the step carries it.
  it('takes the stale flag from the label of the step', () => {
    const found = fromDetails({
      labels: ['dagu.input=run-stale/5/slow/runa'],
      reusedInputs: [run(ReusedInputVia.run)],
    });
    expect(found?.get('slow')?.stale).toBe(true);
  });
});

describe('isEphemeralRun', () => {
  it('reads the flag of the server, and the old label of a run that has no flag', () => {
    expect(isEphemeralRun({ ephemeral: true })).toBe(true);
    expect(isEphemeralRun({ labels: ['dagu.ephemeral=true'] })).toBe(true);
    expect(isEphemeralRun({ labels: ['dagu.source=editor'] })).toBe(false);
    expect(isEphemeralRun({ ephemeral: false })).toBe(false);
    expect(isEphemeralRun(null)).toBe(false);
  });
});

describe('reusedInputs', () => {
  const base = {
    previous: ['seed', 'orders', 'users'],
    steps: ['merge'],
    fromRuns: {},
    source: null,
    stale: [],
    missing: [],
  };

  it('names the run of a step that has one of its own, with the times that the step had there', () => {
    expect(
      reusedInputs({
        ...base,
        fromRuns: {
          seed: {
            runId: 'r-seed',
            startedAt: '2026-10-01T00:00:00Z',
            finishedAt: '2026-10-01T00:00:20Z',
          },
        },
      })
    ).toEqual([
      {
        step: 'seed',
        source: 'run',
        runId: 'r-seed',
        stale: false,
        capturedAt: Date.parse('2026-10-01T00:00:20Z'),
        startedAt: Date.parse('2026-10-01T00:00:00Z'),
        finishedAt: Date.parse('2026-10-01T00:00:20Z'),
      },
    ]);
  });

  it('names the source run for the other steps that it holds, with the stale mark and the node times', () => {
    expect(
      reusedInputs({
        ...base,
        fromRuns: { seed: { runId: 'r0' } },
        source: {
          dagRunId: 'r1',
          startedAt: '2026-10-01T00:00:00Z',
          finishedAt: '2026-10-01T00:00:09Z',
          nodes: [
            {
              step: { name: 'orders' },
              startedAt: '2026-10-01T00:00:02Z',
              finishedAt: '2026-10-01T00:00:09Z',
            } as never,
          ],
        },
        stale: ['orders'],
        missing: ['users'],
      })
    ).toEqual([
      {
        step: 'seed',
        source: 'run',
        runId: 'r0',
        stale: false,
        capturedAt: null,
        startedAt: null,
        finishedAt: null,
      },
      {
        step: 'orders',
        source: 'run',
        runId: 'r1',
        stale: true,
        capturedAt: Date.parse('2026-10-01T00:00:09Z'),
        startedAt: Date.parse('2026-10-01T00:00:02Z'),
        finishedAt: Date.parse('2026-10-01T00:00:09Z'),
      },
    ]);
  });

  it('leaves out the steps that the request runs and the steps that nothing covers', () => {
    expect(
      reusedInputs({
        ...base,
        steps: ['orders', 'merge'],
        source: { dagRunId: 'r1' },
        missing: ['users'],
      })
    ).toEqual([
      {
        step: 'seed',
        source: 'run',
        runId: 'r1',
        stale: false,
        capturedAt: null,
        startedAt: null,
        finishedAt: null,
      },
    ]);
    expect(reusedInputs(base)).toEqual([]);
  });
});

describe('the time of a reused step', () => {
  const at = (s: string) => Date.parse(`2026-10-01T00:00:${s}Z`);

  it('is the time that it took in its source run', () => {
    expect(
      reusedDurationMs({
        ...record(),
        startedAt: at('00'),
        finishedAt: at('20'),
      })
    ).toBe(20_000);
    expect(reusedDurationMs(record())).toBeNull();
    expect(
      reusedDurationMs({
        ...record(),
        startedAt: at('20'),
        finishedAt: at('00'),
      })
    ).toBeNull();
  });

  it('adds up for a run, and is null when no time is known', () => {
    const run = {
      reusedInputs: [
        {
          step: 'a',
          runId: 'r1',
          startedAt: '2026-10-01T00:00:00Z',
          finishedAt: '2026-10-01T00:00:20Z',
          via: ReusedInputVia.run,
        },
        {
          step: 'b',
          runId: 'r1',
          startedAt: '2026-10-01T00:00:20Z',
          finishedAt: '2026-10-01T00:00:22Z',
          via: ReusedInputVia.run,
        },
        { step: 'c', runId: 'r2', via: ReusedInputVia.cache },
      ],
      nodes: [node('a'), node('b'), node('c')],
    };
    expect(reusedTimeMs(run)).toBe(22_000);
    expect(reusedTimeMs(null)).toBeNull();
    expect(reusedTimeMs({ nodes: [node('a')] })).toBeNull();
  });

  it('is filled from the node of a run that has no times in its record', () => {
    const found = runInputProvenance({
      labels: ['dagu.outputs-from=r1'],
      nodes: [
        node('slow', {
          doneCount: 1,
          startedAt: '2026-10-01T00:00:00Z',
          finishedAt: '2026-10-01T00:00:20Z',
        }),
      ],
    });
    expect(reusedDurationMs(found.get('slow')!)).toBe(20_000);
  });
});
