// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, useRef, type ReactElement, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { RestartPolicy, Status } from '@/api/v1/schema';
import { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import type { RunLinks } from '../continuous/EventsList';
import type { LiveRun } from '../continuous/SamplesChart';
import { runHistorySource } from '../continuous/sampleSource';
import { SamplesPanel, type PanelRange } from '../continuous/SamplesPanel';
import type { ApiDAGRunDetails } from '../contracts';
import { useWorkspaceRun } from '../data';
import { useWorkspaceData } from '../data/context';
import { useRetainedRuns } from '../data/useServiceDag';
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
  const { ts } = useI18n();
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
  // The history of a DAG that gets no new run cannot change: a suspended
  // one, and an unless-stopped one whose latest run was stopped. Its panel
  // and its retained runs do not poll. A run that starts after all makes
  // them poll again.
  const restarts =
    liveRun !== null ||
    !(
      dagData.suspended ||
      (dagData.dag?.restartPolicy === RestartPolicy.unless_stopped &&
        latest?.status === Status.Aborted)
    );
  const retained = useRetainedRuns(
    true,
    range.preset === 'custom' ? `${range.from}-${range.to}` : range.preset,
    restarts
  );

  // The links keep one identity while the retained runs do not change.
  const select = useRef(run.select);
  select.current = run.select;
  const links = useMemo<RunLinks>(
    () => ({
      // The last read is up to 30 s old. A run that started after its oldest
      // run links: it was retained at that read or started since. A count
      // retention may drop the oldest of them meanwhile; the next read shows
      // those as text.
      has: (runId, time) =>
        retained.ids.has(runId) ||
        (retained.oldestListedAt !== null &&
          Date.parse(time) >= retained.oldestListedAt),
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
        // The dock stops the polls of a closed panel by itself.
        active={restarts}
        range={panelRange}
        links={links}
        notice={notice}
        liveRun={liveRun}
        onOpenRun={links.open}
        testId="ww-service-history-panel"
        // The phone view of Activity is a whole screen: the availability
        // takes two columns there, so the chart needs more height.
        className="max-h-[60vh] min-h-0 space-y-2 overflow-auto py-2 md:max-h-[min(16rem,26vh)]"
      />
    </div>
  );
}
