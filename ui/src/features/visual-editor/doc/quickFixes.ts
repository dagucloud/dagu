// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type {
  DagDocModel,
  StepNameMap,
  StepView,
  TextEdit,
  ValidationIssue,
} from '../contracts';
import { NO_ID_MESSAGE, OUTPUTS_ID_MESSAGE } from './issueMessages';
import { issueMarkerMessage, locateIssue } from './issues';
import { validateStepId } from './names';
import { resolveStepNames } from './parse';

/**
 * A fix that the user applies with one click. `edits` are minimal text hunks
 * against the text of the model that planned them, in ascending order.
 */
export interface QuickFix {
  /** Message key of the change, for the button and its tooltip. */
  message: string;
  params: Record<string, string | number>;
  edits: TextEdit[];
}

const MAX_ID_BYTES = 40;
// Room for a `_<n>` suffix of up to four digits inside the limit.
const MAX_ID_BASE = MAX_ID_BYTES - 5;

/** True for a message that says that a step needs an `id`. */
function isMissingIdMessage(message: string): boolean {
  return (
    message === NO_ID_MESSAGE ||
    message === OUTPUTS_ID_MESSAGE ||
    /requires an explicit id/i.test(message)
  );
}

/**
 * The id that a step name gives: accents drop, each run of characters other
 * than letters, digits and `_` becomes one `_`, and the ends lose their `_`
 * (`show title` gives `show_title`). The case stays. The result is a valid
 * step id, or '' when the name gives no usable character.
 */
export function slugStepId(name: string): string {
  let slug = name
    .normalize('NFD')
    .replace(/\p{M}+/gu, '')
    .replace(/[^A-Za-z0-9_]+/g, '_')
    .replace(/^_+|_+$/g, '');
  if (slug === '') return '';
  // An id starts with a letter, and some words are reserved.
  if (!/^[A-Za-z]/.test(slug) || validateStepId(slug) !== null) {
    slug = `step_${slug}`;
  }
  return slug.slice(0, MAX_ID_BASE).replace(/_+$/, '');
}

/**
 * The first of `slug`, `slug_1`, `slug_2`, ... that is not in `taken`. An empty
 * slug becomes `step_<position>`.
 */
export function uniqueStepId(
  slug: string,
  position: number,
  taken: ReadonlySet<string>
): string {
  const root = slug === '' ? `step_${position}` : slug;
  if (!taken.has(root)) return root;
  for (let n = 1; ; n++) {
    const candidate = `${root}_${n}`;
    if (!taken.has(candidate)) return candidate;
  }
}

interface NameLine {
  /** Offset of the end of the line, before its line break. */
  end: number;
  /** Column of the keys of the step. */
  column: number;
}

/**
 * The `name:` line of a block step: the one that holds its whole value on
 * one line, at the column of the keys of the step. Null for any other form.
 */
function nameLineOf(text: string, step: StepView): NameLine | null {
  if (step.form !== 'block' || step.explicitName === undefined) return null;
  let start = text.lastIndexOf('\n', step.range.from - 1) + 1;
  let column = -1;
  let first = true;
  while (start <= step.range.to && start < text.length) {
    const newline = text.indexOf('\n', start);
    let end = newline < 0 ? text.length : newline;
    if (end > start && text[end - 1] === '\r') end--;
    const line = text.slice(start, end);
    let rest = line.trimStart();
    let lineColumn = line.length - rest.length;
    if (first) {
      const dash = /^(\s*)-(\s+)(\S.*)?$/.exec(line);
      if (!dash) return null;
      lineColumn = dash[1]!.length + 1 + dash[2]!.length;
      rest = dash[3] ?? '';
      column = lineColumn;
      first = false;
    } else if (rest !== '' && lineColumn < column) {
      return null;
    }
    if (rest !== '' && lineColumn === column && /^name\s*:(\s|$)/.test(rest)) {
      const value = rest.replace(/^name\s*:/, '').trim();
      if (value === '' || /^[|>&!]/.test(value)) return null;
      // A value that goes on in the next line is not a single line.
      const next =
        newline < 0 ? '' : text.slice(newline + 1).split('\n', 1)[0]!;
      const nextRest = next.trimStart();
      if (nextRest !== '' && next.length - nextRest.length > column) {
        return null;
      }
      return { end, column };
    }
    if (newline < 0) return null;
    start = newline + 1;
  }
  return null;
}

/**
 * The quick fixes of a list of issues, by issue. A step without an `id` gets
 * an `id:` line after its `name:` line, with the slug of its name; a number
 * (`_1`, `_2`, ...) makes it differ from every name and id of the file and
 * from the ids that the other fixes of the same plan add. One fix serves all
 * the issues of a step. The fixes are in step order, so that two steps with
 * one name always get the same ids.
 */
export function planQuickFixes(
  model: DagDocModel,
  issues: readonly ValidationIssue[]
): Map<ValidationIssue, QuickFix> {
  const fixes = new Map<ValidationIssue, QuickFix>();
  const wanted = new Map<StepView, ValidationIssue[]>();
  for (const issue of issues) {
    if (issue.severity === 'notice' || !isMissingIdMessage(issue.message)) {
      continue;
    }
    const step =
      model.steps.find((candidate) => candidate.key === issue.stepPath) ??
      model.steps.find(
        (candidate) =>
          issue.stepName !== undefined &&
          candidate.explicitName === issue.stepName
      );
    if (!step || step.id !== undefined || step.broken !== undefined) continue;
    wanted.set(step, [...(wanted.get(step) ?? []), issue]);
  }
  if (wanted.size === 0) return fixes;

  const taken = new Set<string>();
  for (const step of [...model.steps, ...model.handlers]) {
    if (step.explicitName !== undefined) taken.add(step.explicitName);
    if (step.id !== undefined) taken.add(step.id);
  }
  const eol = model.text.includes('\r\n') ? '\r\n' : '\n';
  for (const step of model.steps) {
    const own = wanted.get(step);
    if (!own) continue;
    const line = nameLineOf(model.text, step);
    if (!line || step.explicitName === undefined) continue;
    // A step may take its own name as its id, unless another step has it as
    // its name or its id.
    const others = new Set(taken);
    const shared = [...model.steps, ...model.handlers].some(
      (other) =>
        other !== step &&
        (other.explicitName === step.explicitName ||
          other.id === step.explicitName)
    );
    if (!shared) others.delete(step.explicitName);
    const slug = slugStepId(step.explicitName);
    const id = uniqueStepId(slug, (step.index ?? 0) + 1, others);
    taken.add(id);
    const fix: QuickFix = {
      message: 'Add id: {id}',
      params: { id },
      edits: [
        {
          from: line.end,
          to: line.end,
          insert: `${eol}${' '.repeat(line.column)}id: ${id}`,
        },
      ],
    };
    for (const issue of own) fixes.set(issue, fix);
  }
  return fixes;
}

/**
 * The quick fixes of `planQuickFixes` whose edits all lie inside `range`
 * (the range of one step). The plan is the one of the whole text, so that
 * an id that a fix gives never clashes with the id of another step, and the
 * fixes are the ones that the whole-file wand applies to this range.
 */
export function planQuickFixesInRange(
  model: DagDocModel,
  issues: readonly ValidationIssue[],
  range: { from: number; to: number }
): Map<ValidationIssue, QuickFix> {
  const inside = new Map<ValidationIssue, QuickFix>();
  for (const [issue, fix] of planQuickFixes(model, issues)) {
    if (fix.edits.every((e) => e.from >= range.from && e.to <= range.to)) {
      inside.set(issue, fix);
    }
  }
  return inside;
}

/** The edits of several fixes in one ascending list, each fix once. */
export function mergeQuickFixes(fixes: Iterable<QuickFix>): TextEdit[] {
  return [...new Set(fixes)]
    .flatMap((fix) => fix.edits)
    .sort((a, b) => a.from - b.from || a.to - b.to);
}

/** The part of an editor marker that identifies the issue that it shows. */
export interface MarkerLike {
  message: string;
  startLineNumber: number;
  startColumn: number;
}

/**
 * The quick fix of the issue that an editor marker shows, or undefined when
 * the marker has no fix. A marker is the issue's message at the start of the
 * range that `locateIssue` gives. `names` are those that drew the markers.
 * The wand, the Issues popover and the code actions of the editor all read
 * their fixes from `planQuickFixes`, so they never disagree.
 */
export function quickFixesForMarker(
  model: DagDocModel,
  issues: readonly ValidationIssue[],
  marker: MarkerLike,
  names: StepNameMap = resolveStepNames(model)
): QuickFix | undefined {
  for (const [issue, fix] of planQuickFixes(model, issues)) {
    if (issueMarkerMessage(issue.message) !== marker.message) continue;
    const range = locateIssue(model, issue, names);
    if (
      range &&
      range.startLine === marker.startLineNumber &&
      range.startColumn === marker.startColumn
    ) {
      return fix;
    }
  }
  return undefined;
}
