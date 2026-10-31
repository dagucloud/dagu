// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo } from 'react';
import { components, RuntimeProfileStatus } from '../../../api/v1/schema';
import { useRemoteNode } from '../../../contexts/RemoteNodeContext';
import { useQuery } from '../../../hooks/api';
import { whenEnabled } from '../../../hooks/queryUtils';

export type RuntimeProfile = components['schemas']['RuntimeProfileResponse'];

export interface UseProfilesOptions {
  /** The request is sent only when true. */
  enabled: boolean;
  /** Overrides the remote node of the context. */
  remoteNode?: string;
}

export interface UseProfilesResult {
  profiles: RuntimeProfile[];
  activeProfiles: RuntimeProfile[];
  isLoading: boolean;
  error: unknown;
  retry(): void;
}

const NO_PROFILES: RuntimeProfile[] = [];

/**
 * Reads GET /profiles. All callers with the same remote node share one SWR
 * key, so a page that shows several profile pickers sends one request.
 */
export function useProfiles({
  enabled,
  remoteNode: remoteNodeOverride,
}: UseProfilesOptions): UseProfilesResult {
  const remoteNode = useRemoteNode(remoteNodeOverride);
  const init = useMemo(
    () => whenEnabled(enabled, { params: { query: { remoteNode } } }),
    [enabled, remoteNode]
  );
  const { data, error, isLoading, mutate } = useQuery('/profiles', init);
  const profiles = data?.profiles ?? NO_PROFILES;
  const activeProfiles = useMemo(
    () =>
      profiles.filter(
        (profile) => profile.status === RuntimeProfileStatus.active
      ),
    [profiles]
  );
  const retry = useCallback(() => {
    void mutate?.();
  }, [mutate]);
  return {
    profiles,
    activeProfiles,
    isLoading: Boolean(isLoading),
    error,
    retry,
  };
}
