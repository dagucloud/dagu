// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  sanitizeWorkspaceName,
  visibleWikiPagePathForWorkspace,
} from '@/lib/workspace';

export type WikiPageMutationTarget = {
  path: string;
  workspace?: string | null;
};

type OpenWikiPageTab = {
  id: string;
  wikiPagePath: string;
  workspace?: string | null;
};

type ResolveMoveInput = {
  dragId: string;
  dragWorkspace?: string | null;
  parentId: string | null;
  parentWorkspace?: string | null;
  rootWorkspace?: string | null;
};

export function normalizedWikiPageMutationWorkspace(
  workspace?: string | null
): string | null {
  return sanitizeWorkspaceName(workspace ?? '') || null;
}

export function isWorkspaceRootTreeNode(
  id: string,
  workspace?: string | null
): boolean {
  const normalized = normalizedWikiPageMutationWorkspace(workspace);
  return !!normalized && id === normalized;
}

export function wikiPageMutationPathForTreeNode(
  id: string,
  workspace?: string | null
): string {
  const normalized = normalizedWikiPageMutationWorkspace(workspace);
  if (normalized && id === normalized) {
    return '';
  }
  return visibleWikiPagePathForWorkspace(id, normalized);
}

export function wikiPageMutationTargetForTreeNode(
  id: string,
  workspace?: string | null
): WikiPageMutationTarget {
  return {
    path: wikiPageMutationPathForTreeNode(id, workspace),
    workspace: normalizedWikiPageMutationWorkspace(workspace),
  };
}

export function wikiPageMutationHasUnsavedTabs(
  tabs: readonly OpenWikiPageTab[],
  unsavedTabIds: ReadonlySet<string>,
  targetPath: string,
  workspace?: string | null
): boolean {
  const normalizedWorkspace = normalizedWikiPageMutationWorkspace(workspace);
  return tabs.some(
    (tab) =>
      normalizedWikiPageMutationWorkspace(tab.workspace) === normalizedWorkspace &&
      (tab.wikiPagePath === targetPath ||
        tab.wikiPagePath.startsWith(`${targetPath}/`)) &&
      unsavedTabIds.has(tab.id)
  );
}

export function resolveWikiTreeMove({
  dragId,
  dragWorkspace,
  parentId,
  parentWorkspace,
  rootWorkspace,
}: ResolveMoveInput): {
  oldPath: string;
  newPath: string;
  workspace: string | null;
} | null {
  const workspace = normalizedWikiPageMutationWorkspace(dragWorkspace);
  const destinationWorkspace = parentId
    ? normalizedWikiPageMutationWorkspace(parentWorkspace)
    : normalizedWikiPageMutationWorkspace(rootWorkspace);
  if (workspace !== destinationWorkspace) {
    return null;
  }
  if (isWorkspaceRootTreeNode(dragId, workspace)) {
    return null;
  }
  if (parentId && (parentId === dragId || parentId.startsWith(`${dragId}/`))) {
    return null;
  }

  const nodeName = dragId.split('/').pop() || dragId;
  const newTreePath = parentId ? `${parentId}/${nodeName}` : nodeName;
  const oldPath = wikiPageMutationPathForTreeNode(dragId, workspace);
  const newPath = wikiPageMutationPathForTreeNode(newTreePath, workspace);
  if (!oldPath || !newPath || oldPath === newPath) {
    return null;
  }

  return {
    oldPath,
    newPath,
    workspace,
  };
}
