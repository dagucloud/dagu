// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, within } from '@testing-library/react';
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

function baseProps(): ComponentProps<typeof ViewSelector> {
  return {
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
  };
}

function renderSelector(
  overrides: Partial<ComponentProps<typeof ViewSelector>> = {}
) {
  const props: ComponentProps<typeof ViewSelector> = {
    ...baseProps(),
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

  it('labels the trigger with the artifact kind when used for artifacts', async () => {
    const user = userEvent.setup();
    renderSelector({ kind: 'artifact' });

    const trigger = screen.getByRole('button', {
      name: 'Artifact view: All artifacts',
    });
    expect(trigger).toBeVisible();

    await user.click(trigger);
    expect(
      screen.getByRole('menuitem', { name: 'All artifacts' })
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

  it('keeps default selection and deletion independent', async () => {
    const user = userEvent.setup();
    const props = renderSelector({ defaultViewId: undefined });

    await user.click(
      screen.getByRole('button', { name: 'Run view: All runs' })
    );
    await user.click(screen.getByRole('menuitem', { name: 'Manage views…' }));
    await user.click(
      screen.getByRole('button', {
        name: 'Make Failed runs the default view',
      })
    );
    expect(props.onSetDefault).toHaveBeenCalledWith('failed-runs');
    expect(props.onDeleteView).not.toHaveBeenCalled();

    await user.click(
      screen.getByRole('button', { name: 'Delete Failed runs' })
    );
    await user.click(screen.getByRole('button', { name: 'Delete view' }));
    expect(props.onDeleteView).toHaveBeenCalledWith('failed-runs');
    expect(props.onSetDefault).toHaveBeenCalledOnce();
  });

  // The sidebar star was removed: no control offers it and views are saved unpinned.
  it('offers no star or sidebar control', async () => {
    const user = userEvent.setup();
    const props = renderSelector({
      views: views.map((view) => ({ ...view, pinned: true })),
      defaultViewId: undefined,
    });

    const trigger = screen.getByRole('button', { name: 'Run view: All runs' });
    expect(trigger.querySelector('.lucide-star')).toBeNull();
    await user.click(trigger);
    expect(document.querySelector('.lucide-star')).toBeNull();
    expect(screen.queryByText(/sidebar/i)).not.toBeInTheDocument();

    await user.click(screen.getByRole('menuitem', { name: 'Manage views…' }));
    expect(
      screen.queryByRole('button', { name: /sidebar/i })
    ).not.toBeInTheDocument();
    expect(document.querySelector('.lucide-star')).toBeNull();
    await user.click(screen.getByRole('button', { name: 'Done' }));

    await user.click(trigger);
    await user.click(
      screen.getByRole('menuitem', { name: 'Save current filters as view…' })
    );
    expect(screen.queryByText(/sidebar/i)).not.toBeInTheDocument();
    expect(
      screen.queryByRole('checkbox', { name: /sidebar|star/i })
    ).not.toBeInTheDocument();
    await user.type(screen.getByRole('textbox', { name: 'Name' }), 'New view');
    await user.click(screen.getByRole('button', { name: 'Save view' }));
    expect(props.onSaveView).toHaveBeenCalledWith('New view', false, false);
    expect(props.onSetPinned).not.toHaveBeenCalled();
  });

  describe('with the view toolbar (onRenameView)', () => {
    const toolbar = () => ({
      onRenameView: vi.fn().mockResolvedValue(undefined),
    });

    it('removes the save and manage items from the dropdown', async () => {
      const user = userEvent.setup();
      renderSelector({ ...toolbar() });

      await user.click(
        screen.getByRole('button', { name: 'Run view: All runs' })
      );
      expect(screen.getByRole('menuitem', { name: 'All runs' })).toBeVisible();
      expect(
        screen.queryByRole('menuitem', {
          name: 'Save current filters as view…',
        })
      ).not.toBeInTheDocument();
      expect(
        screen.queryByRole('menuitem', { name: 'Manage views…' })
      ).not.toBeInTheDocument();
    });

    it('moves the update item to the Save button', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: 'production',
        isAllView: false,
        isActiveViewEdited: true,
      });

      await user.click(
        screen.getByRole('button', { name: 'Run view: Production operations' })
      );
      expect(
        screen.queryByRole('menuitem', {
          name: 'Update “Production operations”',
        })
      ).not.toBeInTheDocument();
      expect(
        screen.getByRole('menuitem', { name: 'Reset changes' })
      ).toBeVisible();
      await user.keyboard('{Escape}');

      await user.click(screen.getByRole('button', { name: 'Save view' }));
      expect(props.onUpdateView).toHaveBeenCalledOnce();
    });

    it('disables Save while the active view is not edited', () => {
      renderSelector({
        ...toolbar(),
        activeViewId: 'production',
        isAllView: false,
        isActiveViewEdited: false,
      });

      expect(screen.getByRole('button', { name: 'Save view' })).toBeDisabled();
    });

    it('disables Save without an active view on All or saved filters', () => {
      const { unmount } = render(
        <ViewSelector
          {...baseProps()}
          {...toolbar()}
          activeViewId={null}
          isAllView
        />
      );
      expect(screen.getByRole('button', { name: 'Save view' })).toBeDisabled();
      unmount();

      render(
        <ViewSelector
          {...baseProps()}
          {...toolbar()}
          activeViewId={null}
          isAllView={false}
          filtersAlreadySaved
        />
      );
      expect(screen.getByRole('button', { name: 'Save view' })).toBeDisabled();
    });

    it('saves new filters as a view from the Save button', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: null,
        isAllView: false,
      });

      const save = screen.getByRole('button', { name: 'Save view' });
      expect(save).toBeEnabled();
      await user.click(save);
      await user.type(
        screen.getByRole('textbox', { name: 'Name' }),
        'Slow runs'
      );
      await user.click(screen.getByRole('button', { name: 'Save view' }));

      expect(props.onSaveView).toHaveBeenCalledWith('Slow runs', false, false);
    });

    it('disables Delete without an active view and hides Rename', () => {
      renderSelector({ ...toolbar() });

      expect(
        screen.getByRole('button', { name: 'Delete view' })
      ).toBeDisabled();
      expect(
        screen.queryByRole('button', { name: 'Rename view' })
      ).not.toBeInTheDocument();
    });

    it('asks for confirmation before it deletes the active view', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: 'failed-runs',
        isAllView: false,
      });

      await user.click(screen.getByRole('button', { name: 'Delete view' }));
      expect(props.onDeleteView).not.toHaveBeenCalled();

      const dialog = await screen.findByRole('dialog');
      expect(dialog).toHaveTextContent('Failed runs');
      await user.click(
        within(dialog).getByRole('button', { name: 'Delete view' })
      );
      expect(props.onDeleteView).toHaveBeenCalledWith('failed-runs');
    });

    it('renames the active view and rejects a duplicate name', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: 'failed-runs',
        isAllView: false,
      });

      await user.click(screen.getByRole('button', { name: 'Rename view' }));
      const dialog = await screen.findByRole('dialog');
      const input = within(dialog).getByRole('textbox', { name: 'Name' });
      const submit = within(dialog).getByRole('button', { name: 'Rename' });
      expect(input).toHaveValue('Failed runs');
      // The name is unchanged: nothing to rename.
      expect(submit).toBeDisabled();

      await user.clear(input);
      await user.type(input, 'production operations');
      expect(
        within(dialog).getByText('A view with this name already exists.')
      ).toBeVisible();
      expect(submit).toBeDisabled();

      await user.clear(input);
      await user.type(input, '  Broken runs ');
      expect(
        within(dialog).queryByText('A view with this name already exists.')
      ).not.toBeInTheDocument();
      await user.click(submit);

      expect(props.onRenameView).toHaveBeenCalledWith(
        'failed-runs',
        'Broken runs'
      );
    });
  });
});
