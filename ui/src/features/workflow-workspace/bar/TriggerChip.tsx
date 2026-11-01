// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Webhook } from 'lucide-react';
import { useMemo } from 'react';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import { isNotFoundQueryError } from '@/lib/queryErrors';
import { toneClasses } from '@/lib/statusPalette';
import { shouldRetryQueryError } from '@/lib/requestTimeout';
import { cn } from '@/lib/utils';
import type { BarSlotProps } from '../contracts';
import { useWorkspacePermissions } from '../data';
import { useWorkspaceUrl } from '../state/workspaceUrl';

const part =
  'inline-flex h-6 min-w-0 items-center gap-1 whitespace-nowrap rounded-md px-1.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:pointer-events-none disabled:opacity-50';

// In the bar (a size container) the text shows from 1280px; below, the
// icons stay and the text is for the screen reader only.
const chipText = 'sr-only @7xl:not-sr-only @7xl:inline-flex @7xl:items-center';

// A missing webhook is an answer, not a failure to retry. The app-wide rule
// (no retry after 401 or an abort) still applies.
const webhookQueryConfig = {
  shouldRetryOnError: (error: unknown) =>
    !isNotFoundQueryError(error) && shouldRetryQueryError(error),
};

/**
 * The webhook state of the workflow; nothing without a webhook. The schedule
 * shows on the Cron launcher and in the Workflow panel, not in the bar.
 */
export function TriggerChip({ fileName }: BarSlotProps) {
  const { ts } = useI18n();
  const { canManageWebhooks } = useWorkspacePermissions();
  const [, setUrl] = useWorkspaceUrl();
  const remoteNode = useRemoteNode();

  const webhookInit = useMemo(
    () =>
      whenEnabled(canManageWebhooks, {
        params: { path: { fileName }, query: { remoteNode } },
      }),
    [canManageWebhooks, fileName, remoteNode]
  );
  const { data: webhook, error: webhookError } = useQuery(
    '/dags/{fileName}/webhook',
    webhookInit,
    webhookQueryConfig
  );

  // SWR keeps the last data next to a later error: a 404 after a delete wins.
  // A missing webhook leaves the bar empty.
  const webhookState: 'enabled' | 'disabled' | null =
    !canManageWebhooks || isNotFoundQueryError(webhookError) || !webhook
      ? null
      : webhook.enabled
        ? 'enabled'
        : 'disabled';
  if (!webhookState) return null;

  // Below a bar of 1280px the chip shows only its icons: the title keeps
  // the webhook state in reach of the pointer.
  const webhookTitle = `${ts('Webhook')} ${ts(webhookState === 'enabled' ? 'Enabled' : 'Disabled')}\n${ts('Open webhook settings')}`;

  // The canvas opens the launcher panel that the URL names, and clears the name.
  const openWebhook = () => setUrl({ panel: 'webhook' });

  return (
    <div
      data-testid="ww-trigger-chip"
      className="flex min-w-0 items-center gap-0.5"
    >
      <button
        type="button"
        className={part}
        title={webhookTitle}
        onClick={openWebhook}
      >
        <Webhook className="h-3.5 w-3.5 shrink-0" />
        {webhookState === 'enabled' && (
          // The colour is in the dot: green text on the page is under 4.5:1.
          <span
            aria-hidden="true"
            className={cn('size-1.5 rounded-full', toneClasses('success').dot)}
          />
        )}
        <span data-chip-text className={cn(chipText, 'gap-1')}>
          <span>{ts('Webhook')}</span>{' '}
          <span className="text-[10px] text-muted-foreground">
            {ts(webhookState === 'enabled' ? 'Enabled' : 'Disabled')}
          </span>
        </span>
      </button>
    </div>
  );
}
