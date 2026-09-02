// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useMemo } from 'react';
import { components, ViewColumn } from '@/api/v1/schema';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import {
  KanbanFilters,
  rangeBounds,
  useRangeKanbanData,
} from '../hooks/useDateKanbanData';
import { KanbanBoard } from './KanbanBoard';
import { I18nText } from '@/i18n/I18nText';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

interface Props {
  fromStr: string;
  toStr: string;
  isLive: boolean;
  filters?: KanbanFilters;
  onCardClick: (run: DAGRunSummary) => void;
  onArtifactsClick: (run: DAGRunSummary) => void;
  visibleColumns?: readonly ViewColumn[];
}

function formatBucketHeader(fromStr: string, toStr: string): string {
  if (fromStr === toStr) {
    return `${fromStr} ${dayjs(fromStr).format('ddd')}`;
  }
  return `${fromStr} to ${toStr}`;
}

/**
 * Renders one Kanban board aggregating all runs in the inclusive day range
 * [fromStr, toStr] (an N-day bucket). The range analog of DateKanbanSection.
 */
export function BucketKanbanSection({
  fromStr,
  toStr,
  isLive,
  filters,
  onCardClick,
  onArtifactsClick,
  visibleColumns,
}: Props): React.ReactElement {
  const { tzOffsetInSec } = useConfig();
  const { fromDate, toDate } = useMemo(
    () => rangeBounds(fromStr, toStr, tzOffsetInSec),
    [fromStr, toStr, tzOffsetInSec]
  );
  const { columns, error, isLoading, isEmpty, retry } = useRangeKanbanData(
    fromDate,
    toDate,
    isLive,
    isLive ? 2000 : 0,
    filters
  );

  return (
    <div>
      <div className="px-1 pb-2">
        <h2 className="text-sm font-semibold text-foreground">
          {formatBucketHeader(fromStr, toStr)}
        </h2>
      </div>
      {isLoading ? (
        <div className="px-1 py-3 text-xs text-muted-foreground">
          <I18nText text={"Loading runs..."} />
        </div>
      ) : error ? (
        <div className="px-1 py-3 flex items-center gap-3 text-xs">
          <span className="text-destructive">
            {error.message || <I18nText text={"Failed to load runs"} />}
          </span>
          <button
            type="button"
            onClick={() => void retry()}
            className="rounded border border-border px-2 py-1 text-muted-foreground hover:text-foreground"
          >
            <I18nText text={"Retry"} />
          </button>
        </div>
      ) : isEmpty ? (
        <div className="px-1 py-3 text-xs text-muted-foreground"><I18nText text={"No runs"} /></div>
      ) : (
        <KanbanBoard
          columns={columns}
          visibleColumns={visibleColumns}
          onCardClick={onCardClick}
          onArtifactsClick={onArtifactsClick}
        />
      )}
    </div>
  );
}
