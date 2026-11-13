// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';

export type AIEndpointChoice = components['schemas']['AIEndpointChoice'];

/** The DAG that a step belongs to: the use rule of an endpoint depends on it. */
export interface DagIdentity {
  name: string;
  /** The value of the label `workspace` of the DAG. */
  workspace?: string | undefined;
}

/**
 * The endpoints that the DAG may use, from the route of choices. `loaded` is
 * false until the list has arrived, and stays false when the request fails
 * (for example for a role that cannot read it): the form then claims nothing
 * about an endpoint. `cliModelAliases` is undefined before the answer: the
 * form then makes no model check. Nothing is requested while `enabled` is false
 * or the DAG has no name. Every caller shares one request.
 */
export function useEndpointChoices(
  dag: DagIdentity | undefined,
  enabled = true
): {
  choices: AIEndpointChoice[];
  loaded: boolean;
  /** The model names that the CLI claude accepts and the lists do not hold. */
  cliModelAliases: string[] | undefined;
} {
  const remoteNode = useRemoteNode();
  const name = dag?.name.trim() ?? '';
  const { data } = useQuery(
    '/ai/endpoint-choices',
    whenEnabled(enabled && name !== '', {
      params: {
        query: {
          remoteNode,
          dag: name,
          ...(dag?.workspace ? { workspace: dag.workspace } : {}),
        },
      },
    })
  );
  const choices = useMemo(() => data?.choices ?? [], [data?.choices]);
  return {
    choices,
    loaded: data !== undefined,
    cliModelAliases: data?.cliModelAliases,
  };
}
