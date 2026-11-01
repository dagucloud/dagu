// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, isNode } from 'yaml';
import type { DagDocModel, StepView } from '../contracts';
import { parseDocumentAsServer } from '../doc';

/** The line without up to `indent` of its leading spaces. */
function cutIndent(line: string, indent: number): string {
  let cut = 0;
  while (cut < indent && line[cut] === ' ') {
    cut += 1;
  }
  return line.slice(cut);
}

/**
 * The value of the one `key: value` pair of `text`, at column 0. A value
 * on the line of the key keeps the rest of that line; a block value is the
 * lines below the key.
 */
function pairValueText(text: string): string {
  const doc = parseDocumentAsServer(text);
  const pair = isMap(doc.contents) ? doc.contents.items[0] : undefined;
  const keyEnd = isNode(pair?.key) ? pair.key.range?.[1] : undefined;
  const colon = keyEnd === undefined ? -1 : text.indexOf(':', keyEnd);
  if (colon === -1) {
    return text;
  }
  const lineEnd = text.indexOf('\n', colon);
  const rest = text.slice(colon + 1, lineEnd === -1 ? undefined : lineEnd);
  if (rest.trim() !== '' && !rest.trim().startsWith('#')) {
    return text.slice(colon + 1).trimStart();
  }
  if (lineEnd === -1) {
    return '';
  }
  const lines = text.slice(lineEnd + 1).split('\n');
  const first = lines.find(
    (line) => line.trim() !== '' && !line.trim().startsWith('#')
  );
  const indent = first ? first.length - first.trimStart().length : 0;
  return lines.map((line) => cutIndent(line, indent)).join('\n');
}

/**
 * YAML of one step at column 0: the text of `step.range`, with the indent
 * of the entry (`startColumn - 1` spaces) removed from each line after the
 * first. Lines with less indent (blank lines) lose their spaces. The range
 * of a map entry or a handler starts at its key; the text is its value,
 * which is what `replaceStepYaml` replaces.
 */
export function stepYamlFromModel(model: DagDocModel, step: StepView): string {
  const indent = Math.max(0, step.range.startColumn - 1);
  const text = model.text
    .slice(step.range.from, step.range.to)
    .split('\n')
    .map((line, index) => (index === 0 ? line : cutIndent(line, indent)))
    .join('\n');
  return step.form === 'mapEntry' || step.handler !== undefined
    ? pairValueText(text)
    : text;
}

export interface StepYamlCheck {
  ok: boolean;
  /**
   * The parsed step when `ok`. It is absent when an alias names an anchor
   * outside the text: the anchor can be elsewhere in the DAG.
   */
  value?: Record<string, unknown>;
  /** Syntax error, with the 1-based line and column when known. */
  error?: { message: string; line?: number; column?: number };
}

/** Parses the YAML of one step; the text must hold one mapping. */
export function parseStepYaml(text: string): StepYamlCheck {
  const doc = parseDocumentAsServer(text);
  const [first] = doc.errors;
  if (first) {
    const pos = first.linePos?.[0];
    // The message ends with " at line L, column C:" and a code frame.
    const message = first.message
      .split('\n')[0]!
      .replace(/ at line \d+, column \d+:?$/, '');
    return {
      ok: false,
      error: { message, line: pos?.line, column: pos?.col },
    };
  }
  if (!isMap(doc.contents)) {
    return {
      ok: false,
      error: { message: 'The step must be a YAML mapping.' },
    };
  }
  try {
    return { ok: true, value: doc.toJS() as Record<string, unknown> };
  } catch {
    // An alias to an anchor outside the text: the edit of the DAG resolves
    // it, or the validation of the DAG reports it.
    return { ok: true };
  }
}
