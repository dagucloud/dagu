import React, { useMemo } from 'react';
import { components } from '@/api/v1/schema';
import dayjs from '@/lib/dayjs';
import {
  KanbanFilters,
  useDateKanbanData,
} from '../hooks/useDateKanbanData';
import { KanbanBoard } from './KanbanBoard';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

interface Props {
  date: string;
  todayStr: string;
  /**
   * Explicit filters for a saved view. When omitted, the Kanban data falls
   * back to the global AppBar workspace selection (Cockpit behavior).
   */
  filters?: KanbanFilters;
  onCardClick: (run: DAGRunSummary) => void;
  onArtifactsClick: (run: DAGRunSummary) => void;
}

function formatDateHeader(date: string): string {
  return `${date} ${dayjs(date).format('ddd')}`;
}

export function DateKanbanSection({
  date,
  todayStr,
  filters,
  onCardClick,
  onArtifactsClick,
}: Props): React.ReactElement {
  const yesterdayStr = useMemo(
    () => dayjs(todayStr).subtract(1, 'day').format('YYYY-MM-DD'),
    [todayStr]
  );
  const isToday = date === todayStr;
  const isLive = isToday || date === yesterdayStr;
  const { columns, error, isLoading, isEmpty, retry } = useDateKanbanData(
    date,
    isToday,
    isLive,
    filters
  );

  return (
    <div>
      <div className="px-1 pb-2">
        <h2 className="text-sm font-semibold text-foreground">
          {formatDateHeader(date)}
        </h2>
      </div>
      {isLoading ? (
        <div className="px-1 py-3 text-xs text-muted-foreground">
          Loading runs...
        </div>
      ) : error ? (
        <div className="px-1 py-3 flex items-center gap-3 text-xs">
          <span className="text-destructive">
            {error.message || 'Failed to load runs'}
          </span>
          <button
            type="button"
            onClick={() => void retry()}
            className="rounded border border-border px-2 py-1 text-muted-foreground hover:text-foreground"
          >
            Retry
          </button>
        </div>
      ) : isEmpty ? (
        <div className="px-1 py-3 text-xs text-muted-foreground">No runs</div>
      ) : (
        <KanbanBoard
          columns={columns}
          onCardClick={onCardClick}
          onArtifactsClick={onArtifactsClick}
        />
      )}
    </div>
  );
}
