// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { fireEvent, render, screen, within } from '@testing-library/react';
import React from 'react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { Status } from '@/api/v1/schema';
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
    isAllWorkflowsView?: boolean;
    panelWidth?: number | null;
  } = {}
) {
  const onShowAllWorkflows = vi.fn();
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
            sortField="name"
            sortOrder="asc"
            onSortChange={vi.fn()}
            workflowViews={options.workflowViews ?? []}
            activeWorkflowViewId={options.activeWorkflowViewId ?? null}
            isAllWorkflowsView={options.isAllWorkflowsView ?? true}
            isWorkflowViewEdited={false}
            canManageWorkflowViews={true}
            onSelectWorkflowView={vi.fn()}
            onShowAllWorkflows={onShowAllWorkflows}
            onResetWorkflowView={vi.fn()}
            onSaveWorkflowView={vi.fn()}
            onUpdateWorkflowView={vi.fn()}
            onSetDefaultWorkflowView={vi.fn()}
            onDeleteWorkflowView={vi.fn()}
          />
        </PanelWidthContext.Provider>
      </AppBarContext.Provider>
    </MemoryRouter>
  );
  return { ...result, onShowAllWorkflows };
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

  it('explains an empty saved view and offers to show all workflows', () => {
    const { onShowAllWorkflows } = renderTable('', {
      dags: [],
      workflowViews: [
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
