// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useWorkspaceData } from './context';

/**
 * Runs the reads in parallel and settles when all of them settle. Rejects
 * with the first error, after the other reads finish.
 */
export async function settleAll(
  reads: ReadonlyArray<(() => Promise<unknown>) | null | undefined>
): Promise<void> {
  const results = await Promise.allSettled(
    reads.map((read) => (read ? read() : undefined))
  );
  const failed = results.find(
    (r): r is PromiseRejectedResult => r.status === 'rejected'
  );
  if (failed) throw failed.reason;
}

/**
 * Refreshes the DAG, the pinned run and the history (while a history list is
 * mounted). Needs WorkspaceDataProvider.
 */
export function useWorkspaceRefresh(): () => Promise<void> {
  return useWorkspaceData('useWorkspaceRefresh').refresh;
}
