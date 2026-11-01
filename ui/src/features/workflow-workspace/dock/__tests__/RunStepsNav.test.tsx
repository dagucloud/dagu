// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { NodeStatus, Status } from '@/api/v1/schema';
import { node, run } from '../../data/__tests__/fixtures';
import { nodesByStep, NO_WAITING } from '../../data/runFacts';
import type { ApiDAGRunDetails, WorkspaceRunValue } from '../../data/types';
import { RunStepsNav } from '../RunStepsNav';

const h = vi.hoisted(() => ({
  run: null as unknown as WorkspaceRunValue,
  dock: {
    openStep: vi.fn(),
    openRunLog: vi.fn(),
    autoStep: null as { step: string; stream?: 'stderr' } | null,
  },
  revealStep: vi.fn(),
}));

vi.mock('../../data', () => ({ useWorkspaceRun: () => h.run }));
vi.mock('../DockContext', () => ({
  useDock: () => h.dock,
  useDockCommands: () => h.dock,
}));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({ handle: { revealStep: h.revealStep } }),
}));

function makeRun(details: ApiDAGRunDetails | null): WorkspaceRunValue {
  return {
    selection: 'latest',
    ref: details ? { name: details.name, runId: details.dagRunId } : null,
    details,
    hasRun: !!details,
    isLatest: true,
    isActive: false,
    notFound: false,
    latestRef: null,
    overlay: {},
    nodeByStep: nodesByStep(details),
    handlers: [],
    waiting: NO_WAITING,
    failedStep: null,
    select: vi.fn(),
    applyNodeStatus: vi.fn(),
    refresh: vi.fn(async () => {}),
  };
}

function renderNav(url = '/dags/demo/executions') {
  render(
    <MemoryRouter initialEntries={[url]}>
      <RunStepsNav dag={{ name: 'demo' } as never} />
    </MemoryRouter>
  );
}

const list = () => screen.getByRole('list', { name: 'Run steps' });
// A row is named by its title: the step name, or 'Run summary'.
const row = (name: string) => within(list()).getByRole('listitem', { name });
const rows = () =>
  within(list())
    .getAllByRole('listitem')
    .map((li) => li.title);

beforeEach(() => {
  h.run = makeRun(
    run('r1', [node('a'), node('b', NodeStatus.Failed)], Status.Failed)
  );
  h.dock.openStep.mockReset();
  h.dock.openRunLog.mockReset();
  h.dock.autoStep = null;
  h.revealStep.mockReset();
});

describe('RunStepsNav', () => {
  it('lists the summary row and the steps of the selected run', () => {
    renderNav();
    expect(rows()).toEqual(['Run summary', 'a', 'b']);
    expect(row('Run summary')).toHaveAttribute('aria-current', 'true');
  });

  it('selects the step of ?step', () => {
    renderNav('/dags/demo/executions?step=b');
    expect(row('b')).toHaveAttribute('aria-current', 'true');
  });

  it('selects the step that the auto-open chose when ?step is empty', () => {
    h.dock.autoStep = { step: 'b', stream: 'stderr' };
    renderNav();
    expect(row('b')).toHaveAttribute('aria-current', 'true');
  });

  it('opens the log of a picked step and selects it on the canvas', () => {
    renderNav();
    fireEvent.click(within(row('a')).getByRole('button', { name: 'a' }));
    expect(h.dock.openStep).toHaveBeenCalledWith('a', { keepTab: true });
    expect(h.revealStep).toHaveBeenCalledWith('a');
  });

  it('opens the run log from the summary row', () => {
    renderNav('/dags/demo/executions?step=a');
    fireEvent.click(
      within(row('Run summary')).getByRole('button', { name: 'Run summary' })
    );
    expect(h.dock.openRunLog).toHaveBeenCalled();
    expect(h.dock.openStep).not.toHaveBeenCalled();
  });

  it('shows no rows for a workflow that never ran, and a note while the run loads', () => {
    h.run = makeRun(null);
    renderNav();
    expect(screen.queryByRole('list')).toBeNull();

    h.run = { ...makeRun(run('r1')), details: null };
    cleanup();
    renderNav();
    expect(screen.getByText('Loading run...')).toBeInTheDocument();
  });
});
