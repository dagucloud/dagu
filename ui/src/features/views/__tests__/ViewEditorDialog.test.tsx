// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import React from 'react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
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
        pinned: false,
      })
    );
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
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
});
