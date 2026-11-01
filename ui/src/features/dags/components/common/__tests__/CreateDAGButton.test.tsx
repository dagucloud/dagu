// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { WorkspaceKind } from '@/lib/workspace';
import CreateDAGButton from '../CreateDAGButton';

const { postMock } = vi.hoisted(() => ({
  postMock: vi.fn(),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWrite: () => true,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({
    POST: postMock,
  }),
}));

function renderButton() {
  render(
    <AppBarContext.Provider
      value={{
        title: '',
        setTitle: vi.fn(),
        remoteNodes: ['local'],
        setRemoteNodes: vi.fn(),
        selectedRemoteNode: 'local',
        selectRemoteNode: vi.fn(),
        workspaces: [{ id: 'workspace-1', name: 'ops' }],
        workspaceSelection: {
          kind: WorkspaceKind.workspace,
          workspace: 'ops',
        },
        selectWorkspace: vi.fn(),
      }}
    >
      <CreateDAGButton />
    </AppBarContext.Provider>
  );
}

describe('CreateDAGButton', () => {
  beforeEach(() => {
    postMock.mockReset();
  });

  it('creates new DAGs with the selected workspace label', async () => {
    postMock.mockResolvedValueOnce({
      error: { message: 'stop before redirect' },
    });

    renderButton();

    fireEvent.click(screen.getByRole('button', { name: 'Create new DAG' }));
    fireEvent.change(screen.getByLabelText('Workflow name'), {
      target: { value: 'header-new-dag' },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    await waitFor(() => expect(postMock).toHaveBeenCalled());

    expect(postMock).toHaveBeenCalledWith('/dags', {
      params: {
        query: {
          remoteNode: 'local',
        },
      },
      body: {
        name: 'header-new-dag',
        spec: expect.stringContaining('workspace=ops'),
      },
    });
  });

  it('names the length limit for a long name', () => {
    renderButton();

    fireEvent.click(screen.getByRole('button', { name: 'Create new DAG' }));
    fireEvent.change(screen.getByLabelText('Workflow name'), {
      target: { value: 'a'.repeat(41) },
    });
    fireEvent.click(screen.getByRole('button', { name: 'Create' }));

    expect(
      screen.getByText('DAG name must be 40 characters or less')
    ).toBeVisible();
    expect(postMock).not.toHaveBeenCalled();
  });
});
