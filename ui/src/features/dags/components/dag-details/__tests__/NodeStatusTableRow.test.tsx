// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { components } from '@/api/v1/schema';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import { ToastProvider } from '@/components/ui/simple-toast';
import { AppBarContext } from '@/contexts/AppBarContext';
import { DAGRunContext } from '@/features/dag-runs/contexts/DAGRunContext';
import { useQuery } from '@/hooks/api';
import dayjs from '@/lib/dayjs';
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
  useQuery as unknown as (
    path: unknown,
    init: unknown,
    config?: unknown
  ) => unknown
);

const stepLogPath = '/dag-runs/{name}/{dagRunId}/steps/{stepName}/log';

const NOW = '2026-07-22T01:05:00Z';
const FIVE_MINUTES_AGO = '2026-07-22T01:00:00Z';
const ABSOLUTE_FORMAT = 'YYYY-MM-DD HH:mm:ss';

const timedNode = {
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

// Fakes only Date so relative labels are stable without stalling timers.
function freezeNow() {
  vi.useFakeTimers({ toFake: ['Date'] });
  vi.setSystemTime(new Date(NOW));
}

function renderRow(
  node: components['schemas']['Node'],
  view: 'desktop' | 'mobile',
  props: Partial<React.ComponentProps<typeof NodeStatusTableRow>> = {}
) {
  const row = (
    <NodeStatusTableRow
      rownum={1}
      node={node}
      name="example.yaml"
      dagRun={dagRun}
      view={view}
      {...props}
    />
  );
  return render(
    <MemoryRouter>
      <AppBarContext.Provider value={appBarValue}>
        {view === 'desktop' ? (
          <table>
            <tbody>{row}</tbody>
          </table>
        ) : (
          row
        )}
      </AppBarContext.Provider>
    </MemoryRouter>
  );
}

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

  afterEach(() => {
    vi.useRealTimers();
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
        '/dags/producer?remoteNode=worker-a&run=run-2'
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

  it('shows every alternative of an unmet any-of precondition', () => {
    const node = {
      step: {
        name: 'process',
        preconditions: [
          {
            condition: '${MODE}',
            expectedAny: ['full', 'minimal'],
            error: 'condition was not met',
          },
        ],
      },
      status: NodeStatus.Skipped,
      statusLabel: NodeStatusLabel.skipped,
      stdout: '',
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
            <NodeStatusTableRow
              rownum={1}
              node={node}
              name="example.yaml"
              dagRun={dagRun}
              view="mobile"
            />
          </DAGContext.Provider>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    expect(screen.getByText(/full \| minimal/)).toBeInTheDocument();
  });

  it.each(['desktop', 'mobile'] as const)(
    'shows the step start time relative to now in the %s view',
    (view) => {
      freezeNow();
      renderRow({ ...timedNode, startedAt: FIVE_MINUTES_AGO }, view);

      expect(screen.getByText('5 minutes ago')).toHaveAttribute(
        'title',
        dayjs(FIVE_MINUTES_AGO).format(ABSOLUTE_FORMAT)
      );
    }
  );

  it.each(['desktop', 'mobile'] as const)(
    'shows how often a step was retried in the %s view',
    (view) => {
      renderRow({ ...timedNode, retryCount: 2 }, view);

      expect(screen.getByText('Retried 2×')).toBeVisible();
    }
  );

  it('shows no retry count for a step that ran once', () => {
    renderRow(timedNode, 'desktop');

    expect(screen.queryByText(/Retried/)).not.toBeInTheDocument();
  });

  it.each([
    [
      'Completed by:',
      { humanTaskCompletedBy: 'alice', finishedAt: FIVE_MINUTES_AGO },
    ],
    ['Approved by:', { approvedBy: 'alice', approvedAt: FIVE_MINUTES_AGO }],
    ['Rejected by:', { rejectedBy: 'alice', rejectedAt: FIVE_MINUTES_AGO }],
  ])('shows the "%s" time relative to now', (label, fields) => {
    freezeNow();
    renderRow({ ...timedNode, ...fields }, 'desktop');

    const line = screen.getByText(label).parentElement!;
    expect(within(line).getByText('5 minutes ago')).toHaveAttribute(
      'title',
      dayjs(FIVE_MINUTES_AGO).format(ABSOLUTE_FORMAT)
    );
  });

  it.each([
    ['finished', NodeStatus.Success, 0],
    ['running', NodeStatus.Running, 2000],
  ])(
    'polls the expanded log of a %s step only while it runs',
    (_, status, interval) => {
      // Running status text animates and reads the reduced-motion preference.
      Object.defineProperty(window, 'matchMedia', {
        writable: true,
        value: vi.fn().mockImplementation((query: string) => ({
          matches: false,
          media: query,
          addEventListener: vi.fn(),
          removeEventListener: vi.fn(),
        })),
      });
      mockedUseQuery.mockClear();
      const node = {
        ...timedNode,
        status,
        stdout: '/tmp/build.out',
      } as components['schemas']['Node'];

      render(
        <MemoryRouter>
          <AppBarContext.Provider value={appBarValue}>
            <table>
              <tbody>
                <NodeStatusTableRow
                  rownum={1}
                  node={node}
                  name="example.yaml"
                  dagRun={dagRun}
                  view="desktop"
                  defaultLogExpanded
                />
              </tbody>
            </table>
          </AppBarContext.Provider>
        </MemoryRouter>
      );

      const inlineLogCalls = mockedUseQuery.mock.calls.filter(
        ([path, init]) =>
          path === stepLogPath &&
          (init as { params?: { query?: { tail?: number } } } | undefined)
            ?.params?.query?.tail === 100
      );
      expect(inlineLogCalls.length).toBeGreaterThan(0);
      for (const call of inlineLogCalls) {
        expect(call[2]).toMatchObject({ refreshInterval: interval });
      }
    }
  );

  // A row and its graph node open the same step details; the chevron keeps
  // the quick inline log.
  it('opens step details from a row click and keeps the log toggle', () => {
    const onInspect = vi.fn();
    const node = {
      ...timedNode,
      stdout: '/tmp/build.out',
    } as components['schemas']['Node'];
    render(
      <MemoryRouter>
        <AppBarContext.Provider value={appBarValue}>
          <table>
            <tbody>
              <NodeStatusTableRow
                rownum={1}
                node={node}
                name="example.yaml"
                dagRun={dagRun}
                view="desktop"
                selected
                onInspect={onInspect}
              />
            </tbody>
          </table>
        </AppBarContext.Provider>
      </MemoryRouter>
    );

    const row = screen.getByText('build').closest('tr')!;
    expect(row).toHaveAttribute('data-selected', 'true');
    fireEvent.click(row);
    expect(onInspect).toHaveBeenCalledWith('build');

    fireEvent.click(screen.getByRole('button', { name: 'Show logs' }));
    expect(screen.getByRole('button', { name: 'Hide logs' })).toHaveAttribute(
      'aria-expanded',
      'true'
    );
    expect(onInspect).toHaveBeenCalledTimes(1);
  });

  // The step name is a button, so keyboard and touch users reach the same
  // details a row click opens.
  it.each(['desktop', 'mobile'] as const)(
    'opens step details from the step name in the %s view',
    (view) => {
      const onInspect = vi.fn();
      renderRow(timedNode, view, { onInspect });

      fireEvent.click(screen.getByRole('button', { name: 'build' }));

      expect(onInspect).toHaveBeenCalledTimes(1);
      expect(onInspect).toHaveBeenCalledWith('build');
    }
  );

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
    const refresh = vi.fn();
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
        <ToastProvider>
          <AppBarContext.Provider value={appBarValue}>
            <DAGContext.Provider
              value={{
                refresh,
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
        </ToastProvider>
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
    await waitFor(() => expect(refresh).toHaveBeenCalled());
    // A success shows no notification in the middle of the screen.
    expect(screen.queryByText('Step retry started')).toBeNull();
  });

  it('retries the selected step and downstream steps when requested', async () => {
    const user = userEvent.setup();
    postMock.mockResolvedValueOnce({});
    const refresh = vi.fn();
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
        <ToastProvider>
          <AppBarContext.Provider value={appBarValue}>
            <DAGContext.Provider
              value={{
                refresh,
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
        </ToastProvider>
      </MemoryRouter>
    );

    await user.click(screen.getByRole('button', { name: 'Step actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Retry step' }));
    const dialog = screen.getByRole('dialog');
    await user.click(
      within(dialog).getByRole('radio', {
        name: /This step and all downstream steps/i,
      })
    );
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));

    await vi.waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/retry',
        {
          params: {
            path: { name: 'example', dagRunId: 'run-1' },
            query: { remoteNode: 'local' },
          },
          body: {
            dagRunId: 'run-1',
            stepName: 'build',
            includeDownstream: true,
          },
        }
      );
    });
    await waitFor(() => expect(refresh).toHaveBeenCalled());
    // A success shows no notification in the middle of the screen.
    expect(screen.queryByText('Step retry started')).toBeNull();
  });

  it('sends bypassPreconditions when the checkbox is checked', async () => {
    const user = userEvent.setup();
    postMock.mockResolvedValueOnce({});
    const refresh = vi.fn();
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
        <ToastProvider>
          <AppBarContext.Provider value={appBarValue}>
            <DAGContext.Provider
              value={{
                refresh,
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
        </ToastProvider>
      </MemoryRouter>
    );

    await user.click(screen.getByRole('button', { name: 'Step actions' }));
    await user.click(screen.getByRole('menuitem', { name: 'Retry step' }));
    const dialog = screen.getByRole('dialog');
    const checkbox = within(dialog).getByRole('checkbox', {
      name: /Bypass step preconditions/i,
    });
    expect(checkbox).not.toBeChecked();
    await user.click(checkbox);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Retry' }));

    await vi.waitFor(() => {
      expect(postMock).toHaveBeenCalledWith(
        '/dag-runs/{name}/{dagRunId}/retry',
        {
          params: {
            path: { name: 'example', dagRunId: 'run-1' },
            query: { remoteNode: 'local' },
          },
          body: {
            dagRunId: 'run-1',
            stepName: 'build',
            bypassPreconditions: true,
          },
        }
      );
    });
    await waitFor(() => expect(refresh).toHaveBeenCalled());
    // A success shows no notification in the middle of the screen.
    expect(screen.queryByText('Step retry started')).toBeNull();
  });

  it('resets the bypass preconditions checkbox each time the dialog opens', async () => {
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

    const openDialog = async () => {
      await user.click(screen.getByRole('button', { name: 'Step actions' }));
      await user.click(screen.getByRole('menuitem', { name: 'Retry step' }));
    };

    await openDialog();
    let dialog = screen.getByRole('dialog');
    await user.click(
      within(dialog).getByRole('checkbox', {
        name: /Bypass step preconditions/i,
      })
    );
    await user.click(within(dialog).getByRole('button', { name: 'Cancel' }));

    await openDialog();
    dialog = screen.getByRole('dialog');
    expect(
      within(dialog).getByRole('checkbox', {
        name: /Bypass step preconditions/i,
      })
    ).not.toBeChecked();
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

      expect(screen.getByText('Update status')).toBeVisible();
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

    fireEvent.click(screen.getByText('Waiting'));

    expect(screen.queryByText('Update status')).not.toBeInTheDocument();
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
