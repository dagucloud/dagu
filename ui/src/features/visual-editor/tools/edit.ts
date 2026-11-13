// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import {
  Scalar,
  YAMLMap,
  YAMLSeq,
  isMap,
  isNode,
  isScalar,
  isSeq,
  parseDocument,
} from 'yaml';
import type { Document } from 'yaml';
import type { EditResult, StepKey } from '../contracts';
import { editDocument } from '../doc/patch';
import type { MutateOutcome } from '../doc/patch';
import { findStep, isAliasStep } from '../doc/stepIndex';
import { categoryPath, type ToolCategory } from './model';

/** The limits of a chat step that the Tools section edits. */
export type ToolLimitKey = 'max_tools' | 'tool_result_max_bytes';

/**
 * One change of the tools of a step. `entry` is the plain value of a tool
 * entry: a mapping with a `name`. `category` is the list that the change reads
 * or writes (`with.dags` or `with.tools.mcp`), and `index` is the place of the
 * entry in it.
 * `path` is relative to the entry and `value` undefined removes the key.
 * `replace` swaps the whole entry for the mapping of a YAML text.
 */
export type ToolsChange =
  | { kind: 'add'; category: ToolCategory; entry: unknown; at?: number }
  | { kind: 'remove'; category: ToolCategory; index: number }
  | { kind: 'move'; category: ToolCategory; from: number; to: number }
  | { kind: 'replace'; category: ToolCategory; index: number; yaml: string }
  | {
      kind: 'update';
      category: ToolCategory;
      index: number;
      path: (string | number)[];
      value: unknown;
    }
  | { kind: 'setLimit'; key: ToolLimitKey; value: number | undefined };

/** One change of the skills of a step. `name` is a skill name. */
export type SkillsChange =
  | { kind: 'add'; name: string }
  | { kind: 'remove'; index: number };

type Container = YAMLMap | YAMLSeq;

/** A failure that the change reports as an edit error. */
class ToolsEditError extends Error {}

function isContainer(node: unknown): node is Container {
  return isMap(node) || isSeq(node);
}

function isNull(node: unknown): boolean {
  return (
    node === undefined ||
    node === null ||
    (isScalar(node) && node.value === null)
  );
}

function childOf(container: Container, key: string | number): unknown {
  return isMap(container)
    ? container.get(key as string, true)
    : container.items[key as number];
}

function putChild(
  doc: Document,
  container: Container,
  key: string | number,
  value: unknown
): void {
  const node = doc.createNode(value);
  if (isMap(container)) {
    container.set(key as string, node);
    return;
  }
  const index = key as number;
  if (index > container.items.length) {
    throw new ToolsEditError(`The list index ${index} is out of range.`);
  }
  container.items[index] = node;
}

function deleteChild(container: Container, key: string | number): boolean {
  return isMap(container)
    ? container.delete(key as string)
    : container.delete(key as number);
}

/**
 * Sets one value under `root`, or removes it when `value` is undefined.
 * Missing maps on the path are created. A string that replaces a string
 * keeps the quote style of the old scalar. A map or a list that the removal
 * leaves empty goes too, but `root` stays. Returns false when there is nothing to
 * remove.
 */
function setUnder(
  doc: Document,
  root: Container,
  path: (string | number)[],
  value: unknown
): boolean {
  const last = path[path.length - 1];
  if (last === undefined) throw new ToolsEditError('The path is empty.');
  const removing = value === undefined;
  const trail: Container[] = [root];
  let container = root;
  for (let i = 0; i < path.length - 1; i++) {
    const key = path[i] as string | number;
    const child = childOf(container, key);
    if (isContainer(child)) {
      container = child;
    } else if (isNull(child)) {
      if (removing) return false;
      const created =
        typeof path[i + 1] === 'number' ? new YAMLSeq() : new YAMLMap();
      putChild(doc, container, key, created);
      container = created;
    } else {
      throw new ToolsEditError(
        `Cannot reach "${path.join('.')}": "${key}" is a scalar.`
      );
    }
    trail.push(container);
  }
  if (removing) {
    if (!deleteChild(container, last)) return false;
    for (let i = trail.length - 1; i > 0; i--) {
      const current = trail[i] as Container;
      if (current.items.length > 0) break;
      deleteChild(trail[i - 1] as Container, path[i - 1] as string | number);
    }
    return true;
  }
  const existing = childOf(container, last);
  if (
    typeof value === 'string' &&
    isScalar(existing) &&
    typeof existing.value === 'string'
  ) {
    existing.value = value;
    return true;
  }
  putChild(doc, container, last, value);
  return true;
}

function stepMap(doc: Document, key: StepKey): YAMLMap {
  const step = findStep(doc, key);
  if (!step) throw new ToolsEditError(`No step has the key "${key}".`);
  if (isAliasStep(step) || !isMap(step.node)) {
    throw new ToolsEditError(`Step "${key}" is not a mapping.`);
  }
  return step.node;
}

/**
 * Rewrites the list form `tools: [a, b]` to the key `dags`:
 * `dags: [{name: a}, {name: b}]`. The names keep their scalar nodes, so a
 * quote style and a trailing comment stay. The new key takes the place of
 * `tools`. When `with.dags` is a list already, the names go in front of its
 * items and `tools` goes. A list item that is not a string stays as it is, in
 * `dags`: the validation of the file reports it. Does nothing when
 * `with.tools` is not a list.
 */
function upgradeListForm(root: YAMLMap): void {
  const withMap = root.get('with', true);
  if (!isMap(withMap)) return;
  const list = withMap.get('tools', true);
  if (!isSeq(list)) return;
  const items: unknown[] = [];
  for (const item of list.items) {
    if (isScalar(item) && typeof item.value === 'string') {
      const entry = new YAMLMap();
      entry.commentBefore = item.commentBefore;
      item.commentBefore = undefined;
      entry.set('name', item);
      items.push(entry);
    } else {
      items.push(item);
    }
  }
  const existing = withMap.get('dags', true);
  if (isSeq(existing)) {
    existing.items.unshift(...items);
    withMap.delete('tools');
    return;
  }
  const dags = new YAMLSeq();
  // The comments of the list go with it, as in a step that has `dags`.
  dags.commentBefore = list.commentBefore;
  dags.comment = list.comment;
  dags.items.push(...items);
  const pair = withMap.items.find(
    (item) => isScalar(item.key) && item.key.value === 'tools'
  );
  if (!pair) return;
  const key = new Scalar('dags');
  if (isScalar(pair.key)) {
    key.commentBefore = pair.key.commentBefore;
    key.spaceBefore = pair.key.spaceBefore;
  }
  pair.key = key;
  pair.value = dags;
}

function skillsList(
  doc: Document,
  root: YAMLMap,
  create: boolean
): YAMLSeq | null {
  const existing = root.getIn(['with', 'skills'], true);
  if (isSeq(existing)) return existing;
  if (!isNull(existing)) {
    throw new ToolsEditError('The skills of the step are not a list.');
  }
  if (!create) return null;
  const seq = new YAMLSeq();
  setUnder(doc, root, ['with', 'skills'], seq);
  return seq;
}

/**
 * The list of one category, created when `create` is set: `with.dags` for
 * the DAG tools, `with.tools.mcp` for the MCP servers.
 */
function categoryList(
  doc: Document,
  root: YAMLMap,
  category: ToolCategory,
  create: boolean
): YAMLSeq | null {
  const path = categoryPath(category);
  const tools = root.getIn(['with', 'tools'], true);
  if (category === 'mcp' && !isNull(tools) && !isMap(tools)) {
    throw new ToolsEditError('The tools of the step are not a map.');
  }
  const existing = root.getIn(path, true);
  if (isSeq(existing)) return existing;
  if (!isNull(existing)) {
    throw new ToolsEditError(
      `The tools "${path.slice(1).join('.')}" are not a list.`
    );
  }
  if (!create) return null;
  const seq = new YAMLSeq();
  setUnder(doc, root, path, seq);
  return seq;
}

function entryAt(seq: YAMLSeq | null, index: number): unknown {
  if (
    !seq ||
    !Number.isInteger(index) ||
    index < 0 ||
    index >= seq.items.length
  ) {
    throw new ToolsEditError(`No tool entry at ${index}.`);
  }
  return seq.items[index];
}

function change(doc: Document, key: StepKey, op: ToolsChange): MutateOutcome {
  const root = stepMap(doc, key);
  // The first edit of the entries leaves the list form. A limit is no entry:
  // it never rewrites the key.
  if (op.kind !== 'setLimit') upgradeListForm(root);
  switch (op.kind) {
    case 'add': {
      const seq = categoryList(doc, root, op.category, true)!;
      const at =
        op.at === undefined
          ? seq.items.length
          : Math.max(0, Math.min(op.at, seq.items.length));
      seq.items.splice(at, 0, doc.createNode(op.entry));
      return;
    }
    case 'remove': {
      const seq = categoryList(doc, root, op.category, false);
      entryAt(seq, op.index);
      seq!.items.splice(op.index, 1);
      if (seq!.items.length === 0) {
        setUnder(doc, root, categoryPath(op.category), undefined);
      }
      return;
    }
    case 'move': {
      const seq = categoryList(doc, root, op.category, false);
      entryAt(seq, op.from);
      if (!Number.isInteger(op.to) || op.to < 0 || op.to >= seq!.items.length) {
        throw new ToolsEditError(`No tool entry at ${op.to}.`);
      }
      const [item] = seq!.items.splice(op.from, 1);
      seq!.items.splice(op.to, 0, item);
      return;
    }
    case 'replace': {
      const seq = categoryList(doc, root, op.category, false);
      const old = entryAt(seq, op.index);
      const parsed = parseDocument(op.yaml);
      const error = parsed.errors[0];
      if (error) {
        throw new ToolsEditError(
          (error.message.split('\n')[0] ?? error.message).replace(/:$/, '')
        );
      }
      if (!isMap(parsed.contents)) {
        throw new ToolsEditError('The tool entry YAML must be one mapping.');
      }
      const node = parsed.contents.clone() as YAMLMap;
      // The comment above the item belongs to the item node.
      if (isNode(old) && old.commentBefore !== undefined) {
        node.commentBefore ??= old.commentBefore;
      }
      seq!.items[op.index] = node;
      return;
    }
    case 'update': {
      const seq = categoryList(doc, root, op.category, false);
      const entry = entryAt(seq, op.index);
      if (!isMap(entry)) {
        throw new ToolsEditError(
          `The tool entry ${op.index} is not a mapping.`
        );
      }
      if (!setUnder(doc, entry, op.path, op.value)) {
        return { ok: false, code: 'noop', message: 'The field is absent.' };
      }
      return;
    }
    case 'setLimit': {
      if (!setUnder(doc, root, ['with', op.key], op.value)) {
        return { ok: false, code: 'noop', message: 'The field is absent.' };
      }
      return;
    }
  }
}

function changeSkills(
  doc: Document,
  key: StepKey,
  op: SkillsChange
): MutateOutcome {
  const root = stepMap(doc, key);
  switch (op.kind) {
    case 'add': {
      skillsList(doc, root, true)!.items.push(doc.createNode(op.name));
      return;
    }
    case 'remove': {
      const seq = skillsList(doc, root, false);
      entryAt(seq, op.index);
      seq!.items.splice(op.index, 1);
      if (seq!.items.length === 0)
        setUnder(doc, root, ['with', 'skills'], undefined);
      return;
    }
  }
}

/**
 * Applies one change to the skills of a step of document 0, as
 * `editStepTools` does for the tools.
 */
export function editStepSkills(
  text: string,
  step: StepKey,
  op: SkillsChange
): EditResult {
  return editDocument(text, (doc) => {
    try {
      return changeSkills(doc, step, op);
    } catch (error) {
      if (error instanceof ToolsEditError) {
        return { ok: false, code: 'invalidYaml', message: error.message };
      }
      throw error;
    }
  });
}

/**
 * Applies one change to the tools of a step of document 0. The edits touch
 * only the lines next to the change: the comments and the formatting of the
 * rest of the text stay. A change of an entry first rewrites the list form
 * `tools: [name]` to the key `dags`. A failure gives an error code and an
 * English message.
 */
export function editStepTools(
  text: string,
  step: StepKey,
  op: ToolsChange
): EditResult {
  return editDocument(text, (doc) => {
    try {
      return change(doc, step, op);
    } catch (error) {
      if (error instanceof ToolsEditError) {
        return { ok: false, code: 'invalidYaml', message: error.message };
      }
      throw error;
    }
  });
}
