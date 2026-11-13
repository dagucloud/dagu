// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { components } from '@/api/v1/schema';

type StepEvent = components['schemas']['StepEvent'];
type RunHistoryEvent = components['schemas']['RunHistoryEvent'];

/**
 * Where the samples, the availability and the events come from: a
 * continuous step of a root run, or the run history of a service DAG. Both
 * answer with the same series, so one panel shows either.
 */
export type SampleSource =
  | { kind: 'step'; name: string; runId: string; step: string }
  | { kind: 'run-history'; fileName: string };

/** A continuous step, in a root run. */
export function stepSource(
  name: string,
  runId: string,
  step: string
): SampleSource {
  return { kind: 'step', name, runId, step };
}

/** The run history of a service DAG, by its file name. */
export function runHistorySource(fileName: string): SampleSource {
  return { kind: 'run-history', fileName };
}

/** An event of either source. Only a run history has `gap` and a run ID. */
export type SourceEvent = StepEvent | RunHistoryEvent;

/** The run that an event of a run history is about, if the event has one. */
export function eventRunId(event: SourceEvent): string | undefined {
  return 'runId' in event ? event.runId : undefined;
}

/** What one sample counts: an execution of a step, or a run of a service DAG. */
export type SampleUnit = 'executions' | 'runs';

export function unitOf(source: SampleSource): SampleUnit {
  return source.kind === 'step' ? 'executions' : 'runs';
}
