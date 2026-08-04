// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { ComponentProps } from 'react';
import { describe, expect, it, vi } from 'vitest';
import type { WorkflowFilterView } from '@/contexts/UserPreference';
import { WorkflowViewSelector } from '../WorkflowViewSelector';

const views: WorkflowFilterView[] = [
  {
    id: 'production',
    name: 'Production operations',
    filters: {
      searchText: '',
      searchLabels: ['env=prod'],
      sortField: 'name',
      sortOrder: 'asc',
    },
  },
];

function renderSelector(
  overrides: Partial<ComponentProps<typeof WorkflowViewSelector>> = {}
) {
  const props: ComponentProps<typeof WorkflowViewSelector> = {
    views,
    activeViewId: null,
    defaultViewId: 'production',
    isAllView: true,
    isActiveViewEdited: false,
    onSelectView: vi.fn(),
    onShowAll: vi.fn(),
    onResetView: vi.fn(),
    onSaveView: vi.fn(),
    onUpdateView: vi.fn(),
    onSetDefault: vi.fn(),
    onDeleteView: vi.fn(),
    ...overrides,
  };
  render(<WorkflowViewSelector {...props} />);
  return props;
}

describe('WorkflowViewSelector', () => {
  it('selects saved views and keeps All workflows available', async () => {
    const user = userEvent.setup();
    const props = renderSelector();

    await user.click(
      screen.getByRole('button', { name: 'Workflow view: All workflows' })
    );
    await user.click(
      screen.getByRole('menuitem', { name: /production operations/i })
    );
    expect(props.onSelectView).toHaveBeenCalledWith('production');

    await user.click(
      screen.getByRole('button', { name: 'Workflow view: All workflows' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'All workflows' }));
    expect(props.onShowAll).toHaveBeenCalledOnce();
  });

  it('saves the current filters as a named default view', async () => {
    const user = userEvent.setup();
    const props = renderSelector({ views: [], defaultViewId: undefined });

    await user.click(
      screen.getByRole('button', { name: 'Workflow view: All workflows' })
    );
    await user.click(
      screen.getByRole('menuitem', {
        name: 'Save current filters as view…',
      })
    );
    await user.type(
      screen.getByRole('textbox', { name: 'Name' }),
      'Production operations'
    );
    await user.click(
      screen.getByRole('checkbox', {
        name: 'Make this my default view',
      })
    );
    await user.click(screen.getByRole('button', { name: 'Save view' }));

    expect(props.onSaveView).toHaveBeenCalledWith(
      'Production operations',
      true
    );
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
        name: 'Workflow view: Production operations',
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
        name: 'Workflow view: Production operations',
      })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Reset changes' }));
    expect(props.onResetView).toHaveBeenCalledOnce();
  });

  it('lets users choose a default and delete a saved view', async () => {
    const user = userEvent.setup();
    const props = renderSelector({ defaultViewId: undefined });

    await user.click(
      screen.getByRole('button', { name: 'Workflow view: All workflows' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Manage views…' }));
    await user.click(
      screen.getByRole('button', {
        name: 'Make Production operations the default view',
      })
    );
    expect(props.onSetDefault).toHaveBeenCalledWith('production');

    await user.click(
      screen.getByRole('button', { name: 'Delete Production operations' })
    );
    await user.click(screen.getByRole('button', { name: 'Delete view' }));
    expect(props.onDeleteView).toHaveBeenCalledWith('production');
  });
});
