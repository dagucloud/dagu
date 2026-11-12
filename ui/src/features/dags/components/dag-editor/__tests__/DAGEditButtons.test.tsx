// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import DAGEditButtons, { useDAGEditActions } from '../DAGEditButtons';

const mocks = vi.hoisted(() => ({
  post: vi.fn(),
  del: vi.fn(),
  showError: vi.fn(),
  canWrite: true,
}));

vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: mocks.post, DELETE: mocks.del }),
}));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => mocks.canWrite,
}));

vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: mocks.showError }),
}));

// jsdom does not navigate, so the redirect is read from a stand-in location.
const realLocation = window.location;
let location: { href: string; pathname: string };

beforeEach(() => {
  mocks.post.mockReset().mockResolvedValue({});
  mocks.del.mockReset().mockResolvedValue({});
  mocks.showError.mockReset();
  mocks.canWrite = true;
  location = { href: 'http://localhost/dags/etl', pathname: '/dags/etl' };
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: location,
  });
});

afterEach(() => {
  Object.defineProperty(window, 'location', {
    configurable: true,
    value: realLocation,
  });
});

// Any trigger can open the dialogs: here two plain buttons, like a menu.
function MenuHarness() {
  const { canWrite, openRename, openDelete, dialogs } = useDAGEditActions(
    'etl',
    'ops'
  );
  return (
    <>
      <output data-testid="can-write">{String(canWrite)}</output>
      <button type="button" onClick={openRename}>
        open rename
      </button>
      <button type="button" onClick={openDelete}>
        open delete
      </button>
      {dialogs}
    </>
  );
}

function renderHarness() {
  return render(
    <RemoteNodeProvider remoteNode="remote-a">
      <MenuHarness />
    </RemoteNodeProvider>
  );
}

function submitRename(name: string) {
  fireEvent.click(screen.getByRole('button', { name: 'open rename' }));
  fireEvent.change(screen.getByLabelText('Workflow name'), {
    target: { value: name },
  });
  fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
}

// parity:dag-edit-buttons-header
describe('useDAGEditActions', () => {
  it('renames the workflow and loads its new page', async () => {
    renderHarness();
    submitRename('etl_v2');

    await waitFor(() =>
      expect(location.href).toBe('/dags/etl_v2?remoteNode=remote-a')
    );
    expect(mocks.post).toHaveBeenCalledWith('/dags/{fileName}/rename', {
      params: { path: { fileName: 'etl' }, query: { remoteNode: 'remote-a' } },
      body: { newFileName: 'etl_v2' },
    });
  });

  it('shows the server error of a rename and stays on the page', async () => {
    mocks.post.mockResolvedValue({ error: { message: 'name is taken' } });
    renderHarness();
    submitRename('taken');

    expect(await screen.findByText('name is taken')).toBeInTheDocument();
    expect(screen.getByRole('dialog')).toBeInTheDocument();
    expect(location.href).toBe('http://localhost/dags/etl');
  });

  it('deletes the workflow after the confirm and loads the list', async () => {
    renderHarness();
    fireEvent.click(screen.getByRole('button', { name: 'open delete' }));
    expect(
      screen.getByText('Do you really want to delete this workflow?')
    ).toBeInTheDocument();
    expect(mocks.del).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(location.href).toBe('/dags/?remoteNode=remote-a')
    );
    expect(mocks.del).toHaveBeenCalledWith('/dags/{fileName}', {
      params: { path: { fileName: 'etl' }, query: { remoteNode: 'remote-a' } },
    });
  });

  it('reports a failed delete and keeps the page', async () => {
    mocks.del.mockResolvedValue({ error: { message: 'locked' } });
    renderHarness();
    fireEvent.click(screen.getByRole('button', { name: 'open delete' }));
    fireEvent.click(screen.getByRole('button', { name: 'Delete' }));

    await waitFor(() =>
      expect(mocks.showError).toHaveBeenCalledWith(
        'locked',
        'Please try again or check the server connection.'
      )
    );
    expect(location.href).toBe('http://localhost/dags/etl');
  });

  it('gives no dialogs without write permission', () => {
    mocks.canWrite = false;
    renderHarness();
    expect(screen.getByTestId('can-write')).toHaveTextContent('false');
    fireEvent.click(screen.getByRole('button', { name: 'open delete' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});

describe('DAGEditButtons', () => {
  it('opens the rename and delete dialogs from its icon buttons', () => {
    render(<DAGEditButtons fileName="etl" workspace="ops" />);
    fireEvent.click(screen.getByRole('button', { name: 'Rename' }));
    expect(
      screen.getByRole('dialog', { name: 'Rename workflow' })
    ).toBeInTheDocument();
  });

  it('renders nothing without write permission', () => {
    mocks.canWrite = false;
    const { container } = render(<DAGEditButtons fileName="etl" />);
    expect(container).toBeEmptyDOMElement();
  });
});
