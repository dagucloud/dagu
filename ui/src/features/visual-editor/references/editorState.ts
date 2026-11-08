// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { Locale } from '@/contexts/UserPreference';
import { getYamlPathAtOffset } from '@/hooks/useYamlCursorPath';
import { parseDagDocLenient } from '../doc/parse';
import type {
  DagDocModel,
  ReferenceCandidate,
  ReferenceScope,
  StepKey,
} from '../contracts';
import {
  buildReferenceCandidates,
  declaresRepeat,
  foreachFramesAt,
} from './candidates';

export type ReferenceScopeStep = ReferenceScope['steps'][number];

/** Data that the Monaco providers read each time they run. */
export interface ReferenceEditorState {
  scope?: ReferenceScope;
  model?: DagDocModel;
}

// True when the text from `from` to `to` is white space only.
function isBlank(text: string, from: number, to: number): boolean {
  for (let at = from; at < to; at++) {
    if (/\S/.test(text.charAt(at))) return false;
  }
  return true;
}

// A step range ends at its last character, so the blank text after it, where
// the author types the next line of the step, is also in the step.
function stepKeyFromModel(
  text: string,
  offset: number,
  model: DagDocModel
): StepKey | null {
  if (model.text !== text) {
    return null;
  }
  let best: { key: StepKey; size: number } | null = null;
  for (const step of [...model.steps, ...model.handlers]) {
    const { from, to } = step.range;
    if (offset < from || (offset > to && !isBlank(text, to, offset))) {
      continue;
    }
    if (!best || to - from < best.size) {
      best = { key: step.key, size: to - from };
    }
  }
  return best ? best.key : null;
}

/** Step of the scope that owns the offset, or null for a DAG-level field. */
export function findOwningStep(
  text: string,
  offset: number,
  state: ReferenceEditorState
): ReferenceScopeStep | null {
  const steps = state.scope?.steps ?? [];
  if (steps.length === 0) {
    return null;
  }
  if (state.model && state.model.text === text) {
    const key = stepKeyFromModel(text, offset, state.model);
    return key ? (steps.find((s) => s.stepPath === key) ?? null) : null;
  }
  const path = getYamlPathAtOffset(text, offset).path;
  const [root, second] = path;
  if (second === undefined) {
    return null;
  }
  if (root === 'steps') {
    if (/^\d+$/.test(second)) {
      return steps.find((s) => s.stepPath === `steps[${second}]`) ?? null;
    }
    return steps.find((s) => s.name === second) ?? null;
  }
  if (root === 'handler_on') {
    return steps.find((s) => s.stepPath === `handler_on.${second}`) ?? null;
  }
  return null;
}

/**
 * The YAML path below a step entry, for a path under `steps` or `handler_on`.
 * A step of a parallel group has one more index: ['steps', '1', '0', 'run'].
 */
export function pathBelowStep(path: readonly string[]): string[] {
  return path.slice(/^\d+$/.test(path[2] ?? '') ? 3 : 2);
}

/** The path below the innermost step of `below`, a path below a step entry. */
export function pathBelowBodies(below: string[]): string[] {
  let inner = below;
  while (inner[0] === 'foreach' && inner[1] === 'steps') {
    inner = inner.slice(3);
  }
  return inner;
}

/** Candidates for a field at the offset, as seen by the step that owns it. */
export function candidatesAt(
  text: string,
  offset: number,
  state: ReferenceEditorState
): ReferenceCandidate[] {
  const owner = findOwningStep(text, offset, state);
  const view = state.model?.steps.find((s) => s.key === owner?.stepPath);
  const frames = owner
    ? foreachFramesAt(
        owner,
        pathBelowStep(getYamlPathAtOffset(text, offset).path)
      )
    : [];
  return buildReferenceCandidates(state.scope, owner?.name ?? null, {
    repeats: frames.length === 0 && declaresRepeat(view),
    frames,
    doc: currentDoc(text, state.model),
  });
}

// The model trails the text by a parse, and a completion runs on the keystroke.
// A state without a model is one where nothing can edit.
function currentDoc(
  text: string,
  model: DagDocModel | undefined
): DagDocModel | undefined {
  if (!model) return undefined;
  return model.text === text ? model : parseDagDocLenient(text);
}

const RUN_FIELDS = new Set(['run', 'script', 'command']);

/** True for a step field that a shell reads. */
export function isRunField(field: string | undefined): boolean {
  return field !== undefined && RUN_FIELDS.has(field);
}

/** True for a step field that runs as the command of the executor. */
export function isCommandField(field: string | undefined): boolean {
  return isRunField(field) || field === 'args';
}

/** True when the path points at a shell field of a step, at any depth of foreach bodies. */
export function isRunFieldPath(path: string[]): boolean {
  const [root] = path;
  return (
    (root === 'steps' || root === 'handler_on') &&
    isRunField(pathBelowBodies(pathBelowStep(path))[0])
  );
}

/** True when the offset is in the run, script or command field of a step. */
export function isInRunField(text: string, offset: number): boolean {
  return isRunFieldPath(getYamlPathAtOffset(text, offset).path);
}

/** Locale of the page. Monaco code runs outside React, so it reads the document. */
export function editorLocale(): Locale {
  const lang =
    typeof document === 'undefined' ? '' : document.documentElement.lang;
  if (lang.toLowerCase().startsWith('zh')) {
    return 'zh-CN';
  }
  if (lang.toLowerCase().startsWith('ja')) {
    return 'ja';
  }
  return 'en';
}
