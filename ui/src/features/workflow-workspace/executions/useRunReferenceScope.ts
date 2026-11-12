// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useEffect, useState } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useClient } from '@/hooks/api';

type ReferenceScope = components['schemas']['ReferenceScope'];

/**
 * The steps and outputs that the INPUT column of the step view lists, for the
 * definition text of a run. The server validates the text, as the editor does
 * for its saved text. Nothing is read until `enabled`, so a run that no step
 * view opens makes no request. An unreadable answer leaves the scope empty.
 */
export function useRunReferenceScope(
  text: string | null,
  fileName: string,
  enabled: boolean
): ReferenceScope | undefined {
  const client = useClient();
  const remoteNode = useRemoteNode();
  const [result, setResult] = useState<{
    text: string;
    scope: ReferenceScope | undefined;
  } | null>(null);

  useEffect(() => {
    if (!enabled || text === null || result?.text === text) return;
    let cancelled = false;
    const done = (scope: ReferenceScope | undefined) => {
      if (!cancelled) setResult({ text, scope });
    };
    Promise.resolve(
      client.POST('/dags/validate', {
        params: { query: { remoteNode } },
        body: { spec: text, name: fileName },
      })
    )
      .then((response) => done(response?.data?.referenceScope))
      .catch(() => done(undefined));
    return () => {
      cancelled = true;
    };
  }, [client, enabled, fileName, remoteNode, result?.text, text]);

  return result?.text === text ? result.scope : undefined;
}
