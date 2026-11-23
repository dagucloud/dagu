// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { render, screen, waitFor, within } from '@testing-library/react';
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

  // With onRenameView, a toolbar next to the selector saves, renames and
  // deletes the active view. The menu keeps all its items.
  describe('with the view toolbar', () => {
    const toolbar = () => ({
      onRenameView: vi.fn().mockResolvedValue(undefined),
    });
    const button = (name: string) =>
      within(screen.getByRole('group', { name: 'View actions' })).getByRole(
        'button',
        { name }
      );

    it('keeps the save, update and manage items of the menu', async () => {
      const user = userEvent.setup();
      renderSelector({
        ...toolbar(),
        activeViewId: 'production',
        isAllView: false,
        isActiveViewEdited: true,
      });

      await user.click(
        screen.getByRole('button', { name: 'Run view: Production operations' })
      );
      for (const name of [
        'Update “Production operations”',
        'Save current filters as view…',
        'Manage views…',
      ]) {
        expect(screen.getByRole('menuitem', { name })).toBeVisible();
      }
    });

    it('saves the changes of the active view from the Save button', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: 'production',
        isAllView: false,
        isActiveViewEdited: true,
      });

      await user.click(button('Save the changes of “Production operations”'));
      expect(props.onUpdateView).toHaveBeenCalledOnce();
    });

    // The update turns the Save button off: the focus goes back to the
    // selector, not to the page.
    it('focuses the selector after it saves the active view', async () => {
      const user = userEvent.setup();
      renderSelector({
        ...toolbar(),
        activeViewId: 'production',
        isAllView: false,
        isActiveViewEdited: true,
      });

      await user.click(button('Save the changes of “Production operations”'));
      await waitFor(() =>
        expect(
          screen.getByRole('button', {
            name: 'Run view: Production operations',
          })
        ).toHaveFocus()
      );
    });

    it('turns off Save while the active view is not edited', () => {
      renderSelector({
        ...toolbar(),
        activeViewId: 'production',
        isAllView: false,
        isActiveViewEdited: false,
      });

      expect(
        button('Save the changes of “Production operations”')
      ).toBeDisabled();
    });

    it.each([
      ['on All runs', { isAllView: true }],
      [
        'on the filters of a saved view',
        { isAllView: false, filtersAlreadySaved: true },
      ],
    ])('turns off Save without an active view %s', (_name, extra) => {
      renderSelector({ ...toolbar(), activeViewId: null, ...extra });

      expect(button('Save current filters as view…')).toBeDisabled();
    });

    it('saves new filters as a view from the Save button', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: null,
        isAllView: false,
      });

      await user.click(button('Save current filters as view…'));
      await user.type(
        screen.getByRole('textbox', { name: 'Name' }),
        'Slow runs'
      );
      await user.click(screen.getByRole('button', { name: 'Save view' }));

      expect(props.onSaveView).toHaveBeenCalledWith('Slow runs', false, false);
    });

    it('turns off Rename and Delete without an active view', () => {
      renderSelector({ ...toolbar() });

      expect(button('Rename view')).toBeDisabled();
      expect(button('Delete view')).toBeDisabled();
    });

    // Delete is destructive: Cancel has the focus first. The toolbar button
    // turns off once the view is gone, so the focus goes back to the selector.
    it('asks before it deletes the active view, then focuses the selector', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: 'failed-runs',
        isAllView: false,
      });

      await user.click(button('Delete view'));
      expect(props.onDeleteView).not.toHaveBeenCalled();
      const dialog = await screen.findByRole('dialog');
      expect(dialog).toHaveTextContent('Failed runs');
      await waitFor(() =>
        expect(
          within(dialog).getByRole('button', { name: 'Cancel' })
        ).toHaveFocus()
      );

      await user.click(
        within(dialog).getByRole('button', { name: 'Delete view' })
      );
      expect(props.onDeleteView).toHaveBeenCalledWith('failed-runs');
      await waitFor(() =>
        expect(
          screen.getByRole('button', { name: 'Run view: Failed runs' })
        ).toHaveFocus()
      );
    });

    it('renames the active view and refuses a name that is taken', async () => {
      const user = userEvent.setup();
      const props = renderSelector({
        ...toolbar(),
        activeViewId: 'failed-runs',
        isAllView: false,
      });

      await user.click(button('Rename view'));
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
      await user.click(submit);

      expect(props.onRenameView).toHaveBeenCalledWith(
        'failed-runs',
        'Broken runs'
      );
    });
  });
});
