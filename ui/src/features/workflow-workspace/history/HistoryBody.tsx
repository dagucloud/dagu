// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useEffect, useMemo, useRef } from 'react';
import { useClock } from '@/hooks/useClock';
import { useI18n } from '@/i18n/I18nProvider';
import { isContinuousStep } from '@/lib/runDisplayStatus';
import { useWorkspaceEditor } from '../canvas/WorkspaceEditorContext';
import { useVisibleRuns } from '../data/useVisibleRuns';
import { useDockCommands } from '../dock/DockContext';
import { useDockHidden } from '../dock/FreezeWhenHidden';
import { RunTabEmpty } from '../dock/tabs/runTabBodies';
import type { DockTabBodyProps } from '../dock/tabs/types';
import { useWorkspaceUrl } from '../state/workspaceUrl';
import { StepHistory } from './StepHistory';
import { StepsGrid } from './StepsGrid';
import {
  hasRunningStep,
  stepGrid,
  stepHistory,
  withLiveRun,
} from './stepHistoryModel';

/**
 * The History tab: the steps grid in the run view, the history of the step
 * in a step view. The runs are the ones that the run timeline shows (its
 * count or range, its test runs and trigger types), with the live details of
 * the selected run; the tab makes no request of its own. A pick pushes ?run,
 * so that Back returns to the run before, and keeps the step.
 */
export function HistoryBody({ run, dag, step, selectTab }: DockTabBodyProps) {
  const { ts } = useI18n();
  const visible = useVisibleRuns({ enabled: true, pollTestRuns: true });
  const runs = useMemo(
    () => withLiveRun(visible.runs, run.details),
    [visible.runs, run.details]
  );

  // The URL writer changes with the location: the memoized rows get a
  // function that keeps its identity.
  const [, setUrl] = useWorkspaceUrl();
  const setUrlRef = useRef(setUrl);
  setUrlRef.current = setUrl;
  const pick = useCallback((runId: string, name: string) => {
    setUrlRef.current({
      run: runId,
      step: name,
      stream: null,
      item: null,
      body: null,
      sub: null,
    });
  }, []);
  // A run header selects that run alone, and the grid stays: a step that
  // the auto-open chose for that run would show the step view instead.
  const { openStep, dropAutoStep } = useDockCommands();
  const pickColumn = useCallback(
    (runId: string) => {
      dropAutoStep?.();
      setUrlRef.current({ run: runId });
    },
    [dropAutoStep]
  );

  // The canvas lights the node of the row under the pointer, and the light
  // ends with the grid.
  const { handle } = useWorkspaceEditor();
  const handleRef = useRef(handle);
  handleRef.current = handle;
  const spotlight = useCallback(
    (name: string | null) => handleRef.current?.highlightStep(name),
    []
  );
  const gridShown = step === undefined;
  useEffect(() => {
    if (!gridShown) return;
    return () => handleRef.current?.highlightStep(null);
  }, [gridShown]);

  // A running step counts up to now: while one is shown and the dock shows,
  // the entries are built again each second.
  const hidden = useDockHidden();
  const ticking = useMemo(
    () => !hidden && hasRunningStep(runs, step),
    [hidden, runs, step]
  );
  const second = useClock(ticking ? 1000 : null, (now) =>
    ticking ? Math.floor(now / 1000) : null
  );
  const now = second === null ? null : second * 1000;
  const grid = useMemo(
    () => (step === undefined ? stepGrid(dag, runs, now ?? Date.now()) : null),
    [dag, runs, step, now]
  );
  const entries = useMemo(
    () =>
      step === undefined ? null : stepHistory(runs, step, now ?? Date.now()),
    [runs, step, now]
  );

  // A step name opens the history of that step and keeps this tab.
  const showStep = useCallback(
    (name: string) => openStep(name, { keepTab: true }),
    [openStep]
  );

  const pickRun = useCallback(
    (runId: string) => {
      if (step !== undefined) pick(runId, step);
    },
    [pick, step]
  );
  const openLog = useMemo(
    () =>
      selectTab && step !== undefined
        ? (runId: string) => {
            pick(runId, step);
            selectTab('log');
          }
        : undefined,
    [pick, selectTab, step]
  );

  if (runs.length === 0) {
    return (
      <RunTabEmpty>
        {ts(
          visible.isLoading
            ? 'Loading runs...'
            : visible.settings.mode === 'range'
              ? 'No runs in this time range'
              : 'No runs yet'
        )}
      </RunTabEmpty>
    );
  }
  if (grid) {
    return (
      <StepsGrid
        grid={grid}
        runs={runs}
        selectedId={visible.selectedId}
        onPick={pick}
        onPickRun={pickColumn}
        onOpenStep={showStep}
        onSpotlight={spotlight}
      />
    );
  }
  if (!entries || step === undefined) return null;

  const definition = dag.steps?.find((candidate) => candidate.name === step);
  const node = run.nodeByStep.get(step);
  const continuous = isContinuousStep(definition) || node?.continuous === true;
  return (
    <StepHistory
      // The filter and the focus of the chart are about one step.
      key={step}
      step={step}
      entries={entries}
      selectedId={visible.selectedId}
      continuous={continuous}
      subWorkflow={!!(definition?.call || node?.step.call)}
      onPick={pickRun}
      onLog={openLog}
      onContinuous={
        continuous && selectTab ? () => selectTab('continuous') : undefined
      }
    />
  );
}
