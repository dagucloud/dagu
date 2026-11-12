// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo } from 'react';
import type { components } from '@/api/v1/schema';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';

type WebhookDetails = components['schemas']['WebhookDetails'];

/**
 * The webhook of a workflow: undefined while it is unknown or disabled, null
 * when the workflow has none. It comes from the list of webhooks, which
 * answers for a workflow without one too, so a page never requests a
 * webhook that does not exist.
 */
export function useWorkflowWebhook(
  fileName: string,
  enabled: boolean
): { webhook: WebhookDetails | null | undefined; reload(): void } {
  const remoteNode = useRemoteNode();
  const init = useMemo(
    () => whenEnabled(enabled, { params: { query: { remoteNode } } }),
    [enabled, remoteNode]
  );
  const { data, mutate } = useQuery('/webhooks', init);
  const webhooks = enabled ? data?.webhooks : undefined;
  const webhook = Array.isArray(webhooks)
    ? (webhooks.find((item) => item.dagName === fileName) ?? null)
    : undefined;
  const reload = useMemo(() => () => void mutate?.(), [mutate]);
  return { webhook, reload };
}
