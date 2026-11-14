// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

type DAGDetails = components['schemas']['DAGDetails'];

/** The labels of the DAG, with the deprecated tags that are not yet labels. */
export function visibleLabels(dag: Pick<DAGDetails, 'labels' | 'tags'>) {
  return [...new Set([...(dag.labels ?? []), ...(dag.tags ?? [])])];
}

/** The names of `NAME=value` entries. The values may be secrets: they stay out. */
export function envVariableNames(env: string[] | undefined): string[] {
  return [
    ...new Set(
      (env ?? [])
        .map((entry) => entry.split('=', 1)[0]?.trim() ?? '')
        .filter((name) => name !== '')
    ),
  ];
}
