// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useState, useEffect, type ReactNode } from 'react';
import { useQuery } from '@/hooks/api';
import { LicenseContext } from '@/contexts/LicenseContext';
import type { LicenseStatus } from '@/contexts/ConfigContext';
import type { components } from '@/api/v1/schema';
const LICENSE_REFRESH_INTERVAL = 60_000;

type LicenseResponse = components['schemas']['LicenseStatusResponse'];

export function LicenseProvider({
  enabled,
  remoteNode,
  initialLicense,
  children,
}: {
  enabled: boolean;
  remoteNode: string;
  initialLicense: LicenseStatus;
  children: ReactNode;
}) {
  const [bootstrap, setBootstrap] = useState<{
    remoteNode: string;
    license?: LicenseResponse;
  }>(() => ({
    remoteNode,
    license: initialLicense
      ? {
          ...initialLicense,
          graceEndsAt: initialLicense.graceEndsAt ?? '',
          error: initialLicense.error ?? '',
        }
      : undefined,
  }));
  const [now, setNow] = useState(Date.now);
  useEffect(() => {
    if (!enabled) return;
    const timer = window.setInterval(
      () => setNow(Date.now()),
      LICENSE_REFRESH_INTERVAL
    );
    return () => window.clearInterval(timer);
  }, [enabled]);
  // Bootstrap status belongs to the local server, never a persisted remote selection.
  if (bootstrap.remoteNode !== remoteNode) {
    setBootstrap({ remoteNode });
  }
  const { data, error, mutate } = useQuery(
    '/license/status',
    enabled ? { params: { query: { remoteNode } } } : null,
    {
      fallbackData:
        remoteNode === 'local' && bootstrap.remoteNode === remoteNode
          ? bootstrap.license
          : undefined,
      keepPreviousData: false,
      refreshInterval: LICENSE_REFRESH_INTERVAL,
      revalidateOnFocus: true,
      revalidateOnReconnect: true,
      shouldRetryOnError: false,
    }
  );

  // Session resets must not restore an HTML snapshot superseded by live status.
  if (bootstrap.license && data && data !== bootstrap.license) {
    setBootstrap({ remoteNode });
  }

  return (
    <LicenseContext.Provider value={{ license: data, error, mutate, now }}>
      {children}
    </LicenseContext.Provider>
  );
}
