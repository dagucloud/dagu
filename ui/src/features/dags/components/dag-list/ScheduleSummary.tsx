// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { Webhook } from 'lucide-react';
import { useMemo, type ReactNode } from 'react';
import { Badge } from '@/components/ui/badge';
import RelativeTime from '@/components/ui/relative-time';
import Ticker from '@/components/ui/ticker';
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from '@/components/ui/tooltip';
import { useCanManageWebhooks } from '@/contexts/AuthContext';
import { useConfig } from '@/contexts/ConfigContext';
import { useRemoteNode } from '@/contexts/RemoteNodeContext';
import { useQuery } from '@/hooks/api';
import { whenEnabled } from '@/hooks/queryUtils';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { cn } from '@/lib/utils';
import type { components } from '@/api/v1/schema';
import { parseNextRun } from '../../../../lib/dagSchedule';
import {
  summarizeSchedules,
  type ScheduleEntry,
  type ScheduleSummary,
  type ScheduleWording,
} from '../../lib/scheduleSummary';

type DAGFile = components['schemas']['DAGFile'];

type Translate = ReturnType<typeof useI18n>['ts'];

const REFRESH_MS = 15_000;

/**
 * The webhook marker of one workflow; nothing without a webhook, and nothing
 * when the instance gives no webhook list (no webhook store, a lower role or
 * a remote node). The list is one request that every row shares.
 */
export function WebhookIndicator({ fileName }: { fileName: string }) {
  const { ts } = useI18n();
  const canManage = useCanManageWebhooks();
  const remoteNode = useRemoteNode();
  // The gate reads the local server only: a remote node can refuse the read
  // with a 401, which signs the user out of the local server.
  const enabled = canManage && remoteNode === 'local';
  const init = useMemo(
    () => whenEnabled(enabled, { params: { query: { remoteNode } } }),
    [enabled, remoteNode]
  );
  const { data } = useQuery('/webhooks', init);
  if (!enabled || !Array.isArray(data?.webhooks)) return null;
  const webhook = data.webhooks.find((item) => item.dagName === fileName);
  if (!webhook) return null;

  const label = ts(webhook.enabled ? 'Webhook enabled' : 'Webhook disabled');
  return (
    <span
      role="img"
      aria-label={label}
      title={label}
      data-testid="webhook-indicator"
      className={cn(
        'inline-flex size-5 shrink-0 items-center justify-center',
        webhook.enabled ? 'text-foreground/80' : 'text-muted-foreground/50'
      )}
    >
      <Webhook className="size-3.5" aria-hidden="true" />
    </span>
  );
}

function wordingText(ts: Translate, entry: ScheduleEntry): string {
  const wording: ScheduleWording | null = entry.wording;
  return wording ? ts(wording.key, wording.values) : entry.expression;
}

function ScheduleList({
  summary,
  ts,
}: {
  summary: ScheduleSummary;
  ts: Translate;
}) {
  return (
    <ul
      className="grid grid-cols-[auto_auto_auto] items-baseline gap-x-3 gap-y-1"
      data-testid="schedule-list"
    >
      {summary.entries.map((entry) => {
        const soonest = summary.nextRun !== null && entry === summary.lead;
        const next = summary.suspended
          ? null
          : formatAbsoluteTime(entry.next, { seconds: false });
        return (
          // One row of three cells; the grid aligns the cells of all rows.
          <li key={entry.id} className="contents">
            <span
              className={cn(
                soonest && 'font-medium',
                !entry.wording && 'font-mono'
              )}
            >
              {wordingText(ts, entry)}
              {entry.profile && (
                <span className="ml-1.5 font-normal text-muted-foreground">
                  {ts('profile {name}', { name: entry.profile })}
                </span>
              )}
            </span>
            <span className="font-mono text-muted-foreground">
              {entry.wording && entry.kind === 'cron' ? entry.expression : ''}
            </span>
            <span className="flex items-center justify-end gap-1 tabular-nums text-muted-foreground">
              {soonest && (
                <Badge variant="primary" size="sm">
                  {ts('Next')}
                </Badge>
              )}
              {next ??
                (summary.suspended ? ts('Suspended') : ts('No upcoming run'))}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

function ScheduleText({
  summary,
  ts,
  now,
}: {
  summary: ScheduleSummary;
  ts: Translate;
  now: Date;
}) {
  const { lead, nextRun, suspended } = summary;
  const wording = lead ? wordingText(ts, lead) : '';
  const wordingNode = lead ? (
    <span
      title={lead.expression}
      className={cn(!lead.wording && 'font-mono text-[11px]')}
    >
      {wording}
    </span>
  ) : null;

  let head: ReactNode;
  if (suspended) {
    head = <span>{ts('Suspended')}</span>;
  } else if (nextRun && nextRun.getTime() <= now.getTime()) {
    head = <span>{ts('Due now')}</span>;
  } else if (nextRun) {
    head = (
      <RelativeTime
        timestamp={nextRun.toISOString()}
        absolute={formatAbsoluteTime(nextRun)}
        className="text-foreground/80"
      />
    );
  } else {
    head = <span>{ts('No upcoming run')}</span>;
  }

  return (
    <span
      className={cn('min-w-0 truncate', suspended && 'text-muted-foreground')}
    >
      {suspended ? (
        <>
          {wordingNode}
          {wordingNode && ' · '}
          {head}
        </>
      ) : (
        <>
          {head}
          {wordingNode && ' · '}
          {wordingNode}
        </>
      )}
    </span>
  );
}

function ScheduleBody({ dag }: { dag: DAGFile }) {
  const { ts } = useI18n();
  const { tz } = useConfig();
  const now = new Date();
  const summary = summarizeSchedules(dag.dag.schedule ?? [], now, {
    suspended: dag.suspended,
    serverNextRun: parseNextRun(dag.nextRun),
    timeZone: tz,
  });
  return (
    <span className="flex min-w-0 items-center gap-1">
      <ScheduleText summary={summary} ts={ts} now={now} />
      {summary.extra > 0 && (
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label={ts('Show all {count} schedules', {
                count: summary.entries.length,
              })}
              className="shrink-0 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
              onClick={(event) => event.stopPropagation()}
            >
              <Badge size="sm">+{summary.extra}</Badge>
            </button>
          </TooltipTrigger>
          {/* The list is in a portal, but its clicks reach the row in React. */}
          <TooltipContent
            side="bottom"
            align="start"
            className="w-auto max-w-sm"
            onClick={(event) => event.stopPropagation()}
          >
            <ScheduleList summary={summary} ts={ts} />
          </TooltipContent>
        </Tooltip>
      )}
    </span>
  );
}

/**
 * The schedule of a workflow on one line: when it runs next and which
 * schedule fires then, a "+N" marker with the list of all schedules, and the
 * webhook marker. `emptyLabel` false hides the "No schedule" text.
 */
export function ScheduleLine({
  dag,
  emptyLabel = true,
}: {
  dag: DAGFile;
  emptyLabel?: boolean;
}) {
  const { ts } = useI18n();
  const hasSchedule = (dag.dag.schedule ?? []).length > 0;
  return (
    <div
      data-testid="schedule-line"
      className="flex h-6 min-w-0 items-center gap-1 text-xs font-normal text-muted-foreground"
    >
      {hasSchedule ? (
        <Ticker intervalMs={REFRESH_MS}>
          {() => <ScheduleBody dag={dag} />}
        </Ticker>
      ) : (
        emptyLabel && <span className="truncate">{ts('No schedule')}</span>
      )}
      <WebhookIndicator fileName={dag.fileName} />
    </div>
  );
}
