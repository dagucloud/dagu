// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { ChevronDown, ChevronRight, GitBranch, Layers } from 'lucide-react';
import { useMemo, useState, type MouseEvent } from 'react';
import { StatusLabel, type components } from '@/api/v1/schema';
import RelativeTime from '@/components/ui/relative-time';
import { useI18n } from '@/i18n/I18nProvider';
import { statusLabelText } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import type { ApiDAGRunDetails, ApiNode } from '../contracts';
import { useSubRuns } from '../data/useSubRuns';
import { useWorkspaceRun } from '../data/useWorkspaceRun';
import { RunStatusDot } from '../executions/RunStatusDot';
import {
  allSubRunsOf,
  renderParallelPicker,
  subDagNameOf,
  useSubRunOpener,
} from './useStepRunActions';

type SubRunDetail = components['schemas']['SubDAGRunDetail'];
type StatusFilter = 'all' | StatusLabel;

// Chips appear in this order, each only while it has runs.
const STATUS_ORDER: StatusLabel[] = [
  StatusLabel.running,
  StatusLabel.queued,
  StatusLabel.succeeded,
  StatusLabel.partially_succeeded,
  StatusLabel.failed,
  StatusLabel.aborted,
  StatusLabel.not_started,
];

export interface StepSubRunsProps {
  node: ApiNode;
  /** The run that holds the step: the selected run or one of its sub runs. */
  holder: ApiDAGRunDetails;
  /** Set when the holder is a sub run of the selected run. */
  parentSubDAGRunId?: string;
}

function isNewTab(e: MouseEvent): boolean {
  return e.metaKey || e.ctrlKey;
}

/**
 * The child runs of one step. One child opens on a click; several list with
 * status filters and open one by one or through the parallel picker.
 */
export function StepSubRuns({
  node,
  holder,
  parentSubDAGRunId,
}: StepSubRunsProps) {
  const { ts } = useI18n();
  const run = useWorkspaceRun();
  const open = useSubRunOpener();
  const children = allSubRunsOf(node);
  const several = children.length > 1;
  const { subRuns } = useSubRuns(several ? run.ref : null, parentSubDAGRunId);
  const [expanded, setExpanded] = useState(false);
  const [filter, setFilter] = useState<StatusFilter>('all');
  const [picker, setPicker] = useState<{
    node: ApiNode;
    holder: ApiDAGRunDetails;
  } | null>(null);
  const subDagName = subDagNameOf(node);

  // The statuses come from the sub-run list; only the children of this step count.
  const detailById = useMemo(() => {
    const ids = new Set(children.map((c) => c.dagRunId));
    const map = new Map<string, SubRunDetail>();
    for (const detail of subRuns) {
      if (ids.has(detail.dagRunId)) map.set(detail.dagRunId, detail);
    }
    return map;
  }, [children, subRuns]);

  const rows = useMemo(() => {
    const indexed = children.map((child, index) => ({
      child,
      index,
      detail: detailById.get(child.dagRunId),
    }));
    if (detailById.size === 0) return indexed;
    // Newest first, as SubDAGRunsList, once timings are known. A child that
    // is not listed yet, or has no start time, comes last in its own order.
    const startOf = (row: (typeof indexed)[number]) => {
      const t = Date.parse(row.detail?.startedAt ?? '');
      return Number.isFinite(t) ? t : -Infinity;
    };
    return [...indexed].sort(
      (a, b) => startOf(b) - startOf(a) || a.index - b.index
    );
  }, [children, detailById]);

  const filters = useMemo(() => {
    const counts = new Map<StatusLabel, number>();
    for (const row of rows) {
      const label = row.detail?.statusLabel;
      if (label) counts.set(label, (counts.get(label) ?? 0) + 1);
    }
    const list: { value: StatusFilter; label: string; count: number }[] = [
      { value: 'all', label: 'All', count: rows.length },
    ];
    for (const status of STATUS_ORDER) {
      const count = counts.get(status);
      if (count) {
        list.push({ value: status, label: statusLabelText(status), count });
      }
    }
    return list;
  }, [rows]);

  const shown =
    filter === 'all'
      ? rows
      : rows.filter((row) => row.detail?.statusLabel === filter);

  if (children.length === 0) return null;

  const linkClass =
    'text-left text-xs font-medium text-primary hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring rounded-sm';
  const linkTitle = ts(
    'Click to view sub DAG run (Cmd/Ctrl+Click to open in new tab)'
  );

  if (!several) {
    const child = children[0]!;
    return (
      <ul className="space-y-1" data-testid="ww-step-subruns">
        <li className="space-y-1">
          <button
            type="button"
            className={cn(linkClass, 'inline-flex items-center gap-1')}
            title={linkTitle}
            onClick={(e) =>
              open({ node, index: 0, holder, newTab: isNewTab(e) })
            }
          >
            <GitBranch className="h-3.5 w-3.5" aria-hidden="true" />
            {ts('View Sub DAG Run:')} {child.dagName || subDagName}
          </button>
          {child.params && (
            <div className="text-xs text-muted-foreground">
              {ts('Parameters:')}{' '}
              <span className="font-mono">{child.params}</span>
            </div>
          )}
        </li>
      </ul>
    );
  }

  return (
    <div
      className="space-y-1 text-xs text-muted-foreground"
      data-testid="ww-step-subruns"
    >
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          aria-expanded={expanded}
          className={cn(linkClass, 'inline-flex items-center gap-1')}
          onClick={() => setExpanded(!expanded)}
        >
          {expanded ? (
            <ChevronDown className="h-3 w-3" aria-hidden="true" />
          ) : (
            <ChevronRight className="h-3 w-3" aria-hidden="true" />
          )}
          {shown.length === children.length
            ? ts('Multiple executions: {count} sub DAG runs', {
                count: children.length,
              })
            : ts('Multiple executions: {visible} of {total} sub DAG runs', {
                visible: shown.length,
                total: children.length,
              })}
        </button>
        <button
          type="button"
          className="inline-flex items-center gap-1 rounded border border-border bg-background px-1.5 py-0.5 text-xs font-medium hover:bg-muted"
          onClick={() => setPicker({ node, holder })}
        >
          <Layers className="h-3 w-3" aria-hidden="true" />
          {ts('Choose a sub DAG run')}
        </button>
      </div>

      {expanded && filters.length > 2 && (
        <div className="ml-4 flex flex-wrap items-center gap-1">
          {filters.map((f) => {
            const active = filter === f.value;
            return (
              <button
                key={f.value}
                type="button"
                aria-pressed={active}
                onClick={() => setFilter(f.value)}
                className={cn(
                  'rounded px-1.5 py-0.5 text-xs transition-colors',
                  active
                    ? 'bg-primary/15 text-primary'
                    : 'bg-muted text-muted-foreground hover:bg-accent'
                )}
              >
                {ts(f.label)}
                <span
                  className={cn(
                    'ml-1',
                    active ? 'text-primary' : 'text-muted-foreground'
                  )}
                >
                  {f.count}
                </span>
              </button>
            );
          })}
        </div>
      )}

      {expanded && (
        <div className="ml-4 space-y-1 border-l border-border pl-3">
          {shown.length === 0 ? (
            <div className="py-2 italic">
              {ts('No sub DAG runs match the selected filter')}
            </div>
          ) : (
            <ul className="space-y-1">
              {shown.map(({ child, index, detail }) => (
                <li key={child.dagRunId} className="py-1">
                  <div className="flex items-center gap-2">
                    <button
                      type="button"
                      className={linkClass}
                      title={linkTitle}
                      onClick={(e) =>
                        open({ node, index, holder, newTab: isNewTab(e) })
                      }
                    >
                      #{String(index + 1).padStart(2, '0')}:{' '}
                      {child.dagName || subDagName}
                    </button>
                    {detail?.startedAt && (
                      <RelativeTime
                        className="block text-xs text-muted-foreground"
                        timestamp={detail.startedAt}
                      />
                    )}
                    {detail && <RunStatusDot status={detail.status} />}
                  </div>
                  {child.params && (
                    <div className="mt-1 font-mono">{child.params}</div>
                  )}
                </li>
              ))}
            </ul>
          )}
        </div>
      )}
      {renderParallelPicker(picker, () => setPicker(null), open)}
    </div>
  );
}
