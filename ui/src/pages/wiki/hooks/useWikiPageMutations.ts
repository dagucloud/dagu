// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import { useWikiPageTabContext } from '@/contexts/WikiPageTabContext';
import { useClient } from '@/hooks/api';
import { workspaceTargetQueryForWorkspace } from '@/lib/workspace';
import {
  wikiPageMutationHasUnsavedTabs,
  normalizedWikiPageMutationWorkspace,
  type WikiPageMutationTarget,
} from '../lib/wiki-page-mutation';

type PathChange = {
  oldPath: string;
  newPath: string;
  workspace?: string | null;
  failureMessage: string;
  revalidateOnFailure?: boolean;
};

type BatchDeleteResult = {
  deletedCount: number;
  failedCount: number;
};

function pathMatches(wikiPagePath: string, targetPath: string): boolean {
  return wikiPagePath === targetPath || wikiPagePath.startsWith(`${targetPath}/`);
}

function workspaceMatches(
  left?: string | null,
  right?: string | null
): boolean {
  return (
    normalizedWikiPageMutationWorkspace(left) ===
    normalizedWikiPageMutationWorkspace(right)
  );
}

export function useWikiPageMutations({
  remoteNode,
  revalidateTree,
}: {
  remoteNode: string;
  revalidateTree: () => void;
}) {
  const client = useClient();
  const { tabs, unsavedTabIds, updateTab, closeTab } = useWikiPageTabContext();

  const hasUnsavedTabs = useCallback(
    (path: string, workspace?: string | null) =>
      wikiPageMutationHasUnsavedTabs(tabs, unsavedTabIds, path, workspace),
    [tabs, unsavedTabIds]
  );

  const updateTabsAfterPathChange = useCallback(
    (oldPath: string, newPath: string, workspace?: string | null) => {
      for (const tab of tabs) {
        if (
          workspaceMatches(tab.workspace, workspace) &&
          pathMatches(tab.wikiPagePath, oldPath)
        ) {
          const wikiPagePath = newPath + tab.wikiPagePath.slice(oldPath.length);
          updateTab(tab.id, {
            wikiPagePath,
            title: wikiPagePath.split('/').pop() || wikiPagePath,
          });
        }
      }
    },
    [tabs, updateTab]
  );

  const closeTabsForDeletedPaths = useCallback(
    (paths: ReadonlySet<string>, workspace?: string | null) => {
      for (const tab of tabs) {
        if (
          workspaceMatches(tab.workspace, workspace) &&
          [...paths].some((path) => pathMatches(tab.wikiPagePath, path))
        ) {
          closeTab(tab.id);
        }
      }
    },
    [closeTab, tabs]
  );

  const changePath = useCallback(
    async ({
      oldPath,
      newPath,
      workspace,
      failureMessage,
      revalidateOnFailure = false,
    }: PathChange): Promise<string | null> => {
      const normalizedWorkspace = normalizedWikiPageMutationWorkspace(workspace);
      const mutationQuery =
        workspaceTargetQueryForWorkspace(normalizedWorkspace);
      try {
        const { error } = await client.POST('/wiki/page/rename', {
          params: {
            query: { remoteNode, path: oldPath, ...mutationQuery },
          },
          body: { newPath },
        });
        if (error) {
          if (revalidateOnFailure) revalidateTree();
          return error.message || failureMessage;
        }
        revalidateTree();
        updateTabsAfterPathChange(oldPath, newPath, normalizedWorkspace);
        return null;
      } catch {
        if (revalidateOnFailure) revalidateTree();
        return failureMessage;
      }
    },
    [client, remoteNode, revalidateTree, updateTabsAfterPathChange]
  );

  const deletePath = useCallback(
    async (path: string, workspace?: string | null): Promise<string | null> => {
      const normalizedWorkspace = normalizedWikiPageMutationWorkspace(workspace);
      const mutationQuery =
        workspaceTargetQueryForWorkspace(normalizedWorkspace);
      try {
        const { error } = await client.DELETE('/wiki/page', {
          params: {
            query: { remoteNode, path, ...mutationQuery },
          },
        });
        if (error) return error.message || 'Failed to delete Wiki page';

        revalidateTree();
        closeTabsForDeletedPaths(new Set([path]), normalizedWorkspace);
        return null;
      } catch {
        return 'Failed to delete Wiki page';
      }
    },
    [client, closeTabsForDeletedPaths, remoteNode, revalidateTree]
  );

  const deleteBatch = useCallback(
    async (targets: WikiPageMutationTarget[]): Promise<BatchDeleteResult> => {
      const grouped = new Map<string, WikiPageMutationTarget[]>();
      for (const target of targets) {
        const workspace = normalizedWikiPageMutationWorkspace(target.workspace);
        const key = workspace ?? '';
        grouped.set(key, [...(grouped.get(key) ?? []), target]);
      }

      let deletedCount = 0;
      let failedCount = 0;
      const deletedByWorkspace = new Map<string, Set<string>>();

      for (const [workspaceKey, workspaceTargets] of grouped) {
        const workspace = workspaceKey || null;
        const mutationQuery = workspaceTargetQueryForWorkspace(workspace);
        const { data, error } = await client.POST('/wiki/delete-batch', {
          params: { query: { remoteNode, ...mutationQuery } },
          body: { paths: workspaceTargets.map((target) => target.path) },
        });
        if (error) {
          failedCount += workspaceTargets.length;
          continue;
        }
        deletedCount += data.deleted.length;
        failedCount += data.failed?.length || 0;
        deletedByWorkspace.set(workspaceKey, new Set(data.deleted));
      }

      revalidateTree();
      for (const [workspaceKey, deletedPaths] of deletedByWorkspace) {
        closeTabsForDeletedPaths(deletedPaths, workspaceKey || null);
      }
      return { deletedCount, failedCount };
    },
    [client, closeTabsForDeletedPaths, remoteNode, revalidateTree]
  );

  return {
    changePath,
    deleteBatch,
    deletePath,
    hasUnsavedTabs,
  };
}
