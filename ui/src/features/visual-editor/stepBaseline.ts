// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { DagDocModel, StepKey, StepView } from './contracts';

function everyStep(model: DagDocModel): StepView[] {
  return [...model.steps, ...model.handlers];
}

function sameStep(
  saved: StepView,
  current: StepView,
  model: DagDocModel
): boolean {
  if (current.id !== undefined && saved.id === current.id) return true;
  if (
    current.explicitName !== undefined &&
    saved.explicitName === current.explicitName
  ) {
    return true;
  }
  // Same place with another name: a rename, unless the saved name belongs to
  // another step of the text (a step was inserted before this one).
  if (saved.key !== current.key) return false;
  const taken = everyStep(model).some(
    (other) =>
      other !== current &&
      ((saved.id !== undefined && other.id === saved.id) ||
        (saved.explicitName !== undefined &&
          other.explicitName === saved.explicitName))
  );
  return !taken;
}

/**
 * The text of the step `key` in the saved text: its lines, as written. Null
 * when the saved text has no such step (a new step): every line is new.
 */
export function savedStepText(
  saved: DagDocModel,
  current: DagDocModel,
  key: StepKey
): string | null {
  const step = everyStep(current).find((item) => item.key === key);
  if (!step) return null;
  const savedSteps = everyStep(saved);
  // The id, then the name, of any saved step goes before a match by place.
  const match =
    (step.id !== undefined
      ? savedSteps.find((item) => item.id === step.id)
      : undefined) ??
    (step.explicitName !== undefined
      ? savedSteps.find((item) => item.explicitName === step.explicitName)
      : undefined) ??
    savedSteps.find((item) => sameStep(item, step, current));
  if (!match) return null;
  const lines = saved.text.split('\n');
  return lines
    .slice(match.range.startLine - 1, match.range.endLine)
    .map((line) => line.replace(/\r$/, ''))
    .join('\n');
}
