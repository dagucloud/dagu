// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useClient } from '@/hooks/api';
import DAGRunBatchActions from '../DAGRunBatchActions';

const postMock = vi.fn();
const getMock = vi.fn();
const deleteMock = vi.fn();

vi.mock('@/hooks/api', () => ({
  useClient: vi.fn(),
}));

const useClientMock = vi.mocked(useClient);
const appBarContextValue = {
  title: '',
  setTitle: vi.fn(),
  remoteNodes: [],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const createDeferred = <T,>() => {
  let resolve: (value: T) => void = () => undefined;
  const promise = new Promise<T>((nextResolve) => {
    resolve = nextResolve;
  });
  return { promise, resolve };
};

describe('DAGRunBatchActions', () => {
  beforeEach(() => {
    postMock.mockReset();
    getMock.mockReset();
    deleteMock.mockReset();
    useClientMock.mockReturnValue({
      DELETE: deleteMock,
      GET: getMock,
      POST: postMock,
    } as never);
  });

  it('offers bulk actions only once runs are selected', () => {
    const renderActions = (
      selectedRuns: { name: string; dagRunId: string }[]
    ) => (
      <AppBarContext.Provider value={appBarContextValue}>
        <DAGRunBatchActions
          selectedRuns={selectedRuns}
          onReplaceSelection={vi.fn()}
          onActionComplete={vi.fn()}
        />
      </AppBarContext.Provider>
    );
    const { rerender } = render(renderActions([]));

    expect(screen.queryByRole('button')).not.toBeInTheDocument();

    rerender(renderActions([{ name: 'alpha', dagRunId: 'run-1' }]));

    expect(
      screen.getByRole('button', { name: 'Retry selected' })
    ).toBeEnabled();
    expect(
      screen.getByRole('button', { name: 'Reschedule selected' })
    ).toBeEnabled();
    expect(screen.getByRole('button', { name: 'Delete (1)' })).toBeEnabled();
  });

  it('submits reschedules sequentially and keeps failed items selected', async () => {
    const firstRequest = createDeferred<{
      data: {
        dagRunId: string;
        queued: boolean;
      };
    }>();
    const secondRequest = createDeferred<{
      error: {
        message: string;
      };
    }>();
    const actionComplete = createDeferred<void>();

    postMock
      .mockImplementationOnce(() => firstRequest.promise)
      .mockImplementationOnce(() => secondRequest.promise);
    getMock
      .mockResolvedValueOnce({
        data: {
          dagRunDetails: {
            specFromFile: true,
          },
        },
      })
      .mockResolvedValueOnce({
        data: {
          dagRunDetails: {
            specFromFile: true,
          },
        },
      });

    const onActionComplete = vi
      .fn()
      .mockImplementation(() => actionComplete.promise);
    const onReplaceSelection = vi.fn();

    render(
      <AppBarContext.Provider value={appBarContextValue}>
        <DAGRunBatchActions
          selectedRuns={[
            { name: 'alpha', dagRunId: 'run-1' },
            { name: 'beta', dagRunId: 'run-2' },
          ]}
          onReplaceSelection={onReplaceSelection}
          onActionComplete={onActionComplete}
        />
      </AppBarContext.Provider>
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Reschedule selected' })
    );
    expect(await screen.findByLabelText('Use original DAG file')).toBeChecked();
    fireEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Reschedule 2 Runs',
      })
    );

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(1));
    expect(postMock).toHaveBeenNthCalledWith(
      1,
      '/dag-runs/{name}/{dagRunId}/reschedule',
      {
        params: {
          path: {
            name: 'alpha',
            dagRunId: 'run-1',
          },
          query: {
            remoteNode: 'local',
          },
        },
        body: {
          dagRunId: undefined,
          useCurrentDagFile: true,
        },
      }
    );

    expect(screen.getByText('Submitting requests...')).toBeInTheDocument();
    firstRequest.resolve({
      data: {
        dagRunId: 'run-1-copy',
        queued: false,
      },
    });

    await waitFor(() => expect(postMock).toHaveBeenCalledTimes(2));
    expect(
      await screen.findAllByTestId('batch-action-result-item')
    ).toHaveLength(1);
    expect(screen.getByText('Submitting requests...')).toBeInTheDocument();
    expect(postMock).toHaveBeenNthCalledWith(
      2,
      '/dag-runs/{name}/{dagRunId}/reschedule',
      {
        params: {
          path: {
            name: 'beta',
            dagRunId: 'run-2',
          },
          query: {
            remoteNode: 'local',
          },
        },
        body: {
          dagRunId: undefined,
          useCurrentDagFile: true,
        },
      }
    );

    secondRequest.resolve({
      error: {
        message: 'unable to reschedule',
      },
    });

    expect(
      await screen.findByText('Refreshing the DAG-run list')
    ).toBeInTheDocument();
    expect(screen.getByText('Refreshing...')).toBeDisabled();
    expect(await screen.findByText('unable to reschedule')).toBeInTheDocument();

    actionComplete.resolve();

    const items = await screen.findAllByTestId('batch-action-result-item');
    expect(await screen.findByRole('button', { name: 'Close' })).toBeVisible();

    expect(onActionComplete).toHaveBeenCalledTimes(1);
    expect(onReplaceSelection).toHaveBeenCalledWith([
      { name: 'beta', dagRunId: 'run-2' },
    ]);
    expect(items[0]).toHaveTextContent('alpha');
    expect(items[0]).toHaveTextContent('run-1-copy');
    expect(items[1]).toHaveTextContent('beta');
    expect(items[1]).toHaveTextContent('unable to reschedule');
  });

  it('uses the existing retry endpoint and shows submission-only success details', async () => {
    postMock.mockResolvedValueOnce({});
    const onReplaceSelection = vi.fn();

    render(
      <AppBarContext.Provider value={appBarContextValue}>
        <DAGRunBatchActions
          selectedRuns={[{ name: 'alpha', dagRunId: 'run-1' }]}
          onReplaceSelection={onReplaceSelection}
          onActionComplete={vi.fn().mockResolvedValue(undefined)}
        />
      </AppBarContext.Provider>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Retry selected' }));
    fireEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Retry 1 Run',
      })
    );

    expect(
      await screen.findByText('Retry request accepted')
    ).toBeInTheDocument();
    expect(postMock).toHaveBeenCalledWith('/dag-runs/{name}/{dagRunId}/retry', {
      params: {
        path: {
          name: 'alpha',
          dagRunId: 'run-1',
        },
        query: {
          remoteNode: 'local',
        },
      },
      body: {
        dagRunId: 'run-1',
      },
    });
    expect(onReplaceSelection).toHaveBeenCalledWith([]);
  });

  it('uses the delete endpoint and shows destructive confirmation details', async () => {
    deleteMock.mockResolvedValueOnce({});
    const onReplaceSelection = vi.fn();

    render(
      <AppBarContext.Provider value={appBarContextValue}>
        <DAGRunBatchActions
          selectedRuns={[{ name: 'alpha', dagRunId: 'run-1' }]}
          onReplaceSelection={onReplaceSelection}
          onActionComplete={vi.fn().mockResolvedValue(undefined)}
        />
      </AppBarContext.Provider>
    );

    fireEvent.click(screen.getByRole('button', { name: 'Delete (1)' }));

    const dialog = await screen.findByRole('dialog');
    expect(
      within(dialog).getByText(
        'This permanently removes run records, logs, artifacts, and related run data.'
      )
    ).toBeInTheDocument();

    fireEvent.click(
      within(dialog).getByRole('button', {
        name: 'Delete 1 Run',
      })
    );

    expect(
      await screen.findByText('Delete request accepted')
    ).toBeInTheDocument();
    expect(deleteMock).toHaveBeenCalledWith('/dag-runs/{name}/{dagRunId}', {
      params: {
        path: {
          name: 'alpha',
          dagRunId: 'run-1',
        },
        query: {
          remoteNode: 'local',
        },
      },
    });
    expect(onReplaceSelection).toHaveBeenCalledWith([]);
  });

  it('treats a reschedule response without a new run ID as a failure', async () => {
    postMock.mockResolvedValueOnce({
      data: {},
    });
    getMock.mockResolvedValueOnce({
      data: {
        dagRunDetails: {
          specFromFile: false,
        },
      },
    });

    render(
      <AppBarContext.Provider value={appBarContextValue}>
        <DAGRunBatchActions
          selectedRuns={[{ name: 'alpha', dagRunId: 'run-1' }]}
          onReplaceSelection={vi.fn()}
          onActionComplete={vi.fn().mockResolvedValue(undefined)}
        />
      </AppBarContext.Provider>
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Reschedule selected' })
    );
    fireEvent.click(
      within(await screen.findByRole('dialog')).getByRole('button', {
        name: 'Reschedule 1 Run',
      })
    );

    expect(
      await screen.findByText(
        'Reschedule request did not return a new DAG run ID.'
      )
    ).toBeInTheDocument();
  });

  it('shows the reschedule file checkbox as disabled when any selected run has no source file', async () => {
    getMock
      .mockResolvedValueOnce({
        data: {
          dagRunDetails: {
            specFromFile: true,
          },
        },
      })
      .mockResolvedValueOnce({
        data: {
          dagRunDetails: {
            specFromFile: false,
          },
        },
      });

    render(
      <AppBarContext.Provider value={appBarContextValue}>
        <DAGRunBatchActions
          selectedRuns={[
            { name: 'alpha', dagRunId: 'run-1' },
            { name: 'beta', dagRunId: 'run-2' },
          ]}
          onReplaceSelection={vi.fn()}
          onActionComplete={vi.fn().mockResolvedValue(undefined)}
        />
      </AppBarContext.Provider>
    );

    fireEvent.click(
      screen.getByRole('button', { name: 'Reschedule selected' })
    );

    const dialog = await screen.findByRole('dialog');
    await within(dialog).findByText(
      'Stored YAML snapshots will be used because one or more selected DAG runs do not have the original DAG file available.'
    );
    const checkbox = within(dialog).getByLabelText('Use original DAG file');
    expect(checkbox).not.toBeChecked();
    expect(checkbox).toBeDisabled();
  });
});
