// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, renderHook, screen } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status, type components } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { useIsAdmin } from '@/contexts/AuthContext';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import { LicenseContext } from '@/contexts/LicenseContext';
import { SubRunOpenProvider } from '@/features/dags/components/common/SubRunStackModal';
import type {
  ApiDAGRunDetails,
  DAGPageData,
  WorkspaceNotice,
  WorkspaceRunValue,
} from '../../contracts';
import {
  WorkspaceDataContext,
  type WorkspaceDataValue,
} from '../../data/context';
import type { DockController } from '../../dock/DockContext';
import { useWorkspaceNotices } from '../useWorkspaceNotices';

vi.mock('@/contexts/AuthContext', () => ({ useIsAdmin: vi.fn() }));

const ws = vi.hoisted(() => ({ dock: null as unknown }));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ws.dock,
  useDockCommands: () => ws.dock,
}));

// The sub-run list of the run, as the server reports it.
const subRunList = vi.hoisted(() => ({
  value: [] as Array<{ dagRunId: string; dagName?: string; status: number }>,
}));
vi.mock('../../data/useSubRuns', () => ({
  useSubRuns: () => ({ subRuns: subRunList.value, isLoading: false }),
}));

const useIsAdminMock = vi.mocked(useIsAdmin);

const community: LicenseStatus = {
  valid: false,
  plan: '',
  expiry: '',
  features: [],
  gracePeriod: false,
  graceEndsAt: '',
  community: true,
  source: '',
  warningCode: '',
  error: '',
};

const OFFER_KEY = 'dagu.monitoringOffer:local';

interface Setup {
  run?: Partial<WorkspaceRunValue>;
  details?: Partial<ApiDAGRunDetails> | null;
  schedule?: boolean;
  suspended?: boolean;
  license?: { license?: LicenseStatus; error?: unknown };
  /** The opener of the sub-run stack of the workspace. */
  openSubRun?: (entry: { name: string; dagRunId: string }) => void;
}

function makeRun(setup: Setup): WorkspaceRunValue {
  const details =
    setup.details === null
      ? null
      : ({
          name: 'example',
          dagRunId: 'run-7',
          status: Status.Success,
          nodes: [],
          ...setup.details,
        } as ApiDAGRunDetails);
  return {
    selection: 'latest',
    ref: { name: 'example', runId: 'run-7' },
    details,
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: { runId: 'run-7', status: Status.Success },
    overlay: {},
    nodeByStep: new Map(),
    handlers: [],
    waiting: { approvals: 0, humanTasks: 0, agent: 0, firstWaitingStep: null },
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => undefined),
    ...setup.run,
  };
}

function makeDock(): DockController {
  return {
    open: false,
    tab: 'log',
    height: 0,
    maximized: false,
    setTab: vi.fn(),
    toggle: vi.fn(),
    maximize: vi.fn(),
    openStep: vi.fn(),
    openRunLog: vi.fn(),
  };
}

function LocationProbe() {
  const location = useLocation();
  return (
    <output data-testid="location">{location.pathname + location.hash}</output>
  );
}

let run: WorkspaceRunValue;
let dock: DockController;

function renderNotices(setup: Setup = {}) {
  run = makeRun(setup);
  const dagData = {
    dag: {
      name: 'example',
      schedule: setup.schedule ? [{ expression: '0 * * * *' }] : [],
    },
    suspended: !!setup.suspended,
    localDags: [],
    latestDAGRun: run.details,
  } as unknown as DAGPageData;
  const value = {
    fileName: 'example.yaml',
    dagData,
    workspaceName: null,
    remoteNode: 'local',
    run,
    refresh: vi.fn(async () => undefined),
    registerHistory: () => () => {},
  } satisfies WorkspaceDataValue;
  const license = setup.license ?? { license: community };
  const wrapper = ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={['/dags/example.yaml']}>
      <AppBarContext.Provider value={{ selectedRemoteNode: 'local' } as never}>
        <LicenseContext.Provider value={{ ...license, mutate: vi.fn() }}>
          <WorkspaceDataContext.Provider value={value}>
            <SubRunOpenProvider value={setup.openSubRun ?? null}>
              <LocationProbe />
              {children}
            </SubRunOpenProvider>
          </WorkspaceDataContext.Provider>
        </LicenseContext.Provider>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  return renderHook(() => useWorkspaceNotices(), { wrapper });
}

function ids(notices: WorkspaceNotice[]): string[] {
  return notices.map((n) => n.id);
}

function find(notices: WorkspaceNotice[], id: string): WorkspaceNotice {
  const notice = notices.find((n) => n.id === id);
  if (!notice) throw new Error(`no notice ${id}`);
  return notice;
}

beforeEach(() => {
  subRunList.value = [];
  localStorage.clear();
  useIsAdminMock.mockReturnValue(true);
  dock = makeDock();
  ws.dock = dock;
});

describe('useWorkspaceNotices', () => {
  it('gives no notice for a quiet run', () => {
    expect(renderNotices().result.current).toEqual([]);
  });

  describe('run-not-found', () => {
    it('shows when the pinned run was not found', () => {
      const notice = find(
        renderNotices({ run: { notFound: true } }).result.current,
        'run-not-found'
      );
      expect(notice.tone).toBe('warning');
      expect(notice.message).toBe(
        'The run in the link was not found. The latest run is shown.'
      );
      expect(notice.action).toBeUndefined();
      expect(notice.dismissible).toBe(true);
    });

    it('selects latest in place when dismissed', () => {
      const { result } = renderNotices({ run: { notFound: true } });
      find(result.current, 'run-not-found').dismiss?.();
      expect(run.select).toHaveBeenCalledWith('latest', { replace: true });
    });

    // A workflow that never ran has no latest run to show instead.
    it('stays hidden when the workflow has no run', () => {
      expect(
        ids(
          renderNotices({
            run: { notFound: true, hasRun: false, ref: null, details: null },
          }).result.current
        )
      ).not.toContain('run-not-found');
    });

    it('stays hidden while the run is found', () => {
      expect(ids(renderNotices().result.current)).not.toContain(
        'run-not-found'
      );
    });
  });

  // parity:overview-approval-resume-alert parity:run-approval-resume-alert
  describe('approval-resume', () => {
    it('shows for a waiting run whose resume failed and opens Actions', () => {
      const { result } = renderNotices({
        details: { status: Status.Waiting, approvalResumePending: true },
      });
      const notice = find(result.current, 'approval-resume');
      expect(notice.tone).toBe('warning');
      expect(notice.message).toBe('Approval saved; resume failed.');
      expect(notice.action?.label).toBe('Open actions');
      notice.action?.run();
      expect(dock.setTab).toHaveBeenCalledWith('actions', { user: true });
    });

    it('stays hidden when the run is not waiting', () => {
      const { result } = renderNotices({
        details: { status: Status.Running, approvalResumePending: true },
      });
      expect(ids(result.current)).not.toContain('approval-resume');
    });

    it('stays hidden when no resume is pending', () => {
      const { result } = renderNotices({
        details: { status: Status.Waiting, approvalResumePending: false },
      });
      expect(ids(result.current)).not.toContain('approval-resume');
    });

    it('stays hidden when the workflow never ran', () => {
      const { result } = renderNotices({
        details: null,
        run: { ref: null, hasRun: false, latestRef: null },
      });
      expect(result.current).toEqual([]);
    });
  });

  describe('actions-waiting', () => {
    const waiting = (approvals: number, humanTasks: number) => ({
      run: {
        waiting: { approvals, humanTasks, agent: 0, firstWaitingStep: 'a' },
      },
    });

    it('counts approvals and human tasks and opens Actions', () => {
      const { result } = renderNotices(waiting(1, 2));
      const notice = find(result.current, 'actions-waiting');
      expect(notice.tone).toBe('info');
      expect(notice.message).toBe('3 actions are waiting.');
      expect(notice.action?.label).toBe('Open actions');
      notice.action?.run();
      expect(dock.setTab).toHaveBeenCalledWith('actions', { user: true });
    });

    it('says one action for a single human task', () => {
      const { result } = renderNotices(waiting(0, 1));
      expect(find(result.current, 'actions-waiting').message).toBe(
        'An action is waiting.'
      );
    });

    it('stays hidden when nothing waits', () => {
      expect(ids(renderNotices(waiting(0, 0)).result.current)).toEqual([]);
    });
  });

  // parity:overview-summary-agent-bar
  describe('agent-waiting', () => {
    it('keeps the text of the summary bar and opens the Agent tab', () => {
      const { result } = renderNotices({
        run: {
          waiting: {
            approvals: 0,
            humanTasks: 0,
            agent: 2,
            firstWaitingStep: 'a',
          },
        },
      });
      const notice = find(result.current, 'agent-waiting');
      expect(notice.message).toBe('An agent is waiting for an answer.');
      expect(notice.action?.label).toBe('Open agent');
      notice.action?.run();
      expect(dock.setTab).toHaveBeenCalledWith('agent', { user: true });
    });

    it('stays hidden when no agent waits', () => {
      expect(ids(renderNotices().result.current)).not.toContain(
        'agent-waiting'
      );
    });
  });

  describe('newer-run-active', () => {
    const pinned = (latestStatus: Status, latestRunId = 'run-9'): Setup => ({
      run: {
        selection: { runId: 'run-7' },
        isLatest: false,
        latestRef: { runId: latestRunId, status: latestStatus },
      },
    });

    it('shows when a run is pinned and the latest run is active', () => {
      const { result } = renderNotices(pinned(Status.Running));
      const notice = find(result.current, 'newer-run-active');
      expect(notice.message).toBe('A newer run is active');
      expect(notice.action?.label).toBe('Follow latest');
      notice.action?.run();
      expect(run.select).toHaveBeenCalledWith('latest');
    });

    it.each([Status.Queued, Status.Waiting, Status.NotStarted])(
      'counts status %s as active',
      (status) => {
        expect(ids(renderNotices(pinned(status)).result.current)).toContain(
          'newer-run-active'
        );
      }
    );

    it('stays hidden when no run is pinned', () => {
      const { result } = renderNotices({
        run: { latestRef: { runId: 'run-7', status: Status.Running } },
      });
      expect(ids(result.current)).not.toContain('newer-run-active');
    });

    it('stays hidden when the latest run is finished', () => {
      const { result } = renderNotices(pinned(Status.Success));
      expect(ids(result.current)).not.toContain('newer-run-active');
    });

    // The API names an older run that is still active as the latest one.
    it('stays hidden when the active latest run is older than the pinned run', () => {
      const { result } = renderNotices({
        details: { dagRunId: 'run-7', startedAt: '2026-01-01T10:00:05Z' },
        run: {
          selection: { runId: 'run-7' },
          latestRef: {
            runId: 'run-9',
            status: Status.Running,
            at: Date.parse('2026-01-01T10:00:00Z'),
          },
        },
      });
      expect(ids(result.current)).not.toContain('newer-run-active');
    });

    it('shows when the active latest run is newer than the pinned run', () => {
      const { result } = renderNotices({
        details: { dagRunId: 'run-7', startedAt: '2026-01-01T10:00:00Z' },
        run: {
          selection: { runId: 'run-7' },
          latestRef: {
            runId: 'run-9',
            status: Status.Queued,
            at: Date.parse('2026-01-01T10:00:05Z'),
          },
        },
      });
      expect(ids(result.current)).toContain('newer-run-active');
    });

    it('stays hidden when the pinned run is the latest run', () => {
      const { result } = renderNotices(pinned(Status.Running, 'run-7'));
      expect(ids(result.current)).not.toContain('newer-run-active');
    });
  });

  // parity:overview-monitoring-offer parity:shell-monitoring-offer
  describe('monitoring-offer', () => {
    it('offers monitoring for a scheduled workflow and links to the license page', () => {
      const { result } = renderNotices({ schedule: true });
      const notice = find(result.current, 'monitoring-offer');
      expect(notice.message).toBe('Get an email if this server stops');
      expect(notice.action?.label).toBe('Set up for free');
      expect(notice.dismissible).toBe(true);
      act(() => notice.action?.run());
      expect(screen.getByTestId('location')).toHaveTextContent(
        '/license#monitoring'
      );
    });

    it('stays dismissed for the server once dismissed', () => {
      const { result } = renderNotices({ schedule: true });
      act(() => find(result.current, 'monitoring-offer').dismiss?.());
      expect(ids(result.current)).not.toContain('monitoring-offer');
      expect(localStorage.getItem(OFFER_KEY)).toBe('dismissed');
    });

    it('stays hidden without a schedule', () => {
      expect(ids(renderNotices().result.current)).not.toContain(
        'monitoring-offer'
      );
    });

    it('stays hidden for a suspended workflow', () => {
      const { result } = renderNotices({ schedule: true, suspended: true });
      expect(ids(result.current)).not.toContain('monitoring-offer');
    });

    it('stays hidden for a non-admin', () => {
      useIsAdminMock.mockReturnValue(false);
      const { result } = renderNotices({ schedule: true });
      expect(ids(result.current)).not.toContain('monitoring-offer');
    });

    it('stays hidden after an earlier dismissal', () => {
      localStorage.setItem(OFFER_KEY, 'dismissed');
      const { result } = renderNotices({ schedule: true });
      expect(ids(result.current)).not.toContain('monitoring-offer');
    });

    it.each([
      [
        'a licensed server',
        {
          license: {
            ...community,
            community: false,
            valid: true,
            plan: 'team',
          },
        },
      ],
      ['a license still loading', {}],
      ['an unreadable license', { error: new Error('unavailable') }],
      ['an environment license', { license: { ...community, source: 'env' } }],
    ])('stays hidden for %s', (_, license) => {
      const { result } = renderNotices({ schedule: true, license });
      expect(ids(result.current)).not.toContain('monitoring-offer');
    });
  });

  // The runtime shows a call step as waiting while its child run waits for
  // a person; the actions are in the child run.
  describe('sub-run-waiting', () => {
    const fanOut = {
      step: { name: 'fan', call: 'worker' },
      status: NodeStatus.Waiting,
      subRuns: [
        { dagRunId: 'w-1', dagName: 'worker', params: '' },
        { dagRunId: 'w-2', dagName: 'worker', params: '' },
      ],
    } as unknown as components['schemas']['Node'];

    it('opens the child run that waits on the stack', () => {
      subRunList.value = [
        { dagRunId: 'w-1', dagName: 'worker', status: Status.Success },
        { dagRunId: 'w-2', dagName: 'worker', status: Status.Waiting },
      ];
      const openSubRun = vi.fn();
      const { result } = renderNotices({
        details: { status: Status.Waiting, nodes: [fanOut] },
        openSubRun,
      });

      const notice = find(result.current, 'sub-run-waiting');
      expect(notice.message).toBe('Sub-run worker is waiting for an action.');
      notice.action?.run();
      expect(openSubRun).toHaveBeenCalledWith({
        name: 'worker',
        dagRunId: 'w-2',
      });
    });

    it('stays hidden while no call step waits', () => {
      const { result } = renderNotices({
        details: {
          status: Status.Success,
          nodes: [{ ...fanOut, status: NodeStatus.Success }],
        },
        openSubRun: vi.fn(),
      });
      expect(ids(result.current)).not.toContain('sub-run-waiting');
    });
  });

  it('lists every notice with its priority, highest first', () => {
    const { result } = renderNotices({
      schedule: true,
      details: { status: Status.Waiting, approvalResumePending: true },
      run: {
        notFound: true,
        selection: { runId: 'run-7' },
        latestRef: { runId: 'run-9', status: Status.Running },
        waiting: {
          approvals: 1,
          humanTasks: 0,
          agent: 1,
          firstWaitingStep: 'a',
        },
      },
    });
    expect(ids(result.current)).toEqual([
      'run-not-found',
      'approval-resume',
      'agent-waiting',
      'actions-waiting',
      'newer-run-active',
      'monitoring-offer',
    ]);
    const priorities = result.current.map((n) => n.priority);
    expect([...priorities].sort((a, b) => b - a)).toEqual(priorities);
    expect(new Set(priorities).size).toBe(priorities.length);
  });

  it('keeps the same list while nothing changes', () => {
    const { result, rerender } = renderNotices({ run: { notFound: true } });
    const first = result.current;
    rerender();
    expect(result.current).toBe(first);
  });
});
