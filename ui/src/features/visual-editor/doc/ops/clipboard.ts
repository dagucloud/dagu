// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Document,
  Pair,
  Scalar,
  YAMLMap,
  YAMLSeq,
  isMap,
  isNode,
  isScalar,
  isSeq,
  visit,
} from 'yaml';
import type { Node } from 'yaml';
import type {
  DagDocModel,
  EditErrorCode,
  EditResult,
  StepKey,
} from '../../contracts';
import { uniqueStepName, validateStepId, validateStepName } from '../names';
import { BASE_EMIT_OPTIONS, editDocument } from '../patch';
import type { MutateOutcome } from '../patch';
import {
  parseAllDocumentsAsServer,
  parseDocumentAsServer,
} from '../serverYaml';
import { explicitNameOf, findStep, getDagType, indexSteps } from '../stepIndex';
import type { IndexedStep } from '../stepIndex';
import { detectDependsStyle, readDepends, writeDepends } from './depends';
import { rewriteStepReferences } from './references';
import { ensureSteps, takenNames, usesBlankLines } from './structure';

/** The document that a clipboard operation reads: its text and its steps. */
export type DagDoc = DagDocModel;

function fail(code: EditErrorCode, message: string): EditResult {
  return { ok: false, code, message };
}

const INVALID_CLIPBOARD =
  'The clipboard holds no step: give a list of step mappings.';

// A pasted mapping is a step only when one of its keys is a step field. This
// keeps a random text that parses as YAML out of the DAG.
const STEP_FIELDS = new Set([
  'name',
  'id',
  'description',
  'run',
  'command',
  'script',
  'action',
  'call',
  'with',
  'depends',
  'executor',
  'parallel',
  'agent',
  'container',
  'routes',
  'output',
  'exec',
  'config',
  'foreach',
  'approval',
  'llm',
  'messages',
]);

function textOf(node: unknown): string | undefined {
  if (!isScalar(node)) return undefined;
  const value = node.value;
  return typeof value === 'string' ||
    typeof value === 'number' ||
    typeof value === 'bigint' ||
    typeof value === 'boolean'
    ? String(value)
    : undefined;
}

function hasAlias(node: Node): boolean {
  let found = false;
  visit(node, {
    Alias() {
      found = true;
      return visit.BREAK;
    },
  });
  return found;
}

/**
 * A copy of `node` that does not depend on its document: aliases become their
 * values, and anchors go, so that they never catch an alias of the target.
 */
function detach(node: Node, source: Document, target: Document): Node {
  const copy = (
    hasAlias(node) ? target.createNode(node.toJS(source)) : node.clone()
  ) as Node;
  visit(copy, {
    Node(_, n) {
      if (isNode(n)) delete n.anchor;
    },
  });
  delete copy.anchor;
  return copy;
}

/** The entry of a step as a mapping that names the step, as a list item does. */
function entryForCopy(
  step: IndexedStep,
  source: Document,
  target: Document
): YAMLMap | null {
  const node = step.target ?? step.node;
  if (!node) return null;
  if (isScalar(node)) {
    const map = new YAMLMap();
    const run = detach(node, source, target);
    run.commentBefore = undefined;
    run.spaceBefore = undefined;
    map.items.push(new Pair(new Scalar('run'), run));
    return map;
  }
  if (!isMap(node)) return null;
  const map = detach(node, source, target) as YAMLMap;
  map.spaceBefore = undefined;
  // A map entry has its name in the key: the server ignores a `name:` value.
  const name = step.loc.form === 'mapEntry' ? explicitNameOf(step) : undefined;
  if (name !== undefined) {
    map.delete('name');
    map.items.unshift(new Pair(new Scalar('name'), new Scalar(name)));
  }
  return map;
}

/** Integers stay exact beyond 2^53, as the server reads them. */
const EXACT_INTS = { intAsBigInt: true };

function firstDocument(text: string): Document | null {
  const doc = parseAllDocumentsAsServer(text, EXACT_INTS)[0];
  return doc && doc.errors.length === 0 ? (doc as Document) : null;
}

/**
 * The YAML of the steps, as the clipboard holds it: a list of step mappings,
 * in the order of the file. Empty when there is none.
 */
export function copyStepsYaml(doc: DagDoc, steps: StepKey[]): string {
  const wanted = new Set(steps);
  if (!doc.ok || wanted.size === 0) return '';
  const source = firstDocument(doc.text);
  if (!source) return '';
  const out = new Document();
  const seq = new YAMLSeq();
  const indexed = indexSteps(source);
  // Map form lists the steps by name; the clipboard keeps the file order.
  const inFile = [...indexed.steps].sort(
    (a, b) => filePosition(a) - filePosition(b)
  );
  for (const step of [...inFile, ...indexed.handlers]) {
    if (!wanted.has(step.key)) continue;
    const entry = entryForCopy(step, source, out);
    if (entry) seq.items.push(entry);
  }
  if (seq.items.length === 0) return '';
  out.contents = seq;
  return out.toString(BASE_EMIT_OPTIONS);
}

function isStepMap(node: unknown): node is YAMLMap {
  if (!isMap(node) || node.items.length === 0) return false;
  let known = false;
  for (const pair of node.items) {
    const key = textOf(pair.key);
    if (key === undefined) return false;
    if (STEP_FIELDS.has(key)) known = true;
  }
  return known;
}

/** The step mappings of a `steps` value: a list, or a map keyed by name. */
function stepsOf(node: unknown): YAMLMap[] | null {
  if (isSeq(node)) {
    const items = node.items;
    return items.length > 0 && items.every(isStepMap) ? items : null;
  }
  if (isMap(node) && node.items.length > 0) {
    const out: YAMLMap[] = [];
    for (const pair of node.items) {
      const name = textOf(pair.key);
      const value = pair.value;
      if (name === undefined || !isMap(value)) return null;
      const entry = value.clone() as YAMLMap;
      entry.delete('name');
      entry.items.unshift(new Pair(new Scalar('name'), new Scalar(name)));
      out.push(entry);
    }
    return out;
  }
  return null;
}

/**
 * Reads a clipboard text: a list of step mappings, one step mapping, or a
 * whole DAG whose steps are taken. Returns null for anything else.
 */
function readClipboard(
  yaml: string
): { doc: Document; entries: YAMLMap[] } | null {
  const doc = parseDocumentAsServer(yaml, EXACT_INTS);
  if (doc.errors.length > 0) return null;
  const root = doc.contents;
  let entries: YAMLMap[] | null = null;
  if (isMap(root) && root.has('steps')) {
    entries = stepsOf(root.get('steps', true));
  } else if (isSeq(root)) {
    entries = stepsOf(root);
  } else if (isStepMap(root)) {
    entries = [root];
  }
  return entries ? { doc, entries } : null;
}

const MAX_SUFFIX = 10_000;

/** `base_2`, `base_3`, ... : the first one that is valid and free. */
function freshName(base: string, used: Set<string>): string {
  for (let n = 2; n < MAX_SUFFIX; n++) {
    const candidate = `${base}_${n}`;
    if (validateStepName(candidate) !== null) break;
    if (!used.has(candidate)) return candidate;
  }
  return uniqueStepName(base, used);
}

/** As `freshName`, inside the length limit of an id. */
function freshId(base: string, used: Set<string>): string {
  for (let n = 2; n < MAX_SUFFIX; n++) {
    const suffix = `_${n}`;
    const candidate = `${base.slice(0, 40 - suffix.length)}${suffix}`;
    if (validateStepId(candidate) === null && !used.has(candidate)) {
      return candidate;
    }
  }
  return uniqueStepName(base, used);
}

/** The `name` or `id` of an entry as the server reads it: trimmed, if set. */
function identityOf(entry: YAMLMap, key: 'name' | 'id'): string | undefined {
  const text = textOf(entry.get(key, true))?.trim();
  return text === '' ? undefined : text;
}

interface Renamed {
  entry: YAMLMap;
  name?: string;
  id?: string;
}

/** Gives each pasted step a name and an id that the target does not use. */
function renameEntries(
  entries: YAMLMap[],
  taken: Set<string>
): {
  renamed: Renamed[];
  names: Map<string, string>;
  ids: Map<string, string>;
  pasted: Set<string>;
} {
  const pasted = new Set<string>();
  for (const entry of entries) {
    for (const key of ['name', 'id'] as const) {
      const value = identityOf(entry, key);
      if (value !== undefined) pasted.add(value);
    }
  }
  // A new identity is never an old identity of a pasted step, so that the
  // rewrites below never chain.
  const used = new Set(taken);
  const avoid = () => new Set([...used, ...pasted]);
  const names = new Map<string, string>();
  const ids = new Map<string, string>();
  const renamed: Renamed[] = [];
  for (const entry of entries) {
    const oldName = identityOf(entry, 'name');
    const oldId = identityOf(entry, 'id');
    let id = oldId;
    if (oldId !== undefined && used.has(oldId)) {
      id = freshId(oldId, avoid());
      ids.set(oldId, id);
    }
    if (id !== undefined) used.add(id);
    let name = oldName;
    if (oldName !== undefined && oldName === oldId) {
      name = id;
    } else if (oldName !== undefined && used.has(oldName)) {
      name = freshName(oldName, avoid());
    } else if (oldName === undefined && oldId === undefined) {
      const action = textOf(entry.get('action', true));
      name = uniqueStepName(action ?? 'step', avoid());
    }
    if (oldName !== undefined && name !== undefined && name !== oldName) {
      names.set(oldName, name);
    }
    if (name !== undefined) used.add(name);
    renamed.push({ entry, name, id });
  }
  return { renamed, names, ids, pasted };
}

function setScalar(map: YAMLMap, key: string, value: string): void {
  const node = map.get(key, true);
  if (isScalar(node)) node.value = value;
  else map.set(key, new Scalar(value));
}

/** Calls `fn` for each string scalar below `node`, keys left out. */
function eachString(node: unknown, fn: (scalar: Scalar) => void): void {
  if (isMap(node)) {
    for (const pair of node.items) eachString(pair.value, fn);
  } else if (isSeq(node)) {
    for (const item of node.items) eachString(item, fn);
  } else if (isScalar(node) && typeof node.value === 'string') {
    fn(node);
  }
}

/** The scalars that name a step outside `depends`: route and rewind targets. */
function targetScalars(entry: YAMLMap): Scalar[] {
  const out: Scalar[] = [];
  const list = (node: unknown) => {
    if (isScalar(node)) out.push(node);
    else if (isSeq(node)) for (const item of node.items) list(item);
  };
  list(entry.getIn(['approval', 'rewind_to'], true));
  list(entry.getIn(['with', 'push_back', 'rewind_to'], true));
  for (const routes of [
    entry.get('routes', true),
    entry.getIn(['with', 'routes'], true),
  ]) {
    if (isMap(routes)) for (const pair of routes.items) list(pair.value);
  }
  return out;
}

/** Where the pasted entries go in the steps collection. */
function insertIndex(
  steps: YAMLSeq | YAMLMap,
  anchor: IndexedStep | null
): number {
  if (!anchor) return steps.items.length;
  const { loc } = anchor;
  if (isSeq(steps) && 'seqIndex' in loc) return loc.seqIndex + 1;
  if (isMap(steps) && loc.form === 'mapEntry') {
    const at = steps.items.indexOf(loc.pair);
    if (at >= 0) return at + 1;
  }
  return steps.items.length;
}

/**
 * Adds the steps of a clipboard YAML after `after`, or at the end when it is
 * null. Names stay unique, dependencies on absent steps are dropped and
 * references to renamed steps are written again. Invalid input is refused.
 */
export function pasteStepsOp(
  doc: DagDoc,
  yaml: string,
  after: StepKey | null
): EditResult {
  const clipboard = readClipboard(yaml);
  if (!clipboard) return fail('invalidYaml', INVALID_CLIPBOARD);
  return editDocument(doc.text, (target): MutateOutcome => {
    const anchor = after === null ? null : findStep(target, after);
    if (after !== null && !anchor) {
      return {
        ok: false,
        code: 'unknownStep',
        message: `No step has the key ${after}.`,
      };
    }
    const steps = ensureSteps(target);
    if ('ok' in steps) return steps;

    const taken = takenNames(target, {});
    const entries = clipboard.entries.map(
      (entry) => detach(entry, clipboard.doc, target) as YAMLMap
    );
    const { renamed, names, ids, pasted } = renameEntries(entries, taken);
    const type = getDagType(target);
    const keepsDepends = type !== 'chain' && type !== 'agent';
    const style = detectDependsStyle(target);
    const lookup = (value: string): string | undefined =>
      ids.get(value) ??
      names.get(value) ??
      (pasted.has(value) ? value : undefined);

    for (const { entry, name, id } of renamed) {
      if (id !== undefined && identityOf(entry, 'id') !== id) {
        setScalar(entry, 'id', id);
      }
      const current = readDepends(entry.get('depends', true));
      const next: string[] = [];
      for (const d of current) {
        const value = lookup(d) ?? (taken.has(d) ? d : undefined);
        if (value !== undefined && !next.includes(value)) next.push(value);
      }
      if (!keepsDepends) entry.delete('depends');
      else if (next.join('\0') !== current.join('\0')) {
        writeDepends(target, entry, next, style);
      }
      for (const scalar of targetScalars(entry)) {
        const value = lookup(String(scalar.value));
        if (value !== undefined) scalar.value = value;
      }
      eachString(entry, (scalar) => {
        let value = String(scalar.value);
        for (const [from, to] of [...ids, ...names]) {
          value = rewriteStepReferences(value, from, to);
        }
        if (value !== scalar.value) scalar.value = value;
      });
      if (isSeq(steps)) {
        if (name !== undefined && identityOf(entry, 'name') !== name) {
          if (entry.has('name')) setScalar(entry, 'name', name);
          else
            entry.items.unshift(new Pair(new Scalar('name'), new Scalar(name)));
        }
      } else {
        entry.delete('name');
      }
    }

    const spaced = usesBlankLines(steps);
    const at = insertIndex(steps, anchor);
    let added: unknown[];
    if (isSeq(steps)) {
      added = renamed.map(({ entry }) => {
        entry.spaceBefore = spaced || undefined;
        return entry;
      });
    } else {
      added = renamed.map(({ entry, name, id }) => {
        // renameEntries gives a name to each step that has no id.
        const key = new Scalar(name ?? id);
        key.spaceBefore = spaced || undefined;
        // The comment above a map entry stands above its key.
        key.commentBefore = entry.commentBefore;
        entry.commentBefore = undefined;
        entry.spaceBefore = undefined;
        return new Pair(key, entry);
      });
    }
    const items: unknown[] = steps.items;
    items.splice(at, 0, ...added);
    const first = added[0];
    const select = indexSteps(target).steps.find((step) =>
      step.loc.form === 'mapEntry'
        ? step.loc.pair === first
        : step.node === first
    );
    return select ? { ok: true, select: select.key } : { ok: true };
  });
}

/** Position of an entry in the file, or -1 for a handler. */
function filePosition(step: IndexedStep): number {
  const { loc } = step;
  if ('seqIndex' in loc) return loc.seqIndex;
  if (loc.form === 'mapEntry') return loc.map.items.indexOf(loc.pair);
  return -1;
}

/** Adds a copy of each step after the last of them in the file. */
export function duplicateStepsOp(doc: DagDoc, steps: StepKey[]): EditResult {
  if (steps.length === 0) return fail('noop', 'No step to duplicate.');
  const source = firstDocument(doc.text);
  if (!source) return fail('parseError', 'The YAML does not parse.');
  let last: IndexedStep | null = null;
  for (const key of steps) {
    const step = findStep(source, key);
    if (!step) return fail('unknownStep', `No step has the key ${key}.`);
    // A copy of a handler would be pasted as a step that runs on every run.
    if (step.loc.form === 'handler') {
      return fail('noop', 'A handler cannot be duplicated.');
    }
    if (!last || filePosition(step) >= filePosition(last)) last = step;
  }
  const yaml = copyStepsYaml(doc, steps);
  if (yaml === '') return fail('noop', 'The steps have no entry to copy.');
  const after = last && filePosition(last) >= 0 ? last.key : null;
  return pasteStepsOp(doc, yaml, after);
}
