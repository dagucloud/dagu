// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  sanitizeWorkspaceName,
  visibleDocumentPathForWorkspace,
} from '@/lib/workspace';

export type DocMutationTarget = {
  path: string;
  workspace?: string | null;
};

type ResolveMoveInput = {
  dragId: string;
  dragWorkspace?: string | null;
  parentId: string | null;
  parentWorkspace?: string | null;
};

export function normalizedDocMutationWorkspace(
  workspace?: string | null
): string | null {
  return sanitizeWorkspaceName(workspace ?? '') || null;
}

export function isWorkspaceRootTreeNode(
  id: string,
  workspace?: string | null
): boolean {
  const normalized = normalizedDocMutationWorkspace(workspace);
  return !!normalized && id === normalized;
}

export function docMutationPathForTreeNode(
  id: string,
  workspace?: string | null
): string {
  const normalized = normalizedDocMutationWorkspace(workspace);
  if (normalized && id === normalized) {
    return '';
  }
  return visibleDocumentPathForWorkspace(id, normalized);
}

export function docMutationTargetForTreeNode(
  id: string,
  workspace?: string | null
): DocMutationTarget {
  return {
    path: docMutationPathForTreeNode(id, workspace),
    workspace: normalizedDocMutationWorkspace(workspace),
  };
}

export function resolveDocTreeMove({
  dragId,
  dragWorkspace,
  parentId,
  parentWorkspace,
}: ResolveMoveInput): {
  oldPath: string;
  newPath: string;
  workspace?: string | null;
} | null {
  const workspace = normalizedDocMutationWorkspace(dragWorkspace);
  const destinationWorkspace = parentId
    ? normalizedDocMutationWorkspace(parentWorkspace)
    : null;
  if (workspace !== destinationWorkspace) {
    return null;
  }
  if (isWorkspaceRootTreeNode(dragId, workspace)) {
    return null;
  }

  const nodeName = dragId.split('/').pop() || dragId;
  const newTreePath = parentId ? `${parentId}/${nodeName}` : nodeName;
  const oldPath = docMutationPathForTreeNode(dragId, workspace);
  const newPath = docMutationPathForTreeNode(newTreePath, workspace);
  if (!oldPath || !newPath || oldPath === newPath) {
    return null;
  }

  return {
    oldPath,
    newPath,
    workspace,
  };
}
