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

/**
 * How often a running run of a list reads its steps again, in ms. A
 * continuous run never ends, so a page of them polls for as long as it is open.
 */
const RUNNING_REFRESH_MS = 60_000;

/**
 * How long a list reuses the list of service DAGs, in ms: a row that mounts
 * later reads it again once this time has passed.
 */
const SERVICE_LIST_TTL_MS = 5 * 60_000;

/**
 * The status that a list prints for a run. The summary of a run has no steps.
 * A running run can be "Healthy" only when its workflow is a service DAG
 * (restart_policy), so the hook reads the list of service DAGs first (one
 * request shared by all the rows of every list). Only a run of such a
 * workflow reads its details (once, and again every minute while it has a
 * continuous step): runDisplayStatus then decides, with the failed steps in
 * view. Until the details are there, and for every run that is not running,
 * the API status stands. A definition edited after the run started can hide a
 * continuous step of that run until it ends.
 */
export function useListRunDisplay(
  name: string,
  dagRunId: string | undefined,
  status: Status | undefined
): RunDisplayStatus | undefined {
  const remoteNode = useRemoteNode();
  const running = status === Status.Running && !!dagRunId;
  const services = useQuery(
    '/service-dags',
    running ? { params: { query: { remoteNode } } } : null,
    {
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      shouldRetryOnError: false,
      dedupingInterval: SERVICE_LIST_TTL_MS,
    }
  );
  const service = (services.data?.dags ?? []).some((dag) => dag.name === name);
  const enabled = running && service;
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
    ? runDisplayStatus(details, { service })
    : summaryDisplayStatus(status, { service, continuous: false });
}
