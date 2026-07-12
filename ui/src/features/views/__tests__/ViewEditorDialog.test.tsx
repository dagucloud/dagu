// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AppBarContext } from '@/contexts/AppBarContext';
import { ViewColumn } from '@/api/v1/schema';
import { WorkspaceKind } from '@/lib/workspace';
import { ViewEditorDialog } from '../ViewEditorDialog';

const createView = vi.fn();
const updateView = vi.fn();
const deleteView = vi.fn();

vi.mock('@/hooks/useViews', () => ({
  useViews: () => ({ createView, updateView, deleteView }),
}));

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({ data: { labels: [] } }),
  useClient: () => ({}),
}));

beforeEach(() => {
  vi.clearAllMocks();
  createView.mockResolvedValue({
    id: 'new',
    name: 'Prod',
    type: 'kanban',
    intervalDays: 3,
    createdAt: '',
    updatedAt: '',
  });
});

describe('ViewEditorDialog', () => {
  it('disables Create until a name is entered', () => {
    render(<ViewEditorDialog open onOpenChange={vi.fn()} />);
    expect(screen.getByRole('button', { name: /create/i })).toBeDisabled();
  });

  it('creates a view with an empty workspace meaning all workspaces', async () => {
    const user = userEvent.setup();
    const onSaved = vi.fn();
    render(<ViewEditorDialog open onOpenChange={vi.fn()} onSaved={onSaved} />);

    await user.type(screen.getByPlaceholderText('My view'), 'Prod');
    await user.click(screen.getByRole('button', { name: /create/i }));

    await waitFor(() => expect(createView).toHaveBeenCalledTimes(1));
    expect(createView).toHaveBeenCalledWith(
      expect.objectContaining({
        name: 'Prod',
        type: 'kanban',
        workspace: '',
        intervalDays: 1,
        columns: [
          ViewColumn.queued,
          ViewColumn.running,
          ViewColumn.review,
          ViewColumn.done,
          ViewColumn.failed,
        ],
        pinned: false,
      })
    );
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
  });

  it('saves hidden columns and the configured display order', async () => {
    const user = userEvent.setup();
    render(<ViewEditorDialog open onOpenChange={vi.fn()} />);

    await user.type(screen.getByPlaceholderText('My view'), 'Failures first');
    await user.click(screen.getByRole('checkbox', { name: 'Queued' }));
    expect(screen.getByText('Hidden')).toBeInTheDocument();
    expect(
      screen.queryByRole('button', { name: 'Move Queued up' })
    ).not.toBeInTheDocument();
    const moveFailedUp = screen.getByRole('button', {
      name: 'Move Failed up',
    });
    await user.click(moveFailedUp);
    await user.click(moveFailedUp);
    await user.click(moveFailedUp);
    await user.click(screen.getByRole('button', { name: /create/i }));

    await waitFor(() => expect(createView).toHaveBeenCalledTimes(1));
    expect(createView).toHaveBeenCalledWith(
      expect.objectContaining({
        columns: [
          ViewColumn.failed,
          ViewColumn.running,
          ViewColumn.review,
          ViewColumn.done,
        ],
      })
    );
  });

  it('adds a restored column to the end of the visible order', async () => {
    const user = userEvent.setup();
    render(<ViewEditorDialog open onOpenChange={vi.fn()} />);

    await user.type(screen.getByPlaceholderText('My view'), 'Custom order');
    await user.click(screen.getByRole('checkbox', { name: 'Queued' }));
    await user.click(screen.getByRole('checkbox', { name: 'Queued' }));
    await user.click(screen.getByRole('button', { name: /create/i }));

    await waitFor(() => expect(createView).toHaveBeenCalledTimes(1));
    expect(createView).toHaveBeenCalledWith(
      expect.objectContaining({
        columns: [
          ViewColumn.running,
          ViewColumn.review,
          ViewColumn.done,
          ViewColumn.failed,
          ViewColumn.queued,
        ],
      })
    );
  });

  it('edits an existing view via update', async () => {
    const user = userEvent.setup();
    updateView.mockResolvedValue({
      id: 'v1',
      name: 'Renamed',
      type: 'kanban',
      intervalDays: 5,
      createdAt: '',
      updatedAt: '',
    });

    render(
      <ViewEditorDialog
        open
        onOpenChange={vi.fn()}
        view={{
          id: 'v1',
          name: 'Original',
          type: 'kanban',
          workspace: 'prod',
          intervalDays: 5,
          createdAt: '',
          updatedAt: '',
        }}
      />
    );

    const nameInput = screen.getByPlaceholderText('My view');
    await user.clear(nameInput);
    await user.type(nameInput, 'Renamed');
    await user.click(screen.getByRole('button', { name: /save/i }));

    await waitFor(() => expect(updateView).toHaveBeenCalledTimes(1));
    expect(updateView).toHaveBeenCalledWith(
      'v1',
      expect.objectContaining({ name: 'Renamed', workspace: 'prod' })
    );
  });

  it('preserves an all-workspaces scope when editing with a selected workspace', async () => {
    const user = userEvent.setup();
    updateView.mockResolvedValue({
      id: 'v1',
      name: 'Renamed',
      type: 'kanban',
      workspace: '',
      intervalDays: 5,
      createdAt: '',
      updatedAt: '',
    });

    render(
      <AppBarContext.Provider
        value={
          {
            workspaceSelection: {
              kind: WorkspaceKind.workspace,
              workspace: 'prod',
            },
          } as never
        }
      >
        <ViewEditorDialog
          open
          onOpenChange={vi.fn()}
          view={{
            id: 'v1',
            name: 'Original',
            type: 'kanban',
            workspace: '',
            intervalDays: 5,
            createdAt: '',
            updatedAt: '',
          }}
        />
      </AppBarContext.Provider>
    );

    const nameInput = screen.getByPlaceholderText('My view');
    await user.clear(nameInput);
    await user.type(nameInput, 'Renamed');
    await user.click(screen.getByRole('button', { name: /save/i }));

    await waitFor(() => expect(updateView).toHaveBeenCalledTimes(1));
    expect(updateView).toHaveBeenCalledWith(
      'v1',
      expect.objectContaining({ name: 'Renamed', workspace: '' })
    );
  });
});
