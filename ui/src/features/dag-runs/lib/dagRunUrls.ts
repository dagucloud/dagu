// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

type DAGRunPageURLInput = {
  rootDAGRunName: string;
  rootDAGRunId: string;
  remoteNode: string;
  subDAGRunId?: string;
  step?: string;
};

type DAGPageURLInput = {
  fileName: string;
  remoteNode: string;
  tab?: string;
  rootDAGRunName?: string;
  rootDAGRunId?: string;
  subDAGRunId?: string;
  step?: string;
};

function appendQuery(path: string, params: URLSearchParams): string {
  const query = params.toString();
  return query ? `${path}?${query}` : path;
}

export function buildDAGRunPageURL({
  rootDAGRunName,
  rootDAGRunId,
  remoteNode,
  subDAGRunId,
  step,
}: DAGRunPageURLInput): string {
  const params = new URLSearchParams();
  params.set('remoteNode', remoteNode);
  if (subDAGRunId) {
    params.set('subDAGRunId', subDAGRunId);
    params.set('dagRunId', rootDAGRunId);
    params.set('dagRunName', rootDAGRunName);
  }
  if (step) {
    params.set('step', step);
  }

  return appendQuery(
    `/dag-runs/${encodeURIComponent(rootDAGRunName)}/${encodeURIComponent(rootDAGRunId)}`,
    params
  );
}

export function buildDAGPageURL({
  fileName,
  remoteNode,
  tab,
  rootDAGRunName,
  rootDAGRunId,
  subDAGRunId,
  step,
}: DAGPageURLInput): string {
  const params = new URLSearchParams();
  params.set('remoteNode', remoteNode);
  if (rootDAGRunId) {
    params.set('dagRunId', rootDAGRunId);
  }
  if (rootDAGRunName) {
    params.set('dagRunName', rootDAGRunName);
  }
  if (subDAGRunId) {
    params.set('subDAGRunId', subDAGRunId);
  }
  if (step) {
    params.set('step', step);
  }

  const path = tab
    ? `/dags/${encodeURIComponent(fileName)}/${encodeURIComponent(tab)}`
    : `/dags/${encodeURIComponent(fileName)}`;
  return appendQuery(path, params);
}
