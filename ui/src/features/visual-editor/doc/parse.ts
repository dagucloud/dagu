// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  LineCounter,
  Scalar,
  isMap,
  isNode,
  isPair,
  isScalar,
  isSeq,
} from 'yaml';
import type { Document, Node, Pair } from 'yaml';
import type {
  ApiDAGDetails,
  ApiStep,
  DagDocModel,
  DagType,
  StepKey,
  StepNameMap,
  StepKind,
  StepsForm,
  StepView,
  TextRange,
} from '../contracts';
import {
  explicitNameOf,
  getDagType,
  getStepsNode,
  idOf,
  indexSteps,
} from './stepIndex';
import type { IndexedStep } from './stepIndex';
import { salvageText } from './salvage';
import type { BrokenItem, Salvage } from './salvage';
import { parseAllDocumentsAsServer } from './serverYaml';

/**
 * Parses the text into the model of the DAG. Only document 0 is the DAG;
 * later documents are counted and never read. Never throws: a failure of the
 * parser gives a model with `ok: false`.
 */
export function parseDagDoc(text: string): DagDocModel {
  try {
    return buildModel(text);
  } catch (error) {
    const message = error instanceof Error ? error.message : String(error);
    return modelWithoutSteps(
      typeof text === 'string' ? text : '',
      false,
      0,
      'graph',
      [{ message: `Cannot parse the text: ${message}`, line: 1, column: 1 }]
    );
  }
}

/**
 * Like `parseDagDoc`, and reads on when entries of `steps` have YAML errors:
 * each such entry becomes a placeholder step (`broken` is set) and the other
 * steps read as usual. The model is `ok` when the rest of the text parses.
 * A text with another error, for example a tab in the top level keys, gives
 * the result of `parseDagDoc`: `ok` is false and the model has no steps.
 */
export function parseDagDocLenient(text: string): DagDocModel {
  const strict = parseDagDoc(text);
  if (strict.ok) return strict;
  try {
    const salvage = salvageText(text);
    if (salvage) return buildModel(salvage.padded, { text, salvage });
  } catch {
    // The strict result stands.
  }
  return strict;
}

interface Original {
  text: string;
  salvage: Salvage;
}

function buildModel(source: string, original?: Original): DagDocModel {
  const text = source;
  // The parser can read a copy of the text with other line starts, and does
  // not count all the blank lines that replace a broken entry: the line
  // breaks of the text are counted here.
  const docs = parseAllDocumentsAsServer(text);
  const lineCounter = new LineCounter();
  lineCounter.addNewLine(0);
  for (
    let at = text.indexOf('\n');
    at !== -1;
    at = text.indexOf('\n', at + 1)
  ) {
    lineCounter.addNewLine(at + 1);
  }
  const doc = docs[0];
  if (!doc) return modelWithoutSteps(text, true, 0, 'graph', []);
  if (doc.errors.length > 0) {
    const errors = doc.errors.map((e) => ({
      message: plainMessage(e.message),
      line: e.linePos?.[0].line ?? 1,
      column: e.linePos?.[0].col ?? 1,
    }));
    return modelWithoutSteps(
      text,
      false,
      docs.length,
      safeDagType(doc),
      errors
    );
  }
  const broken = new Map<number, BrokenItem>();
  for (const item of original?.salvage.items ?? []) {
    broken.set(item.seqIndex, item);
  }
  const ctx: ViewContext = {
    text: original?.text ?? text,
    lineCounter,
    broken,
  };
  const { steps, handlers } = indexSteps(doc);
  const model: DagDocModel = {
    text: original?.text ?? text,
    ok: true,
    parseErrors: (original?.salvage.items ?? []).map((item) => ({
      message: item.message,
      line: item.line,
      column: item.column,
    })),
    documentCount: docs.length,
    dagType: getDagType(doc),
    stepsForm: stepsFormOf(doc),
    steps: steps.map((s) => stepView(s, doc, ctx)),
    handlers: handlers.map((s) => stepView(s, doc, ctx)),
  };
  if (original) model.sanitizedText = text;
  return model;
}

function modelWithoutSteps(
  text: string,
  ok: boolean,
  documentCount: number,
  dagType: DagType,
  parseErrors: DagDocModel['parseErrors']
): DagDocModel {
  return {
    text,
    ok,
    parseErrors,
    documentCount,
    dagType,
    stepsForm: 'none',
    steps: [],
    handlers: [],
  };
}

/** The `type:` of a document that has errors, when it can still be read. */
function safeDagType(doc: Document): DagType {
  try {
    return getDagType(doc);
  } catch {
    return 'graph';
  }
}

/** First line of a parser message, without its " at line N, column M:" tail. */
function plainMessage(message: string): string {
  const first = message.split('\n')[0] ?? message;
  return first.replace(/ at line \d+, column \d+:?$/, '');
}

interface ViewContext {
  text: string;
  lineCounter: LineCounter;
  /** Broken entries of `steps` by their index in the list. */
  broken?: Map<number, BrokenItem>;
}

function stepsFormOf(doc: Document): StepsForm {
  const node = getStepsNode(doc);
  if (isSeq(node)) return 'array';
  return isMap(node) ? 'map' : 'none';
}

function stepView(
  step: IndexedStep,
  doc: Document,
  ctx: ViewContext
): StepView {
  const value = valueOf(step, doc);
  const action = typeof value.action === 'string' ? value.action : undefined;
  const view: StepView = {
    key: step.key,
    index: step.index,
    form: step.loc.form === 'handler' ? 'block' : step.loc.form,
    kind: kindOf(value, action, step),
    depends: dependsOf(value.depends),
    value,
    range: stepRange(step, ctx),
  };
  if (step.handler) view.handler = step.handler;
  if (step.loc.form === 'parallelGroup') {
    view.parallelGroup = step.loc.groupNumber;
  }
  const explicitName = explicitNameOf(step);
  if (explicitName !== undefined) view.explicitName = explicitName;
  const id = idOf(step);
  if (id !== undefined) view.id = id;
  if (typeof value.description === 'string')
    view.description = value.description;
  if (action !== undefined) view.action = action;
  const runPreview = runPreviewOf(value);
  if (runPreview !== undefined) view.runPreview = runPreview;
  const lost = brokenAt(step, ctx);
  if (lost) {
    view.broken = lost.message;
    view.range = brokenRange(lost, ctx);
    // The placeholder keeps what the text still tells about the step.
    view.value = {};
    view.depends = lost.depends ?? [];
    if (lost.name !== undefined) {
      view.explicitName = lost.name;
      view.value.name = lost.name;
    }
    if (lost.depends) view.value.depends = lost.depends;
  } else {
    const problem = dependsProblem(value.depends);
    if (problem !== undefined) view.problem = problem;
  }
  return view;
}

function brokenAt(step: IndexedStep, ctx: ViewContext): BrokenItem | undefined {
  const { loc } = step;
  if (!ctx.broken || (loc.form !== 'block' && loc.form !== 'shorthand')) {
    return undefined;
  }
  return ctx.broken.get(loc.seqIndex);
}

/** From the first character after "- " to the last line of the entry. */
function brokenRange(item: BrokenItem, ctx: ViewContext): TextRange {
  let from = item.from;
  while (from < item.to && /\s/.test(ctx.text.charAt(from))) from++;
  const start = ctx.lineCounter.linePos(from);
  const end = ctx.lineCounter.linePos(item.to);
  return {
    from,
    to: item.to,
    startLine: start.line,
    startColumn: start.col,
    endLine: end.line,
    endColumn: end.col,
  };
}

function dependsProblem(raw: unknown): string | undefined {
  if (raw === undefined || raw === null) return undefined;
  const entry = (d: unknown) => typeof d === 'string' || typeof d === 'number';
  if (entry(raw)) return undefined;
  if (Array.isArray(raw) && raw.every(entry)) return undefined;
  return 'depends must be a step name or a list of step names';
}

/** Plain JS value of the entry. A shorthand string "echo x" gives `{ run: 'echo x' }`. */
function valueOf(step: IndexedStep, doc: Document): Record<string, unknown> {
  // An alias entry reads like the node it refers to.
  const node = step.target ?? step.node;
  if (isScalar(node)) {
    return typeof node.value === 'string' ? { run: node.value } : {};
  }
  if (!node) return {};
  try {
    const js: unknown = node.toJS(doc);
    return js !== null && typeof js === 'object' && !Array.isArray(js)
      ? (js as Record<string, unknown>)
      : {};
  } catch {
    // A value that cannot be converted must not hide the other steps.
    return {};
  }
}

function kindOf(
  value: Record<string, unknown>,
  action: string | undefined,
  step: IndexedStep
): StepKind {
  if (action === 'router.route') return 'router';
  if ('call' in value || action === 'dag.run') return 'call';
  if ('parallel' in value) return 'parallel';
  if (action !== undefined) return 'action';
  if (
    step.loc.form === 'shorthand' ||
    'run' in value ||
    'command' in value ||
    'script' in value
  ) {
    return 'run';
  }
  return 'other';
}

function dependsOf(raw: unknown): string[] {
  const text = (d: unknown): string | undefined =>
    typeof d === 'string' ? d : typeof d === 'number' ? String(d) : undefined;
  if (!Array.isArray(raw)) {
    const one = text(raw);
    return one === undefined ? [] : [one];
  }
  return raw.flatMap((d) => text(d) ?? []);
}

/** First non-empty line of `run`, else `command`, else `script`. */
function runPreviewOf(value: Record<string, unknown>): string | undefined {
  for (const field of ['run', 'command', 'script']) {
    const raw = value[field];
    const text = Array.isArray(raw)
      ? raw.map(String).join(' ')
      : typeof raw === 'string'
        ? raw
        : undefined;
    const line = text?.split('\n').find((l) => l.trim() !== '');
    if (line !== undefined) return line;
  }
  return undefined;
}

/**
 * Range of a step entry in the text. Offsets are UTF-16 indexes; `to` is
 * exclusive. The range starts at the first character after "- " of a
 * sequence entry, or at the key of a map entry or handler. It ends after the
 * last character of the last value. Comments and blank lines before the
 * entry, comments after the last value and the line end are not in the range.
 * A comment before the last value is in the range.
 */
function stepRange(step: IndexedStep, ctx: ViewContext): TextRange {
  const { loc, node } = step;
  let from: number;
  let to: number;
  if (loc.form === 'mapEntry' || loc.form === 'handler') {
    from = startOf(loc.pair.key) ?? 0;
    to = pairEnd(loc.pair, ctx.text);
  } else {
    from = startOf(node) ?? 0;
    to = node ? contentEnd(node, ctx.text) : from;
  }
  const start = ctx.lineCounter.linePos(from);
  const end = ctx.lineCounter.linePos(to);
  return {
    from,
    to,
    startLine: start.line,
    startColumn: start.col,
    endLine: end.line,
    endColumn: end.col,
  };
}

function startOf(node: unknown): number | undefined {
  return isNode(node) ? node.range?.[0] : undefined;
}

/** Offset after the last character of the value of the node. */
function contentEnd(node: Node, text: string): number {
  const range = node.range ?? [0, 0, 0];
  if (isScalar(node)) {
    if (
      node.type !== Scalar.BLOCK_LITERAL &&
      node.type !== Scalar.BLOCK_FOLDED
    ) {
      return range[1];
    }
    // The range of a block scalar includes the line end and kept blank lines.
    let end = range[1];
    while (end > range[0] && /\s/.test(text.charAt(end - 1))) end--;
    return end;
  }
  if ((isMap(node) || isSeq(node)) && !node.flow) {
    // The range of a block collection includes comments after its last item.
    const last = node.items[node.items.length - 1];
    if (isPair(last)) return pairEnd(last, text);
    if (isNode(last)) return contentEnd(last, text);
  }
  return range[1];
}

function pairEnd(pair: Pair, text: string): number {
  const value = pair.value;
  if (isNode(value) && value.range && value.range[1] > value.range[0]) {
    return contentEnd(value, text);
  }
  // No value: the entry ends after the colon.
  const keyEnd = isNode(pair.key) ? (pair.key.range?.[1] ?? 0) : 0;
  const colon = text.indexOf(':', keyEnd);
  return colon === -1 ? keyEnd : colon + 1;
}

/**
 * Maps the step keys of the model to the step names of the server.
 *
 * When the counts agree, `steps[i]` is `details.steps[i]` (the server numbers
 * steps like the index), unless the step has another explicit name: `details`
 * can be older than the text. A handler is `details.handlerOn.<event>`.
 * Otherwise a step is matched by its explicit name, then by its id. Without
 * `details`, the explicit name is the name. Unmatched keys stay absent.
 */
export function resolveStepNames(
  model: DagDocModel,
  details?: ApiDAGDetails
): StepNameMap {
  const byKey: Record<StepKey, string> = {};
  const serverSteps = details?.steps ?? [];
  const sameCount = serverSteps.length === model.steps.length;
  model.steps.forEach((step, i) => {
    const positional = sameCount ? serverSteps[i]?.name : undefined;
    const name =
      details === undefined
        ? step.explicitName
        : positional !== undefined &&
            (step.explicitName === undefined ||
              step.explicitName === positional)
          ? positional
          : matchByNameOrId(serverSteps, step.explicitName, step.id);
    if (name !== undefined) byKey[step.key] = name;
  });
  const handlerOn = details?.handlerOn as
    | Partial<Record<string, ApiStep>>
    | undefined;
  for (const handler of model.handlers) {
    const name =
      details === undefined
        ? handler.explicitName
        : handler.handler && handlerOn?.[handler.handler]?.name;
    if (name) byKey[handler.key] = name;
  }
  const byName: Record<string, StepKey> = {};
  for (const [key, name] of Object.entries(byKey)) {
    if (!Object.prototype.hasOwnProperty.call(byName, name)) byName[name] = key;
  }
  return { byKey, byName };
}

function matchByNameOrId(
  serverSteps: ApiStep[],
  explicitName: string | undefined,
  id: string | undefined
): string | undefined {
  if (explicitName !== undefined) {
    const named = serverSteps.find((s) => s.name === explicitName);
    if (named) return named.name;
  }
  return id === undefined
    ? undefined
    : serverSteps.find((s) => s.id === id)?.name;
}
