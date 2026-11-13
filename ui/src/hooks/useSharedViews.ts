// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useCallback, useMemo } from 'react';
import { ViewSpecType } from '@/api/v1/schema';
import { useViews, type View, type ViewSpec } from '@/hooks/useViews';

/** The two modes of the Workflows page, which share their saved views. */
export type SharedViewMode = 'workflow' | 'run';

const RUN_FIELDS = [
  'dagRunId',
  'runStatus',
  'dateMode',
  'datePreset',
  'specificPeriod',
  'specificValue',
  'fromDate',
  'toDate',
] as const;

const WORKFLOW_FIELDS = ['activeOnly', 'sortField', 'sortOrder'] as const;

/**
 * The saved views of the Workflows page, the same in its two modes. A view
 * holds the filters of both: name and labels are common, the workflow mode
 * adds its sort and "active only", the run mode adds the run ID, the status and
 * the dates. A mode that saves a view keeps the fields of the other mode as
 * they were, so the view still fits that mode.
 *
 * The views are stored as workflow views or run views (the views that were
 * saved before the two modes shared them are of both types); the list merges
 * both, and a view keeps its type when it changes.
 */
export function useSharedViews(mode: SharedViewMode) {
  const workflow = useViews(ViewSpecType.workflow);
  const run = useViews(ViewSpecType.run);

  const views = useMemo<View[]>(
    () =>
      [...workflow.views, ...run.views].sort((a, b) =>
        a.name.localeCompare(b.name)
      ),
    [workflow.views, run.views]
  );

  const createView = useCallback(
    (spec: ViewSpec): Promise<View> =>
      workflow.createView({ ...spec, type: ViewSpecType.workflow }),
    [workflow]
  );

  const updateView = useCallback(
    (id: string, spec: ViewSpec): Promise<View> => {
      const existing = views.find((view) => view.id === id);
      if (!existing) {
        return workflow.updateView(id, spec);
      }
      // The fields of the other mode stay as they are.
      const kept: Partial<ViewSpec> = {};
      const keep = mode === 'workflow' ? RUN_FIELDS : WORKFLOW_FIELDS;
      for (const field of keep) {
        (kept as Record<string, unknown>)[field] = existing[field];
      }
      const next = { ...spec, ...kept, type: existing.type as ViewSpecType };
      return existing.type === ViewSpecType.run
        ? run.updateView(id, next)
        : workflow.updateView(id, next);
    },
    [mode, run, views, workflow]
  );

  const deleteView = useCallback(
    (id: string): Promise<void> => workflow.deleteView(id),
    [workflow]
  );

  return {
    views,
    isLoading: workflow.isLoading || run.isLoading,
    error: workflow.error ?? run.error,
    createView,
    updateView,
    deleteView,
  };
}
