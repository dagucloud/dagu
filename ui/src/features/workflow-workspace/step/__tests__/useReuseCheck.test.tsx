// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CarriedStepCheckStatus, StepChangeKind } from '@/api/v1/schema';
import { useReuseCheck } from '../useReuseCheck';

const { POST, client } = vi.hoisted(() => {
  const POST = vi.fn();
  return { POST, client: { POST } };
});

vi.mock('@/hooks/api', () => ({ useClient: () => client }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

type Props = Parameters<typeof useReuseCheck>[0];

const BASE: Props = {
  fileName: 'etl.yaml',
  dagName: 'etl',
  step: 'merge',
  sourceRunId: 'run-1',
  entries: { seed: 'Run-seed' },
  draft: null,
  enabled: true,
};

function answer(carried: { step: string; runId: string }[]) {
  return {
    data: {
      carried: carried.map((c) => ({
        ...c,
        status: CarriedStepCheckStatus.unchanged,
        changes: [],
      })),
      missing: [],
    },
    response: { status: 200 },
  };
}

function setup(props: Partial<Props> = {}) {
  return renderHook((p: Props) => useReuseCheck(p), {
    initialProps: { ...BASE, ...props },
  });
}

describe('useReuseCheck', () => {
  beforeEach(() => {
    POST.mockReset();
  });

  // One request asks about the source run and one about the cache entries,
  // with the steps of the start.
  it('asks about the source run and the cache entries of the saved file', async () => {
    POST.mockImplementation(
      (_path: string, init: { body: { outputsFromRunId?: string } }) =>
        Promise.resolve(
          init.body.outputsFromRunId
            ? answer([{ step: 'orders', runId: 'run-1' }])
            : answer([{ step: 'seed', runId: 'Run-seed' }])
        )
    );
    const { result } = setup();

    await waitFor(() => expect(result.current.state).toBe('ready'));
    expect(POST).toHaveBeenCalledWith(
      '/dags/{fileName}/start-preview',
      expect.objectContaining({
        params: {
          path: { fileName: 'etl.yaml' },
          query: { remoteNode: 'node-1' },
        },
        body: { steps: ['merge'], outputsFromRunId: 'run-1' },
      })
    );
    expect(POST).toHaveBeenCalledWith(
      '/dags/{fileName}/start-preview',
      expect.objectContaining({
        body: { steps: ['merge'], outputsFromRuns: { seed: 'Run-seed' } },
      })
    );
    expect(Object.keys(result.current.fromSource)).toEqual(['orders']);
    expect(Object.keys(result.current.fromCache)).toEqual(['seed']);
  });

  // A draft is checked as the server would run it: from its text, with the
  // profile of its file.
  it('sends the text of a draft with its file', async () => {
    POST.mockResolvedValue(answer([]));
    const { result } = setup({ draft: 'steps: []', entries: {} });

    await waitFor(() => expect(result.current.state).toBe('ready'));
    expect(POST).toHaveBeenCalledTimes(1);
    expect(POST).toHaveBeenCalledWith(
      '/dag-runs/preview',
      expect.objectContaining({
        body: {
          spec: 'steps: []',
          name: 'etl',
          draftOf: 'etl.yaml',
          steps: ['merge'],
          outputsFromRunId: 'run-1',
        },
      })
    );
  });

  it('answers at once that nothing changed when nothing gives a step', async () => {
    const { result } = setup({ sourceRunId: null, entries: {} });

    await waitFor(() => expect(result.current.state).toBe('ready'));
    expect(POST).not.toHaveBeenCalled();
  });

  it('asks nothing while the source is still being chosen', () => {
    const { result } = setup({ enabled: false });
    expect(result.current.state).toBe('checking');
    expect(POST).not.toHaveBeenCalled();
  });

  it('reports the message of a failed check and asks again on retry', async () => {
    POST.mockResolvedValue({
      error: { code: 'internal_error', message: 'store unavailable' },
      response: { status: 500 },
    });
    const { result } = setup({ entries: {} });

    await waitFor(() => expect(result.current.state).toBe('failed'));
    expect(result.current.error).toBe('store unavailable');

    POST.mockResolvedValue(answer([]));
    act(() => result.current.retry());
    await waitFor(() => expect(result.current.state).toBe('ready'));
    expect(POST).toHaveBeenCalledTimes(2);
  });

  // The answer for an earlier source never shows for the current one.
  it('drops the answer of a source that is no longer current', async () => {
    let finishOld: (value: unknown) => void = () => undefined;
    POST.mockImplementationOnce(
      () => new Promise((resolve) => (finishOld = resolve))
    ).mockResolvedValue({
      data: {
        carried: [
          {
            step: 'orders',
            runId: 'run-2',
            status: CarriedStepCheckStatus.changed,
            changes: [StepChangeKind.step],
          },
        ],
        missing: [],
      },
      response: { status: 200 },
    });
    const { result, rerender } = setup({ entries: {} });
    rerender({ ...BASE, entries: {}, sourceRunId: 'run-2' });

    await waitFor(() => expect(result.current.state).toBe('ready'));
    finishOld(answer([{ step: 'orders', runId: 'run-1' }]));
    await Promise.resolve();
    expect(result.current.fromSource['orders']?.runId).toBe('run-2');
  });
});
