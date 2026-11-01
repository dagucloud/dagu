// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

/**
 * Semantic comparison and canonical formatting of DAG YAML text.
 *
 * `isSameYaml` tells if two texts hold the same document. `formatYaml` prints
 * a text in one canonical form. Both read the text with the YAML library, so
 * comments stay in the result of `formatYaml` and count in `isSameYaml`.
 */

import { isMap, isNode, isPair, isScalar, isSeq, visit } from 'yaml';
import { stringifyString } from 'yaml/util';
import type { StepView, TextEdit } from '../contracts';
import type {
  Document,
  Node,
  Pair,
  ParseOptions,
  DocumentOptions,
  SchemaOptions,
  YAMLMap,
  YAMLSeq,
} from 'yaml';
import { parseAllDocumentsAsServer } from './serverYaml';

/** Keys of the top level, in canonical order. Other keys follow, in their own order. */
export const DAG_KEY_ORDER: readonly string[] = [
  'name',
  'description',
  'group',
  'labels',
  'tags',
  'schedule',
  'skip_if_successful',
  'catchup_window',
  'overlap_policy',
  'params',
  'consts',
  'env',
  'dotenv',
  'secrets',
  'working_dir',
  'type',
  'queue',
  'max_active_runs',
  'max_active_steps',
  'worker_selector',
  'timeout_sec',
  'delay_sec',
  'restart_wait_sec',
  'max_clean_up_time_sec',
  'hist_retention_days',
  'hist_retention_runs',
  'max_output_size',
  'log_dir',
  'log_output',
  'artifacts',
  'preconditions',
  'retry_policy',
  'shell',
  'shell_args',
  'container',
  'defaults',
  'tools',
  'step_types',
  'actions',
  'tasks',
  'steps',
  'handler_on',
  'mail_on',
  'error_mail',
  'info_mail',
  'smtp',
  'mail_accounts',
  'registry_auths',
  'ssh',
  's3',
  'llm',
  'harness',
  'harnesses',
  'redis',
  'kubernetes',
  'resources',
  'otel',
  'run_config',
  'webhook',
];

/** Keys of a step (and of a handler), in canonical order. */
export const STEP_KEY_ORDER: readonly string[] = [
  'id',
  'name',
  'description',
  'action',
  'run',
  'command',
  'exec',
  'script',
  'call',
  'type',
  'with',
  'config',
  'params',
  'parallel',
  'foreach',
  'routes',
  'shell',
  'shell_args',
  'shell_packages',
  'working_dir',
  'env',
  'container',
  'stdin',
  'depends',
  'dependencies',
  'output',
  'output_schema',
  'outputs',
  'inputs',
  'stdout',
  'stderr',
  'log_output',
  'preconditions',
  'retry_policy',
  'repeat_policy',
  'continue_on',
  'timeout_sec',
  'signal_on_stop',
  'mail_on_error',
  'pass_env',
  'worker_selector',
  'llm',
  'approval',
  'messages',
  'tools',
  'value',
];

/** Keys of `handler_on`, in canonical order. */
export const HANDLER_KEY_ORDER: readonly string[] = [
  'init',
  'success',
  'failure',
  'abort',
  'exit',
];

/** The longest line of a flow sequence of scalars that stays in flow style. */
export const FLOW_MAX_WIDTH = 80;

/**
 * The width in columns at which a plain or quoted string folds onto several
 * lines. A word that is longer than the width (a URL) stays on one line, as
 * do a block scalar and a comment.
 */
export const FORMAT_LINE_WIDTH = 100;

/** The least number of columns that a folded line keeps for its text. */
const FORMAT_MIN_CONTENT_WIDTH = 20;

type Walk = (value: unknown, path: string) => void;

/**
 * The path of the place on top of a node: the first entry of a collection,
 * so that a comment there counts the same on the collection and on the entry.
 */
function headPath(node: unknown, path: string): string {
  if ((isMap(node) || isSeq(node)) && node.items.length > 0) {
    const first = node.items[0];
    return isPair(first) ? `${path}/0` : headPath(first, `${path}/0`);
  }
  return path;
}

/**
 * Every comment of a document, in text order, as non-empty trimmed lines,
 * each with the path of the node that holds it. The head of the document
 * counts as the head of its first entry and the tail as the tail of its
 * root, since blank lines decide where the library puts them.
 */
function commentLines(doc: Document): string[] {
  const lines: string[] = [];
  const push = (comment: string | null | undefined, place: string) => {
    if (!comment) return;
    for (const line of comment.split('\n')) {
      const trimmed = line.trim();
      if (trimmed !== '') lines.push(`${place} ${trimmed}`);
    }
  };
  const walk: Walk = (value, path) => {
    if (isPair(value)) {
      walk(value.key, path);
      walk(value.value, path);
      return;
    }
    if (!isNode(value)) return;
    push(value.commentBefore, `before ${headPath(value, path)}`);
    if (isMap(value) || isSeq(value)) {
      value.items.forEach((item, index) => walk(item, `${path}/${index}`));
    }
    push(value.comment, `after ${path}`);
  };
  push(doc.commentBefore, `before ${headPath(doc.contents, '')}`);
  walk(doc.contents, '');
  push(doc.comment, 'after ');
  return lines;
}

/** Deep equality of parsed values; the order of the keys counts. */
function sameValue(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  if (typeof a !== typeof b) return false;
  if (typeof a === 'number') return Number.isNaN(a) && Number.isNaN(b);
  if (a === null || b === null || typeof a !== 'object') return false;
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) {
      return false;
    }
    return a.every((item, index) => sameValue(item, b[index]));
  }
  if (a instanceof Date || b instanceof Date) {
    return (
      a instanceof Date && b instanceof Date && a.getTime() === b.getTime()
    );
  }
  if (a instanceof Map || b instanceof Map) {
    if (!(a instanceof Map) || !(b instanceof Map) || a.size !== b.size) {
      return false;
    }
    const left = [...a.entries()];
    const right = [...b.entries()];
    return left.every(
      ([key, value], index) =>
        sameValue(key, right[index]?.[0]) && sameValue(value, right[index]?.[1])
    );
  }
  const left = a as Record<string, unknown>;
  const right = b as Record<string, unknown>;
  const keysA = Object.keys(left);
  const keysB = Object.keys(right);
  if (keysA.length !== keysB.length) return false;
  return keysA.every(
    (key, index) => key === keysB[index] && sameValue(left[key], right[key])
  );
}

function toLf(text: string): string {
  return text.replace(/\r\n?/g, '\n');
}

/** Integers stay exact beyond 2^53, as the server reads them. */
const EXACT_INTS = { intAsBigInt: true };

/** The text when it parses without error, as documents; else null. */
function readDocuments(
  text: string,
  options?: ParseOptions & DocumentOptions & SchemaOptions
): Document[] | null {
  try {
    const docs = parseAllDocumentsAsServer(toLf(text), options);
    const list = Array.isArray(docs) ? docs : [docs];
    return list.some((doc) => doc.errors.length > 0) ? null : list;
  } catch {
    return null;
  }
}

/** The text without blank lines and without trailing spaces: for text that does not parse. */
function looseText(text: string): string {
  return toLf(text)
    .split('\n')
    .map((line) => line.trimEnd())
    .filter((line) => line !== '')
    .join('\n');
}

/**
 * True when the two texts are the same document: the parsed values are equal,
 * with the same key order, and the comments are equal, on the same nodes.
 * Blank lines, trailing spaces, the final line end, line ends (LF or CRLF),
 * the indentation width and the quote style of a string are no change. A
 * text that does not parse, or that has an alias without its anchor, is
 * compared as text, with blank lines and trailing spaces dropped.
 */
export function isSameYaml(a: string, b: string): boolean {
  if (a === b) return true;
  const left = readDocuments(a, EXACT_INTS);
  const right = readDocuments(b, EXACT_INTS);
  if (!left || !right) {
    return !left && !right && looseText(a) === looseText(b);
  }
  if (left.length !== right.length) return false;
  try {
    for (let index = 0; index < left.length; index++) {
      const x = left[index]!;
      const y = right[index]!;
      if (!sameValue(x.toJS(), y.toJS())) return false;
      const cx = commentLines(x);
      const cy = commentLines(y);
      if (cx.length !== cy.length || cx.some((line, i) => line !== cy[i])) {
        return false;
      }
    }
  } catch {
    // toJS throws on an alias without its anchor.
    return looseText(a) === looseText(b);
  }
  return true;
}

/** True when the text parses without a YAML error, so that it can be formatted. */
export function isFormattable(text: string): boolean {
  return readDocuments(text) !== null;
}

function keyName(pair: Pair): string | null {
  const key = pair.key;
  if (isScalar(key) && typeof key.value === 'string') return key.value;
  return typeof key === 'string' ? key : null;
}

/**
 * The pairs of `map` in canonical order. A key in `order` goes by its place
 * in `order`; the other keys follow, each in its own order. Equal ranks keep
 * their order, so the sort is stable and idempotent.
 */
function sortPairs(map: YAMLMap, order: readonly string[]): void {
  const rank = new Map(order.map((key, index) => [key, index]));
  const ranked = map.items.map((pair, index) => {
    const name = keyName(pair);
    const place = name === null ? undefined : rank.get(name);
    return { pair, index, place: place ?? order.length };
  });
  ranked.sort((x, y) => x.place - y.place || x.index - y.index);
  map.items = ranked.map((entry) => entry.pair);
}

function valueOf(map: YAMLMap, key: string): unknown {
  for (const pair of map.items) {
    if (keyName(pair) === key) return pair.value;
  }
  return undefined;
}

/** A step: a map in the list `steps`, a value of the map form, or a handler. */
function orderStep(node: unknown): void {
  if (isMap(node)) {
    sortPairs(node, STEP_KEY_ORDER);
  } else if (isSeq(node)) {
    // A group of steps that run in parallel.
    for (const item of node.items) orderStep(item);
  }
}

function orderSteps(node: unknown): void {
  if (isSeq(node)) {
    for (const item of node.items) orderStep(item);
  } else if (isMap(node)) {
    for (const pair of node.items) orderStep(pair.value);
  }
}

function orderDag(root: unknown): void {
  if (!isMap(root)) return;
  sortPairs(root, DAG_KEY_ORDER);
  orderSteps(valueOf(root, 'steps'));
  const handlers = valueOf(root, 'handler_on');
  if (isMap(handlers)) {
    sortPairs(handlers, HANDLER_KEY_ORDER);
    for (const pair of handlers.items) orderStep(pair.value);
  }
}

function itemNode(item: unknown): Node | null {
  if (isPair(item)) return isNode(item.key) ? item.key : null;
  return isNode(item) ? item : null;
}

function setSpaceBefore(item: unknown, space: boolean): void {
  const node = itemNode(item);
  if (node) node.spaceBefore = space;
}

function cleanComment(node: unknown): void {
  const holder = node as {
    comment?: string | null;
    commentBefore?: string | null;
  };
  for (const field of ['comment', 'commentBefore'] as const) {
    const value = holder[field];
    if (typeof value === 'string') {
      // No blank line inside a comment: the blank lines of the text are the
      // ones that the formatter writes.
      holder[field] = value
        .split('\n')
        .map((line) => line.trimEnd())
        .filter((line) => line !== '')
        .join('\n');
    }
  }
}

function flowSeqWidth(seq: YAMLSeq): number {
  return seq.items.reduce<number>((width, item, index) => {
    const text = isScalar(item) ? String(item.value ?? '') : '';
    return width + text.length + (index > 0 ? 2 : 0);
  }, 2);
}

/** True for a sequence of plain scalars that has no comment. */
function isScalarSeq(seq: YAMLSeq): boolean {
  return seq.items.every(
    (item) =>
      isScalar(item) &&
      !item.comment &&
      !item.commentBefore &&
      typeof item.value !== 'object'
  );
}

/**
 * Sets the style of every collection: block, except a short flow sequence of
 * scalars; clears the blank lines the text had; and trims the comments.
 */
function normalise(node: unknown, key: string | null): void {
  if (isPair(node)) {
    normalise(node.key, null);
    normalise(
      node.value,
      isScalar(node.key) && typeof node.key.value === 'string'
        ? node.key.value
        : null
    );
    return;
  }
  if (!isNode(node)) return;
  cleanComment(node);
  node.spaceBefore = false;
  if (isMap(node)) {
    node.flow = false;
    for (const pair of node.items) {
      setSpaceBefore(pair, false);
      normalise(pair, null);
    }
  } else if (isSeq(node)) {
    const keepFlow =
      node.flow === true &&
      key !== null &&
      isScalarSeq(node) &&
      key.length + 2 + flowSeqWidth(node) <= FLOW_MAX_WIDTH;
    node.flow = keepFlow;
    for (const item of node.items) {
      setSpaceBefore(item, false);
      normalise(item, null);
    }
  }
}

/** A blank line before each step but the first, and before each top-level block. */
function spaceDag(root: unknown): void {
  if (!isMap(root)) return;
  root.items.forEach((pair, index) => {
    const value = pair.value;
    const block =
      (isMap(value) || isSeq(value)) && !(isSeq(value) && value.flow === true);
    if (index > 0 && block) setSpaceBefore(pair, true);
  });
  const spaceList = (list: unknown) => {
    if (!isSeq(list)) return;
    list.items.forEach((item, index) => {
      if (index > 0) setSpaceBefore(item, true);
    });
  };
  const steps = valueOf(root, 'steps');
  spaceList(steps);
  if (isMap(steps)) {
    steps.items.forEach((pair, index) => {
      if (index > 0) setSpaceBefore(pair, true);
    });
  }
}

/**
 * The comment on top of the first key goes to the head of the document when
 * another key takes the first place: the head of the file stays on top.
 */
function keepHead(doc: Document, firstKey: Node | null): void {
  const head = firstKey?.commentBefore;
  const root = doc.contents;
  if (!head || !isMap(root) || itemNode(root.items[0]) === firstKey) return;
  firstKey!.commentBefore = undefined;
  doc.commentBefore = [doc.commentBefore, head].filter(Boolean).join('\n');
}

type StringTag = { tag: string; stringify?: unknown };
type StringifyArgs = Parameters<typeof stringifyString>;

/**
 * Prints a string with the folding of the YAML library, at the column
 * FORMAT_LINE_WIDTH. The library does not count the indentation of a key,
 * and it keeps a text that is shorter than the width of its continuation
 * lines on one line, even when the key makes the first line too long. This
 * function corrects both: it narrows the width until the text folds. A string
 * with a line end does not fold.
 */
function stringifyFolded(
  node: StringifyArgs[0],
  ctx: StringifyArgs[1],
  onComment: StringifyArgs[2],
  onChompKeep: StringifyArgs[3]
): string {
  const run = (width: number) =>
    stringifyString(
      node,
      { ...ctx, options: { ...ctx.options, lineWidth: width } },
      onComment,
      onChompKeep
    );
  if (ctx.implicitKey) return run(FORMAT_LINE_WIDTH);
  if (typeof node.value === 'string' && node.value.includes('\n')) {
    // The formatter drops the trailing spaces of each line, and a block or
    // quoted scalar with a real line end would lose them as content.
    if (/[ \t]$/m.test(node.value)) return JSON.stringify(node.value);
    return run(0);
  }
  const start = ctx.indentAtStart;
  // A value of a map starts after the key; an item of a list after its dash.
  const column =
    typeof start === 'number'
      ? ctx.indent.length - 2 + start
      : ctx.indent.length;
  const keyShift = typeof start === 'number' ? ctx.indent.length - 2 : 0;
  let width = FORMAT_LINE_WIDTH - Math.max(0, keyShift);
  let text = run(width);
  const minimum = FORMAT_MIN_CONTENT_WIDTH * 2;
  const isStr = typeof node.value === 'string' && node.value.includes(' ');
  while (
    isStr &&
    !text.includes('\n') &&
    column + text.length > FORMAT_LINE_WIDTH &&
    width > minimum
  ) {
    // The library folds a text longer than its width less the indentation.
    width = Math.max(
      minimum,
      Math.min(width - 1, text.length + ctx.indent.length - 2)
    );
    const next = run(width);
    if (next === text) break;
    text = next;
  }
  return text;
}

/** The tags with `stringifyFolded` in place of the default stringifier of a string. */
const FOLDING_TAGS = (tags: StringTag[]): StringTag[] =>
  tags.map((tag) =>
    tag.tag === 'tag:yaml.org,2002:str'
      ? {
          ...tag,
          stringify: (
            item: StringifyArgs[0] | string,
            ctx: StringifyArgs[1],
            onComment: StringifyArgs[2],
            onChompKeep: StringifyArgs[3]
          ) =>
            stringifyFolded(
              typeof item === 'string' ? { value: item } : item,
              ctx,
              onComment,
              onChompKeep
            ),
        }
      : tag
  );

const PRINT_OPTIONS = {
  indent: 2,
  indentSeq: true,
  flowCollectionPadding: false,
  lineWidth: FORMAT_LINE_WIDTH,
  minContentWidth: FORMAT_MIN_CONTENT_WIDTH,
};

function formatDocument(doc: Document): string {
  // The text puts its own `---` between the documents.
  if (doc.directives) {
    doc.directives.docStart = null;
    doc.directives.docEnd = false;
  }
  const firstKey = isMap(doc.contents) ? itemNode(doc.contents.items[0]) : null;
  orderDag(doc.contents);
  keepHead(doc, firstKey);
  normalise(doc.contents, null);
  spaceDag(doc.contents);
  cleanComment(doc);
  const text = doc.toString(PRINT_OPTIONS);
  return text
    .split('\n')
    .map((line) => line.replace(/[ \t]+$/, ''))
    .join('\n');
}

/**
 * The text in canonical form. Keys of the DAG and of each step go in the
 * order of DAG_KEY_ORDER and STEP_KEY_ORDER; maps and lists use the block
 * style, except a flow sequence of scalars that fits in FLOW_MAX_WIDTH;
 * the indentation is two spaces; one blank line separates the steps; the
 * text ends with one line end. A plain or quoted string wider than
 * FORMAT_LINE_WIDTH folds onto several lines, with the same value. Comments
 * and string styles stay. The result
 * uses LF. A text that does not parse comes back unchanged. The function
 * is idempotent and has no other input than the text.
 */
export function formatYaml(text: string): string {
  const docs = readDocuments(text, {
    ...EXACT_INTS,
    customTags: FOLDING_TAGS as never,
  });
  if (!docs || docs.length === 0) return text;
  // A text without content keeps its text.
  if (docs.every((doc) => doc.contents === null && !doc.commentBefore)) {
    return text;
  }
  try {
    // One line end only: the others can be content of a kept block scalar.
    const parts = docs.map((doc) => formatDocument(doc).replace(/\n$/, ''));
    return `${parts.join('\n---\n')}\n`;
  } catch {
    // The key order can put an alias before its anchor: no canonical form.
    return text;
  }
}

/** The smallest single edit that turns `before` into `after`, or null when equal. */
export function minimalEdit(
  before: string,
  after: string
): { from: number; to: number; insert: string } | null {
  if (before === after) return null;
  let head = 0;
  const max = Math.min(before.length, after.length);
  while (head < max && before.charCodeAt(head) === after.charCodeAt(head)) {
    head++;
  }
  let tail = 0;
  while (
    tail < max - head &&
    before.charCodeAt(before.length - 1 - tail) ===
      after.charCodeAt(after.length - 1 - tail)
  ) {
    tail++;
  }
  return {
    from: head,
    to: before.length - tail,
    insert: after.slice(head, after.length - tail),
  };
}

/** True when a flow collection of document 0 holds the text at `offset`. */
function inFlowCollection(text: string, offset: number): boolean {
  const doc = parseAllDocumentsAsServer(text)[0];
  if (!doc) return false;
  let inside = false;
  visit(doc, {
    Collection(_, node) {
      const range = node.range;
      if (node.flow && range && range[0] < offset && offset < range[1]) {
        inside = true;
        return visit.BREAK;
      }
    },
  });
  return inside;
}

/** What `formatStepRange` reads of a step. */
export type StepSpan = Pick<StepView, 'form' | 'range' | 'handler' | 'broken'>;

/**
 * The edit that puts one step in canonical form, or null when the step is
 * already canonical, is broken, does not parse, or sits in a flow
 * collection, where the block style cannot go. The edit lies inside
 * `step.range`: the rest of the text stays byte for byte. The step gets the
 * rules of `formatYaml` (key order, block style, no blank line, comments
 * kept, strings folded at FORMAT_LINE_WIDTH), keeps its indentation and uses
 * the line ends of the text. The step is printed under wrapper keys that give
 * it the indentation of a canonical file, so that its long strings fold at
 * the same column as `formatYaml` folds them, and the result is the one that
 * `formatYaml` gives to the same step.
 */
export function formatStepRange(text: string, step: StepSpan): TextEdit | null {
  if (step.broken !== undefined) return null;
  const { from, to } = step.range;
  if (inFlowCollection(text, from)) return null;
  const original = text.slice(from, to);
  const column = from - (text.lastIndexOf('\n', from - 1) + 1);
  // A step of the map form, and a handler, comes with its key.
  const keyed = step.form === 'mapEntry' || step.handler !== undefined;
  const levels = Math.max(0, Math.floor((keyed ? column : column - 2) / 2));
  // The columns of the step in the wrapper: the keys of a map step, or the
  // text after "- " of a list item.
  const canon = 2 * levels + (keyed ? 0 : 2);
  const wrapper = Array.from(
    { length: levels },
    (_, level) => `${' '.repeat(2 * level)}k${level}:`
  );
  const own = ' '.repeat(column);
  const inner = ' '.repeat(canon);
  const head = keyed ? inner : `${' '.repeat(2 * levels)}- `;
  // The blank lines after the range are content of a block scalar that
  // keeps its line ends (`|+`) at the end of the step.
  const rest = toLf(text.slice(to)).split('\n').slice(1);
  const filled = rest.findIndex((line) => line.trim() !== '');
  const blank = filled < 0 ? rest : rest.slice(0, filled);
  const source: string[] = [];
  for (const [index, line] of [
    ...toLf(original).split('\n'),
    ...blank,
  ].entries()) {
    if (index === 0) {
      source.push(head + line);
    } else if (line.trim() === '') {
      // The spaces of a blank line can be content of a block scalar.
      source.push(line.startsWith(own) ? inner + line.slice(column) : '');
    } else if (line.startsWith(own)) {
      source.push(inner + line.slice(column));
    } else {
      return null;
    }
  }
  const docs = readDocuments([...wrapper, ...source].join('\n'), {
    ...EXACT_INTS,
    customTags: FOLDING_TAGS as never,
  });
  const doc = docs?.length === 1 ? docs[0]! : null;
  if (!doc || doc.contents === null) return null;
  let node: unknown = doc.contents;
  for (let level = 0; level < levels; level++) {
    if (!isMap(node) || node.items.length !== 1) return null;
    node = node.items[0]!.value;
  }
  if (keyed) {
    if (!isMap(node) || node.items.length !== 1) return null;
    orderStep(node.items[0]!.value);
    normalise(node, null);
  } else {
    if (!isSeq(node) || node.items.length !== 1) return null;
    orderStep(node.items[0]);
    normalise(node.items[0], null);
  }
  let printed: string[];
  try {
    printed = doc
      .toString(PRINT_OPTIONS)
      .replace(/\n+$/, '')
      .split('\n')
      .slice(levels);
  } catch {
    // The key order can put an alias before its anchor.
    return null;
  }
  const lines: string[] = [];
  for (const [index, line] of printed.entries()) {
    const bare = line.replace(/[ \t]+$/, '');
    if (index === 0) {
      lines.push(bare.slice(canon));
    } else if (bare === '') {
      lines.push('');
    } else if (bare.startsWith(inner)) {
      lines.push(own + bare.slice(canon));
    } else {
      return null;
    }
  }
  const edit = minimalEdit(
    original,
    lines.join(text.includes('\r\n') ? '\r\n' : '\n')
  );
  return edit
    ? { from: from + edit.from, to: from + edit.to, insert: edit.insert }
    : null;
}
