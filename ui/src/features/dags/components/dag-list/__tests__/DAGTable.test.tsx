// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status, ViewSortField, ViewSortOrder } from '@/api/v1/schema';
import { PanelWidthContext } from '@/components/SplitLayout';
import { AppBarContext } from '@/contexts/AppBarContext';
import { WorkspaceKind } from '@/lib/workspace';
import DAGTable from '../DAGTable';

vi.mock('@/hooks/api', () => ({
  useQuery: () => ({
    data: {
      labels: ['team=ops'],
    },
  }),
}));

vi.mock('@/features/dags/components/common/DAGActions', () => ({
  default: () => null,
}));

vi.mock('@/features/dags/components/common/LiveSwitch', () => ({
  default: () => null,
}));

vi.mock('@/features/dags/components/common', () => ({
  CreateDAGModal: () => null,
  DAGPagination: () => null,
}));

function renderTable(
  searchText = '',
  options: {
    dags?: React.ComponentProps<typeof DAGTable>['dags'];
    workflowViews?: React.ComponentProps<typeof DAGTable>['workflowViews'];
    activeWorkflowViewId?: string | null;
    activeOnly?: boolean;
    isAllWorkflowsView?: boolean;
    panelWidth?: number | null;
    canDeleteDAGs?: boolean;
    canRenameDAGs?: boolean;
    onDeleteDAGs?: React.ComponentProps<typeof DAGTable>['onDeleteDAGs'];
    onRenameDAG?: (fileName: string, newFileName: string) => Promise<void>;
  } = {}
) {
  const onShowAllWorkflows = vi.fn();
  const handleActiveOnlyChange = vi.fn();
  const onDeleteDAGs =
    options.onDeleteDAGs ??
    vi
      .fn()
      .mockImplementation(async (fileNames: string[]) =>
        fileNames.map((fileName) => ({ fileName }))
      );
  const onRenameDAG =
    options.onRenameDAG ?? vi.fn().mockResolvedValue(undefined);
  const result = render(
    <MemoryRouter>
      <AppBarContext.Provider
        value={
          {
            selectedRemoteNode: 'local',
            workspaceSelection: { kind: WorkspaceKind.all },
          } as never
        }
      >
        <PanelWidthContext.Provider value={options.panelWidth ?? null}>
          <DAGTable
            dags={
              options.dags ?? [
                {
                  fileName: 'example.yaml',
                  dag: {
                    name: searchText || 'example',
                  },
                  latestDAGRun: {
                    status: Status.Success,
                    statusLabel: 'Success',
                  },
                  suspended: false,
                  errors: [],
                } as never,
              ]
            }
            group=""
            refreshFn={vi.fn()}
            searchText={searchText}
            handleSearchTextChange={vi.fn()}
            searchLabels={[]}
            handleSearchLabelsChange={vi.fn()}
            activeOnly={options.activeOnly ?? false}
            handleActiveOnlyChange={handleActiveOnlyChange}
            sortField="name"
            sortOrder="asc"
            onSortChange={vi.fn()}
            workflowViews={options.workflowViews ?? []}
            activeWorkflowViewId={options.activeWorkflowViewId ?? null}
            isAllWorkflowsView={options.isAllWorkflowsView ?? true}
            isWorkflowViewEdited={false}
            canManageWorkflowViews={true}
            canDeleteDAGs={options.canDeleteDAGs ?? true}
            canRenameDAGs={options.canRenameDAGs ?? true}
            onSelectWorkflowView={vi.fn()}
            onShowAllWorkflows={onShowAllWorkflows}
            onResetWorkflowView={vi.fn()}
            onSaveWorkflowView={vi.fn()}
            onUpdateWorkflowView={vi.fn()}
            onSetDefaultWorkflowView={vi.fn()}
            onSetPinnedWorkflowView={vi.fn()}
            onDeleteWorkflowView={vi.fn()}
            onDeleteDAGs={onDeleteDAGs}
            onRenameDAG={onRenameDAG}
          />
        </PanelWidthContext.Provider>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  return {
    ...result,
    handleActiveOnlyChange,
    onDeleteDAGs,
    onRenameDAG,
    onShowAllWorkflows,
  };
}

describe('DAGTable', () => {
  beforeEach(() => {
    vi.stubGlobal('getConfig', () => ({
      tz: 'UTC',
    }));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('uses the same control surface sizing as the executions page', () => {
    renderTable();

    const searchInput = screen.getByPlaceholderText(
      'Filter by workflow name...'
    );
    expect(searchInput.className).toContain('h-9');
    expect(searchInput.className).toContain('w-[200px]');

    const controlSurface = searchInput.closest(
      '[data-testid="workflow-controls"]'
    );
    expect(controlSurface?.className).toContain('mb-3');
    expect(controlSurface?.className).toContain('rounded-lg');
    expect(controlSurface?.className).toContain('border');
    expect(controlSurface?.className).toContain('border-border');
    expect(controlSurface?.className).toContain('bg-card/50');
    expect(controlSurface?.className).toContain('p-3');

    const labelInput = screen.getByRole('combobox', {
      name: 'Filter by labels...',
    });
    expect(labelInput.parentElement?.className).toContain('min-h-9');
    expect(labelInput.parentElement?.className).toContain('bg-card');
  });

  it('links grep to the global DAG search with the current workflow keyword', () => {
    renderTable('daily backup');

    expect(screen.getByRole('link', { name: 'Grep' })).toHaveAttribute(
      'href',
      '/search?q=daily+backup&scope=dags'
    );
  });

  it('toggles the active workflow filter', () => {
    const { handleActiveOnlyChange, unmount } = renderTable();

    const activeOnlySwitch = screen.getByRole('switch', {
      name: 'Active only',
    });
    expect(activeOnlySwitch).toHaveAttribute('aria-checked', 'false');
    fireEvent.click(activeOnlySwitch);
    expect(handleActiveOnlyChange).toHaveBeenCalledWith(true);

    unmount();
    renderTable('', { activeOnly: true });
    expect(screen.getByRole('switch', { name: 'Active only' })).toHaveAttribute(
      'aria-checked',
      'true'
    );
  });

  it('hides workflow mutation controls without write permission', () => {
    renderTable('', { canDeleteDAGs: false, canRenameDAGs: false });

    const table = screen.getByRole('table');
    expect(
      within(table).queryByRole('checkbox', {
        name: 'Select all loaded workflows',
      })
    ).not.toBeInTheDocument();
    expect(
      within(table).queryByRole('button', { name: 'Rename workflow example' })
    ).not.toBeInTheDocument();
    expect(
      within(table).queryByRole('button', { name: 'Delete workflow example' })
    ).not.toBeInTheDocument();
  });

  it('selects only workflows visible through the client-side filter', async () => {
    renderTable('alpha', {
      dags: [
        {
          fileName: 'alpha.yaml',
          dag: { name: 'alpha' },
          latestDAGRun: {
            status: Status.Success,
            statusLabel: 'Success',
          },
          suspended: false,
          errors: [],
        } as never,
        {
          fileName: 'beta.yaml',
          dag: { name: 'beta' },
          latestDAGRun: {
            status: Status.Success,
            statusLabel: 'Success',
          },
          suspended: false,
          errors: [],
        } as never,
      ],
    });

    const table = screen.getByRole('table');
    await waitFor(() => {
      expect(within(table).queryByText('beta')).not.toBeInTheDocument();
    });
    fireEvent.click(
      within(table).getByRole('checkbox', {
        name: 'Select all loaded workflows',
      })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete (1)' }));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('alpha.yaml');
    expect(dialog).not.toHaveTextContent('beta.yaml');
  });

  it('selects loaded workflows and deletes them after confirmation', async () => {
    const { onDeleteDAGs } = renderTable('', {
      dags: [
        {
          fileName: 'alpha.yaml',
          dag: { name: 'alpha' },
          latestDAGRun: {
            status: Status.Success,
            statusLabel: 'Success',
          },
          suspended: false,
          errors: [],
        } as never,
        {
          fileName: 'beta.yaml',
          dag: { name: 'beta' },
          latestDAGRun: {
            status: Status.Success,
            statusLabel: 'Success',
          },
          suspended: false,
          errors: [],
        } as never,
      ],
    });

    expect(
      screen.queryByRole('button', { name: /^Delete \(/ })
    ).not.toBeInTheDocument();
    fireEvent.click(
      within(screen.getByRole('table')).getByRole('checkbox', {
        name: 'Select all loaded workflows',
      })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete (2)' }));

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Delete 2 workflows?');
    expect(dialog).toHaveTextContent('alpha.yaml');
    expect(dialog).toHaveTextContent('beta.yaml');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => {
      expect(onDeleteDAGs).toHaveBeenCalledWith(['alpha.yaml', 'beta.yaml']);
    });
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
    expect(
      screen.queryByRole('button', { name: /^Delete \(/ })
    ).not.toBeInTheDocument();
  });

  it('keeps failed workflows selected with their individual errors', async () => {
    const onDeleteDAGs = vi
      .fn()
      .mockResolvedValue([
        { fileName: 'alpha.yaml' },
        { fileName: 'beta.yaml', error: 'permission denied' },
      ]);
    renderTable('', {
      dags: [
        {
          fileName: 'alpha.yaml',
          dag: { name: 'alpha' },
          latestDAGRun: {
            status: Status.Success,
            statusLabel: 'Success',
          },
          suspended: false,
          errors: [],
        } as never,
        {
          fileName: 'beta.yaml',
          dag: { name: 'beta' },
          latestDAGRun: {
            status: Status.Success,
            statusLabel: 'Success',
          },
          suspended: false,
          errors: [],
        } as never,
      ],
      onDeleteDAGs,
    });

    fireEvent.click(
      within(screen.getByRole('table')).getByRole('checkbox', {
        name: 'Select all loaded workflows',
      })
    );
    fireEvent.click(screen.getByRole('button', { name: 'Delete (2)' }));
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Delete' })
    );

    await waitFor(() => {
      const dialog = screen.getByRole('dialog');
      expect(dialog).not.toHaveTextContent('alpha.yaml');
      expect(dialog).toHaveTextContent('beta.yaml');
      expect(dialog).toHaveTextContent('permission denied');
    });
    fireEvent.click(
      within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' })
    );
    expect(screen.getByRole('button', { name: 'Delete (1)' })).toBeEnabled();
  });

  it('renames a workflow from the actions column', async () => {
    const { onRenameDAG } = renderTable();

    fireEvent.click(
      within(screen.getByRole('table')).getByRole('button', {
        name: 'Rename workflow example',
      })
    );

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Rename DAG');
    const nameInput = within(dialog).getByLabelText('DAG Name');
    expect(nameInput).toHaveValue('example.yaml');
    fireEvent.change(nameInput, { target: { value: 'renamed.yaml' } });
    fireEvent.click(within(dialog).getByRole('button', { name: 'Rename' }));

    await waitFor(() => {
      expect(onRenameDAG).toHaveBeenCalledWith('example.yaml', 'renamed.yaml');
    });
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('deletes a workflow from the actions column after confirmation', async () => {
    const { onDeleteDAGs } = renderTable();

    fireEvent.click(
      within(screen.getByRole('table')).getByRole('button', {
        name: 'Delete workflow example',
      })
    );

    const dialog = screen.getByRole('dialog');
    expect(dialog).toHaveTextContent('Delete workflow?');
    expect(dialog).toHaveTextContent('example.yaml');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));

    await waitFor(() => {
      expect(onDeleteDAGs).toHaveBeenCalledWith(['example.yaml']);
    });
    await waitFor(() => {
      expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });
  });

  it('explains an empty saved view and offers to show all workflows', () => {
    const { onShowAllWorkflows } = renderTable('', {
      dags: [],
      workflowViews: [
        {
          id: 'production',
          name: 'Production operations',
          pinned: false,
          filters: {
            searchText: '',
            searchLabels: ['env=prod'],
            activeOnly: false,
            sortField: ViewSortField.name,
            sortOrder: ViewSortOrder.asc,
          },
        },
      ],
      activeWorkflowViewId: 'production',
      isAllWorkflowsView: false,
      panelWidth: 600,
    });

    expect(screen.getAllByText('No workflows found').length).toBeGreaterThan(0);
    expect(
      screen.getAllByText(/No workflows match the “Production operations” view/)
        .length
    ).toBeGreaterThan(0);

    const cardView = screen.getByTestId('workflow-card-view');
    expect(cardView.className).toContain('block');
    fireEvent.click(
      within(cardView).getByRole('button', { name: 'Show all workflows' })
    );
    expect(onShowAllWorkflows).toHaveBeenCalledOnce();
  });
});
