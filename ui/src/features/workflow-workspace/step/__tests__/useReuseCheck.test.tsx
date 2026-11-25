// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { CarriedStepCheckStatus } from '@/api/v1/schema';
import {
  clearReuseVerdicts,
  useReuseCheck,
  type ReuseCheckInput,
} from '../useReuseCheck';

const { POST, client } = vi.hoisted(() => {
  const POST = vi.fn();
  return { POST, client: { POST } };
});

vi.mock('@/hooks/api', () => ({ useClient: () => client }));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

const BASE: ReuseCheckInput = {
  fileName: 'etl.yaml',
  dagName: 'etl',
  step: 'merge',
  sourceRunId: 'run-1',
  entries: { seed: 'Run-seed' },
  draft: null,
};

function answer(carried: { step: string; runId: string }[], profile = 'prod') {
  return {
    data: {
      carried: carried.map((c) => ({
        ...c,
        status: CarriedStepCheckStatus.unchanged,
        changes: [],
      })),
      missing: [],
      profile,
    },
    response: { status: 200 },
  };
}

type Options = Parameters<typeof useReuseCheck>[1];

function setup(input: Partial<ReuseCheckInput> = {}, options?: Options) {
  return renderHook(
    (p: { input: ReuseCheckInput; options: Options }) =>
      useReuseCheck(p.input, p.options),
    {
      initialProps: {
        input: { ...BASE, ...input },
        options: options ?? { enabled: true, ask: true },
      },
    }
  );
}

describe('useReuseCheck', () => {
  beforeEach(() => {
    POST.mockReset();
    clearReuseVerdicts();
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
    expect(POST).toHaveBeenCalledWith('/dags/{fileName}/start-preview', {
      params: {
        path: { fileName: 'etl.yaml' },
        query: { remoteNode: 'node-1' },
      },
      body: { steps: ['merge'], outputsFromRunId: 'run-1' },
    });
    expect(POST).toHaveBeenCalledWith(
      '/dags/{fileName}/start-preview',
      expect.objectContaining({
        body: { steps: ['merge'], outputsFromRuns: { seed: 'Run-seed' } },
      })
    );
    expect(Object.keys(result.current.fromSource)).toEqual(['orders']);
    expect(Object.keys(result.current.fromCache)).toEqual(['seed']);
    expect(result.current.profile).toBe('prod');
  });

  // A draft is checked as the server would run it: from its text, with the
  // profile of its file or the one the user chose.
  it('sends the text of a draft with its file and the chosen profile', async () => {
    POST.mockResolvedValue(answer([], 'staging'));
    const { result } = setup({
      draft: 'steps: []',
      entries: {},
      profile: 'staging',
    });

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
          profile: 'staging',
        },
      })
    );
    expect(result.current.profile).toBe('staging');
  });

  // With nothing to reuse, the one request names the profile of the run.
  it('asks once for the profile when nothing gives a step', async () => {
    POST.mockResolvedValue(answer([], ''));
    const { result } = setup({ sourceRunId: null, entries: {} });

    await waitFor(() => expect(result.current.state).toBe('ready'));
    expect(POST).toHaveBeenCalledTimes(1);
    expect(result.current.profile).toBe('');
  });

  it('asks nothing while the source is still being chosen', () => {
    const { result } = setup({}, { enabled: false, ask: true });
    expect(result.current.state).toBe('checking');
    expect(POST).not.toHaveBeenCalled();
  });

  // The INPUT column shows what the dialog asked, and never asks itself:
  // the server evaluates the parameters to check.
  it('shares the answer with a caller that does not ask', async () => {
    POST.mockResolvedValue(answer([{ step: 'orders', runId: 'run-1' }]));
    const reader = setup({ entries: {} }, { enabled: true, ask: false });
    expect(reader.result.current.state).toBe('checking');
    expect(POST).not.toHaveBeenCalled();

    const asker = setup({ entries: {} });
    await waitFor(() => expect(asker.result.current.state).toBe('ready'));
    await waitFor(() => expect(reader.result.current.state).toBe('ready'));
    expect(POST).toHaveBeenCalledTimes(1);

    // Opening the dialog again shows the kept answer at once.
    const again = setup({ entries: {} });
    expect(again.result.current.state).toBe('ready');
    expect(POST).toHaveBeenCalledTimes(1);
  });

  // After a refusal, the answer from before it stands no longer.
  it('asks again for an answer older than since', async () => {
    POST.mockResolvedValue(answer([]));
    const first = setup({ entries: {} });
    await waitFor(() => expect(first.result.current.state).toBe('ready'));
    // The refusal comes after the kept answer.
    await new Promise((resolve) => setTimeout(resolve, 5));

    const later = setup(
      { entries: {} },
      { enabled: true, ask: true, since: Date.now() }
    );
    expect(later.result.current.state).toBe('checking');
    await waitFor(() => expect(POST).toHaveBeenCalledTimes(2));
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

  // An older remote node answers the unknown path with text, not an error
  // of the API.
  it('tells a server without the check from a failed check', async () => {
    POST.mockResolvedValue({
      error: 'no matching operation was found',
      response: { status: 404 },
    });
    const { result } = setup({ entries: {} });
    await waitFor(() => expect(result.current.state).toBe('unsupported'));

    clearReuseVerdicts();
    POST.mockResolvedValue({
      error: { code: 'not_found', message: 'dag-run run-1 not found' },
      response: { status: 404 },
    });
    const real = setup({ entries: {}, sourceRunId: 'run-1' });
    await waitFor(() => expect(real.result.current.state).toBe('failed'));
    expect(real.result.current.error).toBe('dag-run run-1 not found');
  });
});
