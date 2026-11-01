// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import React from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useQuery } from '@/hooks/api';
import { WorkspaceKind } from '@/lib/workspace';
import { TemplateSelector } from '../TemplateSelector';

vi.mock('@/hooks/api', () => ({
  useQuery: vi.fn(),
}));

const appBarValue = {
  title: 'Cockpit',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
  workspaceSelection: { kind: WorkspaceKind.all },
};

const mockDags = [
  {
    fileName: 'example.yaml',
    dag: {
      name: 'Example DAG',
      group: 'main',
      labels: ['batch', 'workspace=ops'],
      description: 'Example workflow',
      params: [],
    },
    errors: [],
  },
];

const queryCalls: Array<{
  path: string;
  init?: unknown;
}> = [];

const useQueryMock = useQuery as unknown as {
  mockImplementation: (fn: (path: string, params?: unknown) => unknown) => void;
};

function latestQueryCall(path: string) {
  const calls = queryCalls.filter((call) => call.path === path);
  return calls[calls.length - 1];
}

function renderSelector(
  props?: Partial<React.ComponentProps<typeof TemplateSelector>>
) {
  return render(
    <AppBarContext.Provider value={appBarValue}>
      <TemplateSelector selectedWorkspace="" onSelect={vi.fn()} {...props} />
    </AppBarContext.Provider>
  );
}

afterEach(() => {
  cleanup();
  queryCalls.length = 0;
  vi.clearAllMocks();
});

describe('TemplateSelector', () => {
  it('loads dags only while open and loads labels only when the label filter is opened', () => {
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      if (path === '/dags') {
        return { data: { dags: mockDags }, isLoading: false } as never;
      }
      if (path === '/dags/labels') {
        return { data: { labels: ['batch', 'workspace=ops'] } } as never;
      }
      return { data: undefined } as never;
    });

    renderSelector();

    expect(latestQueryCall('/dags')?.init).toBeNull();
    expect(latestQueryCall('/dags/labels')?.init).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: /select template/i }));

    expect(latestQueryCall('/dags')?.init).toEqual(
      expect.objectContaining({
        params: expect.objectContaining({
          query: expect.objectContaining({ remoteNode: 'local' }),
        }),
      })
    );
    expect(latestQueryCall('/dags/labels')?.init).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: /labels/i }));

    expect(latestQueryCall('/dags/labels')?.init).toEqual(
      expect.objectContaining({
        params: expect.objectContaining({
          query: {
            remoteNode: 'local',
            workspace: WorkspaceKind.all,
          },
        }),
      })
    );
  });

  it('reports the picked template and closes without keeping /dags active', () => {
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      if (path === '/dags') {
        return { data: { dags: mockDags }, isLoading: false } as never;
      }
      if (path === '/dags/labels') {
        return { data: { labels: ['batch', 'workspace=ops'] } } as never;
      }
      return { data: undefined } as never;
    });
    const onSelect = vi.fn();

    renderSelector({ onSelect });

    fireEvent.click(screen.getByRole('button', { name: /select template/i }));
    fireEvent.click(screen.getByText('Example DAG'));

    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(
      screen.queryByPlaceholderText('Search workflows...')
    ).not.toBeInTheDocument();
    expect(latestQueryCall('/dags')?.init).toBeNull();
  });

  it('reports selector open state changes to the parent', () => {
    const onOpenChange = vi.fn();
    useQueryMock.mockImplementation((path, init) => {
      queryCalls.push({ path, init });
      if (path === '/dags') {
        return { data: { dags: mockDags }, isLoading: false } as never;
      }
      if (path === '/dags/labels') {
        return { data: { labels: [] } } as never;
      }
      return { data: undefined } as never;
    });

    renderSelector({ onOpenChange });

    expect(onOpenChange).toHaveBeenCalledWith(false);

    fireEvent.click(screen.getByRole('button', { name: /select template/i }));

    expect(onOpenChange).toHaveBeenLastCalledWith(true);
  });
});
