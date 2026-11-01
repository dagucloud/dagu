// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { AppBarContext } from '@/contexts/AppBarContext';
import { useContext, useEffect, useRef } from 'react';
import { useSearchParams } from 'react-router-dom';

const LOCAL_NODE = 'local';

/**
 * The remote node a calendar page works on: the one its link names, or the
 * node selected in the app bar.
 */
export function useScopedRemoteNode(): string {
  const [searchParams] = useSearchParams();
  const appBarContext = useContext(AppBarContext);
  return (
    searchParams.get('remoteNode')?.trim() ||
    appBarContext.selectedRemoteNode?.trim() ||
    LOCAL_NODE
  );
}

/**
 * Drops the remote node a link named once the user picks another node in
 * the app bar, so the newer choice decides which node the page works on.
 */
export function useFollowAppBarRemoteNode(): void {
  const [searchParams, setSearchParams] = useSearchParams();
  const selected = useContext(AppBarContext).selectedRemoteNode;
  const previous = useRef(selected);
  useEffect(() => {
    if (previous.current === selected) {
      return;
    }
    previous.current = selected;
    if (searchParams.has('remoteNode')) {
      const next = new URLSearchParams(searchParams);
      next.delete('remoteNode');
      setSearchParams(next, { replace: true });
    }
  }, [selected, searchParams, setSearchParams]);
}

/** The page of a calendar on remoteNode. */
export function calendarPath(name: string, remoteNode: string): string {
  return withQuery(`/calendars/${encodeURIComponent(name)}`, { remoteNode });
}

/** The calendar list of remoteNode. */
export function calendarsPath(remoteNode: string): string {
  return withQuery('/calendars', { remoteNode });
}

/** The page of a DAG on remoteNode, opened in the DAG's workspace. */
export function dagPath(
  fileName: string,
  remoteNode: string,
  workspace?: string
): string {
  return withQuery(`/dags/${encodeURIComponent(fileName)}`, {
    remoteNode,
    workspace,
  });
}

function withQuery(
  path: string,
  { remoteNode, workspace }: { remoteNode: string; workspace?: string }
): string {
  // The node is named even when local, as the app bar may pick another.
  const params = new URLSearchParams({ remoteNode });
  if (workspace) {
    params.set('workspace', workspace);
  }
  const query = params.toString();
  return query ? `${path}?${query}` : path;
}
