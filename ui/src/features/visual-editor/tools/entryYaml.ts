// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isNode, isScalar, parseDocument, stringify } from 'yaml';
import { categoryPath, type ToolCategory } from './model';

/**
 * The YAML text of the node at `path` in the text of a step, at column 0, as
 * the step text writes it: the comments and the style of the node stay. Gives
 * an empty text when the step has no such node.
 */
function yamlAt(stepYaml: string, path: (string | number)[]): string {
  const node = parseDocument(stepYaml).getIn(path, true);
  if (!isNode(node) || !node.range) return '';
  if (isScalar(node)) return stringify({ name: node.value }).trimEnd();
  const [from, to] = node.range;
  const lineStart = stepYaml.lastIndexOf('\n', from - 1) + 1;
  const indent = from - lineStart;
  return stepYaml
    .slice(from, to)
    .trimEnd()
    .split('\n')
    .map((line, at) => {
      if (at === 0 || indent === 0) return line;
      let cut = 0;
      while (cut < indent && line[cut] === ' ') cut += 1;
      return line.slice(cut);
    })
    .join('\n');
}

/**
 * The YAML text of one tool entry, at column 0. `stepYaml` is the text of the
 * step. `listForm` is true for an entry of the list `tools: [name]`, which
 * sits at `with.tools[index]`: a name shows as the mapping that the first edit
 * writes.
 */
export function toolEntryYaml(
  stepYaml: string,
  category: ToolCategory,
  index: number,
  listForm = false
): string {
  return yamlAt(
    stepYaml,
    listForm ? ['with', 'tools', index] : [...categoryPath(category), index]
  );
}

/** The YAML text of the built-in tools of an agent step: its `with.tools.builtin` map. */
export function builtinToolsYaml(stepYaml: string): string {
  return yamlAt(stepYaml, ['with', 'tools', 'builtin']);
}
