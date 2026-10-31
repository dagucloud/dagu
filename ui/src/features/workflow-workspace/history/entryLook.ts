// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { CSSProperties } from 'react';
import { NodeStatus } from '@/api/v1/schema';
import type { useI18n } from '@/i18n/I18nProvider';
import { formatAbsoluteTime } from '@/lib/dagRunTiming';
import { HATCH_PATTERN } from '@/lib/hatchPattern';
import { shortRunId } from '@/lib/shortRunId';
import { nodeStatusText } from '@/lib/status-utils';
import { statusTone, toneClasses } from '@/lib/statusPalette';
import { CACHED_WASH } from '../dock/RunTimeline';
import { elapsedMsText, type BarTone } from '../dock/runTimelineModel';
import { isFailedEntry, type StepRunEntry } from './stepHistoryModel';

type Translate = ReturnType<typeof useI18n>['ts'];

/** The colour of the mark of an entry; only a step that ran or was reused has one. */
export function entryTone(entry: StepRunEntry): BarTone {
  if (entry.state === 'reused') {
    return entry.reuseOutcome === 'failed' ? 'error' : 'success';
  }
  if (entry.state !== 'ran' || entry.status === null) return 'neutral';
  if (entry.status === NodeStatus.PartialSuccess) return 'partial';
  return statusTone(entry.status);
}

/** The English text of the state of an entry, for ts(). */
export function entryStateText(entry: StepRunEntry): string {
  switch (entry.state) {
    case 'ran':
      return entry.status === null ? 'Unknown' : nodeStatusText(entry.status);
    case 'reused':
      return entry.reuseOutcome === 'failed'
        ? 'Reused, failed'
        : 'Reused, succeeded';
    case 'skipped':
      return 'Skipped';
    case 'notStarted':
      return 'Not started';
    case 'absent':
      return 'Not in this run';
  }
}

/** The text of the time that an entry took, or null when unknown. */
export function entryDurationText(entry: StepRunEntry): string | null {
  return entry.durationMs === null ? null : elapsedMsText(entry.durationMs);
}

/**
 * The accessible name of an entry, which is also its tooltip: the state,
 * the run, its time and the duration. `step` leads it where the mark does
 * not sit next to the step name.
 */
export function entryLabel(
  ts: Translate,
  entry: StepRunEntry,
  step?: string
): string {
  const state = entry.reusedFrom
    ? ts(
        entry.reuseOutcome === 'failed'
          ? 'Reused from run {id}: failed'
          : 'Reused from run {id}: succeeded',
        { id: shortRunId(entry.reusedFrom) }
      )
    : ts(entryStateText(entry));
  return [
    step,
    ts('Run {id}', { id: shortRunId(entry.runId) }),
    state,
    entry.testRun ? ts('Test run') : null,
    formatAbsoluteTime(entry.at),
    entryDurationText(entry),
  ]
    .filter(Boolean)
    .join(', ');
}

// A cross and a slash in the page colour over the fill: a failed and an
// aborted step differ from a success by their shape, not only the colour.
const CROSS =
  'linear-gradient(45deg, transparent 42%, var(--background) 42% 58%, transparent 58%), linear-gradient(-45deg, transparent 42%, var(--background) 42% 58%, transparent 58%)';
const SLASH =
  'linear-gradient(-45deg, transparent 42%, var(--background) 42% 58%, transparent 58%)';
const REUSED_LAYERS = `${HATCH_PATTERN}, linear-gradient(${CACHED_WASH}, ${CACHED_WASH})`;

/**
 * The fill of the mark of a step that ran or was reused: the tone of the
 * run timeline, the hatch of a test run, the hatch and the wash of a reused
 * step. `marked` adds the cross of a failure and the slash of an abort.
 * `split` is the direction of the two halves of a partial success.
 */
export function entryFill(
  entry: StepRunEntry,
  { marked, split }: { marked: boolean; split: 'to top' | 'to right' }
): { className?: string; style: CSSProperties } {
  const tone = entryTone(entry);
  const layers: string[] = [];
  if (marked && isFailedEntry(entry)) layers.push(CROSS);
  if (marked && entry.status === NodeStatus.Aborted) layers.push(SLASH);
  if (entry.state === 'reused') layers.push(REUSED_LAYERS);
  else if (entry.testRun) layers.push(HATCH_PATTERN);
  if (tone === 'partial') {
    layers.push(
      `linear-gradient(${split}, var(--status-success) 50%, var(--status-warning) 50%)`
    );
  }
  return {
    className: tone === 'partial' ? undefined : toneClasses(tone).dot,
    style: layers.length > 0 ? { backgroundImage: layers.join(', ') } : {},
  };
}
