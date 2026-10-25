// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { RuntimeProfileStatus } from '@/api/v1/schema';
import { useCanManageProfiles } from '@/contexts/AuthContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';

/**
 * Tells whether the Settings tab deserves a place in the tab row. It holds
 * only the default profile, so it is shown when at least one profile is active:
 * "no profile" and that profile are two different runtime choices. The tab stays
 * while it is the open one, so a page that someone reached by its URL keeps its
 * tab. The list of profiles is one request for the whole page, so nothing is
 * read for each DAG; a DAG that keeps a profile that is no longer active does
 * not list the tab.
 *
 * The Webhook tab is not decided here. Its empty state is the only place in
 * the UI where a webhook is created, so it is always listed.
 */
export function useOptionalDAGTabs(activeTab: string): { settings: boolean } {
  const remoteNode = useRemoteNode();
  const canManageProfiles = useCanManageProfiles();

  const { data: profiles } = useQuery(
    '/profiles',
    whenEnabled(canManageProfiles, { params: { query: { remoteNode } } }),
    { shouldRetryOnError: false, revalidateOnFocus: false }
  );

  const activeProfiles = (profiles?.profiles ?? []).filter(
    (profile) => profile.status === RuntimeProfileStatus.active
  ).length;

  return { settings: activeTab === 'settings' || activeProfiles > 0 };
}
