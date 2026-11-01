// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import type { ReactNode } from 'react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  NodeStatus,
  NodeStatusLabel,
  Status,
  StatusLabel,
  type components,
} from '@/api/v1/schema';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import { UserPreferencesProvider } from '@/contexts/UserPreference';
import type { RunOverlay } from '@/features/visual-editor/contracts';
import { WorkspaceDataContext } from '../../data/context';
import { nodesByStep } from '../../data/runFacts';
import type {
  DAGPageData,
  WorkspacePermissions,
  WorkspaceRunValue,
} from '../../data/types';
import { StepRunView, type StepRunTarget } from '../StepRunView';

type Node = components['schemas']['Node'];
type Details = components['schemas']['DAGRunDetails'];

const api = vi.hoisted(() => ({
  log: undefined as unknown,
  outputs: undefined as unknown,
}));
const perms = vi.hoisted(() => ({ value: {} as WorkspacePermissions }));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PATCH: vi.fn(), POST: vi.fn() }),
  useQuery: (path: string, init: unknown) => {
    let data: unknown;
    if (init) {
      if (path.endsWith('/outputs')) data = api.outputs;
      else if (path.endsWith('/log')) data = api.log;
    }
    return {
      data,
      error: undefined,
      isLoading: false,
      isValidating: false,
      mutate: () => Promise.resolve(undefined),
    };
  },
}));
vi.mock('@/hooks/useStepLogSSE', () => ({
  useStepLogSSE: () => ({
    data: null,
    isConnected: false,
    isConnecting: false,
    shouldUseFallback: true,
    error: null,
  }),
}));
vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ apiURL: '/api/v1', permissions: { runDags: true } }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('../../data/useWorkspacePermissions', () => ({
  useWorkspacePermissions: () => perms.value,
}));
vi.mock('../../dock/DockContext', () => ({
  useDock: () => ({ openStep: vi.fn() }),
  useDockCommands: () => ({ openStep: vi.fn() }),
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: null, state: null }),
}));

const STARTED = '2026-10-01T09:00:00Z';
const FINISHED = '2026-10-01T09:00:05Z';

function node(name: string, extra: Partial<Node> = {}): Node {
  return {
    step: { name },
    status: NodeStatus.Failed,
    statusLabel: NodeStatusLabel.failed,
    stdout: '/logs/build.out',
    stderr: '/logs/build.err',
    startedAt: STARTED,
    finishedAt: FINISHED,
    retryCount: 0,
    doneCount: 1,
    ...extra,
  } as Node;
}

function details(nodes: Node[]): Details {
  return {
    name: 'example',
    dagRunId: 'run-1',
    rootDAGRunName: 'example',
    rootDAGRunId: 'run-1',
    status: Status.Failed,
    statusLabel: StatusLabel.failed,
    startedAt: STARTED,
    finishedAt: FINISHED,
    autoRetryCount: 0,
    nodes,
  } as Details;
}

function runValue(d: Details): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: { name: d.name, runId: d.dagRunId },
    details: d,
    hasRun: true,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {} as RunOverlay,
    nodeByStep: nodesByStep(d),
    handlers: [],
    waiting: { approvals: 0, humanTasks: 0, agent: 0, firstWaitingStep: null },
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn().mockResolvedValue(undefined),
  };
}

function renderView(d: Details, view: ReactNode) {
  return render(
    <MemoryRouter initialEntries={['/dags/example.yaml?dock=log&step=build']}>
      <UserPreferencesProvider>
        <RemoteNodeProvider remoteNode="local">
          <WorkspaceDataContext.Provider
            value={{
              fileName: 'example.yaml',
              dagData: { dag: { name: 'example' } } as DAGPageData,
              workspaceName: null,
              remoteNode: 'local',
              run: runValue(d),
              refresh: vi.fn(),
              registerHistory: () => () => undefined,
            }}
          >
            <Routes>
              <Route path="/dags/:fileName" element={view} />
            </Routes>
          </WorkspaceDataContext.Provider>
        </RemoteNodeProvider>
      </UserPreferencesProvider>
    </MemoryRouter>
  );
}

const view = (target: StepRunTarget, variant: 'facts' | 'logs') => (
  <StepRunView target={target} variant={variant} />
);

/** Every button in the scope has a non-empty accessible name. */
function expectNamedButtons(scope: HTMLElement) {
  const buttons = within(scope).getAllByRole('button');
  expect(buttons.length).toBeGreaterThan(0);
  for (const button of buttons) {
    expect(button).toHaveAccessibleName();
  }
}

beforeAll(() => {
  Object.defineProperty(window, 'matchMedia', {
    writable: true,
    value: vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    })),
  });
});

beforeEach(() => {
  api.log = {
    content: 'first output',
    totalLines: 1,
    lineCount: 1,
    hasMore: false,
  };
  api.outputs = {
    outputs: [{ name: 'RESULT', value: '3', truncated: false, size: 1 }],
  };
  perms.value = {
    canWrite: true,
    canRunDags: true,
    canExecute: true,
    canExecuteForWorkspace: true,
    isAdmin: false,
    canManageProfiles: false,
    canManageWebhooks: false,
    canManageNotifications: false,
  };
});

describe('StepRunView accessibility', () => {
  it("names the facts region '<step> run'", () => {
    renderView(details([node('build')]), view({ step: 'build' }, 'facts'));
    expect(screen.getByRole('region', { name: 'build run' })).toBe(
      screen.getByTestId('ww-step-run')
    );
  });

  it('gives every control of the facts a role and a name', () => {
    renderView(
      details([
        node('build', {
          subRuns: [{ dagRunId: 'c-1', dagName: 'child', params: 'N=1' }],
        }),
      ]),
      view({ step: 'build' }, 'facts')
    );
    const region = screen.getByRole('region', { name: 'build run' });
    const facts = within(region);

    // The status chip is a button whose name says the status.
    const chip = facts.getByRole('button', { name: /Failed/ });
    expect(chip).toHaveAttribute('tabindex', '0');
    fireEvent.keyDown(chip, { key: 'Enter' });
    expect(screen.getByText('Update status')).toBeVisible();
    fireEvent.keyDown(screen.getByRole('dialog'), { key: 'Escape' });

    expect(facts.getByRole('link', { name: 'View stdout' })).toBeVisible();
    expect(facts.getByRole('link', { name: 'View stderr' })).toBeVisible();
    expect(facts.getByRole('button', { name: 'Retry step' })).toBeVisible();
    expect(facts.getByRole('button', { name: 'Change status' })).toBeVisible();
    expect(
      facts.queryByRole('button', { name: 'Run step' })
    ).not.toBeInTheDocument();
    expect(facts.getByRole('button', { name: 'Copy value' })).toBeVisible();
    const subRuns = facts.getByRole('region', { name: 'Sub-runs' });
    expect(
      within(subRuns).getByRole('button', { name: /View Sub DAG Run: child/ })
    ).toBeVisible();
    expectNamedButtons(region);
  });

  it('exposes the stream switch of the log with aria-pressed', () => {
    renderView(details([node('build')]), view({ step: 'build' }, 'logs'));

    // The log content remounts on a stream change: query again each time.
    const toggle = (name: 'stdout' | 'stderr') =>
      within(screen.getByRole('group', { name: 'Stream' })).getByRole(
        'button',
        { name }
      );
    expect(toggle('stdout')).toHaveAttribute('aria-pressed', 'true');
    expect(toggle('stderr')).toHaveAttribute('aria-pressed', 'false');
    fireEvent.click(toggle('stderr'));
    expect(toggle('stderr')).toHaveAttribute('aria-pressed', 'true');
    expect(toggle('stdout')).toHaveAttribute('aria-pressed', 'false');
    expectNamedButtons(screen.getByTestId('ww-step-log'));
  });
});
