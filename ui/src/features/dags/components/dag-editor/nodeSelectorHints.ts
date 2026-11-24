// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor, IDisposable } from 'monaco-editor';
import { scalarAtField } from '@/features/visual-editor/doc/issues';
import { parseDagDocLenient } from '@/features/visual-editor/doc/parse';

type Monaco = typeof import('monaco-editor');

/** A literal selector of a step's nodes, and the last row of its value. */
export interface NodeSelectorPoint {
  selector: string;
  line: number;
}

/** What the hints of a whole-file editor need of their host. */
export interface NodeSelectorHintSource {
  /** The literal selectors of `text`, read when Monaco asks. */
  points(text: string): readonly NodeSelectorPoint[];
  /** The number of nodes a selector matches, or undefined while unknown. */
  count(selector: string): number | undefined;
  /** The text of the hint for a count. */
  label(count: number): string;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Returns a reader of the literal `nodes` selectors of a DAG text: a string,
 * a list of terms, which the server joins with commas, or the `labels` of an
 * object. A selector with a reference resolves only when the step starts, so
 * it is left out. The reader keeps the selectors of the last text it read.
 */
export function nodeSelectorReader(): (
  text: string
) => readonly NodeSelectorPoint[] {
  let last: { text: string; points: NodeSelectorPoint[] } | null = null;
  return (text) => {
    if (last?.text === text) return last.points;
    const model = parseDagDocLenient(text);
    const points = model.steps.flatMap((step) => {
      const value = step.value.nodes;
      const path = isRecord(value)
        ? `${step.key}.nodes.labels`
        : `${step.key}.nodes`;
      const terms = isRecord(value) ? value.labels : value;
      let selector: string;
      let at: string;
      if (typeof terms === 'string') {
        selector = terms.trim();
        at = path;
      } else if (
        Array.isArray(terms) &&
        terms.length > 0 &&
        terms.every((term) => typeof term === 'string')
      ) {
        selector = terms.map((term) => term.trim()).join(',');
        at = `${path}[${terms.length - 1}]`;
      } else {
        return [];
      }
      if (selector === '' || selector.includes('${')) return [];
      const range = scalarAtField(model, at)?.range;
      return range ? [{ selector, line: range.endLine }] : [];
    });
    last = { text, points };
    return points;
  };
}

/**
 * Counts the nodes of selectors through `fetchCount`, once for each
 * selector. A count that arrives calls `onCount`; a selector whose count
 * cannot be read stays unknown.
 */
export function nodeSelectorCounter(
  fetchCount: (selector: string) => Promise<number | null>,
  onCount: () => void
): (selector: string) => number | undefined {
  const counts = new Map<string, number | null>();
  return (selector) => {
    if (!counts.has(selector)) {
      counts.set(selector, null);
      fetchCount(selector).then(
        (count) => {
          if (count === null) return;
          counts.set(selector, count);
          onCount();
        },
        () => {}
      );
    }
    return counts.get(selector) ?? undefined;
  };
}

/** The registration of the hints of one editor. */
export interface NodeSelectorHintRegistration extends IDisposable {
  /** Draws the hints again: a count arrived. */
  refresh(): void;
}

/**
 * Puts a hint at the end of the value of each literal `nodes` selector of
 * the model of `codeEditor`: the number of nodes it matches now. The hint
 * takes no row, so the text does not move when the count arrives. Dispose
 * the result when the editor unmounts.
 */
export function registerNodeSelectorHints(
  monaco: Monaco,
  codeEditor: editor.IStandaloneCodeEditor,
  read: () => NodeSelectorHintSource | null
): NodeSelectorHintRegistration {
  const changed = new monaco.Emitter<void>();
  const provider = monaco.languages.registerInlayHintsProvider('yaml', {
    onDidChangeInlayHints: changed.event,
    provideInlayHints(model, range) {
      const source = read();
      if (!source || model !== codeEditor.getModel()) {
        return { hints: [], dispose() {} };
      }
      const lines = model.getLineCount();
      const hints = source.points(model.getValue()).flatMap((point) => {
        if (
          point.line < Math.max(1, range.startLineNumber) ||
          point.line > Math.min(lines, range.endLineNumber)
        ) {
          return [];
        }
        const count = source.count(point.selector);
        if (count === undefined) return [];
        return [
          {
            position: {
              lineNumber: point.line,
              column: model.getLineMaxColumn(point.line),
            },
            label: source.label(count),
            paddingLeft: true,
          },
        ];
      });
      return { hints, dispose() {} };
    },
  });
  return {
    refresh: () => changed.fire(),
    dispose() {
      provider.dispose();
      changed.dispose();
    },
  };
}
