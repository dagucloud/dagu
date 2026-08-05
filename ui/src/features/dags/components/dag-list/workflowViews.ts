// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  ViewSortField,
  ViewSortOrder,
  ViewWorkspaceScope,
} from '@/api/v1/schema';
import {
  sanitizeWorkspaceSelection,
  WorkspaceKind,
  type WorkspaceSelection,
} from '@/lib/workspace';

export type WorkflowFilterSet = {
  searchText: string;
  searchLabels: string[];
  sortField: ViewSortField;
  sortOrder: ViewSortOrder;
};

export type WorkflowFilterView = {
  id: string;
  name: string;
  pinned: boolean;
  filters: WorkflowFilterSet;
};

export type WorkflowViewScope = {
  workspace: string;
  workspaceScope: ViewWorkspaceScope;
};

export function workflowViewScopeForSelection(
  selection?: Partial<WorkspaceSelection> | null
): WorkflowViewScope {
  const sanitized = sanitizeWorkspaceSelection(selection);
  if (sanitized.kind === WorkspaceKind.workspace) {
    return {
      workspace: sanitized.workspace ?? '',
      workspaceScope: ViewWorkspaceScope.workspace,
    };
  }
  return {
    workspace: '',
    workspaceScope:
      sanitized.kind === WorkspaceKind.default
        ? ViewWorkspaceScope.default
        : ViewWorkspaceScope.all,
  };
}

export function workflowViewMatchesScope(
  view: { workspace?: string; workspaceScope?: ViewWorkspaceScope },
  scope: WorkflowViewScope
): boolean {
  return (
    view.workspaceScope === scope.workspaceScope &&
    (scope.workspaceScope !== ViewWorkspaceScope.workspace ||
      view.workspace === scope.workspace)
  );
}
