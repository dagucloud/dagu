// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Status } from '@/api/v1/schema';

type ExecutionsFilter = {
  name?: string;
  status?: Status;
  /** Unix seconds */
  from: number;
  /** Unix seconds */
  to: number;
};

/** Address of the Executions page filtered to a workflow, status, and range. */
export function executionsURL({
  name,
  status,
  from,
  to,
}: ExecutionsFilter): string {
  const params = new URLSearchParams();
  if (name) params.set('name', name);
  if (status !== undefined) params.set('status', String(status));
  params.set('dateMode', 'custom');
  params.set('fromDate', String(from));
  params.set('toDate', String(to));
  return `/dag-runs?${params.toString()}`;
}
