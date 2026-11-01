// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback } from 'react';
import { type components, type operations } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { useClient, useQuery } from './api';

export type LicenseRemoteAccess = components['schemas']['LicenseRemoteAccess'];
type RemoteAccessChange =
  operations['updateLicenseRemoteAccess']['requestBody']['content']['application/json'];

/**
 * Reads how far calls from Dagu Cloud may go on a server, and whether they
 * reach it, and lets an administrator change it. Nothing is requested while
 * `enabled` is false.
 */
export function useLicenseRemoteAccess(
  remoteNode: string,
  { enabled = true, refreshInterval = 0 } = {}
) {
  const client = useClient();
  const { ts } = useI18n();
  const { data, error, mutate } = useQuery(
    '/license/remote-access',
    enabled ? { params: { query: { remoteNode } } } : null,
    { refreshInterval }
  );

  const update = useCallback(
    async (change: RemoteAccessChange) => {
      const { data: next, error: apiError } = await client.PUT(
        '/license/remote-access',
        { params: { query: { remoteNode } }, body: change }
      );
      if (apiError || !next) {
        throw new Error(
          apiError?.message || ts('Could not change remote access.')
        );
      }
      await mutate(next, { revalidate: false });
    },
    [client, mutate, remoteNode, ts]
  );

  return { remoteAccess: data, error, refresh: mutate, update };
}
