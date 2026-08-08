// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { DAGRunContext } from '@/features/dag-runs/contexts/DAGRunContext';
import { useQuery } from '@/hooks/api';
import { DAGContext } from '../../../contexts/DAGContext';
import NodeStatusTableRow from '../NodeStatusTableRow';

const configMock = vi.hoisted(() => ({
  runDags: true,
}));
const postMock = vi.hoisted(() => vi.fn());

vi.mock('@/hooks/api', () => ({
  useClient: () => ({
    PATCH: vi.fn(),
    POST: postMock,
  }),
  useQuery: vi.fn(),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: configMock.runDags,
    },
  }),
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({
    showError: vi.fn(),
  }),
}));

const appBarValue = {
  title: 'DAGs',
  setTitle: vi.fn(),
  remoteNodes: ['local'],
  setRemoteNodes: vi.fn(),
  selectedRemoteNode: 'local',
  selectRemoteNode: vi.fn(),
};

const dagRun = {
  name: 'example',
  dagRunId: 'run-1',
  status: Status.Success,
  statusLabel: StatusLabel.succeeded,
  startedAt: '',
  finishedAt: '',
  autoRetryCount: 0,
} as components['schemas']['DAGRunDetails'];

const mockedUseQuery = vi.mocked(
  useQuery as unknown as (path: unknown, init: unknown) => unknown
);

const stepLogPath = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log';

describe('NodeStatusTableRow', () => {
  beforeEach(() => {
    configMock.runDags = true;
    postMock.mockReset();
    mockedUseQuery.mockImplementation((path, init) => ({
      data:
        path === stepLogPath && init
          ? { content: 'Deploying production\n' }
          : undefined,
      isLoading: false,
      error: undefined,
      isValidating: false,
      mutate: vi.fn(),
    }));
  });

  it('keeps the selected remote node in producer run links', async () => {
    const user = userEvent.setup();
    const node = {
      step: { name: 'build' },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
      build: {
        decision: 'reuse',
        phase: 'complete',
        reason: 'matched',
        producerRun: { name: 'producer', id: 'run-2' },
      },
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider
          value={{
            ...appBarValue,
            remoteNodes: ['worker-a'],
            selectedRemoteNode: 'worker-a',
          }}
        >
          <table>
            <tbody>
              <NodeStatusTableRow
                rownum={1}
                node={node}
                name="example.yaml"
                dagRun={dagRun}
                view="desktop"
              />
            </tbody>
          </table>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    await user.hover(screen.getByText('reused'));
    const links = await screen.findAllByRole('link', {
      name: 'Produced by producer:run-2',
    });
    expect(links.length).toBeGreaterThan(0);
    for (const link of links) {
      expect(link).toHaveAttribute(
        'href',
        '/dag-runs/producer/run-2?remoteNode=worker-a'
      );
    }
  });

  it('shows log step messages in the status table without opening step logs', () => {
    const node = {
      step: {
        name: 'announce',
        executorConfig: {
          type: 'log',
          config: {
            message: 'Deploying ${ENVIRONMENT}',
          },
        },
      },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '/tmp/announce.out',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    const message = screen.getByLabelText('Log message: Deploying production');
    expect(message).toBeInTheDocument();
    expect(message.querySelector('svg')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Deploying ${ENVIRONMENT}')
    ).not.toBeInTheDocument();
    expect(screen.getByText('stdout')).toBeInTheDocument();
  });

  it('falls back to the configured log message when stdout cannot be loaded', () => {
    mockedUseQuery.mockImplementation((path, init) => ({
      data: undefined,
      isLoading: false,
      error:
        path === stepLogPath && init ? new Error('log not found') : undefined,
      isValidating: false,
      mutate: vi.fn(),
    }));

    const node = {
      step: {
        name: 'announce',
        executorConfig: {
          type: 'log',
          config: {
            message: 'Deploying ${ENVIRONMENT}',
          },
        },
      },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '/tmp/announce.out',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.getByLabelText('Log message: Deploying ${ENVIRONMENT}')
    ).toBeInTheDocument();
    expect(screen.queryByText('Loading log output...')).not.toBeInTheDocument();
  });

  it('hides step retry controls when DAG runs are disabled', () => {
    configMock.runDags = false;

    const node = {
      step: {
        name: 'build',
      },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.queryByRole('button', { name: 'Step actions' })
    ).not.toBeInTheDocument();
  });

  it.each([
    ['waiting', Status.Waiting],
    ['queued', Status.Queued],
  ])('hides step retry controls while the DAG run is %s', (_, status) => {
    const node = {
      step: { name: 'build' },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];
    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={{
                    ...dagRun,
                    status,
                  }}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.queryByRole('button', { name: 'Step actions' })
    ).not.toBeInTheDocument();
  });

  it('hides step actions for rows the step APIs cannot address', () => {
    const node = {
      step: { name: 'onInit' },
      status: NodeStatus.Failed,
      statusLabel: NodeStatusLabel.failed,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                  hideActions
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(
      screen.queryByRole('button', { name: 'Step actions' })
    ).not.toBeInTheDocument();
  });

  it('keeps the retry dialog open when the API rejects the request', async () => {
    const user = userEvent.setup();
    postMock.mockResolvedValueOnce({
      error: { message: 'The DAG run cannot be retried' },
    });
    const node = {
      step: { name: 'build' },
      status: NodeStatus.Success,
      statusLabel: NodeStatusLabel.succeeded,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    await user.click(screen.getByRole('button', { name: 'Step actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Retry step' }));
    const dialog = screen.getByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));

    expect(
      await screen.findByText('The DAG run cannot be retried')
    ).toBeVisible();
    expect(screen.getByRole('dialog')).toBeVisible();
    expect(postMock).toHaveBeenCalledWith('/dag-runs/{name}/{dagRunId}/retry', {
      params: {
        path: { name: 'example', dagRunId: 'run-1' },
        query: { remoteNode: 'local' },
      },
      body: { dagRunId: 'run-1', stepName: 'build' },
    });
  });

  it('retries a child step through its root DAG run', async () => {
    const user = userEvent.setup();
    postMock.mockResolvedValueOnce({});
    const node = {
      step: { name: 'build' },
      status: NodeStatus.Failed,
      statusLabel: NodeStatusLabel.failed,
      stdout: '',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 1,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'child',
              fileName: 'child.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="child.yaml"
                  dagRun={{
                    ...dagRun,
                    name: 'child',
                    dagRunId: 'child-run',
                    rootDAGRunName: 'root',
                    rootDAGRunId: 'root-run',
                  }}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    await user.click(screen.getByRole('button', { name: 'Step actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Retry step' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Retry' })
    );

    await vi.waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/retry',
        {
          params: {
            path: { name: 'root', dagRunId: 'root-run' },
            query: { remoteNode: 'local' },
          },
          body: {
            dagRunId: 'root-run',
            stepName: 'build',
            subDAGRunId: 'child-run',
          },
        }
      );
    });
  });

  it.each(['desktop', 'mobile'] as const)(
    'disables child step retries while the root run is running in the %s view',
    async (view) => {
      const user = userEvent.setup();
      const node = {
        step: { name: 'build' },
        status: NodeStatus.Success,
        statusLabel: NodeStatusLabel.succeeded,
        stdout: '',
        stderr: '',
        startedAt: '',
        finishedAt: '',
        retryCount: 0,
        doneCount: 1,
      } as components['schemas']['Node'];
      const row = (
        <NodeStatusTableRow
          rownum={1}
          node={node}
          name="child.yaml"
          dagRun={{
            ...dagRun,
            name: 'child',
            dagRunId: 'child-run',
            rootDAGRunName: 'root',
            rootDAGRunId: 'root-run',
          }}
          view={view}
        />
      );

      render(
        <MemoryRouter>
          <AppBarContext.Provider value={appBarValue}>
            <DAGRunContext.Provider
              value={{
                refresh: vi.fn(),
                name: 'child',
                dagRunId: 'child-run',
                rootStatus: Status.Running,
              }}
            >
              <DAGContext.Provider
                value={{
                  refresh: vi.fn(),
                  name: 'child',
                  fileName: 'child.yaml',
                }}
              >
                {view === 'desktop' ? (
                  <table>
                    <tbody>{row}</tbody>
                  </table>
                ) : (
                  row
                )}
              </DAGContext.Provider>
            </DAGRunContext.Provider>
          </AppBarContext.Provider>
        </MemoryRouter>
      );

      await user.click(screen.getByRole('button', { name: 'Step actions' }));
      expect(
        screen.getByRole('menuitem', { name: 'Retry step' })
      ).toHaveAttribute('data-disabled');
    }
  );

  it.each(['desktop', 'mobile'] as const)(
    'opens status updates from the visible actions menu in the %s view',
    async (view) => {
      const user = userEvent.setup();
      const node = {
        step: { name: 'build' },
        status: NodeStatus.Success,
        statusLabel: NodeStatusLabel.succeeded,
        stdout: '',
        stderr: '',
        startedAt: '',
        finishedAt: '',
        retryCount: 0,
        doneCount: 1,
      } as components['schemas']['Node'];
      const row = (
        <NodeStatusTableRow
          rownum={1}
          node={node}
          name="example.yaml"
          dagRun={dagRun}
          view={view}
        />
      );

      render(
        <MemoryRouter>
          <AppBarContext.Provider value={appBarValue}>
            <DAGContext.Provider
              value={{
                refresh: vi.fn(),
                name: 'example',
                fileName: 'example.yaml',
              }}
            >
              {view === 'desktop' ? (
                <table>
                  <tbody>{row}</tbody>
                </table>
              ) : (
                row
              )}
            </DAGContext.Provider>
          </AppBarContext.Provider>
        </MemoryRouter>
      );

      await user.click(screen.getByRole('button', { name: 'Step actions' }));
      await user.click(screen.getByRole('menuitem', { name: 'Change status' }));

      expect(screen.getByText('Update Status')).toBeVisible();
    }
  );

  it.each([
    {
      name: 'human task',
      step: {
        name: 'review',
        humanTask: { prompt: 'Review deployment' },
      },
      dagRunStatus: Status.Success,
    },
    {
      name: 'waiting approval',
      step: {
        name: 'review',
        approval: { prompt: 'Review deployment' },
      },
      dagRunStatus: Status.Waiting,
    },
  ])('does not open status updates for a $name', ({ step, dagRunStatus }) => {
    const node = {
      step,
      status: NodeStatus.Waiting,
      statusLabel: NodeStatusLabel.waiting,
      stdout: '/tmp/review.out',
      stderr: '',
      startedAt: '',
      finishedAt: '',
      retryCount: 0,
      doneCount: 0,
    } as components['schemas']['Node'];

    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <DAGContext.Provider
            value={{
              refresh: vi.fn(),
              name: 'example',
              fileName: 'example.yaml',
            }}
          >
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={{ ...dagRun, status: dagRunStatus }}
                  view="desktop"
                />
              </tbody>
            </table>
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    fireEvent.click(screen.getByText(NodeStatusLabel.waiting));

    expect(screen.queryByText('Update Status')).not.toBeInTheDocument();
  });

  it.each(['desktop', 'mobile'] as const)(
    'shows the human-task completer subject ID in the %s view',
    async (view) => {
      const user = userEvent.setup();
      const node = {
        step: {
          name: 'review',
          humanTask: { prompt: 'Review deployment' },
        },
        status: NodeStatus.Success,
        statusLabel: NodeStatusLabel.succeeded,
        stdout: '',
        stderr: '',
        startedAt: '2026-07-22T01:00:00Z',
        finishedAt: '2026-07-22T01:05:00Z',
        retryCount: 0,
        doneCount: 1,
        humanTaskCompletedBy: 'alice',
        humanTaskCompletedById: 'user-1',
      } as components['schemas']['Node'];

      const row = (
        <NodeStatusTableRow
          rownum={1}
          node={node}
          name="example.yaml"
          dagRun={dagRun}
          view={view}
        />
      );

      render(
        <MemoryRouter>
          <AppBarContext.Provider value={appBarValue}>
            <DAGContext.Provider
              value={{
                refresh: vi.fn(),
                name: 'example',
                fileName: 'example.yaml',
              }}
            >
              {view === 'desktop' ? (
                <table>
                  <tbody>{row}</tbody>
                </table>
              ) : (
                row
              )}
            </DAGContext.Provider>
          </AppBarContext.Provider>
        </MemoryRouter>
      );

      expect(screen.getByText('Completed by:')).toBeInTheDocument();
      const subject = screen.getByText('alice');
      await user.hover(subject);
      expect(await screen.findByRole('tooltip')).toHaveTextContent(
        'Subject ID: user-1'
      );
    }
  );
});
