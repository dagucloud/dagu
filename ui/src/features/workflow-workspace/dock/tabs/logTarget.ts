// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { ApiDAGRunDetails } from '../../contracts';
import type { WorkspaceUrlState } from '../../state/workspaceUrl';
import type { StepRunTarget } from '../../step/StepRunView';

/** The DAG name of a sub run of the selected run, or '' when no step lists it. */
function subRunDagName(details: ApiDAGRunDetails | null, id: string): string {
  for (const node of details?.nodes ?? []) {
    const child = [
      ...(node.subRuns ?? []),
      ...(node.subRunsRepeated ?? []),
    ].find((sub) => sub.dagRunId === id);
    if (child) return child.dagName || node.step.call || node.step.name;
  }
  return '';
}

/**
 * The step log that ?step, ?stream, ?item, ?body and ?sub name, or null
 * without ?step. A foreach target needs both ?item and ?body.
 */
export function logTargetFromUrl(
  url: Pick<WorkspaceUrlState, 'step' | 'stream' | 'item' | 'body' | 'sub'>,
  details: ApiDAGRunDetails | null
): StepRunTarget | null {
  if (!url.step) return null;
  const target: StepRunTarget = { step: url.step };
  if (url.stream) target.stream = url.stream;
  if (url.item !== null && url.body) {
    target.foreach = { item: url.item, bodyStep: url.body };
  }
  if (url.sub) {
    target.subRun = {
      dagName: subRunDagName(details, url.sub),
      subDAGRunId: url.sub,
    };
  }
  return target;
}
