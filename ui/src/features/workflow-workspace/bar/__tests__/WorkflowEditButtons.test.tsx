// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { RemoteNodeProvider } from '@/contexts/RemoteNodeContext';
import {
  WorkflowDeleteButton,
  WorkflowRenameButton,
} from '../WorkflowEditButtons';

const mocks = vi.hoisted(() => ({ canWrite: true }));

vi.mock('@/contexts/AuthContext', () => ({
  useCanWriteForWorkspace: () => mocks.canWrite,
}));
vi.mock('@/hooks/api', () => ({
  useClient: () => ({ POST: vi.fn(), PUT: vi.fn(), DELETE: vi.fn() }),
}));
vi.mock('@/components/ui/error-modal', () => ({
  useErrorModal: () => ({ showError: vi.fn() }),
}));

function renderButtons() {
  return render(
    <MemoryRouter initialEntries={['/dags/etl']}>
      <RemoteNodeProvider remoteNode="remote-a">
        <WorkflowRenameButton fileName="etl" workspaceName="ops" />
        <WorkflowDeleteButton fileName="etl" workspaceName="ops" />
      </RemoteNodeProvider>
    </MemoryRouter>
  );
}

beforeEach(() => {
  mocks.canWrite = true;
});

describe('WorkflowRenameButton and WorkflowDeleteButton', () => {
  it('shows Rename and Delete as icon buttons with a name and a tooltip', () => {
    renderButtons();
    for (const name of ['Rename', 'Delete']) {
      const button = screen.getByRole('button', { name });
      expect(button).toHaveAttribute('title', name);
      expect(button.querySelector('svg')).not.toBeNull();
      expect(button).toHaveTextContent('');
    }
  });

  it('puts Rename before Delete', () => {
    renderButtons();
    const rename = screen.getByRole('button', { name: 'Rename' });
    const remove = screen.getByRole('button', { name: 'Delete' });
    expect(
      rename.compareDocumentPosition(remove) & Node.DOCUMENT_POSITION_FOLLOWING
    ).toBeTruthy();
  });

  // The default focus colour of the accent gives red text a contrast under
  // 4.5:1 in light mode.
  it('keeps Delete readable with the destructive colour', () => {
    renderButtons();
    expect(screen.getByRole('button', { name: 'Delete' })).toHaveClass(
      'text-destructive'
    );
  });

  // parity:edit-rename
  it('opens the rename dialog', async () => {
    renderButtons();
    await userEvent.click(screen.getByRole('button', { name: 'Rename' }));
    expect(
      await screen.findByRole('dialog', { name: 'Rename workflow' })
    ).toBeInTheDocument();
    expect(screen.getByLabelText('Workflow name')).toHaveValue('etl');
  });

  // parity:edit-delete
  it('opens the delete confirmation', async () => {
    renderButtons();
    await userEvent.click(screen.getByRole('button', { name: 'Delete' }));
    expect(
      await screen.findByText('Do you really want to delete this workflow?')
    ).toBeInTheDocument();
  });

  // parity:edit-permission
  it('shows neither button without write permission', () => {
    mocks.canWrite = false;
    renderButtons();
    expect(screen.queryByRole('button', { name: 'Rename' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete' })).toBeNull();
  });
});
