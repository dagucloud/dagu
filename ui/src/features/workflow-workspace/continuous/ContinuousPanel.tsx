// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { useMemo, type ReactElement } from 'react';
import type { components } from '@/api/v1/schema';
import { presetsFor } from '../rangePresets';
import { stepSource } from './sampleSource';
import { SamplesPanel } from './SamplesPanel';

type Node = components['schemas']['Node'];

const PRESETS = presetsFor('continuous');

export interface ContinuousPanelProps {
  /** The run: the root run that holds the step. */
  name: string;
  runId: string;
  /** The continuous step. */
  node: Node;
  /** The run is active: the short windows and the lists poll. */
  active: boolean;
  /** The start of the run, for the preset "All". */
  startedAt?: string;
}

/**
 * The Continuous panel: how a step that repeats without a limit went. It is
 * the panel of the samples of that step, with its own period buttons.
 */
export function ContinuousPanel({
  name,
  runId,
  node,
  active,
  startedAt,
}: ContinuousPanelProps): ReactElement {
  const source = useMemo(
    () => stepSource(name, runId, node.step.name),
    [name, runId, node.step.name]
  );
  return (
    <SamplesPanel
      source={source}
      active={active}
      startedAt={startedAt ? Date.parse(startedAt) : undefined}
      since={node.startedAt}
      presets={PRESETS}
      step={node.step.name}
    />
  );
}
