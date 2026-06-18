// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { components } from '@/api/v1/schema';
import fetchJson from '@/lib/fetchJson';

export type DAGRunDetails = components['schemas']['DAGRunDetails'];

export type DAGRunDetailsRequestTarget = {
  remoteNode: string;
  name: string;
  dagRunId: string;
  parentName?: string;
  parentDAGRunId?: string;
  subDAGRunId?: string;
};

type DAGRunDetailsResponse = {
  dagRunDetails: DAGRunDetails;
};

export function matchesRequestedDAGRunDetails(
  details: DAGRunDetails | null | undefined,
  requestedDagRunId: string,
  requestedName?: string
): boolean {
  if (!details) {
    return false;
  }
  if (requestedName && details.name !== requestedName) {
    return false;
  }
  return (
    requestedDagRunId === 'latest' || details.dagRunId === requestedDagRunId
  );
}

function buildQueryString(remoteNode: string): string {
  return new URLSearchParams({ remoteNode }).toString();
}

export function buildDAGRunDetailsPath(
  target: DAGRunDetailsRequestTarget
): string {
  const query = buildQueryString(target.remoteNode);

  if (target.subDAGRunId && target.parentDAGRunId && target.parentName) {
    return `/dag-runs/${encodeURIComponent(target.parentName)}/${encodeURIComponent(target.parentDAGRunId)}/sub-dag-runs/${encodeURIComponent(target.subDAGRunId)}?${query}`;
  }

  return `/dag-runs/${encodeURIComponent(target.name)}/${encodeURIComponent(target.dagRunId)}?${query}`;
}

export async function fetchDAGRunDetails(
  target: DAGRunDetailsRequestTarget,
  init?: RequestInit
): Promise<DAGRunDetails> {
  const response = await fetchJson<DAGRunDetailsResponse>(
    buildDAGRunDetailsPath(target),
    init
  );
  return response.dagRunDetails;
}
