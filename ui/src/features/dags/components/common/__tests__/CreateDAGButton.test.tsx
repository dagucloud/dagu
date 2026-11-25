// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ConfigContext, type Config } from '@/contexts/ConfigContext';
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

function renderButton(basePath = '') {
  render(
    <ConfigContext.Provider value={{ basePath } as Config}>
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
    </ConfigContext.Provider>
  );
}

function submitName(name: string) {
  fireEvent.click(screen.getByRole('button', { name: 'Create new DAG' }));
  fireEvent.change(screen.getByLabelText('Workflow name'), {
    target: { value: name },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Create' }));
}

const realLocation = window.location;

describe('CreateDAGButton', () => {
  beforeEach(() => {
    postMock.mockReset();
  });

  afterEach(() => {
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: realLocation,
    });
  });

  it('creates new DAGs with the selected workspace label', async () => {
    postMock.mockResolvedValueOnce({
      error: { message: 'stop before redirect' },
    });

    renderButton();
    submitName('header-new-dag');

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

  // The page path does not tell the base path: the runs page has no /dags.
  it('opens the editor of the new workflow from the runs page', async () => {
    postMock.mockResolvedValueOnce({});
    // jsdom does not navigate, so the redirect is read from a stand-in.
    const location = {
      href: 'http://localhost/dagu/dag-runs',
      pathname: '/dagu/dag-runs',
    };
    Object.defineProperty(window, 'location', {
      configurable: true,
      value: location,
    });

    renderButton('/dagu');
    submitName('new-dag');

    await waitFor(() => expect(location.href).toBe('/dagu/dags/new-dag/spec'));
  });

  it('names the length limit for a long name', () => {
    renderButton();
    submitName('a'.repeat(41));

    expect(
      screen.getByText('DAG name must be 40 characters or less')
    ).toBeVisible();
    expect(postMock).not.toHaveBeenCalled();
  });
});
