// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React from 'react';
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { StepMessagesTable } from '../StepMessagesTable';

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const useQueryMock = useQuery as unknown as {
  mockImplementation: (
    fn: (path: string, init?: unknown) => unknown
  ) => void;
};

function renderTable(
  props?: Partial<React.ComponentProps<typeof StepMessagesTable>>
) {
  return render(
    <AppBarContext.Provider value={appBarValue}>
      <StepMessagesTable
        dagName="child-dag"
        dagRunId="child-run"
        stepName="step-a"
        isActive={false}
        {...props}
      />
    </AppBarContext.Provider>
  );
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('StepMessagesTable', () => {
  it('uses the regular messages query by default and disables the sub-dag query by null init', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      return {
        data: { messages: [], toolDefinitions: [] },
        isLoading: false,
      } as never;
    });

    renderTable();

    expect(
      queryCalls.find((call) => call.path === '/dag-runs/{name}/{dagRunId}/steps/{stepName}/messages')?.init
    ).toEqual(
      expect.objectContaining({
        params: expect.objectContaining({
          path: { name: 'child-dag', dagRunId: 'child-run', stepName: 'step-a' },
        }),
      })
    );
    expect(
      queryCalls.find(
        (call) =>
          call.path ===
          '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/messages'
      )?.init
    ).toBeNull();
    expect(screen.getByText('No messages recorded')).toBeInTheDocument();
  });

  it('uses the sub-dag messages query and disables the regular query by null init', () => {
    const queryCalls: Array<{ path: string; init?: unknown }> = [];
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      return {
        data: { messages: [], toolDefinitions: [] },
        isLoading: false,
      } as never;
    });

    renderTable({
      subDAGRunId: 'sub-run',
      rootDagName: 'root-dag',
      rootDagRunId: 'root-run',
    });

    expect(
      queryCalls.find(
        (call) =>
          call.path ===
          '/dag-runs/{name}/{dagRunId}/sub-dag-runs/{subDAGRunId}/steps/{stepName}/messages'
      )?.init
    ).toEqual(
      expect.objectContaining({
        params: expect.objectContaining({
          path: {
            name: 'root-dag',
            dagRunId: 'root-run',
            subDAGRunId: 'sub-run',
            stepName: 'step-a',
          },
        }),
      })
    );
    expect(
      queryCalls.find((call) => call.path === '/dag-runs/{name}/{dagRunId}/steps/{stepName}/messages')?.init
    ).toBeNull();
    expect(screen.getByText('No messages recorded')).toBeInTheDocument();
  });
});
