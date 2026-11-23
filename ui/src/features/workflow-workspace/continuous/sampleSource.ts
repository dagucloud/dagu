// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Where the samples, the availability and the events come from: a
 * continuous step of a root run.
 */
export type SampleSource = {
  kind: 'step';
  name: string;
  runId: string;
  step: string;
};

/** A continuous step, in a root run. */
export function stepSource(
  name: string,
  runId: string,
  step: string
): SampleSource {
  return { kind: 'step', name, runId, step };
}
