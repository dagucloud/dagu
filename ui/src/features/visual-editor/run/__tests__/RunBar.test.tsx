// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { EditorRunActions } from '../../contracts';
import { RunBar } from '../RunBar';

const { useQueryMock, auth, specs } = vi.hoisted(() => ({
  useQueryMock: vi.fn(),
  auth: { canExecute: true, workspaces: [] as unknown[] },
  specs: {
    run: undefined as string | undefined,
    saved: undefined as string | undefined,
  },
}));

vi.mock('@/hooks/api', () => ({ useQuery: useQueryMock }));
vi.mock('@/contexts/AuthContext', () => ({
  useCanExecuteForWorkspace: (workspace?: string | null) => {
    auth.workspaces.push(workspace);
    return auth.canExecute;
  },
}));
vi.mock('@/contexts/RemoteNodeContext', () => ({
  useRemoteNode: () => 'node-1',
}));

function makeActions(fields: Partial<EditorRunActions> = {}): EditorRunActions {
  return {
    runDag: vi.fn(async () => 'new-run'),
    runDraft: vi.fn(async () => 'draft-run'),
    runStep: vi.fn(async () => null),
    runSteps: vi.fn(async () => null),
    isStarting: false,
    error: null,
    ...fields,
  };
}

function renderBar(
  props: {
    runId?: string | null;
    isDirty?: boolean;
    workspace?: string;
    actions?: EditorRunActions;
  } = {}
) {
  const onRunIdChange = vi.fn();
  const actions = props.actions ?? makeActions();
  render(
    <RunBar
      dagName="dag"
      fileName="dag.yaml"
      runId={props.runId === undefined ? 'latest' : props.runId}
      onRunIdChange={onRunIdChange}
      actions={actions}
      isDirty={props.isDirty ?? false}
      workspace={props.workspace}
    />
  );
  return { onRunIdChange, actions };
}

beforeEach(() => {
  auth.canExecute = true;
  auth.workspaces = [];
  specs.run = undefined;
  specs.saved = undefined;
  useQueryMock.mockReset();
  useQueryMock.mockImplementation((path: string) => {
    if (path === '/dag-runs/{name}/{dagRunId}/spec') {
      return {
        data: specs.run === undefined ? undefined : { spec: specs.run },
      };
    }
    if (path === '/dags/{fileName}/spec') {
      return {
        data: specs.saved === undefined ? undefined : { spec: specs.saved },
      };
    }
    return { data: undefined, mutate: vi.fn() };
  });
});

describe('RunBar run buttons', () => {
  // The saved definition starts from the Start button of the header: it opens
  // the start dialog (parameters, profile, run ID, enqueue).
  it('has no one-click Run of the saved definition', () => {
    renderBar();
    expect(screen.queryByRole('button', { name: 'Run' })).toBeNull();
    renderBar({ isDirty: true });
    expect(screen.queryByRole('button', { name: 'Run' })).toBeNull();
  });

  it('offers the draft run only when the buffer is dirty', () => {
    renderBar({ isDirty: false });
    expect(
      screen.queryByRole('button', { name: 'Run unsaved draft' })
    ).toBeNull();
  });

  it('runs the draft and shows the new run', async () => {
    const { actions, onRunIdChange } = renderBar({ isDirty: true });
    await userEvent.click(
      screen.getByRole('button', { name: 'Run unsaved draft' })
    );
    expect(actions.runDraft).toHaveBeenCalledTimes(1);
    expect(actions.runDag).not.toHaveBeenCalled();
    expect(onRunIdChange).toHaveBeenCalledWith('draft-run');
  });

  it('disables the draft button with a spinner while starting', () => {
    renderBar({ isDirty: true, actions: makeActions({ isStarting: true }) });
    expect(
      screen.getByRole('button', { name: 'Run unsaved draft' })
    ).toBeDisabled();
    expect(screen.getByLabelText('Starting run')).toBeInTheDocument();
  });
});

describe('RunBar permission', () => {
  it('disables the draft button without execute permission', () => {
    auth.canExecute = false;
    renderBar({ isDirty: true });
    const button = screen.getByRole('button', { name: 'Run unsaved draft' });
    expect(button).toBeDisabled();
    expect(button.title).toMatch(/permission/);
  });

  // The role is checked in the workspace of the DAG, not in the one that the
  // app bar shows.
  it('checks the permission in the workspace of the DAG', () => {
    renderBar({ isDirty: true, workspace: 'team-a' });
    expect(new Set(auth.workspaces)).toEqual(new Set(['team-a']));
  });
});

describe('RunBar errors', () => {
  it('shows the error of a failed start', () => {
    renderBar({ actions: makeActions({ error: 'Failed to start the run' }) });
    expect(screen.getByRole('alert')).toHaveTextContent(
      'Failed to start the run'
    );
  });

  it('shows no alert without an error', () => {
    renderBar();
    expect(screen.queryByRole('alert')).toBeNull();
  });
});

describe('RunBar stale note', () => {
  const note = 'This run used a different definition than the saved one.';

  it('shows the note when the specs differ', () => {
    specs.run = 'steps:\n  - run: a\n';
    specs.saved = 'steps:\n  - run: b\n';
    renderBar();
    expect(screen.getByText(note)).toBeInTheDocument();
  });

  it('hides the note when the specs are equal', () => {
    specs.run = 'steps:\n  - run: a\n';
    specs.saved = 'steps:\r\n  - run: a';
    renderBar();
    expect(screen.queryByText(note)).toBeNull();
  });

  it('hides the note until both specs are loaded', () => {
    specs.run = 'steps: []\n';
    renderBar();
    expect(screen.queryByText(note)).toBeNull();
  });

  it('asks for the specs of the shown run with remoteNode', () => {
    renderBar({ runId: 'r1' });
    expect(useQueryMock).toHaveBeenCalledWith(
      '/dag-runs/{name}/{dagRunId}/spec',
      {
        params: {
          path: { name: 'dag', dagRunId: 'r1' },
          query: { remoteNode: 'node-1' },
        },
      }
    );
    expect(useQueryMock).toHaveBeenCalledWith('/dags/{fileName}/spec', {
      params: {
        path: { fileName: 'dag.yaml' },
        query: { remoteNode: 'node-1' },
      },
    });
  });

  it('makes no spec request without a run', () => {
    specs.run = 'a';
    specs.saved = 'b';
    renderBar({ runId: null });
    const specCalls = useQueryMock.mock.calls.filter(
      (call) => call[0] !== '/dags/{fileName}/dag-runs'
    );
    expect(specCalls).toHaveLength(2);
    expect(specCalls.every((call) => call[1] === null)).toBe(true);
    expect(screen.queryByText(note)).toBeNull();
  });
});
