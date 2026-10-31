// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import type { ApiDAGDetails } from '../../contracts';
import { DAGIdentity } from '../DAGIdentity';

const mocks = vi.hoisted(() => ({
  canWrite: true,
  copyText: vi.fn(),
  refresh: vi.fn(),
  put: vi.fn(),
  dirty: false,
}));

vi.mock('@/contexts/ConfigContext', () => ({
  useConfig: () => ({ basePath: '/base' }),
}));
vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => mocks.canWrite,
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
    handle: null,
    state: { dirty: mocks.dirty },
  }),
}));
vi.mock('../../data', () => ({ useWorkspaceRefresh: () => mocks.refresh }));

const description = 'Loads nightly sales into the warehouse.';
const origin = window.location.origin;

function renderIdentity({
  url = '/dags/etl',
  remoteNode,
  pinned,
  dag = { name: 'etl', description } as ApiDAGDetails,
}: {
  url?: string;
  remoteNode?: string;
  pinned?: boolean;
  dag?: ApiDAGDetails;
} = {}) {
  return render(
    <MemoryRouter initialEntries={[url]}>
      <RemoteNodeProvider remoteNode={remoteNode}>
        <Routes>
          <Route
            path="/dags/:fileName/:tab?"
            element={
              <DAGIdentity
                fileName="etl"
                dag={dag}
                workspaceName="ops"
                pinned={pinned}
              />
            }
          />
        </Routes>
      </RemoteNodeProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  mocks.canWrite = true;
  mocks.dirty = false;
  mocks.copyText.mockReset().mockResolvedValue(true);
  mocks.refresh.mockReset().mockResolvedValue(undefined);
  mocks.put.mockReset().mockResolvedValue({});
});

// parity:header-pin parity:test-pin-button
describe('DAGIdentity pin', () => {
  it('puts the pin button to the left of the workflow name', () => {
    renderIdentity({ pinned: false });
    const pin = screen.getByRole('button', { name: 'Pin workflow etl' });
    const title = screen.getByRole('heading', { name: 'etl' });
    expect(
      pin.compareDocumentPosition(title) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
    expect(pin).toHaveAttribute('aria-pressed', 'false');
  });

  it('refreshes the workspace after a pin change', async () => {
    renderIdentity({ pinned: false });
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Pin workflow etl' }));
    });
    expect(mocks.put).toHaveBeenCalled();
    expect(mocks.refresh).toHaveBeenCalledTimes(1);
  });

  it('shows only the marker to a read-only user', () => {
    mocks.canWrite = false;
    renderIdentity({ pinned: true });
    expect(screen.getByRole('img', { name: 'Pinned' })).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Pin workflow etl' })
    ).not.toBeInTheDocument();
  });

  // Remote nodes running an older version do not report pins.
  it('offers no pin when the pin state is unknown', () => {
    renderIdentity();
    expect(
      screen.queryByRole('button', { name: 'Pin workflow etl' })
    ).not.toBeInTheDocument();
  });
});

// parity:header-name
describe('DAGIdentity name', () => {
  it('shows the DAG name in a truncated heading', () => {
    renderIdentity();
    const title = screen.getByRole('heading', { level: 1, name: 'etl' });
    expect(title).toHaveClass('truncate');
    expect(title).toHaveAttribute('title', 'etl');
  });

  // Below md the pin hides. The copy and description buttons need a bar of
  // 672px, so they hide before the name is cut. The '...' menu has them
  // at every width.
  it('hides the pin below md and the buttons in a narrow bar', () => {
    renderIdentity({ pinned: false });
    expect(screen.getByRole('heading', { name: 'etl' })).not.toHaveClass(
      'hidden'
    );
    expect(
      screen
        .getByRole('button', { name: 'Pin workflow etl' })
        .closest('.hidden')
    ).toHaveClass('md:flex');
    for (const name of [
      'Copy name',
      'Copy link to this workflow',
      'Show description',
    ]) {
      const slot = screen.getByRole('button', { name }).closest('.hidden');
      expect(slot).toHaveClass('@2xl:flex');
      expect(slot).not.toHaveClass('md:flex');
    }
  });
});

// parity:header-copy-name parity:dup-21
describe('DAGIdentity copy name', () => {
  it('swaps Copy for Check and renames the button', async () => {
    renderIdentity();
    const button = screen.getByRole('button', { name: 'Copy name' });
    expect(button.querySelector('.lucide-copy')).not.toBeNull();
    expect(button).toHaveTextContent('');

    await act(async () => {
      fireEvent.click(button);
    });

    expect(mocks.copyText).toHaveBeenCalledWith('etl');
    const copied = screen.getByRole('button', { name: 'Name copied' });
    expect(copied.querySelector('.lucide-check')).not.toBeNull();
  });
});

// The check of a copy takes the success token of the theme, so it follows
// light and dark.
describe('DAGIdentity colours', () => {
  it('colours the check of a copy with the success token', async () => {
    renderIdentity();
    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Copy name' }));
    });
    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Copy link to this workflow' })
      );
    });
    for (const name of ['Name copied', 'Link copied']) {
      const check = screen
        .getByRole('button', { name })
        .querySelector('.lucide-check');
      expect(check).toHaveClass('text-success');
      expect(check?.getAttribute('class')).not.toMatch(/-(green|emerald)-\d/);
    }
  });
});

// parity:header-copy-link parity:dup-21
describe('DAGIdentity copy link', () => {
  async function copyLink() {
    await act(async () => {
      fireEvent.click(
        screen.getByRole('button', { name: 'Copy link to this workflow' })
      );
    });
    return mocks.copyText.mock.calls[0]?.[0];
  }

  it('copies the workflow link without the run, the step or the page mode', async () => {
    renderIdentity({ url: '/dags/etl/executions?run=r1&step=s&ui=workspace' });
    expect(await copyLink()).toBe(origin + '/base/dags/etl');
    expect(
      screen.getByRole('button', { name: 'Link copied' })
    ).toBeInTheDocument();
  });

  it('keeps the remote node and the workspace of a scoped page', async () => {
    renderIdentity({
      url: '/dags/etl?run=r1&remoteNode=remote-a&workspace=ops',
      remoteNode: 'remote-a',
    });
    expect(await copyLink()).toBe(
      origin + '/base/dags/etl?remoteNode=remote-a&workspace=ops'
    );
  });

  it('drops a workspace name that the page does not accept', async () => {
    renderIdentity({ url: '/dags/etl?workspace=all' });
    expect(await copyLink()).toBe(origin + '/base/dags/etl');
  });

  it('keeps a remote node chosen in the app bar', async () => {
    renderIdentity({ remoteNode: 'remote-b' });
    expect(await copyLink()).toBe(
      origin + '/base/dags/etl?remoteNode=remote-b'
    );
  });
});

// parity:header-description
describe('DAGIdentity description', () => {
  it('keeps the description for screen readers', () => {
    renderIdentity();
    expect(screen.getByText(description)).toHaveClass('sr-only');
  });

  it('shows the description in a panel on click', () => {
    renderIdentity();
    const info = screen.getByRole('button', { name: 'Show description' });
    expect(screen.queryByTestId('ww-description-panel')).toBeNull();
    fireEvent.click(info);
    const panel = screen.getByTestId('ww-description-panel');
    expect(panel).toHaveTextContent(description);
    expect(info).toHaveAttribute('aria-expanded', 'true');
    fireEvent.click(info);
    expect(screen.queryByTestId('ww-description-panel')).toBeNull();
  });

  it('closes the panel on Esc and on a pointer down outside', () => {
    renderIdentity();
    const info = screen.getByRole('button', { name: 'Show description' });
    fireEvent.click(info);
    fireEvent.keyDown(document.body, { key: 'Escape' });
    expect(screen.queryByTestId('ww-description-panel')).toBeNull();

    fireEvent.click(info);
    fireEvent.pointerDown(screen.getByTestId('ww-description-panel'));
    expect(screen.getByTestId('ww-description-panel')).toBeInTheDocument();
    fireEvent.pointerDown(document.body);
    expect(screen.queryByTestId('ww-description-panel')).toBeNull();
  });

  // The panel sits above dialogs. Focus that leaves it, such as a tab to
  // the Rename button, closes it so the rename dialog opens alone.
  it('closes the panel when focus moves outside it', async () => {
    renderIdentity();
    fireEvent.click(screen.getByRole('button', { name: 'Show description' }));
    const rename = screen.getByRole('button', { name: 'Rename' });
    act(() => rename.focus());
    expect(screen.queryByTestId('ww-description-panel')).toBeNull();

    fireEvent.click(rename);
    await screen.findByRole('dialog', { name: 'Rename workflow' });
    fireEvent.keyDown(document.activeElement ?? document.body, {
      key: 'Escape',
    });
    expect(
      screen.queryByRole('dialog', { name: 'Rename workflow' })
    ).toBeNull();
  });

  it('has no info button without a description', () => {
    renderIdentity({
      dag: { name: 'etl', description: '  ' } as ApiDAGDetails,
    });
    expect(
      screen.queryByRole('button', { name: 'Show description' })
    ).not.toBeInTheDocument();
  });
});

describe('DAGIdentity rename', () => {
  // The pencil sits right after the name, before the copy and info buttons.
  it('puts the Rename button directly after the name', () => {
    renderIdentity();
    const title = screen.getByRole('heading', { name: 'etl' });
    const rename = screen.getByRole('button', { name: 'Rename' });
    expect(title.nextElementSibling).toBe(rename);
    expect(rename).toHaveAttribute('data-testid', 'ww-bar-rename');
    expect(rename.nextElementSibling).toHaveAttribute(
      'data-testid',
      'ww-identity-secondary'
    );
  });

  it('keeps the Rename button in a narrow bar', () => {
    renderIdentity();
    expect(
      screen.getByRole('button', { name: 'Rename' }).closest('.hidden')
    ).toBeNull();
  });

  it('opens the rename dialog', async () => {
    renderIdentity();
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
    expect(
      await screen.findByRole('dialog', { name: 'Rename workflow' })
    ).toBeInTheDocument();
  });

  it('shows no Rename button without write permission', () => {
    mocks.canWrite = false;
    renderIdentity();
    expect(screen.queryByRole('button', { name: 'Rename' })).toBeNull();
  });
});

describe('DAGIdentity unsaved state', () => {
  it('shows no visible dot, whether the editor is clean or dirty', () => {
    const clean = renderIdentity();
    expect(screen.queryByTestId('ww-unsaved')).toBeNull();
    clean.unmount();

    mocks.dirty = true;
    const { container } = renderIdentity();
    expect(container.querySelector('.bg-warning')).toBeNull();
    expect(container.querySelector('.rounded-full')).toBeNull();
  });

  it('keeps the unsaved state for screen readers only', () => {
    mocks.dirty = true;
    renderIdentity();
    const marker = screen.getByTestId('ww-unsaved');
    expect(marker).toHaveClass('sr-only');
    expect(marker).toHaveTextContent('This workflow has unsaved changes');
  });
});
