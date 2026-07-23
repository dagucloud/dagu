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
import ControllerStatusPage from '@/pages/controllers/controller/status';
import { createControllerDraft, serializeControllerDefinition } from '../draft';
import type { ControllerDetail } from '../types';

const controllerID = 'ctrl_aaaaaaaaaaaaaaaa';
const runRef = {
  state: 'default',
  dag: 'triage',
  dagRunId: 'run-1',
};

const fakes = vi.hoisted(() => ({
  detail: null as ControllerDetail | null,
  details: new Map<string, ControllerDetail>(),
  mutate: vi.fn(),
  prompt: vi.fn(),
  routeNavigate: null as ((to: string) => void) | null,
  showToast: vi.fn(),
  start: vi.fn(),
  stop: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: () => true,
  useCanWriteForWorkspace: () => true,
}));

vi.mock('@/features/controllers/api', () => ({
  useControllerAPI: () => ({
    prompt: fakes.prompt,
    start: fakes.start,
    stop: fakes.stop,
  }),
  useControllerDetail: (id: string | undefined) => ({
    data: (id ? fakes.details.get(id) : undefined) ?? fakes.detail,
    error: undefined,
    isLoading: false,
    mutate: fakes.mutate,
  }),
}));

vi.mock('@/components/ui/simple-toast', () => ({
  useSimpleToast: () => ({ showToast: fakes.showToast }),
}));

vi.mock('@/features/controllers/components/ControllerGraph', () => ({
  ControllerGraph: () => <div>Controller graph</div>,
}));

vi.mock('@/features/controllers/components/ControllerContext', () => ({
  ControllerContext: () => <div>Controller context</div>,
}));

vi.mock('@/features/controllers/components/ControllerStatusChip', () => ({
  ControllerStatusChip: () => <span>Controller status</span>,
}));

function detail(status: Status, id = controllerID): ControllerDetail {
  const definition = createControllerDraft('ops');
  definition.id = id;
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
    id,
    definition: definition as ControllerDetail['definition'],
    runtime: {
      status,
      statusLabel:
        status === Status.NotStarted
          ? StatusLabel.not_started
          : status === Status.Running
            ? StatusLabel.running
            : status === Status.Waiting
              ? StatusLabel.waiting
              : StatusLabel.succeeded,
      currentState: 'default',
      turnCount: 1,
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

function renderPage(id = controllerID) {
  return render(
    <MemoryRouter initialEntries={[`/controllers/${id}/status`]}>
      <NavigationProbe />
      <Routes>
        <Route
          path="/controllers/:id/status"
          element={<ControllerStatusPage />}
        />
      </Routes>
    </MemoryRouter>
  );
}

describe('ControllerStatusPage DAG run links', () => {
  beforeEach(() => {
    fakes.detail = null;
    fakes.details.clear();
    fakes.mutate.mockReset();
    fakes.mutate.mockResolvedValue(undefined);
    fakes.prompt.mockReset();
    fakes.routeNavigate = null;
    fakes.showToast.mockReset();
    fakes.start.mockReset();
    fakes.stop.mockReset();
  });

  it('shows an active ref as pending text until its summary exists', () => {
    const value = detail(Status.Waiting);
    value.runtime.activeDAGRun = runRef;
    fakes.detail = value;
    renderPage();

    expect(screen.getByText('triage').closest('a')).toBeNull();
    expect(
      screen.getByText('triage DAG run', { selector: 'span' }).closest('a')
    ).toBeNull();
    expect(screen.getByText('Pending')).toBeVisible();
  });

  it('shows a retained missing ref as expired text', () => {
    const value = detail(Status.Success);
    value.runtime.dagRunRefs = [runRef];
    value.runtime.finishedAt = '2026-01-01T00:00:00Z';
    fakes.detail = value;
    renderPage();

    expect(screen.getByText('triage').closest('a')).toBeNull();
    expect(screen.getByText('Expired')).toBeVisible();
  });

  it('does not clear the new route prompt when an old prompt request finishes', async () => {
    const user = userEvent.setup();
    const firstID = 'ctrl_aaaaaaaaaaaaaaaa';
    const secondID = 'ctrl_bbbbbbbbbbbbbbbb';
    const first = detail(Status.Waiting, firstID);
    first.runtime.waitingQuestion = 'Which region should be used?';
    const second = detail(Status.Waiting, secondID);
    second.runtime.waitingQuestion = 'Which account should be used?';
    fakes.details.set(firstID, first);
    fakes.details.set(secondID, second);
    let resolvePrompt!: () => void;
    fakes.prompt.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolvePrompt = resolve;
        })
    );
    renderPage(firstID);

    const firstPrompt = screen.getByPlaceholderText('Reply to the Controller…');
    await user.type(firstPrompt, 'ap-northeast-1');
    await user.click(screen.getByRole('button', { name: 'Send' }));

    act(() => fakes.routeNavigate?.(`/controllers/${secondID}/status`));
    expect(screen.getByText('Which account should be used?')).toBeVisible();
    const secondPrompt = screen.getByPlaceholderText(
      'Reply to the Controller…'
    );
    await user.type(secondPrompt, 'production');

    await act(async () => {
      resolvePrompt();
      await Promise.resolve();
    });

    expect(secondPrompt).toHaveValue('production');
    expect(fakes.mutate).not.toHaveBeenCalled();
    expect(fakes.showToast).not.toHaveBeenCalled();
    expect(screen.getByLabelText('Current route')).toHaveTextContent(
      `/controllers/${secondID}/status`
    );
  });

  it('does not close the new route dialog when an old start request finishes', async () => {
    const user = userEvent.setup();
    const firstID = 'ctrl_aaaaaaaaaaaaaaaa';
    const secondID = 'ctrl_bbbbbbbbbbbbbbbb';
    fakes.details.set(firstID, detail(Status.NotStarted, firstID));
    fakes.details.set(secondID, detail(Status.NotStarted, secondID));
    let resolveStart!: () => void;
    fakes.start.mockImplementation(
      () =>
        new Promise<void>((resolve) => {
          resolveStart = resolve;
        })
    );
    renderPage(firstID);

    await user.click(screen.getByRole('button', { name: 'Start' }));
    let dialog = screen.getByRole('dialog');
    await user.type(
      within(dialog).getByPlaceholderText(
        'Describe the outcome the Controller should achieve…'
      ),
      'Start the first Controller'
    );
    await user.click(within(dialog).getByRole('button', { name: 'Start' }));

    act(() => fakes.routeNavigate?.(`/controllers/${secondID}/status`));
    await user.click(screen.getByRole('button', { name: 'Start' }));
    dialog = screen.getByRole('dialog');
    const secondPrompt = within(dialog).getByPlaceholderText(
      'Describe the outcome the Controller should achieve…'
    );
    await user.type(secondPrompt, 'Start the second Controller');

    await act(async () => {
      resolveStart();
      await Promise.resolve();
    });

    expect(screen.getByRole('dialog')).toBeVisible();
    expect(secondPrompt).toHaveValue('Start the second Controller');
    expect(fakes.mutate).not.toHaveBeenCalled();
    expect(fakes.showToast).not.toHaveBeenCalled();
  });
});
