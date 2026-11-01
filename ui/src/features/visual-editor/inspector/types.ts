// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { DagEditOp, StepView, ValidationIssue } from '../contracts';

/** Path of a value inside one step, as `setStepField` takes it. */
export type FieldPath = (string | number)[];

/** One changed leaf of a form: `value` undefined removes the key. */
export interface FieldChange {
  path: FieldPath;
  value: unknown;
}

/**
 * Issues of one step. `byField` keys are dot paths relative to the step
 * (`"with.url"`, `"depends"`, `"run"`); `general` holds the issues that
 * name the step but no field of it.
 */
export interface StepIssues {
  byField: Record<string, ValidationIssue[]>;
  general: ValidationIssue[];
}

/** Props that every inspector section receives. */
export interface SectionProps {
  step: StepView;
  readOnly: boolean;
  onEdit(op: DagEditOp): void;
  fieldIssues: Record<string, ValidationIssue[]>;
  /** The name that the server gives the step, for a step with no `name:`. */
  derivedName?: string;
}
