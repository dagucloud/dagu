// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { useDebouncedValue } from '@/hooks/useDebouncedValue';
import { whenEnabled } from '@/hooks/queryUtils';

/** Number of DAGs that one request of the catalogue lists (the API maximum). */
export const DAG_CATALOG_SIZE = 100;

/** A DAG of the DAGs directory, as the Tools dock lists it. */
export interface DagCatalogItem {
  name: string;
  /** The description of the DAG. Empty when it has none. */
  description: string;
  /** The names of the parameters that the DAG declares. */
  params: string[];
}

/**
 * The DAGs of the DAGs directory with their descriptions, for the catalogue of
 * the Tools dock and for the description of a DAG tool. A search also asks the
 * server, so that a directory with more than `DAG_CATALOG_SIZE` DAGs is found
 * too. `loading` is true until the first list has arrived, and `complete`
 * tells that the list holds every DAG, so that a name it lacks is unknown. Nothing is
 * requested while `enabled` is false.
 */
export function useDagCatalog(
  enabled: boolean,
  search: string
): { dags: DagCatalogItem[]; loading: boolean; complete: boolean } {
  const remoteNode = useRemoteNode();
  const needle = useDebouncedValue(search.trim(), 250);
  const all = useQuery(
    '/dags',
    whenEnabled(enabled, {
      params: { query: { remoteNode, perPage: DAG_CATALOG_SIZE } },
    })
  );
  const found = useQuery(
    '/dags',
    whenEnabled(enabled && needle !== '', {
      params: {
        query: { remoteNode, perPage: DAG_CATALOG_SIZE, name: needle },
      },
    })
  );
  const dags = useMemo(() => {
    const seen = new Map<string, DagCatalogItem>();
    for (const file of [
      ...(all.data?.dags ?? []),
      ...(found.data?.dags ?? []),
    ]) {
      const dag: components['schemas']['DAG'] = file.dag;
      if (seen.has(dag.name)) continue;
      seen.set(dag.name, {
        name: dag.name,
        description: dag.description?.trim() ?? '',
        params: dag.params ?? [],
      });
    }
    return [...seen.values()];
  }, [all.data?.dags, found.data?.dags]);
  // The list is complete when one request holds every DAG of the directory.
  const complete =
    all.data !== undefined && all.data.dags.length < DAG_CATALOG_SIZE;
  return { dags, loading: enabled && all.isLoading, complete };
}

/** One input of a DAG, as the DAG declares it. */
export interface DagInputDef {
  name: string;
  type: string;
  required: boolean;
  description: string;
  /** The default, as text. Empty when the DAG has none. */
  fallback: string;
  /** The allowed values, as text. Empty when any value is allowed. */
  choices: string[];
}

const PLAIN_NAME = /^[^${}]+$/;

/** What the server says about a DAG that a tool names. */
export interface DagDetails {
  /** False when the server does not know the DAG, true when it does, undefined while the answer is not there. */
  known: boolean | undefined;
  description: string;
  inputs: DagInputDef[];
  /** The DAG as the API describes it, for its miniature. */
  dag: components['schemas']['DAGDetails'] | undefined;
  /** True for a name that is a `${...}` reference: it names a DAG only when the step runs. */
  dynamic: boolean;
}

/**
 * The details of a DAG for the detail area of a DAG tool: its description and
 * its inputs with their type, and the DAG itself for its miniature. `known` is
 * false when the server does not know the DAG, true when it does, and
 * undefined while the answer is not there. A name that is a `${...}` reference
 * gives no request. Nothing is requested while `enabled` is false.
 */
export function useDagDetails(name: string, enabled: boolean): DagDetails {
  const remoteNode = useRemoteNode();
  const plain = enabled && PLAIN_NAME.test(name.trim());
  const { data, error } = useQuery(
    '/dags/{fileName}',
    whenEnabled(plain, {
      params: { path: { fileName: name.trim() }, query: { remoteNode } },
    })
  );
  const dynamic = name.trim() !== '' && !PLAIN_NAME.test(name.trim());
  return useMemo(() => {
    if (!plain) {
      return {
        known: undefined,
        description: '',
        inputs: [],
        dag: undefined,
        dynamic,
      };
    }
    if (!data?.dag) {
      return {
        known: error ? false : undefined,
        description: '',
        inputs: [],
        dag: undefined,
        dynamic,
      };
    }
    const inputs = (data.dag.paramDefs ?? []).flatMap((def): DagInputDef[] =>
      def.name
        ? [
            {
              name: def.name,
              type: def.type,
              required: def.required,
              description: def.description ?? '',
              fallback: def.default === undefined ? '' : String(def.default),
              choices: (def.enum ?? []).map(String),
            },
          ]
        : []
    );
    return {
      known: true,
      description: data.dag.description?.trim() ?? '',
      inputs,
      dag: data.dag,
      dynamic,
    };
  }, [plain, dynamic, data?.dag, error]);
}
