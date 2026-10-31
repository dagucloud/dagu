// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  ApplyEditOptions,
  DagEditOp,
  EditResult,
  TextEdit,
} from '../contracts';
import { parseDagDoc } from './parse';
import { applyTextEdits } from './patch';
import { salvageText } from './salvage';
import type { Salvage } from './salvage';
import { opConvertStepsToExplicitForm } from './ops/convert';
import {
  opReplaceStepYaml,
  opRenameStep,
  opSetStepField,
  opSetStepId,
} from './ops/fields';
import { opSetSchedule } from './ops/schedule';
import {
  opAddDependency,
  opAddStep,
  opDeleteStep,
  opRemoveDependency,
  opSetRouteTargets,
} from './ops/structure';

export { createTextHistory } from './history';
export { attributeIssues, groupIssues, locateIssue } from './issues';
export { parseDagDoc, parseDagDocLenient, resolveStepNames } from './parse';
export { readSchedule } from './ops/schedule';
export type { ScheduleView } from './ops/schedule';

/**
 * Applies one edit operation to the DAG YAML text. The result holds the new
 * text and the minimal edits against `text`. Document 1 and later never
 * change. A failure gives an error code and an English message.
 *
 * When entries of `steps` have YAML errors of their own, the edit runs on
 * the text without them and the edits map back to `text`. An edit that
 * would change one of those entries fails with `parseError`.
 */
export function applyEdit(
  text: string,
  op: DagEditOp,
  options: ApplyEditOptions = {}
): EditResult {
  const result = applyStrict(text, op, options);
  if (result.ok || result.code !== 'parseError') return result;
  const salvage = salvageText(text);
  if (!salvage) return result;
  const edited = applyStrict(salvage.shrunk, op, options);
  if (!edited.ok) return edited;
  // Replacing the YAML of a step is how a broken step is fixed.
  const target =
    op.kind === 'replaceStepYaml'
      ? parseDagDoc(salvage.shrunk).steps.find((s) => s.key === op.step)
      : undefined;
  return mapBack(text, salvage, edited, target ? target.range.from - 1 : -1);
}

type Pieces = Salvage['pieces'];

/** Offset of `shrunk` in `text`, or null inside a replaced entry. */
function toOriginal(pieces: Pieces, offset: number): number | null {
  let shift = 0;
  for (const piece of pieces) {
    if (offset <= piece.shrunkFrom) break;
    if (offset < piece.shrunkTo) return null;
    shift += piece.to - piece.from - (piece.shrunkTo - piece.shrunkFrom);
  }
  return offset + shift;
}

function mapBack(
  text: string,
  salvage: Salvage,
  edited: Extract<EditResult, { ok: true }>,
  replaceable: number
): EditResult {
  const edits: TextEdit[] = [];
  for (const edit of edited.edits) {
    const from = toOriginal(salvage.pieces, edit.from);
    const to = toOriginal(salvage.pieces, edit.to);
    // An edit that replaces a whole entry has an end past it: it is refused too.
    const covers = salvage.pieces.some(
      (piece) =>
        piece.shrunkFrom !== replaceable &&
        edit.from <= piece.shrunkFrom &&
        edit.to >= piece.shrunkTo
    );
    if (from === null || to === null || covers) {
      return {
        ok: false,
        code: 'parseError',
        message: 'Fix the step that has a YAML error first.',
      };
    }
    edits.push({ ...edit, from, to });
  }
  const result: EditResult = {
    ok: true,
    text: applyTextEdits(text, edits),
    edits,
  };
  if (edited.select !== undefined) result.select = edited.select;
  return result;
}

function applyStrict(
  text: string,
  op: DagEditOp,
  options: ApplyEditOptions
): EditResult {
  switch (op.kind) {
    case 'addStep':
      return opAddStep(text, op, options);
    case 'deleteStep':
      return opDeleteStep(text, op, options);
    case 'renameStep':
      return opRenameStep(text, op, options);
    case 'setStepId':
      return opSetStepId(text, op, options);
    case 'addDependency':
      return opAddDependency(text, op, options);
    case 'removeDependency':
      return opRemoveDependency(text, op, options);
    case 'setStepField':
      return opSetStepField(text, op, options);
    case 'replaceStepYaml':
      return opReplaceStepYaml(text, op, options);
    case 'setRouteTargets':
      return opSetRouteTargets(text, op, options);
    case 'convertStepsToExplicitForm':
      return opConvertStepsToExplicitForm(text, op, options);
    case 'setSchedule':
      return opSetSchedule(text, op);
  }
}
