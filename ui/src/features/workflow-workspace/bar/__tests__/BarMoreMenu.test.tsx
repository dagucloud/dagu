// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { ApiDAGDetails } from '../../contracts';
import { BarMoreMenu } from '../BarMoreMenu';

const mocks = vi.hoisted(() => ({
  canWrite: true,
  canManageWebhooks: true,
  copyText: vi.fn(),
  refresh: vi.fn(),
  put: vi.fn(),
  openWorkflowPanel: vi.fn(),
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ basePath: '' }),
}));
vi.mock('@/hooks/api', () => ({
  useClient: () => ({ PUT: mocks.put, DELETE: mocks.put }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));
vi.mock('@/lib/clipboard', () => ({ copyText: mocks.copyText }));
vi.mock('../../canvas/WorkspaceEditorContext', () => ({
  useWorkspaceEditor: () => ({
    handle: { openWorkflowPanel: mocks.openWorkflowPanel },
    state: null,
  }),
}));
vi.mock('../../data', () => ({ useWorkspaceRefresh: () => mocks.refresh }));
vi.mock('../../data/useWorkspacePermissions', () => ({
  useWorkspacePermissions: () => ({
    canWrite: mocks.canWrite,
    canManageWebhooks: mocks.canManageWebhooks,
  }),
}));

function Location() {
  const location = useLocation();
  return <output data-testid="location">{location.search}</output>;
}

function renderMenu(pinned?: boolean) {
  render(
    <MemoryRouter initialEntries={['/dags/etl']}>
      <Routes>
        <Route
          path="/dags/:fileName"
          element={
            <>
              <BarMoreMenu
                fileName="etl"
                dag={{ name: 'etl' } as ApiDAGDetails}
                workspaceName={null}
                pinned={pinned}
              />
              <Location />
            </>
          }
        />
      </Routes>
    </MemoryRouter>
  );
}

async function openMenu() {
  await userEvent.click(screen.getByRole('button', { name: 'More actions' }));
  return screen.findByRole('menu');
}

beforeEach(() => {
  mocks.canWrite = true;
  mocks.canManageWebhooks = true;
  mocks.copyText.mockReset().mockResolvedValue(true);
  mocks.refresh.mockReset().mockResolvedValue(undefined);
  mocks.put.mockReset().mockResolvedValue({});
  mocks.openWorkflowPanel.mockReset();
});

// A bar under 672px hides the copy buttons, the labels and the description,
// and a window under md the pin, Refresh and the webhook: the menu has them.
describe('BarMoreMenu', () => {
  it('shows in a bar under 672px or a window under md', () => {
    renderMenu();
    expect(screen.getByTestId('ww-bar-more')).toHaveClass('md:@2xl:hidden');
  });

  // The workflow page is the run page: the menu leaves it out too.
  it('has no run page entry', async () => {
    renderMenu();
    await openMenu();
    expect(screen.queryByText('Open run page')).toBeNull();
  });

  it('copies the name and the link of the workflow', async () => {
    renderMenu();
    await openMenu();
    await userEvent.click(screen.getByRole('menuitem', { name: 'Copy name' }));
    expect(mocks.copyText).toHaveBeenLastCalledWith('etl');

    await openMenu();
    await userEvent.click(
      screen.getByRole('menuitem', { name: 'Copy link to this workflow' })
    );
    expect(mocks.copyText).toHaveBeenLastCalledWith(
      `${window.location.origin}/dags/etl`
    );
  });

  it('pins the workflow and reads it again', async () => {
    renderMenu(false);
    await openMenu();
    await userEvent.click(
      screen.getByRole('menuitem', { name: 'Pin workflow' })
    );
    await waitFor(() => expect(mocks.refresh).toHaveBeenCalledTimes(1));
    expect(mocks.put).toHaveBeenCalledWith('/dags/{fileName}/pin', {
      params: { path: { fileName: 'etl' }, query: { remoteNode: 'local' } },
    });
  });

  it('opens the Workflow panel and the webhook panel', async () => {
    renderMenu();
    await openMenu();
    await userEvent.click(
      screen.getByRole('menuitem', { name: 'Workflow panel' })
    );
    expect(mocks.openWorkflowPanel).toHaveBeenCalledTimes(1);

    await openMenu();
    await userEvent.click(
      screen.getByRole('menuitem', { name: 'Open Webhook' })
    );
    expect(screen.getByTestId('location')).toHaveTextContent('panel=webhook');
  });

  it('leaves out what the user may not do', async () => {
    mocks.canWrite = false;
    mocks.canManageWebhooks = false;
    renderMenu(false);
    await openMenu();
    expect(
      screen.queryByRole('menuitem', { name: 'Pin workflow' })
    ).not.toBeInTheDocument();
    expect(
      screen.queryByRole('menuitem', { name: 'Open Webhook' })
    ).not.toBeInTheDocument();
  });
});
