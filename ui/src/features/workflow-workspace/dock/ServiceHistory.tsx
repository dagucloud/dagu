// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, type ReactElement, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import type { LiveRun } from '../continuous/SamplesChart';
import { runHistorySource } from '../continuous/sampleSource';
import { SamplesPanel, type PanelRange } from '../continuous/SamplesPanel';
import type { ApiDAGRunDetails } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useWorkspaceData } from '../data/context';
import { runSpan } from './runExtents';
import type { RunRange } from './runTimelineModel';

/** The latest run of the DAG while it runs or waits; null otherwise. */
function liveRunOf(latest: ApiDAGRunDetails | undefined): LiveRun | null {
  const span = latest ? runSpan(latest, Date.now()) : null;
  return span?.running ? { id: span.id, startedAt: span.ownStart } : null;
}

/**
 * The run history of a service DAG, for a time range of the Activity panel:
 * buckets of the history, not the retained runs, and the run in progress,
 * which has no sample until it ends. The controls are those of the run
 * timeline; `controlsHost` follows the rule of the timeline.
 */
export function ServiceHistory({
  range,
  controls,
  controlsHost,
}: {
  range: RunRange;
  controls: ReactNode;
  /** undefined: in place. null: hidden. An element: the controls go there. */
  controlsHost: HTMLElement | null | undefined;
}): ReactElement {
  const { fileName, dagData } = useWorkspaceData('ServiceHistory');
  const run = useWorkspaceRun();
  const source = useMemo(() => runHistorySource(fileName), [fileName]);
  const panelRange = useMemo<PanelRange>(
    () =>
      range.preset === 'custom'
        ? { from: range.from, to: range.to }
        : { preset: range.preset },
    [range]
  );
  const latest = dagData.latestDAGRun;
  const liveRun = useMemo(() => liveRunOf(latest), [latest]);

  return (
    <div
      data-testid="ww-service-history"
      className="flex shrink-0 flex-col border-b border-border px-3 pb-1 pt-1"
    >
      {controlsHost === undefined
        ? controls
        : controlsHost && createPortal(controls, controlsHost)}
      <SamplesPanel
        source={source}
        // The dock stops the polls of a closed panel: the panel itself is
        // always live, so closing it reads nothing again.
        active
        range={panelRange}
        liveRun={liveRun}
        onOpenRun={(id) => run.select(id, { replace: true })}
        testId="ww-service-history-panel"
        // The phone view of Activity is a whole screen: the availability
        // takes two columns there, so the chart needs more height.
        className="max-h-[60vh] min-h-0 space-y-2 overflow-auto py-2 md:max-h-[min(16rem,26vh)]"
      />
    </div>
  );
}
