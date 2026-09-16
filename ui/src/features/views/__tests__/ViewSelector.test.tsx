// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import { ViewSelector, type SavedViewItem } from '../ViewSelector';

const views: SavedViewItem[] = [
  {
    id: 'production',
    name: 'Production operations',
    pinned: false,
  },
  {
    id: 'failed-runs',
    name: 'Failed runs',
    pinned: false,
  },
];

function renderSelector(
  overrides: Partial<ComponentProps<typeof ViewSelector>> = {}
) {
  const props: ComponentProps<typeof ViewSelector> = {
    kind: 'run',
    views,
    activeViewId: null,
    defaultViewId: 'production',
    isAllView: true,
    isActiveViewEdited: false,
    canManageViews: true,
    onSelectView: vi.fn(),
    onShowAll: vi.fn(),
    onResetView: vi.fn(),
    onSaveView: vi.fn().mockResolvedValue(undefined),
    onUpdateView: vi.fn().mockResolvedValue(undefined),
    onSetDefault: vi.fn().mockResolvedValue(undefined),
    onSetPinned: vi.fn().mockResolvedValue(undefined),
    onDeleteView: vi.fn().mockResolvedValue(undefined),
    ...overrides,
  };
  render(<ViewSelector {...props} />);
  return props;
}

describe('ViewSelector', () => {
  it('selects saved views and keeps All runs available', async () => {
    const user = userEvent.setup();
    const props = renderSelector();

    await user.click(
      screen.getByRole('button', { name: 'Run view: All runs' })
    );
    await user.click(
      screen.getByRole('menuitem', { name: /production operations/i })
    );
    expect(props.onSelectView).toHaveBeenCalledWith('production');

    await user.click(
      screen.getByRole('button', { name: 'Run view: All runs' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'All runs' }));
    expect(props.onShowAll).toHaveBeenCalledOnce();
  });

  it('labels the trigger with the workflow kind when used for workflows', async () => {
    renderSelector({ kind: 'workflow' });

    expect(
      screen.getByRole('button', { name: 'Workflow view: All workflows' })
    ).toBeVisible();
  });

  it('saves the current filters as a named default run view', async () => {
    const user = userEvent.setup();
    const props = renderSelector({ views: [], defaultViewId: undefined });

    await user.click(
      screen.getByRole('button', { name: 'Run view: All runs' })
    );
    await user.click(
      screen.getByRole('menuitem', {
        name: 'Save current filters as view…',
      })
    );
    await user.type(
      screen.getByRole('textbox', { name: 'Name' }),
      'Failed runs'
    );
    await user.click(
      screen.getByRole('checkbox', {
        name: 'Make this the default view for everyone',
      })
    );
    await user.click(screen.getByRole('button', { name: 'Save view' }));

    expect(props.onSaveView).toHaveBeenCalledWith('Failed runs', true, false);
  });

  it('lets read-only users select shared views without mutation actions', async () => {
    const user = userEvent.setup();
    const props = renderSelector({ canManageViews: false });

    await user.click(
      screen.getByRole('button', { name: 'Run view: All runs' })
    );
    await user.click(
      screen.getByRole('menuitem', { name: /production operations/i })
    );

    expect(props.onSelectView).toHaveBeenCalledWith('production');
    expect(
      screen.queryByRole('menuitem', { name: 'Save current filters as view…' })
    ).not.toBeInTheDocument();
  });

  it('offers update and reset actions when a saved view is edited', async () => {
    const user = userEvent.setup();
    const props = renderSelector({
      activeViewId: 'production',
      isAllView: false,
      isActiveViewEdited: true,
    });

    expect(screen.getByText('Edited')).toBeVisible();
    await user.click(
      screen.getByRole('button', {
        name: 'Run view: Production operations',
      })
    );
    await user.click(
      screen.getByRole('menuitem', {
        name: 'Update “Production operations”',
      })
    );
    expect(props.onUpdateView).toHaveBeenCalledOnce();

    await user.click(
      screen.getByRole('button', {
        name: 'Run view: Production operations',
      })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Reset changes' }));
    expect(props.onResetView).toHaveBeenCalledOnce();
  });

  it('keeps starring, default selection, and deletion independent', async () => {
    const user = userEvent.setup();
    const props = renderSelector({ defaultViewId: undefined });

    await user.click(
      screen.getByRole('button', { name: 'Run view: All runs' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Manage views…' }));
    await user.click(
      screen.getByRole('button', {
        name: 'Add Failed runs to the sidebar',
      })
    );
    expect(props.onSetPinned).toHaveBeenCalledWith('failed-runs', true);
    expect(props.onSetDefault).not.toHaveBeenCalled();

    await user.click(
      screen.getByRole('button', {
        name: 'Make Failed runs the default view',
      })
    );
    expect(props.onSetDefault).toHaveBeenCalledWith('failed-runs');

    await user.click(
      screen.getByRole('button', { name: 'Delete Failed runs' })
    );
    await user.click(screen.getByRole('button', { name: 'Delete view' }));
    expect(props.onDeleteView).toHaveBeenCalledWith('failed-runs');
  });
});
