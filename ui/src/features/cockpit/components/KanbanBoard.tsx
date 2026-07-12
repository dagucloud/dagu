import React from 'react';
import { components, ViewColumn } from '@/api/v1/schema';
import { useIsMobile } from '@/hooks/useIsMobile';
import {
  normalizeViewColumns,
  VIEW_COLUMN_LABELS,
} from '@/features/views/viewColumns';
import { KanbanColumn } from './KanbanColumn';
import { MobileKanbanBoard } from './MobileKanbanBoard';
import type { KanbanColumns } from '../hooks/useDateKanbanData';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

interface Props {
  columns: KanbanColumns;
  onCardClick: (run: DAGRunSummary) => void;
  onArtifactsClick: (run: DAGRunSummary) => void;
  visibleColumns?: readonly ViewColumn[];
}

export function KanbanBoard({
  columns,
  onCardClick,
  onArtifactsClick,
  visibleColumns,
}: Props): React.ReactElement {
  const isMobile = useIsMobile();
  const columnOrder = normalizeViewColumns(visibleColumns);

  if (isMobile) {
    return (
      <MobileKanbanBoard
        columns={columns}
        visibleColumns={columnOrder}
        onCardClick={onCardClick}
        onArtifactsClick={onArtifactsClick}
      />
    );
  }

  return (
    <div className="flex gap-3 min-h-0 overflow-x-auto p-1 max-h-[50vh]">
      {columnOrder.map((column) => (
        <KanbanColumn
          key={column}
          title={VIEW_COLUMN_LABELS[column]}
          column={columns[column]}
          onCardClick={onCardClick}
          onArtifactsClick={onArtifactsClick}
        />
      ))}
    </div>
  );
}
