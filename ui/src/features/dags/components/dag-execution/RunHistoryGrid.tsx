// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import React, { useCallback, useMemo } from 'react';
import { components } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import HistoryTable from './HistoryTable';

type DAGRunDetails = components['schemas']['DAGRunDetails'];
type DAGGridItem = components['schemas']['DAGGridItem'];

type Props = {
  /** The runs of the DAG, newest first. */
  dagRuns: DAGRunDetails[];
  gridData: DAGGridItem[];
  /** ID of the run that was picked, or null while the latest run is shown. */
  selectedRunId: string | null;
  /** Called with the ID of the picked run, or null for the latest run. */
  onSelect: (dagRunId: string | null) => void;
};

/**
 * The grid of the latest runs of a DAG, one column for each run, oldest on the
 * left. A click on a column picks that run; the newest column goes back to the
 * latest run, which keeps following new runs. Nothing shows until there is
 * more than one run to pick from. While the grid has the focus, which a click
 * on a column gives it, the left and right arrow keys move to the run before and
 * the run after.
 */
export function RunHistoryGrid({
  dagRuns,
  gridData,
  selectedRunId,
  onSelect,
}: Props): React.ReactElement | null {
  const { ts } = useI18n();
  // The grid reads from the oldest run to the newest.
  const oldestFirst = useMemo(() => [...dagRuns].reverse(), [dagRuns]);
  const latestIdx = oldestFirst.length - 1;
  const foundIdx = selectedRunId
    ? oldestFirst.findIndex((run) => run.dagRunId === selectedRunId)
    : -1;
  const idx = foundIdx >= 0 ? foundIdx : latestIdx;

  const pick = useCallback(
    (next: number) => {
      const run = oldestFirst[next];
      if (!run) return;
      onSelect(next === latestIdx ? null : run.dagRunId);
    },
    [latestIdx, oldestFirst, onSelect]
  );

  const shown = oldestFirst.length >= 2;

  if (!shown) {
    return null;
  }

  // The arrow keys act while the grid has the focus, which a click on a column
  // gives it. A key handler on the whole document would also move the run when
  // a dialog elsewhere on the page uses the same keys.
  const onKeyDown = (event: React.KeyboardEvent<HTMLDivElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) {
      return;
    }
    if (event.key === 'ArrowLeft') {
      event.preventDefault();
      pick(idx - 1);
    } else if (event.key === 'ArrowRight') {
      event.preventDefault();
      pick(idx + 1);
    }
  };

  return (
    <div
      tabIndex={0}
      role="group"
      aria-label={ts('Latest runs')}
      onKeyDown={onKeyDown}
      className="rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
    >
      <HistoryTable
        dagRuns={oldestFirst}
        gridData={gridData}
        idx={idx}
        onSelect={pick}
      />
    </div>
  );
}
