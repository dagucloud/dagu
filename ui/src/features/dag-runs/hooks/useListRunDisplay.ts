// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import {
  hasContinuousStep,
  runDisplayStatus,
  summaryDisplayStatus,
  type RunDisplayStatus,
} from '@/lib/runDisplayStatus';

/** How often a running run of a list reads its steps again, in ms. */
const RUNNING_REFRESH_MS = 10_000;

/**
 * The status that a list prints for a run. The summary of a run has no steps,
 * so a running run reads its details (once, cached for each run, and again
 * every 10 s while it runs when it has a continuous step, the only kind that
 * can be Healthy): runDisplayStatus then decides, with the failed steps in
 * view. Until the details are there, and for every run that is not running,
 * the API status stands.
 */
export function useListRunDisplay(
  name: string,
  dagRunId: string | undefined,
  status: Status | undefined
): RunDisplayStatus | undefined {
  const remoteNode = useRemoteNode();
  const enabled = status === Status.Running && !!dagRunId;
  const { data } = useQuery(
    '/dag-runs/{name}/{dagRunId}',
    enabled
      ? {
          params: {
            path: { name, dagRunId: dagRunId! },
            query: { remoteNode },
          },
        }
      : null,
    {
      revalidateOnFocus: false,
      refreshInterval: (latest) =>
        hasContinuousStep(latest?.dagRunDetails.nodes?.map((node) => node.step))
          ? RUNNING_REFRESH_MS
          : 0,
    }
  );
  if (status === undefined) return undefined;
  const details = data?.dagRunDetails;
  return details && details.dagRunId === dagRunId
    ? runDisplayStatus(details)
    : summaryDisplayStatus(status, false);
}
