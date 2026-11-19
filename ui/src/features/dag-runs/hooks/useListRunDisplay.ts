// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Status } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { isServiceDAG } from '@/lib/dagSchedule';
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
 * How long a list reuses the definition of a workflow, in ms: a row that
 * mounts later reads it again once this time has passed.
 */
const DEFINITION_TTL_MS = 5 * 60_000;

/**
 * The status that a list prints for a run. The summary of a run has no steps.
 * A running run can be "Healthy" only when its workflow is a service DAG
 * (restart_policy), so the hook reads the definition first (one request for
 * each workflow name, shared by all its rows). Only such a workflow reads the
 * details of the run (once, and again every minute while it has a continuous
 * step): runDisplayStatus then
 * decides, with the failed steps in view. Until the details are there, for a
 * definition that the name does not give (for example a name that differs
 * from the file name) and for every run that is not running, the API status
 * stands. A definition edited after the run started can hide a continuous
 * step of that run until it ends.
 */
export function useListRunDisplay(
  name: string,
  dagRunId: string | undefined,
  status: Status | undefined
): RunDisplayStatus | undefined {
  const remoteNode = useRemoteNode();
  const running = status === Status.Running && !!dagRunId;
  const definition = useQuery(
    '/dags/{fileName}',
    running
      ? { params: { path: { fileName: name }, query: { remoteNode } } }
      : null,
    {
      revalidateOnFocus: false,
      revalidateOnReconnect: false,
      shouldRetryOnError: false,
      dedupingInterval: DEFINITION_TTL_MS,
    }
  );
  const service =
    definition.data?.dag !== undefined && isServiceDAG(definition.data.dag);
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
