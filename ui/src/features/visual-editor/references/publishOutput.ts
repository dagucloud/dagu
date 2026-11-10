// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  DagDocModel,
  EditResult,
  PublishOnPick,
  ReferenceCandidate,
  ReferenceScope,
  StepView,
} from '../contracts';
import { minimalEdit } from '../doc/format';
import { applyEdit } from '../doc/index';

type ScopeStep = ReferenceScope['steps'][number];

const STDOUT = 'stdout';

// The fields through which a step already says what it publishes.
const PUBLISHING_FIELDS = ['output', 'outputs', 'output_schema', STDOUT];

// The name syntax of a step in a ${steps.<id>...} reference.
const STEP_ID = /^[A-Za-z][A-Za-z0-9_]*$/;

// A scope comes back from a validation, which can trail the text: the entry at
// the path of `step` may be another step by now.
function isSameStep(step: ScopeStep, view: StepView): boolean {
  if (step.id !== view.id) return false;
  return view.explicitName === undefined || view.explicitName === step.name;
}

/**
 * The output that a pick would make `step` publish: what a plain command step
 * prints, which is no output until the step says so (spec 012). A build DAG has
 * none, because a scalar output makes a step lose its reuse (spec 033). Empty
 * for any other step.
 *
 * This token is the one reference that the client builds, as the server has no
 * such output until the edit is made.
 */
export function publishCandidates(
  step: ScopeStep,
  distance: number,
  doc: DagDocModel | undefined
): ReferenceCandidate[] {
  if (!doc?.ok || doc.dagType === 'build' || step.outputs.length > 0) return [];
  const view = doc.steps.find((entry) => entry.key === step.stepPath);
  if (!view || view.kind !== 'run' || view.broken !== undefined) return [];
  if (!isSameStep(step, view)) return [];
  if (PUBLISHING_FIELDS.some((field) => view.value[field] !== undefined)) {
    return [];
  }
  const id = view.id ?? (STEP_ID.test(step.name) ? step.name : null);
  if (!id) return [];
  return [
    {
      token: `\${steps.${id}.outputs.${STDOUT}}`,
      label: `${id}.${STDOUT}`,
      group: 'steps',
      stepName: step.name,
      distance,
      known: 'runtime',
      publish: { step: view.key, ...(view.id ? {} : { id }) },
    },
  ];
}

/** The edit that makes a step publish its output, as one change to `text`. */
export function publishEdit(text: string, publish: PublishOnPick): EditResult {
  let current = text;
  if (publish.id) {
    const named = applyEdit(current, {
      kind: 'setStepId',
      step: publish.step,
      id: publish.id,
    });
    if (!named.ok) return named;
    current = named.text;
  }
  const result = applyEdit(current, {
    kind: 'setStepField',
    step: publish.step,
    path: ['output'],
    value: { [STDOUT]: { from: STDOUT } },
  });
  if (!result.ok) return result;
  const edit = minimalEdit(text, result.text);
  return { ...result, edits: edit ? [edit] : [] };
}
