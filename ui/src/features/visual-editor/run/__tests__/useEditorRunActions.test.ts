// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { outputCache } from '../outputCache';
import { useEditorRunActions } from '../useEditorRunActions';

const { POST, client } = vi.hoisted(() => {
  const POST = vi.fn();
  return { POST, client: { POST } };
});

vi.mock('@/hooks/api', () => ({ useClient: () => client }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

const LABELS = ['dagu.source=editor'];
const REUSE_LABELS = [...LABELS, 'dagu.outputs-from=r1'];
const SCOPE = { remoteNode: 'node-1', fileName: 'my-dag' };

function cacheEntry(runId: string) {
  return { values: { OUT: 'x' }, runId, capturedAt: 1, stepHash: 'h' };
}

function refuse(step: string, runId: string) {
  POST.mockResolvedValueOnce({
    error: {
      message: `step ${step} cannot be reused from run ${runId}: the step or a step it depends on changed since that run`,
    },
    response: { status: 400 },
  });
}

function setup(
  overrides: Partial<Parameters<typeof useEditorRunActions>[0]> = {}
) {
  return renderHook((props) => useEditorRunActions(props), {
    initialProps: {
      fileName: 'my-dag',
      dagName: 'my-dag',
      spec: 'steps: []',
      isDirty: false,
      ...overrides,
    },
  });
}

describe('useEditorRunActions', () => {
  beforeEach(() => {
    outputCache.clear();
    POST.mockReset();
    POST.mockResolvedValue({
      data: { dagRunId: 'run-1' },
      response: { status: 200 },
    });
  });

  it('starts the unsaved draft through /dag-runs', async () => {
    const { result } = setup({ isDirty: true, spec: 'steps: [x]' });
    let id: string | null = null;
    await act(async () => {
      id = await result.current.runDraft();
    });
    expect(id).toBe('run-1');
    expect(POST).toHaveBeenCalledWith('/dag-runs', {
      params: { query: { remoteNode: 'node-1' } },
      body: { spec: 'steps: [x]', name: 'my-dag', labels: LABELS },
    });
  });

  it('keeps stable functions and sends the latest spec', async () => {
    const { result, rerender } = setup();
    const first = result.current.runDraft;
    rerender({
      fileName: 'my-dag',
      dagName: 'my-dag',
      spec: 'newer',
      isDirty: true,
    });
    expect(result.current.runDraft).toBe(first);
    await act(async () => {
      await result.current.runDraft();
    });
    expect(POST).toHaveBeenCalledWith('/dag-runs', {
      params: { query: { remoteNode: 'node-1' } },
      body: { spec: 'newer', name: 'my-dag', labels: LABELS },
    });
  });

  it('runs one saved step and reuses outputs from a run', async () => {
    const { result } = setup();
    let id: string | null = null;
    await act(async () => {
      id = await result.current.runStep('b', { reuseOutputsFrom: 'r1' });
    });
    expect(id).toBe('run-1');
    expect(POST).toHaveBeenCalledWith('/dags/{fileName}/start', {
      params: { path: { fileName: 'my-dag' }, query: { remoteNode: 'node-1' } },
      body: { steps: ['b'], outputsFromRunId: 'r1', labels: REUSE_LABELS },
    });
  });

  // The upstream steps of a step run without the step itself.
  it('runs the given saved steps and no other step', async () => {
    const { result } = setup();
    await act(async () => {
      await result.current.runSteps(['a', 'b'], { reuseOutputsFrom: 'r1' });
    });
    expect(POST).toHaveBeenCalledWith('/dags/{fileName}/start', {
      params: { path: { fileName: 'my-dag' }, query: { remoteNode: 'node-1' } },
      body: {
        steps: ['a', 'b'],
        outputsFromRunId: 'r1',
        labels: REUSE_LABELS,
      },
    });
  });

  it('runs the given steps of an unsaved draft from the buffer', async () => {
    const { result } = setup({ isDirty: true, spec: 'draft' });
    await act(async () => {
      await result.current.runSteps(['a', 'b']);
    });
    expect(POST).toHaveBeenCalledWith('/dag-runs', {
      params: { query: { remoteNode: 'node-1' } },
      body: {
        spec: 'draft',
        name: 'my-dag',
        steps: ['a', 'b'],
        ephemeral: true,
        labels: LABELS,
      },
    });
  });

  // A clean definition keeps its step runs in the history.
  it('does not mark the step run of a saved definition ephemeral', async () => {
    const { result } = setup({ isDirty: false });
    await act(async () => {
      await result.current.runSteps(['a', 'b']);
    });
    const body = POST.mock.calls[0]?.[1]?.body;
    expect('ephemeral' in body).toBe(false);
    expect(body.labels).toEqual(LABELS);
  });

  // The whole DAG never runs as a test: the bar buttons start normal runs.
  it('never marks a whole-DAG run ephemeral', async () => {
    const { result } = setup({ isDirty: true, spec: 'draft' });
    await act(async () => {
      await result.current.runDraft();
    });
    const body = POST.mock.calls[0]?.[1]?.body;
    expect('ephemeral' in body).toBe(false);
    expect('steps' in body).toBe(false);
  });

  it('names the run of each step next to the source run, and sends no value', async () => {
    const { result } = setup();
    const outputsFromRuns = { a: 'RunA' };
    await act(async () => {
      await result.current.runStep('b', {
        reuseOutputsFrom: 'r1',
        outputsFromRuns,
      });
    });
    const body = POST.mock.calls[0]?.[1]?.body;
    expect(body).toEqual({
      steps: ['b'],
      outputsFromRunId: 'r1',
      outputsFromRuns,
      labels: REUSE_LABELS,
    });
    expect('outputs' in body).toBe(false);
  });

  it('names the run of a step with no source run and marks a draft run ephemeral', async () => {
    const { result } = setup({ isDirty: true, spec: 'draft' });
    const outputsFromRuns = { a: 'RunA' };
    await act(async () => {
      await result.current.runStep('b', { outputsFromRuns });
    });
    const body = POST.mock.calls[0]?.[1]?.body;
    expect(body).toEqual({
      spec: 'draft',
      name: 'my-dag',
      steps: ['b'],
      outputsFromRuns,
      ephemeral: true,
      labels: LABELS,
    });
    expect('outputsFromRunId' in body).toBe(false);
  });

  it('sends no outputsFromRunId key without reuse', async () => {
    const { result } = setup();
    await act(async () => {
      await result.current.runStep('b');
    });
    const body = POST.mock.calls[0]?.[1]?.body;
    expect(body).toEqual({ steps: ['b'], labels: LABELS });
    expect('outputsFromRunId' in body).toBe(false);
  });

  it('runs one step of an unsaved draft through /dag-runs', async () => {
    const { result } = setup({ isDirty: true, spec: 'draft' });
    let id: string | null = null;
    await act(async () => {
      id = await result.current.runStep('b', { reuseOutputsFrom: 'r1' });
    });
    expect(id).toBe('run-1');
    expect(POST).toHaveBeenCalledTimes(1);
    expect(POST).toHaveBeenCalledWith('/dag-runs', {
      params: { query: { remoteNode: 'node-1' } },
      body: {
        spec: 'draft',
        name: 'my-dag',
        steps: ['b'],
        outputsFromRunId: 'r1',
        ephemeral: true,
        labels: REUSE_LABELS,
      },
    });
  });

  it('omits outputsFromRunId for a draft step without reuse', async () => {
    const { result } = setup({ isDirty: true });
    await act(async () => {
      await result.current.runStep('b');
    });
    const body = POST.mock.calls[0]?.[1]?.body;
    expect('outputsFromRunId' in body).toBe(false);
    expect(body.steps).toEqual(['b']);
  });

  it('reports the server error message and returns null', async () => {
    POST.mockResolvedValue({
      error: {
        code: 'bad_request',
        message: 'outputs are reused only from a finished run',
      },
      response: { status: 400 },
    });
    const { result } = setup();
    let id: string | null = 'x';
    await act(async () => {
      id = await result.current.runStep('b', { reuseOutputsFrom: 'r1' });
    });
    expect(id).toBeNull();
    expect(result.current.error).toBe(
      'outputs are reused only from a finished run'
    );
  });

  it('drops the cache entry of a step whose run the server refuses', async () => {
    outputCache.put(SCOPE, 'a', cacheEntry('RunA'));
    outputCache.put(SCOPE, 'c', cacheEntry('RunC'));
    refuse('a', 'RunA');
    const { result } = setup();
    await act(async () => {
      await result.current.runStep('b', {
        outputsFromRuns: { a: 'RunA', c: 'RunC' },
      });
    });
    expect(Object.keys(outputCache.entries(SCOPE))).toEqual(['c']);
  });

  // The entry of the step may name a newer run by the time the answer comes.
  it('keeps an entry that names another run than the refused one', async () => {
    outputCache.put(SCOPE, 'a', cacheEntry('RunA2'));
    refuse('a', 'RunA');
    const { result } = setup();
    await act(async () => {
      await result.current.runStep('b', { outputsFromRuns: { a: 'RunA' } });
    });
    expect(outputCache.entries(SCOPE)['a']?.runId).toBe('RunA2');
  });

  it('falls back to the HTTP status without a server message', async () => {
    POST.mockResolvedValue({ error: {}, response: { status: 502 } });
    const { result } = setup();
    await act(async () => {
      await result.current.runDraft();
    });
    expect(result.current.error).toBe('Could not start the run (HTTP 502).');
  });

  it('reports the message of a thrown error', async () => {
    POST.mockRejectedValue(new Error('network down'));
    const { result } = setup();
    let id: string | null = 'x';
    await act(async () => {
      id = await result.current.runDraft();
    });
    expect(id).toBeNull();
    expect(result.current.error).toBe('network down');
    expect(result.current.isStarting).toBe(false);
  });

  it('clears the error at the next start', async () => {
    POST.mockResolvedValueOnce({
      error: { message: 'boom' },
      response: { status: 500 },
    });
    const { result } = setup();
    await act(async () => {
      await result.current.runDraft();
    });
    expect(result.current.error).toBe('boom');
    await act(async () => {
      await result.current.runDraft();
    });
    expect(result.current.error).toBeNull();
  });

  it('is starting while the request is in flight', async () => {
    let resolve: (v: unknown) => void = () => {};
    POST.mockReturnValue(new Promise((r) => (resolve = r)));
    const { result } = setup();
    expect(result.current.isStarting).toBe(false);
    let pending: Promise<string | null> = Promise.resolve(null);
    act(() => {
      pending = result.current.runDraft();
    });
    expect(result.current.isStarting).toBe(true);
    await act(async () => {
      resolve({ data: { dagRunId: 'run-9' }, response: { status: 200 } });
      await pending;
    });
    expect(result.current.isStarting).toBe(false);
    await expect(pending).resolves.toBe('run-9');
  });
});
