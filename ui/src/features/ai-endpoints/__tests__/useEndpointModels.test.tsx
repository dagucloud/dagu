// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useEndpointModels } from '../useEndpointModels';

const mocks = vi.hoisted(() => ({
  data: undefined as unknown,
  init: undefined as unknown,
}));

vi.mock('@/hooks/api', () => ({
  useQuery: (_path: string, init: unknown) => {
    mocks.init = init;
    return { data: init === null ? undefined : mocks.data };
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'local',
}));

const dag = { name: 'fix-bug', workspace: 'team' };

beforeEach(() => {
  mocks.data = {
    choices: [
      {
        name: 'cle1',
        protocol: 'anthropic',
        preset: 'anthropic',
        scope: 'global',
        models: ['claude-opus-5-5', 'claude-haiku-5'],
      },
      {
        name: 'plain',
        protocol: 'anthropic',
        preset: 'custom',
        scope: 'global',
        models: [],
      },
    ],
  };
  mocks.init = undefined;
});

describe('useEndpointModels', () => {
  it('gives the models of the endpoint that the DAG may use', () => {
    const { result } = renderHook(() => useEndpointModels('cle1', dag));
    expect(result.current).toEqual({
      models: ['claude-opus-5-5', 'claude-haiku-5'],
      infos: [{ id: 'claude-opus-5-5' }, { id: 'claude-haiku-5' }],
      loaded: true,
      known: true,
    });
    // It asks the route of choices for the DAG, as the Endpoint select does.
    expect(mocks.init).toEqual({
      params: {
        query: { remoteNode: 'local', dag: 'fix-bug', workspace: 'team' },
      },
    });
  });

  it('gives the display name and the efforts of the models that the server describes', () => {
    mocks.data = {
      choices: [
        {
          name: 'cle1',
          protocol: 'anthropic',
          preset: 'anthropic',
          scope: 'global',
          models: ['claude-opus-5-5', 'claude-haiku-5'],
          modelInfo: [
            {
              id: 'claude-opus-5-5',
              displayName: 'Claude Opus 5.5',
              efforts: ['low', 'high'],
            },
          ],
        },
      ],
    };
    const { result } = renderHook(() => useEndpointModels('cle1', dag));
    expect(result.current.infos).toEqual([
      {
        id: 'claude-opus-5-5',
        displayName: 'Claude Opus 5.5',
        efforts: ['low', 'high'],
      },
      { id: 'claude-haiku-5' },
    ]);
  });

  it('gives an empty list for an endpoint with no list', () => {
    const { result } = renderHook(() => useEndpointModels('plain', dag));
    expect(result.current).toEqual({
      models: [],
      infos: [],
      loaded: true,
      known: true,
    });
  });

  it('gives an empty list for a name that the DAG may not use', () => {
    const { result } = renderHook(() => useEndpointModels('other', dag));
    expect(result.current).toEqual({
      models: [],
      infos: [],
      loaded: true,
      known: false,
    });
  });

  it('gives an empty list for a step with no endpoint', () => {
    for (const name of [undefined, '', '  ']) {
      const { result } = renderHook(() => useEndpointModels(name, dag));
      expect(result.current).toEqual({
        models: [],
        infos: [],
        loaded: false,
        known: false,
      });
    }
  });

  it('is not loaded until the choices arrive, and asks nothing for a DAG with no name', () => {
    mocks.data = undefined;
    const pending = renderHook(() => useEndpointModels('cle1', dag));
    expect(pending.result.current.loaded).toBe(false);
    expect(pending.result.current.models).toEqual([]);

    renderHook(() => useEndpointModels('cle1', { name: '' }));
    expect(mocks.init).toBeNull();
    renderHook(() => useEndpointModels('cle1', undefined));
    expect(mocks.init).toBeNull();
  });

  it('keeps the same list between renders, so a field does not redraw for nothing', () => {
    const { result, rerender } = renderHook(() =>
      useEndpointModels('cle1', dag)
    );
    const first = result.current.models;
    rerender();
    expect(result.current.models).toBe(first);
  });
});
