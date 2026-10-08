// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  components,
  ForeachItemStatusFilter,
  NodeStatus,
} from '@/api/v1/schema';
import { formatRunDuration } from '@/lib/dagRunTiming';
import { isActiveNodeStatus } from '@/lib/status-utils';
import { cn } from '@/lib/utils';
import { I18nText } from '@/i18n/I18nText';
import { useI18n } from '@/i18n/I18nProvider';
import { ChevronDown, ChevronRight } from 'lucide-react';
import React, { useEffect, useState } from 'react';
import DAGPagination from '../../common/DAGPagination';
import NodeStatusChip from '../../common/NodeStatusChip';
import { ForeachItemDetail, type OpenBodyStepLog } from './ForeachItemDetail';
import {
  FOREACH_ITEMS_PER_PAGE,
  type ForeachStepRef,
  useForeachItems,
} from '../../../hooks/useForeachQueries';

type ForeachItemSummary = components['schemas']['ForeachItemSummary'];
type ForeachItemCounts = components['schemas']['ForeachItemCounts'];

type Filter = 'all' | ForeachItemStatusFilter;

// Chips appear in this order, each only while it has items.
const FILTERS: Array<{
  value: ForeachItemStatusFilter;
  label: string;
  count: (counts: ForeachItemCounts) => number;
}> = [
  {
    value: ForeachItemStatusFilter.failed,
    label: 'Failed',
    count: (c) => c.failed,
  },
  {
    value: ForeachItemStatusFilter.aborted,
    label: 'Aborted',
    count: (c) => c.aborted,
  },
  {
    value: ForeachItemStatusFilter.running,
    label: 'Running',
    count: (c) => c.running,
  },
  {
    value: ForeachItemStatusFilter.succeeded,
    label: 'Succeeded',
    count: (c) => c.succeeded,
  },
  {
    value: ForeachItemStatusFilter.not_started,
    label: 'Not started',
    count: (c) => c.notStarted,
  },
];

type Props = {
  stepRef: ForeachStepRef;
  /** Status of the foreach step, or of the nested body step for nested lists. */
  stepStatus: NodeStatus;
  /** Lists a nested foreach body step's items when set. */
  parent?: string;
  depth?: number;
  onOpenLog?: OpenBodyStepLog;
};

/**
 * ForeachItemsSection shows the items a foreach step ran, failed ones first,
 * and lets each be expanded into its body steps and their logs.
 */
export function ForeachItemsSection({
  stepRef,
  stepStatus,
  parent,
  depth = 0,
  onOpenLog,
}: Props) {
  const { ts } = useI18n();
  const live = isActiveNodeStatus(stepStatus);
  const [filter, setFilter] = useState<Filter>('all');
  const [page, setPage] = useState(1);
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set());
  const { data, isLoading } = useForeachItems(
    stepRef,
    { parent, status: filter === 'all' ? undefined : filter, page },
    live
  );

  // A filter that lost its last item falls back to the full list.
  useEffect(() => {
    if (!data || filter === 'all') {
      return;
    }
    const definition = FILTERS.find((entry) => entry.value === filter);
    if (definition && definition.count(data.counts) === 0) {
      setFilter('all');
      setPage(1);
    }
  }, [data, filter]);

  const toggleExpanded = (item: string) =>
    setExpanded((current) => {
      const next = new Set(current);
      if (!next.delete(item)) {
        next.add(item);
      }
      return next;
    });

  const selectFilter = (next: Filter) => {
    setFilter(next);
    setPage(1);
  };

  if (!data || data.total === 0) {
    return (
      <section className="space-y-1" aria-label={ts('Items')}>
        {depth === 0 && <SectionHeading />}
        <div className="text-xs text-muted-foreground">
          {isLoading && !data ? (
            <I18nText text={'Loading items…'} />
          ) : live ? (
            <I18nText text={'Waiting for items…'} />
          ) : (
            <I18nText text={'No item records for this run.'} />
          )}
        </div>
      </section>
    );
  }

  const { total, counts, items } = data;
  const done = counts.succeeded + counts.failed + counts.aborted;
  const matched =
    filter === 'all'
      ? total
      : (FILTERS.find((entry) => entry.value === filter)?.count(counts) ?? 0);
  const totalPages = Math.max(1, Math.ceil(matched / FOREACH_ITEMS_PER_PAGE));
  const visibleFilters = FILTERS.filter((entry) => entry.count(counts) > 0);

  return (
    <section className="space-y-2" aria-label={ts('Items')}>
      <div className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-1">
        {depth === 0 ? <SectionHeading /> : <span />}
        <span className="tabular-nums text-xs text-muted-foreground">
          <I18nText text={'{done} of {total} done'} values={{ done, total }} />
        </span>
      </div>
      {live && (
        <div
          className="h-1 w-full overflow-hidden rounded-full bg-muted"
          role="progressbar"
          aria-valuemin={0}
          aria-valuemax={total}
          aria-valuenow={done}
        >
          <div
            className="h-full rounded-full bg-primary transition-[width]"
            style={{ width: `${total ? (done / total) * 100 : 0}%` }}
          />
        </div>
      )}
      {visibleFilters.length > 1 && (
        <div
          className="flex flex-wrap gap-1"
          role="group"
          aria-label={ts('Items')}
        >
          <FilterChip
            active={filter === 'all'}
            onClick={() => selectFilter('all')}
            label="All"
            count={total}
          />
          {visibleFilters.map((entry) => (
            <FilterChip
              key={entry.value}
              active={filter === entry.value}
              onClick={() => selectFilter(entry.value)}
              label={entry.label}
              count={entry.count(counts)}
            />
          ))}
        </div>
      )}
      <ul className="divide-y divide-border/60 rounded-md border border-border">
        {items.map((item) => (
          <ItemRow
            key={item.item}
            stepRef={stepRef}
            item={item}
            expanded={expanded.has(item.item)}
            onToggle={() => toggleExpanded(item.item)}
            depth={depth}
            onOpenLog={onOpenLog}
          />
        ))}
        {items.length === 0 && (
          <li className="px-3 py-2 text-xs text-muted-foreground">
            <I18nText text={'No items match this filter.'} />
          </li>
        )}
      </ul>
      {totalPages > 1 && (
        <DAGPagination
          totalPages={totalPages}
          page={Math.min(page, totalPages)}
          pageLimit={FOREACH_ITEMS_PER_PAGE}
          pageChange={setPage}
        />
      )}
    </section>
  );
}

function SectionHeading() {
  return (
    <h3 className="text-xs font-medium uppercase text-muted-foreground">
      <I18nText text={'Items'} />
    </h3>
  );
}

function FilterChip({
  active,
  onClick,
  label,
  count,
}: {
  active: boolean;
  onClick: () => void;
  label: string;
  count: number;
}) {
  return (
    <button
      type="button"
      aria-pressed={active}
      onClick={onClick}
      className={cn(
        'rounded-full border px-2 py-0.5 text-xs transition-colors',
        active
          ? 'border-primary/40 bg-primary/10 text-primary'
          : 'border-border bg-background text-muted-foreground hover:bg-muted'
      )}
    >
      <I18nText text={label} /> <span className="tabular-nums">{count}</span>
    </button>
  );
}

function ItemRow({
  stepRef,
  item,
  expanded,
  onToggle,
  depth,
  onOpenLog,
}: {
  stepRef: ForeachStepRef;
  item: ForeachItemSummary;
  expanded: boolean;
  onToggle: () => void;
  depth: number;
  onOpenLog?: OpenBodyStepLog;
}) {
  const { ts } = useI18n();
  const duration =
    item.startedAt && formatRunDuration(item.startedAt, item.finishedAt);
  const showKey = item.key !== String(item.index);
  const Chevron = expanded ? ChevronDown : ChevronRight;

  return (
    <li>
      <button
        type="button"
        aria-expanded={expanded}
        aria-label={ts('Item {index}', { index: item.index })}
        onClick={onToggle}
        className="flex w-full flex-wrap items-center gap-x-2 gap-y-0.5 px-2 py-1.5 text-left text-sm hover:bg-muted/60"
      >
        <Chevron className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
        <span className="shrink-0 tabular-nums text-muted-foreground">
          #{item.index}
        </span>
        {showKey && (
          <span
            className="min-w-0 flex-1 truncate font-medium"
            title={item.key}
          >
            {item.key}
          </span>
        )}
        {!showKey && <span className="min-w-0 flex-1" />}
        <NodeStatusChip status={item.status} size="sm">
          {item.statusLabel}
        </NodeStatusChip>
        {duration && duration !== '-' && (
          <span className="shrink-0 tabular-nums text-xs text-muted-foreground">
            {duration}
          </span>
        )}
        {item.error && (
          <span
            className="basis-full truncate pl-5 text-xs text-destructive"
            title={item.error}
          >
            {item.error}
          </span>
        )}
      </button>
      {expanded && (
        <div className="border-t border-border/60 bg-muted/20">
          <ForeachItemDetail
            stepRef={stepRef}
            item={item.item}
            itemStatus={item.status}
            depth={depth}
            onOpenLog={onOpenLog}
          />
        </div>
      )}
    </li>
  );
}
