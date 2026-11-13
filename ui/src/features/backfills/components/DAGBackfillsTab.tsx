// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Link } from 'react-router-dom';
import { LoadErrorState, PageLoading } from '@/components/PageStates';
import { RefreshButton } from '@/components/ui/refresh-button';
import RelativeTime from '@/components/ui/relative-time';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { useBackfillSSE } from '@/hooks/useBackfillSSE';
import { usePageVisible } from '@/hooks/usePageVisible';
import { useI18n } from '@/i18n/I18nProvider';
import { type Backfill, isOpenBackfill, slotsDone } from '../lib/backfillState';
import { BackfillProgress } from './BackfillProgress';
import { BackfillStatusChip } from './BackfillStatusChip';

const READ_ONCE = {
  revalidateOnFocus: false,
  revalidateIfStale: false,
  refreshInterval: 0,
} as const;

/**
 * The backfills of a DAG, newest first. The list is read when the tab opens
 * and on refresh; an open backfill in it follows its own live feed.
 */
export function DAGBackfillsTab({ dagName }: { dagName: string }) {
  const { ts } = useI18n();
  const remoteNode = useRemoteNode();
  const { data, error, mutate } = useQuery(
    '/backfills',
    { params: { query: { remoteNode, dag: dagName } } },
    READ_ONCE
  );

  if (!data) {
    return error ? (
      <LoadErrorState
        title={ts('Failed to load the backfills')}
        error={error}
        onRetry={() => void mutate()}
      />
    ) : (
      <PageLoading />
    );
  }
  const backfills = [...data.backfills].reverse();
  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground">
          {ts('Backfills')}
        </span>
        <RefreshButton
          onRefresh={async () => {
            await mutate();
          }}
        />
      </div>
      {backfills.length === 0 ? (
        <div className="rounded-md border border-dashed px-3 py-4 text-sm text-muted-foreground">
          <p className="font-medium text-foreground">
            {ts('No backfills yet.')}
          </p>
          <p>
            {ts(
              'A backfill runs the past slots of this schedule. Start one with dagu backfill start.'
            )}
          </p>
        </div>
      ) : (
        <ul className="flex flex-col divide-y rounded-md border">
          {backfills.map((backfill) => (
            <BackfillRow
              key={backfill.id}
              backfill={backfill}
              remoteNode={remoteNode}
            />
          ))}
        </ul>
      )}
    </div>
  );
}

function BackfillRow({
  backfill: listed,
  remoteNode,
}: {
  backfill: Backfill;
  remoteNode: string;
}) {
  const visible = usePageVisible();
  const sse = useBackfillSSE(
    listed.id,
    visible && isOpenBackfill(listed),
    remoteNode
  );
  const backfill = sse.data ?? listed;
  return (
    <li>
      <Link
        to={`/backfills/${encodeURIComponent(backfill.id)}`}
        className="flex flex-col gap-1.5 px-3 py-2 outline-none transition-colors hover:bg-muted/50 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
      >
        <div className="flex items-center justify-between gap-2">
          <div className="flex min-w-0 items-center gap-2">
            <BackfillStatusChip status={backfill.status} size="xs" />
            <span className="font-mono text-xs">{backfill.id}</span>
          </div>
          <span className="shrink-0 text-xs tabular-nums text-muted-foreground">
            {slotsDone(backfill)}/{backfill.slotCount}
          </span>
        </div>
        <BackfillProgress backfill={backfill} compact />
        <span className="text-xs text-muted-foreground">
          <RelativeTime timestamp={backfill.submittedAt} />
          {backfill.actor && ` · ${backfill.actor}`}
        </span>
      </Link>
    </li>
  );
}
