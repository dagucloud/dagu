// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, isScalar, isSeq, parseDocument } from 'yaml';
import { getYamlPathAtOffset } from '@/hooks/useYamlCursorPath';
import { translateStatic } from '@/i18n/translateStatic';
import type { ReferenceCandidate } from '../contracts';
import {
  candidatesAt,
  editorLocale,
  isRunFieldPath,
  type ReferenceEditorState,
} from './editorState';
import { registerReferenceHover } from './monacoHover';
import { findOpenReference } from './tokens';

type Monaco = typeof import('monaco-editor');

const TRIGGER_CHARACTERS = ['$', '{', '.'];

export interface ReferenceCompletions {
  items: ReferenceCandidate[];
  /** Start of the text that a chosen item replaces. */
  from: number;
  /** End of the text that a chosen item replaces. */
  to: number;
  inRunField: boolean;
}

// Names of the entries of root `consts` before the entry at `index`.
function constNamesBefore(text: string, index: number): Set<string> {
  const names = new Set<string>();
  const consts = parseDocument(text).get('consts');
  if (!isSeq(consts)) {
    return names;
  }
  for (const entry of consts.items.slice(0, index)) {
    if (!isMap(entry)) {
      continue;
    }
    for (const { key } of entry.items) {
      if (isScalar(key)) {
        names.add(String(key.value));
      }
    }
  }
  return names;
}

// Candidates that resolve in the field at `path`, by the field evaluation
// matrix of spec 003. Null for a field that keeps references as literal text.
function fieldCandidates(
  text: string,
  offset: number,
  path: string[],
  state: ReferenceEditorState
): ReferenceCandidate[] | null {
  const [root, entry, key] = path;
  if (root === 'params' && key !== 'eval') {
    return null;
  }
  const items = candidatesAt(text, offset, state);
  if (root !== 'consts') {
    return items;
  }
  const earlier = constNamesBefore(text, Number(entry));
  return items.filter(
    (item) => item.group === 'consts' && earlier.has(item.label)
  );
}

/**
 * Candidates for the offset, or null when no completion applies.
 * An open "${" applies unless the field is literal. A cursor in a YAML value
 * applies only for an explicit request.
 */
export function computeReferenceCompletions(
  text: string,
  offset: number,
  state: ReferenceEditorState,
  opts: { explicit: boolean }
): ReferenceCompletions | null {
  const cursor = getYamlPathAtOffset(text, offset);
  const range = replacedRange(text, offset, opts.explicit && cursor.isInValue);
  if (!range) {
    return null;
  }
  const items = fieldCandidates(text, offset, cursor.path, state);
  if (!items) {
    return null;
  }
  return {
    items,
    ...range,
    inRunField: isRunFieldPath(cursor.path),
  };
}

// The text that a chosen item replaces, or null when no completion applies:
// an open "${", or the empty text at the offset when `atOffset` allows it.
function replacedRange(
  text: string,
  offset: number,
  atOffset: boolean
): { from: number; to: number } | null {
  const open = findOpenReference(text, offset);
  if (open) {
    // The editor closes a brace that the user types: replace it too.
    return { from: open.from, to: text[offset] === '}' ? offset + 1 : offset };
  }
  return atOffset ? { from: offset, to: offset } : null;
}

function isExplicit(
  monaco: Monaco,
  context: import('monaco-editor').languages.CompletionContext
): boolean {
  return context.triggerKind === monaco.languages.CompletionTriggerKind.Invoke;
}

function toCompletionList(
  monaco: Monaco,
  model: import('monaco-editor').editor.ITextModel,
  result: ReferenceCompletions | null
): import('monaco-editor').languages.CompletionList {
  if (!result) {
    return { suggestions: [] };
  }
  const start = model.getPositionAt(result.from);
  const end = model.getPositionAt(result.to);
  const range = {
    startLineNumber: start.lineNumber,
    startColumn: start.column,
    endLineNumber: end.lineNumber,
    endColumn: end.column,
  };
  const shellNote = translateStatic(
    editorLocale(),
    '${{name}} is shell syntax in run. Use ${env.{name}} for Dagu environment references.',
    { name: 'NAME' }
  );
  return {
    suggestions: result.items.map((item, index) => ({
      label: item.token,
      detail: item.detail ? `${item.label} - ${item.detail}` : item.label,
      kind: monaco.languages.CompletionItemKind.Variable,
      insertText: item.token,
      filterText: item.token,
      sortText: String(index).padStart(5, '0'),
      range,
      ...(result.inRunField && item.group === 'env'
        ? { documentation: shellNote }
        : {}),
    })),
  };
}

/**
 * Register reference completion for the script of one editor, where the text
 * is a `run` value and not YAML. A provider is global to its language, so this
 * one answers only for the model of `editor`. Dispose the result to remove it.
 */
export function registerFieldReferenceCompletion(
  monaco: Monaco,
  editor: { getModel(): unknown },
  language: string,
  getCandidates: () => ReferenceCandidate[]
): { dispose(): void } {
  return monaco.languages.registerCompletionItemProvider(language, {
    triggerCharacters: TRIGGER_CHARACTERS,
    provideCompletionItems(model, position, context) {
      if (model !== editor.getModel()) {
        return { suggestions: [] };
      }
      const range = replacedRange(
        model.getValue(),
        model.getOffsetAt(position),
        isExplicit(monaco, context)
      );
      return toCompletionList(
        monaco,
        model,
        range && { items: getCandidates(), ...range, inRunField: true }
      );
    },
  });
}

/**
 * Register the reference completion and hover providers for YAML models.
 * Dispose the result to remove both.
 */
export function registerReferenceCompletion(
  monaco: Monaco,
  getState: () => ReferenceEditorState
): { dispose(): void } {
  const completion = monaco.languages.registerCompletionItemProvider('yaml', {
    triggerCharacters: TRIGGER_CHARACTERS,
    provideCompletionItems(model, position, context) {
      const result = computeReferenceCompletions(
        model.getValue(),
        model.getOffsetAt(position),
        getState(),
        { explicit: isExplicit(monaco, context) }
      );
      return toCompletionList(monaco, model, result);
    },
  });
  const hover = registerReferenceHover(monaco, getState);
  return {
    dispose() {
      completion.dispose();
      hover.dispose();
    },
  };
}
