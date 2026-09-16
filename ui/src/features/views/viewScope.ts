// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ViewWorkspaceScope } from '@/api/v1/schema';
import {
  sanitizeWorkspaceSelection,
  WorkspaceKind,
  type WorkspaceSelection,
} from '@/lib/workspace';

export type ViewScope = {
  workspace: string;
  workspaceScope: ViewWorkspaceScope;
};

export function viewScopeForSelection(
  selection?: Partial<WorkspaceSelection> | null
): ViewScope {
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

export function viewMatchesScope(
  view: { workspace?: string; workspaceScope?: ViewWorkspaceScope },
  scope: ViewScope
): boolean {
  const workspaceScope =
    view.workspaceScope ||
    (view.workspace ? ViewWorkspaceScope.workspace : ViewWorkspaceScope.all);
  return (
    workspaceScope === scope.workspaceScope &&
    (scope.workspaceScope !== ViewWorkspaceScope.workspace ||
      view.workspace === scope.workspace)
  );
}
