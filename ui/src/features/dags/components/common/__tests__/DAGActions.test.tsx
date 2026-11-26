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
import { MemoryRouter, useLocation } from 'react-router-dom';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import {
  type components,
  NodeStatus,
  RestartPolicy,
  ServiceDAGRestartHold,
  ServiceDAGState,
  Status,
  StatusLabel,
} from '@/api/v1/schema';
import DAGActions from '../DAGActions';
import type { StartRequest } from '../../dag-execution/startRequest';

type ServiceDAG = components['schemas']['ServiceDAG'];
import { AppBarContext } from '@/contexts/AppBarContext';
import { DAGContext } from '@/features/dags/contexts/DAGContext';

const client = vi.hoisted(() => ({ POST: vi.fn(), GET: vi.fn() }));
const runProgress = vi.hoisted(() => ({ pushRunProgress: vi.fn() }));

vi.mock('../../dag-execution/RunProgressStack', () => ({
  pushRunProgress: runProgress.pushRunProgress,
}));

vi.mock('../../dag-execution/StartDAGModal', () => ({
  default: ({
    visible,
    onSubmit,
  }: {
    visible: boolean;
    onSubmit: (request: StartRequest) => Promise<void>;
  }) =>
    visible ? (
      <>
        <button onClick={() => void onSubmit({ params: '', immediate: true })}>
          Submit start
        </button>
        <button onClick={() => void onSubmit({ params: '', immediate: false })}>
          Submit enqueue
        </button>
      </>
    ) : null,
}));

vi.mock('../../../../../contexts/ConfigContext', () => ({
  useConfig: () => ({
    permissions: {
      runDags: true,
    },
  }),
}));

vi.mock('../../../../../hooks/api', () => ({
  useClient: () => client,
  useQuery: () => ({
    data: undefined,
    isLoading: false,
  }),
}));

vi.mock('../../../../../contexts/AuthContext', () => ({
  useCanManageProfiles: () => false,
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({
    showError: vi.fn(),
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({
    showToast: vi.fn(),
  }),
}));

function LocationProbe() {
  const location = useLocation();
  return (
    <output aria-label="Location">{location.pathname + location.search}</output>
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  client.GET.mockResolvedValue({ data: { dag: { name: 'example' } } });
  client.POST.mockResolvedValue({ data: { dagRunId: 'created-run' } });
});

describe('DAGActions', () => {
  it.each(['start', 'enqueue'])(
    'shows progress without leaving the page after %s',
    async (action) => {
      render(
        <MemoryRouter initialEntries={['/dags']}>
          <AppBarContext.Provider
            value={{ selectedRemoteNode: 'edge' } as never}
          >
            <LocationProbe />
            <DAGActions
              fileName="example-file"
              dag={{ name: 'example' }}
              displayMode="full"
            />
          </AppBarContext.Provider>
        </MemoryRouter>
      );
      fireEvent.click(screen.getByRole('button', { name: 'Start' }));
      fireEvent.click(
        await screen.findByRole('button', { name: `Submit ${action}` })
      );
      await waitFor(() =>
        expect(runProgress.pushRunProgress).toHaveBeenCalledWith({
          dagName: 'example',
          fileName: 'example-file',
          dagRunId: 'created-run',
          remoteNode: 'edge',
        })
      );
      expect(screen.getByLabelText('Location')).toHaveTextContent('/dags');
    }
  );

  it('shows progress for DAGs started by a containing panel', async () => {
    render(
      <MemoryRouter initialEntries={['/dags']}>
        <LocationProbe />
        <DAGContext.Provider
          value={{
            name: 'example',
            fileName: 'example-file',
            refresh: vi.fn(),
          }}
        >
          <DAGActions
            fileName="example-file"
            dag={{ name: 'example' }}
            displayMode="full"
          />
        </DAGContext.Provider>
      </MemoryRouter>
    );
    fireEvent.click(screen.getByRole('button', { name: 'Start' }));
    fireEvent.click(
      await screen.findByRole('button', { name: 'Submit start' })
    );
    await waitFor(() =>
      expect(runProgress.pushRunProgress).toHaveBeenCalledWith({
        dagName: 'example',
        fileName: 'example-file',
        dagRunId: 'created-run',
        remoteNode: 'local',
      })
    );
    expect(screen.getByLabelText('Location')).toHaveTextContent('/dags');
  });
  it('shows cancel for failed runs with pending auto retries', () => {
    render(
      <DAGActions
        status={{
          name: 'retry-dag',
          dagRunId: 'run-1',
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
          artifactsAvailable: false,
          autoRetryCount: 1,
          autoRetryLimit: 3,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="retry-dag.yaml"
        dag={{ name: 'retry-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(screen.getByRole('button', { name: 'Cancel' })).toBeEnabled();
  });

  it('disables retry for running DAG executions', () => {
    const view = render(
      <DAGActions
        status={{
          name: 'running-dag',
          dagRunId: 'run-1',
          status: Status.Running,
          statusLabel: StatusLabel.running,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="running-dag.yaml"
        dag={{ name: 'running-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    const queries = within(view.container);

    expect(queries.getByRole('button', { name: 'Retry' })).toBeDisabled();
    expect(queries.getByRole('button', { name: 'Stop' })).toBeEnabled();
  });

  it('disables retry for waiting DAG executions', () => {
    const view = render(
      <DAGActions
        status={{
          name: 'waiting-dag',
          dagRunId: 'run-1',
          status: Status.Waiting,
          statusLabel: StatusLabel.waiting,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="waiting-dag.yaml"
        dag={{ name: 'waiting-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(
      within(view.container).getByRole('button', { name: 'Retry' })
    ).toBeDisabled();
  });

  // The list has no Approve, so it offers no Reject either: the run decides
  // its approvals and human tasks. Stop ends the run without deciding.
  it('offers Stop but not Reject for a run waiting on an approval', () => {
    const view = render(
      <DAGActions
        status={
          {
            name: 'approval-dag',
            dagRunId: 'run-1',
            status: Status.Waiting,
            statusLabel: StatusLabel.waiting,
            artifactsAvailable: false,
            autoRetryCount: 0,
            startedAt: '',
            finishedAt: '',
            nodes: [
              {
                step: { name: 'approve', approval: {} },
                status: NodeStatus.Waiting,
              },
            ],
          } as unknown as React.ComponentProps<typeof DAGActions>['status']
        }
        fileName="approval-dag.yaml"
        dag={{ name: 'approval-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    const queries = within(view.container);
    expect(queries.queryByRole('button', { name: 'Reject' })).toBeNull();
    expect(queries.getByRole('button', { name: 'Stop' })).toBeEnabled();
  });

  it('disables retry when there is no DAG run id', () => {
    const view = render(
      <DAGActions
        status={undefined}
        fileName="finished-dag.yaml"
        dag={{ name: 'finished-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(
      within(view.container).getByRole('button', { name: 'Retry' })
    ).toBeDisabled();
  });

  it('hides whole-run retry for child DAG runs', () => {
    const view = render(
      <DAGActions
        status={{
          name: 'child-dag',
          dagRunId: 'child-run',
          rootDAGRunName: 'root-dag',
          rootDAGRunId: 'root-run',
          status: Status.Failed,
          statusLabel: StatusLabel.failed,
          artifactsAvailable: false,
          autoRetryCount: 0,
          startedAt: '',
          finishedAt: '',
        }}
        fileName="child-dag.yaml"
        dag={{ name: 'child-dag' }}
        displayMode="full"
      />,
      { wrapper: MemoryRouter }
    );

    expect(
      within(view.container).queryByRole('button', { name: 'Retry' })
    ).not.toBeInTheDocument();
  });

  // The workflow header puts Stop and Retry next to the run and Start apart, so
  // it renders two copies, each with some of the buttons.
  describe('only', () => {
    const run = {
      name: 'example',
      dagRunId: 'run-1',
      status: Status.Failed,
      statusLabel: StatusLabel.failed,
    } as never;

    function renderOnly(only: Array<'start' | 'stop' | 'retry'>) {
      render(
        <MemoryRouter>
          <DAGActions
            fileName="example-file"
            dag={{ name: 'example' }}
            status={run}
            displayMode="full"
            only={only}
          />
        </MemoryRouter>
      );
    }

    it('shows only Stop and Retry for the run', () => {
      renderOnly(['stop', 'retry']);

      expect(screen.getByRole('button', { name: /Retry/ })).toBeInTheDocument();
      expect(
        screen.getByRole('button', { name: /Stop|Cancel/ })
      ).toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: 'Start' })
      ).not.toBeInTheDocument();
    });

    it('shows only Start apart from the run', () => {
      renderOnly(['start']);

      expect(screen.getByRole('button', { name: 'Start' })).toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: /Retry/ })
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('button', { name: /Stop|Cancel/ })
      ).not.toBeInTheDocument();
    });

    // The workflow bar shows the text on a wide bar only (container query):
    // the name stays for the screen reader, and the dialog opens as with the
    // text button.
    it('shows the Start text on a wide bar only, with its name', () => {
      render(
        <MemoryRouter>
          <DAGActions
            fileName="example-file"
            dag={{ name: 'example' }}
            displayMode="full"
            only={['start']}
            barStart
          />
        </MemoryRouter>
      );

      const start = screen.getByRole('button', { name: 'Start' });
      expect(start).toHaveAttribute('aria-label', 'Start');
      expect(start.querySelector('svg')).not.toBeNull();
      expect(within(start).getByText('Start').closest('.sr-only')).toHaveClass(
        '@7xl:not-sr-only'
      );
      fireEvent.click(start);
      expect(
        screen.getByRole('button', { name: 'Submit start' })
      ).toBeVisible();
    });

    it('opens the start dialog on request in the copy that has Start only', () => {
      render(
        <MemoryRouter>
          <DAGContext.Provider value={{ autoOpenStartModal: true } as never}>
            <DAGActions
              fileName="example-file"
              dag={{ name: 'example' }}
              status={run}
              displayMode="full"
              only={['stop', 'retry']}
            />
            <DAGActions
              fileName="example-file"
              dag={{ name: 'example' }}
              status={run}
              displayMode="full"
              only={['start']}
            />
          </DAGContext.Provider>
        </MemoryRouter>
      );

      expect(
        screen.getAllByRole('button', { name: 'Submit start' })
      ).toHaveLength(1);
    });
  });

  // A service workflow has one run at a time, and the scheduler starts its
  // next run by itself. A list row or card keeps Start, Retry and Stop in
  // fixed slots; while the scheduler starts the runs, it takes the slots of
  // Start and Retry. The bar has no Start then.
  describe('service workflow', () => {
    const run = (status: Status) =>
      ({
        name: 'svc',
        dagRunId: 'run-1',
        status,
        statusLabel: StatusLabel.running,
        autoRetryCount: 0,
        startedAt: '2026-11-08T10:00:00Z',
        finishedAt: '',
      }) as never;
    const always = {
      name: 'svc',
      restartPolicy: RestartPolicy.always,
      restartWaitSec: 5,
    };
    const unlessStopped = {
      name: 'svc',
      restartPolicy: RestartPolicy.unless_stopped,
      restartWaitSec: 5,
    };
    // The service DAG as GET /service-dags lists it.
    const svc = (fields: Partial<ServiceDAG>): ServiceDAG => ({
      fileName: 'svc',
      name: 'svc',
      restartPolicy: RestartPolicy.always,
      restartWaitSec: 5,
      queue: 'svc',
      state: ServiceDAGState.running,
      suspended: false,
      consecutiveFailures: 0,
      ...fields,
    });

    function renderService(
      dag: { name: string; restartPolicy?: RestartPolicy },
      status: Status,
      extra: Partial<React.ComponentProps<typeof DAGActions>> = {}
    ) {
      return render(
        <MemoryRouter>
          <DAGActions
            fileName="svc"
            dag={dag}
            status={run(status)}
            markActions
            {...extra}
          />
        </MemoryRouter>
      );
    }

    // The slots in their order: an action, marked when it is disabled, or
    // the text of the scheduler slot.
    const slots = (container: HTMLElement) =>
      Array.from(container.firstElementChild?.children ?? []).map((el) => {
        const button = el.querySelector('button');
        if (!button) {
          return el.textContent;
        }
        const action = button.getAttribute('data-ww-action');
        return button.disabled ? `${action} (disabled)` : `${action}`;
      });
    const SCHEDULER = 'Auto';

    it.each([
      [Status.Running, [SCHEDULER, 'stop']],
      [Status.Waiting, [SCHEDULER, 'stop']],
      [Status.Queued, [SCHEDULER, 'stop (disabled)']],
    ])(
      'gives Start and Retry to the scheduler while a run is active (status %s)',
      (status, want) => {
        const { container } = renderService(always, status);
        expect(slots(container)).toEqual(want);
      }
    );

    // The scheduler starts the next run alone in these states.
    it.each([ServiceDAGState.restarting, ServiceDAGState.failing])(
      'gives Start and Retry to the scheduler in the state %s',
      (state) => {
        const { container } = renderService(always, Status.Success, {
          service: svc({ state }),
        });
        expect(slots(container)).toEqual([SCHEDULER, 'stop (disabled)']);
      }
    );

    it.each<[string, Partial<ServiceDAG>]>([
      ['pending', { state: ServiceDAGState.pending }],
      ['suspended', { state: ServiceDAGState.suspended }],
      ['stopped', { state: ServiceDAGState.stopped }],
      [
        'on hold',
        {
          state: ServiceDAGState.failing,
          restartHold: ServiceDAGRestartHold.paused,
        },
      ],
      [
        'unreadable',
        { state: ServiceDAGState.failing, readError: 'status file is corrupt' },
      ],
    ])('keeps Start and Retry when the DAG is %s', (_, fields) => {
      const { container } = renderService(always, Status.Failed, {
        service: svc(fields),
      });
      expect(slots(container)).toEqual(['start', 'retry', 'stop (disabled)']);
    });

    it('keeps Start and Retry, as for a plain workflow, when the latest run ended', () => {
      const { container } = renderService(unlessStopped, Status.Success);
      expect(slots(container)).toEqual(['start', 'retry', 'stop (disabled)']);
    });

    it('ignores the state of a plain workflow', () => {
      const { container } = renderService({ name: 'svc' }, Status.Success, {
        service: svc({ state: ServiceDAGState.restarting }),
      });
      expect(slots(container)).toEqual(['start', 'retry', 'stop (disabled)']);
    });

    it('keeps the slots of a plain workflow with a running run', () => {
      const { container } = renderService({ name: 'svc' }, Status.Running);
      expect(slots(container)).toEqual(['start', 'retry (disabled)', 'stop']);
    });

    it('gives Start and Retry to the scheduler for a run that has not started for 5 minutes at most', () => {
      const { container } = render(
        <MemoryRouter>
          <DAGActions
            fileName="svc"
            dag={always}
            status={
              {
                name: 'svc',
                dagRunId: 'run-1',
                status: Status.NotStarted,
                statusLabel: StatusLabel.not_started,
                queuedAt: new Date().toISOString(),
                startedAt: '-',
              } as never
            }
            markActions
          />
        </MemoryRouter>
      );
      expect(slots(container)).toEqual([SCHEDULER, 'stop (disabled)']);
    });

    it('does not open the Start dialog on request while a run is active', () => {
      render(
        <MemoryRouter>
          <DAGContext.Provider value={{ autoOpenStartModal: true } as never}>
            <DAGActions
              fileName="svc"
              dag={always}
              status={run(Status.Running)}
            />
          </DAGContext.Provider>
        </MemoryRouter>
      );
      expect(screen.queryByRole('button', { name: 'Submit start' })).toBeNull();
    });

    // The server refuses the start: the bar has no Start, and a request
    // opens no dialog.
    it('has no Start in a bar while a run is active', () => {
      render(
        <MemoryRouter>
          <DAGContext.Provider value={{ autoOpenStartModal: true } as never}>
            <DAGActions
              fileName="svc"
              dag={always}
              status={run(Status.Running)}
              barStart
            />
          </DAGContext.Provider>
        </MemoryRouter>
      );
      expect(screen.queryByRole('button', { name: 'Start' })).toBeNull();
      expect(screen.queryByRole('button', { name: 'Submit start' })).toBeNull();
    });

    it('has no Start in a bar while the scheduler restarts the DAG', () => {
      render(
        <MemoryRouter>
          <DAGActions
            fileName="svc"
            dag={always}
            status={run(Status.Failed)}
            service={svc({ state: ServiceDAGState.restarting })}
            barStart
          />
        </MemoryRouter>
      );
      expect(screen.queryByRole('button', { name: 'Start' })).toBeNull();
    });

    it('keeps the Start of a bar while the restarts are on hold', () => {
      render(
        <MemoryRouter>
          <DAGActions
            fileName="svc"
            dag={always}
            status={run(Status.Failed)}
            service={svc({
              state: ServiceDAGState.failing,
              restartHold: ServiceDAGRestartHold.paused,
            })}
            barStart
          />
        </MemoryRouter>
      );
      expect(screen.getByRole('button', { name: 'Start' })).toBeEnabled();
    });

    it('shows the restart wait in the Stop tooltip for always', async () => {
      renderService(always, Status.Running);
      await userEvent.hover(screen.getByRole('button', { name: 'Stop' }));
      expect(
        (await screen.findAllByText(/It restarts after 5 s/))[0]
      ).toHaveTextContent(
        'Stop this run. It restarts after 5 s. Suspend the restarts to stop it for good.'
      );
    });

    it('keeps the plain Stop tooltip for unless-stopped', async () => {
      renderService(unlessStopped, Status.Running);
      await userEvent.hover(screen.getByRole('button', { name: 'Stop' }));
      expect(
        (await screen.findAllByText('Stop the latest run'))[0]
      ).toBeTruthy();
      expect(screen.queryByText(/It restarts after/)).toBeNull();
    });

    // The scheduler starts no run after the stop of such a DAG.
    it.each<[string, Partial<ServiceDAG>]>([
      ['suspended', { state: ServiceDAGState.running, suspended: true }],
      [
        'on hold',
        {
          state: ServiceDAGState.restarting,
          restartHold: ServiceDAGRestartHold.paused,
        },
      ],
    ])(
      'keeps the plain Stop tooltip for an always DAG that is %s',
      async (_, fields) => {
        renderService(always, Status.Running, { service: svc(fields) });
        await userEvent.hover(screen.getByRole('button', { name: 'Stop' }));
        expect(
          (await screen.findAllByText('Stop the latest run'))[0]
        ).toBeTruthy();
        expect(screen.queryByText(/It restarts after/)).toBeNull();
      }
    );
  });
});
