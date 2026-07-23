// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
  useNavigate,
} from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';

import { Status, StatusLabel } from '@/api/v1/schema';
import { AppBarContext } from '@/contexts/AppBarContext';
import { WorkspaceKind } from '@/lib/workspace';
import ControllersPage from '@/pages/controllers';
import { createControllerDraft, serializeControllerDefinition } from '../draft';
import type { ControllerDetail, ControllerSummary } from '../types';

const controllerID = 'ctrl_aaaaaaaaaaaaaaaa';

const fakes = vi.hoisted(() => ({
  get: vi.fn(),
  listMutate: vi.fn(),
  routeNavigate: null as ((to: string) => void) | null,
  summary: null as ControllerSummary | null,
  showToast: vi.fn(),
  start: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: () => true,
  useCanWriteForWorkspace: () => true,
}));

vi.mock('@/features/controllers/api', () => ({
  useControllerAPI: () => ({
    delete: vi.fn(),
    get: fakes.get,
    start: fakes.start,
    stop: vi.fn(),
  }),
  useControllerList: () => ({
    data: { controllers: [fakes.summary ?? controllerSummary()] },
    error: undefined,
    isLoading: false,
    mutate: fakes.listMutate,
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: fakes.showToast }),
}));

vi.mock('@/features/controllers/components/ControllerStatusChip', () => ({
  ControllerStatusChip: () => <span>Controller status</span>,
}));

function controllerSummary(): ControllerSummary {
  return {
    id: controllerID,
    name: 'Incident Controller',
    workspace: 'ops',
    status: Status.NotStarted,
    statusLabel: StatusLabel.not_started,
    currentState: '',
    turnCount: 0,
    maxTurns: 100,
    resourceUpdatedAt: '2026-01-01T00:00:00Z',
  };
}

function controllerDetail(): ControllerDetail {
  const definition = createControllerDraft('ops');
  definition.id = controllerID;
  definition.name = 'Incident Controller';
  definition.description = 'Route incident work.';
  definition.llm.model = 'gpt-5';
  definition.states.default = {
    description: 'Finish the requested work.',
    dags: [],
    transitions: [],
    terminal: 'succeeded',
  };
  return {
    id: controllerID,
    definition: definition as ControllerDetail['definition'],
    runtime: {
      status: Status.NotStarted,
      statusLabel: StatusLabel.not_started,
      currentState: '',
      turnCount: 0,
      dagRunRefs: [],
      context: [],
    },
    dagRuns: [],
    spec: serializeControllerDefinition(definition),
    warnings: [],
    resourceUpdatedAt: '2026-01-01T00:00:00Z',
  };
}

function NavigationProbe() {
  const navigate = useNavigate();
  const location = useLocation();
  fakes.routeNavigate = (to) => navigate(to);
  return <output aria-label="Current route">{location.pathname}</output>;
}

function renderPage() {
  return render(
    <AppBarContext.Provider
      value={
        {
          selectedRemoteNode: 'local',
          setTitle: vi.fn(),
          workspaceSelection: { kind: WorkspaceKind.default },
        } as never
      }
    >
      <MemoryRouter initialEntries={['/controllers']}>
        <NavigationProbe />
        <Routes>
          <Route path="/controllers" element={<ControllersPage />} />
          <Route path="/other" element={<div>Other page</div>} />
        </Routes>
      </MemoryRouter>
    </AppBarContext.Provider>
  );
}

describe('ControllersPage', () => {
  beforeEach(() => {
    fakes.get.mockReset();
    fakes.listMutate.mockReset();
    fakes.listMutate.mockResolvedValue(undefined);
    fakes.routeNavigate = null;
    fakes.summary = null;
    fakes.showToast.mockReset();
    fakes.start.mockReset();
  });

  it('does not navigate for a late duplicate response after leaving the list', async () => {
    const user = userEvent.setup();
    let resolveGet!: (detail: ControllerDetail) => void;
    fakes.get.mockImplementation(
      () =>
        new Promise<ControllerDetail>((resolve) => {
          resolveGet = resolve;
        })
    );
    renderPage();
    await user.click(
      screen.getByRole('button', { name: 'Actions for Incident Controller' })
    );
    await user.click(await screen.findByText('Duplicate'));

    act(() => fakes.routeNavigate?.('/other'));
    expect(screen.getByText('Other page')).toBeVisible();

    await act(async () => {
      resolveGet(controllerDetail());
      await Promise.resolve();
    });

    expect(screen.getByLabelText('Current route')).toHaveTextContent('/other');
    expect(screen.getByText('Other page')).toBeVisible();
  });

  it('does not navigate for a late start response after leaving the list', async () => {
    const user = userEvent.setup();
    let resolveStart!: () => void;
    fakes.start.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveStart = resolve;
        })
    );
    renderPage();
    await user.click(screen.getByRole('button', { name: 'Start' }));
    const dialog = screen.getByRole('dialog');
    await user.type(
      within(dialog).getByPlaceholderText(
        'Describe the outcome the Controller should achieve…'
      ),
      'Route this incident'
    );
    await user.click(within(dialog).getByRole('button', { name: 'Start' }));

    act(() => fakes.routeNavigate?.('/other'));
    expect(screen.getByText('Other page')).toBeVisible();

    await act(async () => {
      resolveStart();
      await Promise.resolve();
    });

    expect(screen.getByLabelText('Current route')).toHaveTextContent('/other');
    expect(screen.getByText('Other page')).toBeVisible();
  });

  it('does not link an active DAG run until its summary exists', () => {
    fakes.summary = {
      ...controllerSummary(),
      status: Status.Running,
      statusLabel: StatusLabel.running,
      activeDAGRun: {
        state: 'default',
        dag: 'triage',
        dagRunId: 'run-1',
      },
      latestDAGRun: {
        state: 'default',
        dag: 'triage',
        dagRunId: 'run-1',
      },
    };
    renderPage();

    expect(screen.getByText('triage').closest('a')).toBeNull();
    expect(screen.getByText('Pending')).toBeVisible();
  });
});
