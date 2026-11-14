// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useBoundedDAGRunDetails } from '@/features/dag-runs/hooks/useBoundedDAGRunDetails';
import type { ApiDAGRunDetails } from '../contracts';
import { isRunActive } from './stepRunInfo';

/** Poll period while the run is active, when the live stream is not there. */
export const FOLLOWED_RUN_POLL_MS = 2000;

export interface FollowedRunState {
  details: ApiDAGRunDetails | null;
  /** True until the run is no longer queued, started or waiting. */
  active: boolean;
  error: Error | null;
}

/** The details of a run that the step view follows, refreshed while it runs. */
export function useFollowedRun(
  dagName: string,
  runId: string | null
): FollowedRunState {
  const remoteNode = useRemoteNode();
  const target = useMemo(
    () =>
      dagName && runId ? { remoteNode, name: dagName, dagRunId: runId } : null,
    [dagName, runId, remoteNode]
  );
  const { data, error } = useBoundedDAGRunDetails({
    target,
    pollIntervalMs: FOLLOWED_RUN_POLL_MS,
  });
  const details = data ?? null;
  // Before the first answer the run counts as active: its steps wait.
  const active = target !== null && (details === null || isRunActive(details));
  return { details, active, error };
}
