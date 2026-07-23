// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import * as React from 'react';
import { describe, expect, it, vi } from 'vitest';

import { AppBarContext } from '@/contexts/AppBarContext';
import { WorkspaceKind } from '@/lib/workspace';
import { LocalControllerScope } from '../components/LocalControllerScope';

function LocalScopeHarness({
  selectWorkspace,
}: {
  selectWorkspace: ReturnType<typeof vi.fn>;
}) {
  const [remoteNode, setRemoteNode] = React.useState('local');
  return (
    <AppBarContext.Provider
      value={
        {
          selectedRemoteNode: remoteNode,
          selectRemoteNode: setRemoteNode,
          selectWorkspace,
        } as never
      }
    >
      <button type="button" onClick={() => setRemoteNode('remote-a')}>
        Select remote node
      </button>
      <LocalControllerScope>
        <input aria-label="Controller draft" defaultValue="" />
      </LocalControllerScope>
    </AppBarContext.Provider>
  );
}

describe('LocalControllerScope', () => {
  it('switches remote workspace state to the local aggregate scope', async () => {
    const selectRemoteNode = vi.fn();
    const selectWorkspace = vi.fn();
    render(
      <AppBarContext.Provider
        value={
          {
            selectedRemoteNode: 'remote-a',
            selectRemoteNode,
            selectWorkspace,
          } as never
        }
      >
        <LocalControllerScope>
          <div>Controller content</div>
        </LocalControllerScope>
      </AppBarContext.Provider>
    );

    expect(screen.queryByText('Controller content')).toBeNull();
    await waitFor(() => {
      expect(selectRemoteNode).toHaveBeenCalledWith('local');
      expect(selectWorkspace).toHaveBeenCalledWith({
        kind: WorkspaceKind.all,
      });
    });
  });

  it('renders Controller content on the local node', () => {
    render(
      <AppBarContext.Provider value={{ selectedRemoteNode: 'local' } as never}>
        <LocalControllerScope>
          <div>Controller content</div>
        </LocalControllerScope>
      </AppBarContext.Provider>
    );

    expect(screen.getByText('Controller content')).toBeInTheDocument();
  });

  it('preserves Controller state while coercing a remote selection to local', async () => {
    const user = userEvent.setup();
    const selectWorkspace = vi.fn();
    render(<LocalScopeHarness selectWorkspace={selectWorkspace} />);
    const draft = screen.getByRole('textbox', { name: 'Controller draft' });

    await user.type(draft, 'unsaved draft');
    await user.click(
      screen.getByRole('button', { name: 'Select remote node' })
    );

    await waitFor(() => {
      expect(selectWorkspace).toHaveBeenCalledWith({
        kind: WorkspaceKind.all,
      });
    });
    expect(draft).toHaveValue('unsaved draft');
    expect(screen.getByRole('textbox', { name: 'Controller draft' })).toBe(
      draft
    );
  });
});
