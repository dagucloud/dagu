// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useCallback, useMemo, useState } from 'react';
import { components } from '@/api/v1/schema';
import { useConfig } from '@/contexts/ConfigContext';
import dayjs from '@/lib/dayjs';
import DAGRunDetailsModal from '@/features/dag-runs/components/dag-run-details/DAGRunDetailsModal';
import type { KanbanFilters } from '../hooks/useDateKanbanData';
import { ArtifactListModal } from './ArtifactListModal';
import { DateKanbanSection } from './DateKanbanSection';

type DAGRunSummary = components['schemas']['DAGRunSummary'];

const MAX_LOOKBACK_DAYS = 30;

interface Props {
  lookbackDays: number;
  filters: KanbanFilters;
}

/**
 * Renders a bounded list of Kanban day-sections (today back through
 * today-(N-1)) for a saved view. Reuses DateKanbanSection, so SSE and polling
 * apply only to the freshest sections exactly as in the Cockpit tab.
 */
export function LookbackKanbanList({
  lookbackDays,
  filters,
}: Props): React.ReactElement {
  const { tzOffsetInSec } = useConfig();
  const [selectedRun, setSelectedRun] = useState<DAGRunSummary | null>(null);
  const [isDetailsOpen, setIsDetailsOpen] = useState(false);
  const [artifactRun, setArtifactRun] = useState<DAGRunSummary | null>(null);

  const { dates, todayStr } = useMemo(() => {
    const now =
      tzOffsetInSec !== undefined
        ? dayjs().utcOffset(tzOffsetInSec / 60)
        : dayjs();
    const today = now.format('YYYY-MM-DD');
    const count = Math.max(1, Math.min(lookbackDays, MAX_LOOKBACK_DAYS));
    const out: string[] = [];
    for (let i = 0; i < count; i++) {
      out.push(now.subtract(i, 'day').format('YYYY-MM-DD'));
    }
    return { dates: out, todayStr: today };
  }, [lookbackDays, tzOffsetInSec]);

  const handleCardClick = useCallback((run: DAGRunSummary) => {
    setSelectedRun(run);
    setIsDetailsOpen(true);
  }, []);

  const handleArtifactsClick = useCallback((run: DAGRunSummary) => {
    setArtifactRun(run);
  }, []);

  return (
    <>
      <div className="flex flex-col overflow-y-auto flex-1 min-h-0 gap-6 p-1">
        {dates.map((date) => (
          <DateKanbanSection
            key={date}
            date={date}
            todayStr={todayStr}
            filters={filters}
            onCardClick={handleCardClick}
            onArtifactsClick={handleArtifactsClick}
          />
        ))}
      </div>
      <DAGRunDetailsModal
        name={selectedRun?.name ?? ''}
        dagRunId={selectedRun?.dagRunId ?? ''}
        isOpen={isDetailsOpen && !!selectedRun}
        initialTab={selectedRun?.artifactsAvailable ? 'artifacts' : 'status'}
        onClose={() => setIsDetailsOpen(false)}
      />
      <ArtifactListModal
        run={artifactRun}
        isOpen={!!artifactRun}
        onClose={() => setArtifactRun(null)}
      />
    </>
  );
}
