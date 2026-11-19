// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import type { ToolInputHint } from '../../../tools/targets';

/** Number of DAG names that the picker lists for one search text. */
export const DAG_CHOICES = 30;

/**
 * The names of the DAGs that match `search`, from the DAG list API, for the
 * picker. Nothing is requested while `enabled` is false.
 */
export function useDagChoices(
  enabled: boolean,
  search: string
): { dags: string[]; loading: boolean } {
  const remoteNode = useRemoteNode();
  const { data, isLoading } = useQuery(
    '/dags',
    whenEnabled(enabled, {
      params: {
        query: {
          remoteNode,
          perPage: DAG_CHOICES,
          ...(search.trim() ? { name: search.trim() } : {}),
        },
      },
    })
  );
  const dags = useMemo(
    () => (data?.dags ?? []).map((file) => file.dag.name),
    [data?.dags]
  );
  return { dags, loading: enabled && isLoading };
}

const PLAIN_NAME = /^[^${}]+$/;

/**
 * The inputs that a DAG declares, from its details, for the inputs of a DAG
 * tool. A name that is a `${...}` reference, an empty name and a DAG that the
 * server does not know give no input. Nothing is requested while `enabled`
 * is false.
 */
export function useDagInputs(name: string, enabled: boolean): ToolInputHint[] {
  const remoteNode = useRemoteNode();
  const known = enabled && PLAIN_NAME.test(name.trim());
  const { data } = useQuery(
    '/dags/{fileName}',
    whenEnabled(known, {
      params: { path: { fileName: name.trim() }, query: { remoteNode } },
    })
  );
  return useMemo(
    () =>
      known
        ? (data?.dag?.paramDefs ?? []).flatMap((def) =>
            def.name
              ? [
                  def.description
                    ? { name: def.name, description: def.description }
                    : { name: def.name },
                ]
              : []
          )
        : [],
    [known, data?.dag?.paramDefs]
  );
}
