// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/** What the Start dialog asks for: a run of the DAG, started now or queued. */
export type StartRequest = {
  params: string;
  dagRunId?: string;
  /** Starts the run now; otherwise the run waits in the queue. */
  immediate: boolean;
  /** The profile the run uses in place of the DAG's; '' runs without one. */
  profile?: string;
  noReuse?: boolean;
  scheduleTime?: string;
  /** The one node a node: required DAG runs bound to. */
  targetNodes?: string[];
};

/** The body of a start or enqueue request, with the fields the request sets. */
type StartRequestBody = {
  params: string;
  dagRunId?: string;
  profile?: string;
  noReuse?: boolean;
  scheduleTime?: string;
  targetNodes?: string[];
};

/** The body of the start or enqueue request of the DAG for a request. */
export function startRequestBody(request: StartRequest): StartRequestBody {
  const body: StartRequestBody = { params: request.params };
  if (request.dagRunId) body.dagRunId = request.dagRunId;
  if (request.profile !== undefined) body.profile = request.profile;
  if (request.noReuse !== undefined) body.noReuse = request.noReuse;
  if (request.scheduleTime) body.scheduleTime = request.scheduleTime;
  if (request.targetNodes?.length) body.targetNodes = request.targetNodes;
  return body;
}
