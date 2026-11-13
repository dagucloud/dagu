// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useRef, type ReactElement, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import type { RunLinks } from '../continuous/EventsList';
import { runHistorySource } from '../continuous/sampleSource';
import { SamplesPanel, type PanelRange } from '../continuous/SamplesPanel';
import { useWorkspaceRun } from '../data';
import { useWorkspaceData } from '../data/context';
import { useRetainedRuns } from '../data/useServiceDag';
import { useDock } from './DockContext';
import type { RunRange } from './runTimelineModel';

/**
 * The run history of a service DAG, for a time range of the Activity panel:
 * buckets of the history, not the retained runs. The controls are those of
 * the run timeline; `controlsHost` follows the rule of the timeline.
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
  const { ts } = useI18n();
  const { fileName } = useWorkspaceData('ServiceHistory');
  const run = useWorkspaceRun();
  const dock = useDock();
  const retained = useRetainedRuns(
    true,
    range.preset === 'custom' ? `${range.from}-${range.to}` : range.preset
  );
  const source = useMemo(() => runHistorySource(fileName), [fileName]);
  const panelRange = useMemo<PanelRange>(
    () =>
      range.preset === 'custom'
        ? { from: range.from, to: range.to }
        : { preset: range.preset },
    [range]
  );

  // The links keep one identity while the retained runs do not change.
  const select = useRef(run.select);
  select.current = run.select;
  const links = useMemo<RunLinks>(
    () => ({
      // The list is up to 30 s old: a run that is not newer than the oldest
      // retained run is the only one that can be gone.
      has: (runId, time) =>
        retained.ids.has(runId) ||
        (retained.oldestAt !== null && Date.parse(time) >= retained.oldestAt),
      open: (runId) => select.current(runId, { replace: true }),
    }),
    [retained]
  );

  const notice = (window: { from: number; to: number }) =>
    retained.oldestAt !== null && window.from < retained.oldestAt ? (
      <p
        data-testid="ww-service-history-notice"
        className="text-[11px] text-muted-foreground"
      >
        {ts('The runs before {time} are not kept.', {
          time: formatAbsoluteTime(retained.oldestAt) ?? '',
        })}
      </p>
    ) : null;

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
        active={dock.open}
        range={panelRange}
        links={links}
        notice={notice}
        testId="ww-service-history-panel"
        // The phone view of Activity is a whole screen: the availability
        // takes two columns there, so the chart needs more height.
        className="max-h-[60vh] min-h-0 space-y-2 overflow-auto py-2 md:max-h-[min(16rem,26vh)]"
      />
    </div>
  );
}
