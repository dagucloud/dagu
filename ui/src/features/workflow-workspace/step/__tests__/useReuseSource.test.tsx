// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import type { ReactNode } from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { outputCache } from '@/features/visual-editor/run/outputCache';
import { sourceChoiceStore } from '@/features/visual-editor/run/runSourceChoice';
import { stepHashes } from '@/features/visual-editor/run/stepHashes';
import { WorkspaceDataContext } from '../../data/context';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceRunValue,
} from '../../data/types';
import { MAX_OUTPUT_REQUESTS, useReuseSource } from '../useReuseSource';

type Node = components['schemas']['Node'];

const h = vi.hoisted(() => ({
  get: vi.fn(),
  queries: [] as { path: string; init: unknown }[],
  history: [] as unknown[],
  /** The runs that the server still has, by id, for the check of the cache. */
  details: {} as Record<string, unknown>,
  specs: {
    run: '' as string | undefined,
    saved: undefined as string | undefined,
  },
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ GET: h.get }),
  useQuery: (path: string, init: unknown) => {
    if (init !== null) h.queries.push({ path, init });
    if (init === null) return { data: undefined, isLoading: false };
    switch (path) {
      case '/dags/{fileName}/dag-runs':
        return {
          data: { dagRuns: h.history, gridData: [] },
          isLoading: false,
          mutate: vi.fn(),
        };
      case '/dag-runs/{name}/{dagRunId}/spec':
        return {
          data: h.specs.run === undefined ? undefined : { spec: h.specs.run },
        };
      case '/dags/{fileName}/spec':
        return {
          data:
            h.specs.saved === undefined ? undefined : { spec: h.specs.saved },
        };
      default:
        return { data: undefined };
    }
  },
}));
vi.mock('@/hooks/useDAGHistorySSE', () => ({
  useDAGHistorySSE: () => ({ isConnected: false, shouldUseFallback: true }),
}));
vi.mock('@/hooks/useSSECacheSync', () => ({
  sseFallbackOptions: () => ({}),
  useSSECacheSync: () => undefined,
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'edge',
}));

function node(name: string, status: NodeStatus = NodeStatus.Success): Node {
  return {
    step: { name },
    status,
    statusLabel: 'succeeded',
    finishedAt: '2026-10-01T00:00:05Z',
    doneCount: 1,
  } as unknown as Node;
}

function runOf(
  id: string,
  status: Status = Status.Success,
  nodes: Node[] = [node('a'), node('b')],
  params?: string
): ApiDAGRunDetails {
  return {
    name: 'example',
    dagRunId: id,
    status,
    statusLabel: StatusLabel.succeeded,
    nodes,
    ...(params !== undefined ? { params } : {}),
  } as ApiDAGRunDetails;
}

const SPEC = `steps:
  - name: a
    run: echo a
  - name: b
    run: echo b
    depends: [a]
  - name: c
    run: echo c
    depends: [b]
`;

function wrapper(
  selected: ApiDAGRunDetails | null,
  defaultParams?: string
): (props: { children: ReactNode }) => ReactNode {
  const run = {
    selection: 'latest',
    ref: selected ? { name: 'example', runId: selected.dagRunId } : null,
    details: selected,
  } as unknown as WorkspaceRunValue;
  const value = {
    fileName: 'example.yaml',
    dagData: {
      dag: {
        name: 'example',
        ...(defaultParams !== undefined ? { defaultParams } : {}),
        steps: [
          { name: 'a' },
          { name: 'b', depends: ['a'] },
          { name: 'c', depends: ['b'] },
        ],
      },
    } as unknown as DAGPageData,
    workspaceName: null,
    remoteNode: 'edge',
    run,
    refresh: async () => undefined,
    registerHistory: () => () => undefined,
  };
  return function Wrapper({ children }: { children: ReactNode }) {
    return (
      <WorkspaceDataContext.Provider value={value}>
        {children}
      </WorkspaceDataContext.Provider>
    );
  };
}

function outputsCalls() {
  return h.get.mock.calls.filter((call) =>
    String(call[0]).endsWith('/outputs')
  );
}

function render(
  p: Partial<Parameters<typeof useReuseSource>[0]> = {},
  selected: ApiDAGRunDetails | null = null,
  defaultParams?: string
) {
  return renderHook(
    () =>
      useReuseSource({
        fileName: 'example.yaml',
        step: 'c',
        previous: ['a', 'b'],
        reads: ['b'],
        followedUpstreamRunId: null,
        ...p,
      }),
    { wrapper: wrapper(selected, defaultParams) }
  );
}

beforeEach(() => {
  h.get.mockReset();
  // A request for a run answers with its details, as the server does, or 404
  // when the server dropped it.
  h.details = { r0: runOf('r0') };
  h.get.mockImplementation(
    async (path: string, init: { params: { path: { dagRunId: string } } }) => {
      if (path === '/dag-runs/{name}/{dagRunId}') {
        const found = h.details[init.params.path.dagRunId];
        return found
          ? { data: { dagRunDetails: found }, response: { status: 200 } }
          : { data: undefined, response: { status: 404 } };
      }
      return {
        data: {
          outputs: [{ name: 'x', value: '1' }],
          outputVariable: undefined,
        },
      };
    }
  );
  h.queries.length = 0;
  h.history = [runOf('new'), runOf('old')];
  h.specs.run = SPEC;
  h.specs.saved = undefined;
  outputCache.clear();
  sourceChoiceStore.set(
    { remoteNode: 'edge', fileName: 'example.yaml' },
    { kind: 'auto' }
  );
});

const SCOPE = { remoteNode: 'edge', fileName: 'example.yaml' };

/** An entry of the cache, fresh for the step in the given text. */
function cacheStep(
  step: string,
  text: string,
  values = { x: '1' },
  runId = 'r0'
) {
  outputCache.put(SCOPE, step, {
    values,
    runId,
    capturedAt: 5000,
    stepHash: stepHashes(text).get(step) ?? '',
  });
}

describe('useReuseSource', () => {
  it('picks the newest finished run and reports a fresh state', async () => {
    h.history = [runOf('active', Status.Running), runOf('good')];
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.source?.dagRunId).toBe('good');
    expect(view.result.current.stale).toEqual([]);
    expect(view.result.current.missing).toEqual([]);
    expect(view.result.current.headChanged).toBe(false);
    expect(view.result.current.waitingFor).toBeNull();
  });

  it('marks the changed previous step and the steps downstream', async () => {
    const view = render({
      previous: ['a', 'b'],
      reads: ['b'],
      liveText: SPEC.replace('echo a', 'echo changed'),
    });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.stale).toEqual(['a', 'b']);
  });

  it('marks every previous step when the head changed', async () => {
    const view = render({ liveText: `name: other\n${SPEC}` });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.headChanged).toBe(true);
    expect(view.result.current.stale).toEqual(['a', 'b']);
  });

  it('lists the previous steps that the source holds no outputs of', async () => {
    h.history = [runOf('r', Status.Failed, [node('a'), node('b')])];
    const view = render({
      previous: ['a', 'b', 'x'],
      reads: ['b'],
      liveText: SPEC,
    });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.missing).toEqual(['x']);
  });

  it('reports different parameters', async () => {
    h.history = [runOf('r', Status.Success, [node('a'), node('b')], 'N=2')];
    const view = render({ liveText: SPEC }, null, 'N=1');
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.paramsDiffer).toBe(true);
  });

  it('waits for an active run', async () => {
    h.history = [runOf('active', Status.Running)];
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.source).toBeNull();
    expect(view.result.current.waitingFor).toBe('active');
  });

  it('makes at most three outputs requests', async () => {
    const reads = ['a', 'b'];
    h.history = Array.from({ length: 6 }, (_, i) =>
      runOf(`r${i}`, Status.Success)
    );
    h.get.mockResolvedValue({ data: { outputs: [] } });
    const view = render({ reads, liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(outputsCalls().length).toBeLessThanOrEqual(MAX_OUTPUT_REQUESTS);
    expect(outputsCalls().length).toBeGreaterThan(0);
  });

  it('makes no outputs request when the step reads nothing', async () => {
    const view = render({ reads: [], liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.source?.dagRunId).toBe('new');
    expect(outputsCalls()).toHaveLength(0);
  });

  it('asks for the outputs with the name of the DAG and the remote node', async () => {
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(h.get).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/steps/{stepName}/outputs',
      {
        params: {
          path: { name: 'example', dagRunId: 'new', stepName: 'b' },
          query: { remoteNode: 'edge' },
        },
      }
    );
  });

  it('uses the SWR keys of the history and of the run spec', async () => {
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    // The history has the test runs, so that the user can choose one.
    expect(h.queries).toContainEqual({
      path: '/dags/{fileName}/dag-runs',
      init: {
        params: {
          query: { remoteNode: 'edge', includeEphemeral: true },
          path: { fileName: 'example.yaml' },
        },
      },
    });
    expect(h.queries).toContainEqual({
      path: '/dag-runs/{name}/{dagRunId}/spec',
      init: {
        params: {
          path: { name: 'example', dagRunId: 'new' },
          query: { remoteNode: 'edge' },
        },
      },
    });
  });

  it('does not request the saved definition when it has a live text', async () => {
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(h.queries.map((q) => q.path)).not.toContain('/dags/{fileName}/spec');
  });

  it('chooses again when the history changes', async () => {
    h.history = [runOf('first')];
    const view = render({ liveText: SPEC });
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('first')
    );
    h.history = [runOf('second'), runOf('first')];
    view.rerender();
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('second')
    );
  });

  describe('with the cache of the browser', () => {
    it('covers a changed previous step with a fresh cached entry', async () => {
      const live = SPEC.replace('echo a', 'echo changed');
      cacheStep('a', live, { x: 'cached-a' });
      const view = render({ liveText: live });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      // The run is stale for a and b; the cache is fresh for a.
      expect(view.result.current.cached).toEqual(['a']);
      expect(view.result.current.stale).toEqual(['b']);
      expect(Object.keys(view.result.current.cachedRuns)).toEqual(['a']);
      expect(view.result.current.cacheCapturedAt).toBe(5000);
      expect(view.result.current.source?.dagRunId).toBe('new');
    });

    it('does not use an entry that the step change made stale', async () => {
      cacheStep('a', SPEC, { x: 'old' });
      const live = SPEC.replace('echo a', 'echo changed');
      const view = render({ liveText: live });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.cached).toEqual([]);
      expect(view.result.current.stale).toEqual(['a', 'b']);
      expect(view.result.current.cachedRuns).toEqual({});
    });

    it('makes an entry stale when a step upstream of it changed', async () => {
      cacheStep('b', SPEC, { x: 'b-old' });
      const live = SPEC.replace('echo a', 'echo changed');
      const view = render({ liveText: live });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.cached).toEqual([]);
    });

    it('covers every previous step with no run at all', async () => {
      h.history = [];
      cacheStep('a', SPEC);
      cacheStep('b', SPEC);
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.source).toBeNull();
      expect(view.result.current.cached).toEqual(['a', 'b']);
      expect(view.result.current.missing).toEqual([]);
      expect(Object.keys(view.result.current.cachedRuns)).toEqual(['a', 'b']);
    });

    // The run that holds a step gives its outputs: the cache gives only what
    // the run cannot.
    it('leaves a step to the source run when the run holds it and it did not change', async () => {
      cacheStep('a', SPEC, { x: 'cached-a' });
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.source?.dagRunId).toBe('new');
      expect(view.result.current.cached).toEqual([]);
      expect(view.result.current.cachedRuns).toEqual({});
    });

    it('names the run that each cached step is read from', async () => {
      h.history = [];
      cacheStep('a', SPEC);
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.cachedRuns).toEqual({
        a: { runId: 'r0', capturedAt: 5000 },
      });
      // The run of the step and the times that the step had in it.
      expect(view.result.current.stepRuns['a']).toMatchObject({
        runId: 'r0',
        finishedAt: '2026-10-01T00:00:05Z',
      });
    });

    // The server reads the outputs from the run: a run that is gone (removed,
    // or a test run that expired) cannot give the step any more.
    it('counts a step as missing when the run of its entry is gone', async () => {
      h.history = [];
      h.details = {};
      cacheStep('a', SPEC);
      cacheStep('b', SPEC);
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.cached).toEqual([]);
      expect(view.result.current.gone).toEqual(['a', 'b']);
      expect(view.result.current.cachedRuns).toEqual({});
      expect(view.result.current.options.some((o) => o.id === 'cache')).toBe(
        false
      );
    });

    it('asks the server for a run that the history does not list, once for each run', async () => {
      h.history = [];
      cacheStep('a', SPEC);
      cacheStep('b', SPEC);
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      const asked = h.get.mock.calls.filter(
        (call) => call[0] === '/dag-runs/{name}/{dagRunId}'
      );
      expect(asked).toHaveLength(1);
      expect(asked[0]?.[1]).toMatchObject({
        params: { path: { name: 'example', dagRunId: 'r0' } },
      });
    });

    // An answer must not drop the requests still in flight: each run is asked
    // once, whatever the order of the answers.
    it('asks for each run once when the answers arrive at different times', async () => {
      h.history = [];
      h.details = { r0: runOf('r0'), r1: runOf('r1') };
      const answer = h.get.getMockImplementation()!;
      h.get.mockImplementation(
        async (
          path: string,
          init: { params: { path: { dagRunId: string } } }
        ) => {
          if (init.params.path.dagRunId === 'r1') {
            await new Promise((resolve) => setTimeout(resolve, 20));
          }
          return answer(path, init);
        }
      );
      cacheStep('a', SPEC);
      cacheStep('b', SPEC, { x: '1' }, 'r1');
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      const asked = h.get.mock.calls
        .filter((call) => call[0] === '/dag-runs/{name}/{dagRunId}')
        .map((call) => call[1].params.path.dagRunId);
      expect(asked).toEqual(['r0', 'r1']);
      expect(view.result.current.cached).toEqual(['a', 'b']);
    });

    it('does not ask for a run that the history lists', async () => {
      h.history = [runOf('r0')];
      h.details = {};
      cacheStep('a', SPEC.replace('echo a', 'echo a2'));
      const view = render({ liveText: SPEC.replace('echo a', 'echo a2') });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(
        h.get.mock.calls.filter(
          (call) => call[0] === '/dag-runs/{name}/{dagRunId}'
        )
      ).toEqual([]);
    });

    it('does not count a step as covered by a run that did not succeed at it', async () => {
      h.history = [];
      h.details = {
        r0: runOf('r0', Status.Failed, [node('a', NodeStatus.Failed)]),
      };
      cacheStep('a', SPEC);
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.gone).toEqual(['a']);
    });

    // The server reuses outputs of a finished run only: an entry of a run
    // that is still active covers nothing until the run ends.
    it('does not cover a step from a run that is still active', async () => {
      h.history = [runOf('r0', Status.Running)];
      cacheStep('a', SPEC);
      cacheStep('b', SPEC);
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.cached).toEqual([]);
      expect(view.result.current.gone).toEqual([]);
      expect(view.result.current.options.some((o) => o.id === 'cache')).toBe(
        false
      );
      h.history = [runOf('r0')];
      view.rerender();
      await waitFor(() =>
        expect(view.result.current.options.some((o) => o.id === 'cache')).toBe(
          true
        )
      );
    });

    it('hashes the live text and does not read the saved definition', async () => {
      h.history = [];
      cacheStep('a', SPEC);
      const view = render({ liveText: SPEC });
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.cached).toEqual(['a']);
      expect(h.queries.map((q) => q.path)).not.toContain(
        '/dags/{fileName}/spec'
      );
    });

    it('reads the saved definition for the hashes without a live text', async () => {
      h.specs.saved = SPEC;
      h.history = [];
      cacheStep('a', SPEC);
      const view = render();
      await waitFor(() => expect(view.result.current.loading).toBe(false));
      expect(view.result.current.cached).toEqual(['a']);
    });
  });
});

describe('the choice of the run', () => {
  const choose = (id: string) =>
    sourceChoiceStore.set(SCOPE, { kind: 'run', runId: id });

  it('lists the runs newest first, the automatic one first', async () => {
    h.history = [runOf('new'), runOf('old')];
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.options.map((o) => o.id)).toEqual([
      'run:new',
      'run:old',
    ]);
    expect(view.result.current.options[0]?.auto).toBe(true);
    expect(view.result.current.selectedOptionId).toBe('run:new');
  });

  it('blocks a run that does not hold what the step reads, with the steps', async () => {
    h.history = [runOf('new'), runOf('thin', Status.Success, [node('a')])];
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.options[1]).toMatchObject({
      id: 'run:thin',
      blocked: { reason: 'missing', steps: ['b'] },
    });
  });

  it('uses the run that the user chose, and keeps the choice for the workflow', async () => {
    choose('old');
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.source?.dagRunId).toBe('old');
    expect(view.result.current.selectedOptionId).toBe('run:old');
    // The automatic choice stays the newest run.
    expect(view.result.current.options[0]).toMatchObject({
      id: 'run:new',
      auto: true,
    });
  });

  it('changes the source when the user chooses a line, and goes back with the automatic one', async () => {
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    act(() => view.result.current.choose(view.result.current.options[1]!));
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('old')
    );
    act(() => view.result.current.choose(view.result.current.options[0]!));
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('new')
    );
    expect(sourceChoiceStore.get(SCOPE)).toBe('auto');
  });

  it('falls back to the automatic choice when the chosen run is gone or cannot be a source', async () => {
    choose('gone');
    const view = render({ liveText: SPEC });
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('new')
    );
    choose('thin');
    h.history = [runOf('new'), runOf('thin', Status.Success, [node('a')])];
    view.rerender();
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('new')
    );
    choose('busy');
    h.history = [runOf('new'), runOf('busy', Status.Running)];
    view.rerender();
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('new')
    );
  });

  // The history lists the newest runs only: a chosen run that newer runs
  // pushed out of it stays the source.
  it('keeps a chosen run that the history no longer lists', async () => {
    h.details = { older: runOf('older') };
    choose('older');
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.source?.dagRunId).toBe('older');
    expect(view.result.current.selectedOptionId).toBe('run:older');
  });

  // A stored choice of the cache gives way once no fresh entry backs it, as
  // a chosen run that is gone does.
  it('falls back to the automatic choice when the chosen cache has no fresh entry', async () => {
    sourceChoiceStore.set(SCOPE, { kind: 'cache' });
    cacheStep('a', SPEC.replace('echo a', 'echo other'));
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.source?.dagRunId).toBe('new');
    expect(view.result.current.selectedOptionId).toBe('run:new');
  });

  it('can choose a test run that still exists, but never picks one by itself', async () => {
    h.history = [{ ...runOf('test'), ephemeral: true }, runOf('kept')];
    const view = render({ liveText: SPEC });
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('kept')
    );
    expect(
      view.result.current.options.find((o) => o.id === 'run:test')
    ).toMatchObject({
      ephemeral: true,
    });
    choose('test');
    view.rerender();
    await waitFor(() =>
      expect(view.result.current.source?.dagRunId).toBe('test')
    );
  });

  it('offers the cached values last, with the run that they were read from, and uses them when chosen', async () => {
    cacheStep('a', SPEC);
    cacheStep('b', SPEC);
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    const last =
      view.result.current.options[view.result.current.options.length - 1];
    expect(last).toMatchObject({
      id: 'cache',
      cache: { runIds: ['r0'], capturedAt: 5000 },
    });
    act(() => view.result.current.choose(last!));
    await waitFor(() => expect(view.result.current.source).toBeNull());
    expect(view.result.current.selectedOptionId).toBe('cache');
    expect(view.result.current.cached).toEqual(['a', 'b']);
    expect(view.result.current.cachedRuns).toEqual({
      a: { runId: 'r0', capturedAt: 5000 },
      b: { runId: 'r0', capturedAt: 5000 },
    });
  });

  // The chosen cache gives the reads: no run is waited for.
  it('does not wait for an active run when the cache is chosen', async () => {
    h.history = [runOf('active', Status.Running)];
    cacheStep('a', SPEC);
    cacheStep('b', SPEC);
    sourceChoiceStore.set(SCOPE, { kind: 'cache' });
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.selectedOptionId).toBe('cache');
    expect(view.result.current.waitingFor).toBeNull();
  });

  it('offers no cached values when no entry is fresh', async () => {
    cacheStep('a', SPEC.replace('echo a', 'echo other'));
    const view = render({ liveText: SPEC });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    expect(view.result.current.options.some((o) => o.id === 'cache')).toBe(
      false
    );
  });
});
