// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, NodeStatusLabel } from '@/api/v1/schema';
import type { StepOutputs } from '../../contracts';
import { outputCache } from '../outputCache';
import { stepHashes } from '../stepHashes';
import {
  cacheableValues,
  forgetCaptures,
  useCaptureStepOutputs,
} from '../useCaptureStepOutputs';

const h = vi.hoisted(() => ({
  spec: undefined as string | undefined,
  /** The definition of the run, when it differs from `spec` (the saved file). */
  runSpec: undefined as string | undefined,
  /** The saved definition answers. */
  saved: true,
  /** The parameters of the run, and the defaults of the workflow. */
  runParams: '',
  defaultParams: '',
  /** The server names of the steps, in the run and in the workflow. */
  stepNames: [] as string[],
  paths: [] as string[],
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (path: string, init: unknown) => {
    if (init !== null) h.paths.push(path);
    if (init === null || h.spec === undefined) return { data: undefined };
    if (path === '/dags/{fileName}/spec') {
      return h.saved ? { data: { spec: h.spec } } : { data: undefined };
    }
    const steps = h.stepNames.map((name) => ({ name }));
    if (path === '/dag-runs/{name}/{dagRunId}') {
      return {
        data: {
          dagRunDetails: {
            params: h.runParams,
            nodes: steps.map((step) => ({ step })),
          },
        },
      };
    }
    if (path === '/dags/{fileName}') {
      return {
        data: { dag: { name: 'wf', defaultParams: h.defaultParams, steps } },
      };
    }
    return { data: { spec: h.runSpec ?? h.spec } };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const SPEC = `steps:
  - id: fetch
    name: fetch
    run: echo hi
  - id: use
    name: use
    depends: [fetch]
    run: echo use
`;
const SCOPE = { remoteNode: 'local', fileName: 'wf.yaml' };

function outputs(extra: Partial<StepOutputs> = {}): StepOutputs {
  return {
    stepName: 'fetch',
    status: NodeStatus.Success,
    statusLabel: NodeStatusLabel.succeeded,
    outputs: [{ name: 'data', value: '{"n":1}', truncated: false, size: 7 }],
    ...extra,
  };
}

function capture(p: {
  data?: StepOutputs;
  runId?: string | null;
  fileName?: string;
  step?: string;
}) {
  return renderHook(
    (props: typeof p) =>
      useCaptureStepOutputs({
        fileName: 'fileName' in props ? props.fileName : 'wf.yaml',
        dagName: 'wf',
        runId: props.runId === undefined ? 'r1' : props.runId,
        step: props.step ?? 'fetch',
        data: props.data,
      }),
    { initialProps: p }
  );
}

beforeEach(() => {
  outputCache.clear();
  forgetCaptures(SCOPE, 'fetch');
  h.spec = SPEC;
  h.runSpec = undefined;
  h.saved = true;
  h.runParams = '';
  h.defaultParams = '';
  h.stepNames = [];
  h.paths = [];
});

describe('cacheableValues', () => {
  it('gives the named outputs and the output variable by name', () => {
    expect(
      cacheableValues(
        outputs({
          outputVariable: {
            name: 'VAR',
            value: 'v',
            truncated: false,
            size: 1,
          },
        })
      )
    ).toEqual({ data: '{"n":1}', VAR: 'v' });
  });

  it('gives nothing for a step that did not succeed', () => {
    expect(cacheableValues(outputs({ status: NodeStatus.Failed }))).toBeNull();
    expect(cacheableValues(outputs({ status: NodeStatus.Skipped }))).toBeNull();
    expect(cacheableValues(undefined)).toBeNull();
  });

  it('gives nothing when a value was cut or there is no output', () => {
    expect(
      cacheableValues(
        outputs({
          outputs: [{ name: 'data', value: 'x', truncated: true, size: 99999 }],
        })
      )
    ).toBeNull();
    expect(cacheableValues(outputs({ outputs: [] }))).toBeNull();
  });

  it('gives nothing for a value larger than 64 KB', () => {
    const big = 'x'.repeat(64 * 1024 + 1);
    expect(
      cacheableValues(
        outputs({
          outputs: [
            { name: 'data', value: big, truncated: false, size: big.length },
          ],
        })
      )
    ).toBeNull();
  });
});

describe('useCaptureStepOutputs', () => {
  it('stores the outputs with the hash of the step in the run definition', () => {
    capture({ data: outputs() });
    const entry = outputCache.entries(SCOPE)['fetch'];
    expect(entry).toMatchObject({
      values: { data: '{"n":1}' },
      runId: 'r1',
      stepHash: stepHashes(SPEC).get('fetch'),
    });
    expect(entry?.capturedAt).toBeGreaterThan(0);
  });

  it('makes no request for the definition when there is nothing to keep', () => {
    capture({ data: outputs({ status: NodeStatus.Failed }) });
    expect(h.paths).toEqual([]);
    expect(outputCache.entries(SCOPE)).toEqual({});
  });

  it('waits for the definition of the run', () => {
    h.spec = undefined;
    capture({ data: outputs() });
    expect(outputCache.entries(SCOPE)).toEqual({});
  });

  it('stores nothing without a file name or a run', () => {
    capture({ data: outputs(), fileName: undefined });
    capture({ data: outputs(), runId: null });
    expect(outputCache.entries(SCOPE)).toEqual({});
  });

  it('names the run that the values were read from', () => {
    capture({ data: outputs(), runId: 'RunA' });
    expect(outputCache.entries(SCOPE)['fetch']?.runId).toBe('RunA');
  });

  it('keeps nothing for an empty run id: an entry always names its run', () => {
    capture({ data: outputs(), runId: '' });
    expect(outputCache.entries(SCOPE)['fetch']).toBeUndefined();
  });

  it('stores a run once, then stores again after the entry was reset', () => {
    const view = capture({ data: outputs() });
    outputCache.remove(SCOPE, 'fetch');
    view.rerender({ data: outputs() });
    // The pair (run, step) was stored: no second capture of the same run.
    expect(outputCache.entries(SCOPE)['fetch']).toBeUndefined();
    forgetCaptures(SCOPE, 'fetch');
    view.rerender({
      data: outputs({
        outputs: [{ name: 'data', value: '2', truncated: false, size: 1 }],
      }),
    });
    expect(outputCache.entries(SCOPE)['fetch']?.values).toEqual({ data: '2' });
  });

  // The user looks at an older run: its step is not the step of the saved
  // file, and the entry that is fresh for the file stays.
  it('does not replace an entry that is fresh for the saved definition by an older run', () => {
    const view = capture({ data: outputs(), runId: 'new' });
    expect(outputCache.entries(SCOPE)['fetch']?.runId).toBe('new');
    h.runSpec = SPEC.replace('echo hi', 'echo old');
    view.rerender({
      data: outputs({
        outputs: [{ name: 'data', value: 'old', truncated: false, size: 3 }],
      }),
      runId: 'old',
    });
    expect(outputCache.entries(SCOPE)['fetch']).toMatchObject({
      runId: 'new',
      values: { data: '{"n":1}' },
    });
  });

  it('replaces an entry that is stale for the saved definition', () => {
    h.runSpec = SPEC.replace('echo hi', 'echo old');
    const view = capture({ data: outputs(), runId: 'old' });
    expect(outputCache.entries(SCOPE)['fetch']?.runId).toBe('old');
    h.runSpec = undefined;
    view.rerender({ data: outputs(), runId: 'new' });
    expect(outputCache.entries(SCOPE)['fetch']?.runId).toBe('new');
  });

  // A step run uses the defaults: the outputs of a run with other
  // parameters are not what it would get.
  it('keeps nothing from a run with other parameters than the defaults', () => {
    h.defaultParams = 'ENV=dev';
    h.runParams = 'ENV=prod';
    const view = capture({ data: outputs(), runId: 'prod' });
    expect(outputCache.entries(SCOPE)['fetch']).toBeUndefined();
    h.runParams = 'ENV=dev';
    view.rerender({ data: outputs(), runId: 'dev' });
    expect(outputCache.entries(SCOPE)['fetch']?.runId).toBe('dev');
  });

  it('waits for the saved definition', () => {
    h.saved = false;
    capture({ data: outputs() });
    expect(outputCache.entries(SCOPE)).toEqual({});
  });

  // A retry keeps the run id: the values of the new attempt replace the
  // values of the first one.
  it('stores the new values of a run that was retried', () => {
    const view = capture({ data: outputs(), runId: 'r1' });
    view.rerender({
      data: outputs({
        outputs: [
          { name: 'data', value: 'retried', truncated: false, size: 7 },
        ],
      }),
      runId: 'r1',
    });
    expect(outputCache.entries(SCOPE)['fetch']?.values).toEqual({
      data: 'retried',
    });
  });

  // The server names a step that has no name: the hash takes that name.
  it('stores a step that has a name from the server', () => {
    h.spec = `type: chain
steps:
  - run: echo hello
    output: MESSAGE
`;
    h.stepNames = ['cmd_1'];
    forgetCaptures(SCOPE, 'cmd_1');
    capture({
      data: outputs({
        stepName: 'cmd_1',
        outputs: [],
        outputVariable: {
          name: 'MESSAGE',
          value: 'hello',
          truncated: false,
          size: 5,
        },
      }),
      step: 'cmd_1',
    });
    const hash = stepHashes(h.spec, { name: 'wf', steps: [{ name: 'cmd_1' }] });
    expect(hash.get('cmd_1')).toBeDefined();
    expect(outputCache.entries(SCOPE)['cmd_1']).toMatchObject({
      values: { MESSAGE: 'hello' },
      stepHash: hash.get('cmd_1'),
    });
  });

  it('keeps the entry of a new run in place of the old one', () => {
    const view = capture({ data: outputs(), runId: 'r1' });
    view.rerender({
      data: outputs({
        outputs: [
          { name: 'data', value: '{"n":2}', truncated: false, size: 7 },
        ],
      }),
      runId: 'r2',
    });
    expect(outputCache.entries(SCOPE)['fetch']).toMatchObject({
      runId: 'r2',
      values: { data: '{"n":2}' },
    });
  });
});
