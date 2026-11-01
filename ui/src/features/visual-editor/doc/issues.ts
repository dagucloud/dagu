// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  isMap,
  isNode,
  isScalar,
  isSeq,
  parseAllDocuments,
  type Document,
  type Node,
} from 'yaml';
import type {
  DagDocModel,
  IssueGroups,
  StepKey,
  StepNameMap,
  StepView,
  TextRange,
  ValidationIssue,
} from '../contracts';

function position(
  text: string,
  offset: number
): { line: number; column: number } {
  let line = 1;
  let lineStart = 0;
  for (let i = text.indexOf('\n'); i !== -1 && i < offset; ) {
    line++;
    lineStart = i + 1;
    i = text.indexOf('\n', lineStart);
  }
  return { line, column: offset - lineStart + 1 };
}

/**
 * Builds a `TextRange` from UTF-16 offsets. The offsets are clamped to the
 * text, and `to` is never before `from`.
 */
export function rangeFromOffsets(
  text: string,
  from: number,
  to: number
): TextRange {
  const start = Math.min(Math.max(from, 0), text.length);
  const end = Math.min(Math.max(to, start), text.length);
  const a = position(text, start);
  const b = position(text, end);
  return {
    from: start,
    to: end,
    startLine: a.line,
    startColumn: a.column,
    endLine: b.line,
    endColumn: b.column,
  };
}

function modelStepKeys(model: DagDocModel): Set<StepKey> {
  const keys = new Set<StepKey>();
  for (const step of model.steps) keys.add(step.key);
  for (const step of model.handlers) keys.add(step.key);
  return keys;
}

/**
 * Resolves the step key of an issue: `stepPath` when the model has that step
 * or handler, else the key that `names` gives for `stepName`, else null.
 */
function issueStepKey(
  keys: Set<StepKey>,
  issue: ValidationIssue,
  names: StepNameMap
): StepKey | null {
  if (issue.stepPath && keys.has(issue.stepPath)) return issue.stepPath;
  if (issue.stepName !== undefined) {
    const byName = Object.prototype.hasOwnProperty.call(
      names.byName,
      issue.stepName
    )
      ? names.byName[issue.stepName]
      : undefined;
    if (byName !== undefined && keys.has(byName)) return byName;
  }
  return null;
}

/**
 * Groups issues by step key. Issues that match no step of the model go to
 * `dag`. The order of the input is kept inside each group.
 */
export function groupIssues(
  model: DagDocModel,
  issues: ValidationIssue[],
  names: StepNameMap
): IssueGroups {
  const keys = modelStepKeys(model);
  const groups: IssueGroups = { byStep: {}, dag: [] };
  for (const issue of issues) {
    const key = issueStepKey(keys, issue, names);
    if (key === null) {
      groups.dag.push(issue);
    } else {
      (groups.byStep[key] ??= []).push(issue);
    }
  }
  return groups;
}

const EMPTY_COMMAND = /\bstep command is empty\b/;

function hasEmptyCommand(step: StepView): boolean {
  const empty = (value: unknown) =>
    value === null || (typeof value === 'string' && value.trim() === '');
  const { value } = step;
  if (step.kind === 'run') {
    return ['run', 'command', 'script'].some(
      (key) => key in value && empty(value[key])
    );
  }
  const exec = value.exec;
  return (
    typeof exec === 'object' &&
    exec !== null &&
    'command' in exec &&
    empty((exec as Record<string, unknown>).command)
  );
}

/**
 * Gives a step to the 'step command is empty' errors that the server sends
 * without one. The server reports the first error only, so the first step of
 * the text with an empty command is the one. Every other issue is kept.
 */
export function attributeIssues(
  model: DagDocModel,
  issues: ValidationIssue[]
): ValidationIssue[] {
  if (!issues.some((issue) => !issue.stepName && !issue.stepPath)) {
    return issues;
  }
  const step = [...model.steps, ...model.handlers].find(
    (item) => item.explicitName !== undefined && hasEmptyCommand(item)
  );
  if (!step) return issues;
  return issues.map((issue) =>
    issue.severity === 'error' &&
    !issue.stepName &&
    !issue.stepPath &&
    EMPTY_COMMAND.test(issue.message)
      ? {
          ...issue,
          stepName: step.explicitName,
          stepPath: step.key,
          fieldPath: issue.fieldPath ?? `${step.key}.run`,
        }
      : issue
  );
}

function findModelStep(model: DagDocModel, key: StepKey): StepView | null {
  return (
    model.steps.find((s) => s.key === key) ??
    model.handlers.find((s) => s.key === key) ??
    null
  );
}

function trimmedEnd(text: string, from: number, to: number): number {
  let end = to;
  while (end > from && /\s/.test(text.charAt(end - 1))) end--;
  return end;
}

function lineRange(
  text: string,
  line: number,
  column: number
): TextRange | null {
  if (!Number.isInteger(line) || line < 1) return null;
  let start = 0;
  for (let n = 1; n < line; n++) {
    const next = text.indexOf('\n', start);
    if (next === -1) return null;
    start = next + 1;
  }
  // A line break at the very end of the text does not open a line.
  if (start >= text.length && start > 0) return null;
  let end = text.indexOf('\n', start);
  if (end === -1) end = text.length;
  if (end > start && text.charAt(end - 1) === '\r') end--;
  const col = Number.isInteger(column) && column > 1 ? column : 1;
  return rangeFromOffsets(text, Math.min(start + col - 1, end), end);
}

type PathToken = string | number;

function parsePath(path: string): PathToken[] {
  const tokens: PathToken[] = [];
  for (const m of path.matchAll(/\[(\d+)\]|([^.[\]]+)/g)) {
    tokens.push(m[1] !== undefined ? Number(m[1]) : (m[2] as string));
  }
  return tokens;
}

function startsAt(node: unknown, offset: number): node is Node {
  return isNode(node) && node.range != null && node.range[0] === offset;
}

function seqEntryAt(seq: Node, offset: number): Node | null {
  if (!isSeq(seq)) return null;
  for (const item of seq.items) {
    // A group starts where its first entry starts: look inside it first.
    if (isSeq(item)) {
      const inner = seqEntryAt(item, offset);
      if (inner) return inner;
    } else if (startsAt(item, offset)) {
      return item;
    }
  }
  return null;
}

/** Finds the AST node of a model step by the start offset of its range. */
function stepEntry(doc: Document, step: StepView): Node | null {
  const root = doc.contents;
  if (!isMap(root)) return null;
  const container = root.get(step.handler ? 'handler_on' : 'steps', true);
  if (isSeq(container)) return seqEntryAt(container, step.range.from);
  if (!isMap(container)) return null;
  for (const pair of container.items) {
    const byKey =
      startsAt(pair.key, step.range.from) ||
      (step.handler !== undefined &&
        isScalar(pair.key) &&
        pair.key.value === step.handler);
    if (byKey || startsAt(pair.value, step.range.from)) {
      if (isNode(pair.value) && !emptyValue(pair.value)) return pair.value;
      return isNode(pair.key) ? pair.key : null;
    }
  }
  return null;
}

function emptyValue(value: unknown): boolean {
  return (
    !isNode(value) ||
    (isScalar(value) &&
      value.value === null &&
      value.range != null &&
      value.range[0] === value.range[1])
  );
}

function walk(start: Node, tokens: PathToken[]): Node {
  let node = start;
  for (const token of tokens) {
    if (isMap(node)) {
      const pair = node.items.find(
        (p) => isScalar(p.key) && String(p.key.value) === String(token)
      );
      if (!pair) break;
      if (emptyValue(pair.value)) {
        if (isNode(pair.key)) node = pair.key;
        break;
      }
      node = pair.value as Node;
    } else if (isSeq(node) && typeof token === 'number') {
      const item = node.items[token];
      if (!isNode(item)) break;
      node = item;
    } else {
      break;
    }
  }
  return node;
}

function locateFieldPath(
  model: DagDocModel,
  fieldPath: string
): TextRange | null {
  const tokens = parsePath(fieldPath);
  if (tokens.length === 0) return null;
  let doc: Document | undefined;
  try {
    doc = parseAllDocuments(model.sanitizedText ?? model.text)[0];
  } catch {
    return null;
  }
  if (!doc || !isMap(doc.contents)) return null;

  let start: Node | null = doc.contents;
  let rest = tokens;
  const [first, second] = tokens;
  let key: StepKey | null = null;
  if (first === 'steps' && typeof second === 'number') {
    key = `steps[${second}]`;
  } else if (first === 'handler_on' && typeof second === 'string') {
    key = `handler_on.${second}`;
  }
  if (key !== null) {
    const step = findModelStep(model, key);
    start = step ? stepEntry(doc, step) : null;
    rest = tokens.slice(2);
  } else if (!doc.contents.has(String(first))) {
    return null;
  }
  if (!start?.range) return null;

  const node = walk(start, rest);
  if (!node.range) return null;
  const from = node.range[0];
  return rangeFromOffsets(
    model.text,
    from,
    trimmedEnd(model.text, from, node.range[1])
  );
}

const POSITION_PREFIX = /^\[\d+:\d+\]\s*/;

/**
 * The message that an editor marker shows for an issue: the message without
 * its "[line:column]" prefix.
 */
export function issueMarkerMessage(message: string): string {
  return message.replace(POSITION_PREFIX, '') || message;
}

/**
 * Finds the text range of an issue. It uses, in this order: the line and
 * column of the issue, the node of its `fieldPath` (the deepest node that
 * exists), and the range of its step. Returns null when none applies.
 */
export function locateIssue(
  model: DagDocModel,
  issue: ValidationIssue,
  names: StepNameMap
): TextRange | null {
  if (issue.line !== undefined) {
    const range = lineRange(model.text, issue.line, issue.column ?? 1);
    if (range) return range;
  }
  if (issue.fieldPath) {
    const range = locateFieldPath(model, issue.fieldPath);
    if (range) return range;
  }
  const key = issueStepKey(modelStepKeys(model), issue, names);
  return key === null ? null : (findModelStep(model, key)?.range ?? null);
}
