// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

type ServiceDAG = components['schemas']['ServiceDAG'];

/** The service DAGs whose name contains the filter, ignoring case. */
export function filterServiceDAGsByName(
  serviceDAGs: ServiceDAG[],
  nameFilter: string
): ServiceDAG[] {
  const query = nameFilter.trim().toLowerCase();
  return query
    ? serviceDAGs.filter((dag) => dag.name.toLowerCase().includes(query))
    : serviceDAGs;
}
