// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import type { DagIdentity } from './useEndpointChoices';

export type AIMCPServerChoice = components['schemas']['AIMCPServerChoice'];

/**
 * The MCP server entries that the DAG may use, from the route of choices. `loaded`
 * is false until the list has arrived, and stays false when the request fails
 * (for example for a role that cannot read it): the form then claims nothing
 * about an entry. Nothing is requested while `enabled` is false or the DAG has
 * no name. Every caller shares one request.
 */
export function useMcpServerChoices(
  dag: DagIdentity | undefined,
  enabled = true
): { choices: AIMCPServerChoice[]; loaded: boolean } {
  const remoteNode = useRemoteNode();
  const name = dag?.name.trim() ?? '';
  const { data } = useQuery(
    '/ai/mcp-server-choices',
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
  return { choices, loaded: data !== undefined };
}
