// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, isScalar, isSeq, parseDocument } from 'yaml';
import { getYamlPathAtOffset } from '@/hooks/useYamlCursorPath';
import { translateStatic } from '@/i18n/translateStatic';
import type { ReferenceCandidate } from '../contracts';
import { envEntryCandidates, withoutBuildOutputs } from './candidates';
import {
  candidatesAt,
  editorLocale,
  isCommandField,
  isRunFieldPath,
  pathBelowStep,
  type ReferenceEditorState,
} from './editorState';
import { registerReferenceHover } from './monacoHover';
import { findOpenReference } from './tokens';

type Monaco = typeof import('monaco-editor');

// A lone "$" is shell text as often as the start of a reference: "${" opens.
const TRIGGER_CHARACTERS = ['{', '.'];

export interface ReferenceCompletions {
  items: ReferenceCandidate[];
  /** Start of the text that a chosen item replaces. */
  from: number;
  /** End of the text that a chosen item replaces. */
  to: number;
  inRunField: boolean;
}

// Fields that spec 003 keeps as literal text.
const LITERAL_ROOT_FIELDS = new Set([
  'name',
  'description',
  'group',
  'tags',
  'type',
  'schedule',
  'retry_policy',
  'timeout_sec',
  'delay_sec',
  'max_active_steps',
  'max_active_runs',
  'max_clean_up_time_sec',
  'max_output_size',
  'secrets',
]);
const LITERAL_STEP_FIELDS = new Set([
  'id',
  'name',
  'description',
  'depends',
  'action',
  'type',
  'timeout_sec',
  'continue_on',
  'mail_on_error',
  'signal_on_stop',
]);
const LITERAL_FOREACH_FIELDS = new Set(['as', 'max_concurrent']);
const LITERAL_OUTPUT_FIELDS = new Set(['name', 'type']);

const STEP_ROOTS = new Set(['steps', 'handler_on']);

// The path below the innermost step around a field: the step entry, then each
// foreach body. Null for a path outside any step.
function pathBelowInnermostStep(path: string[]): string[] | null {
  if (!STEP_ROOTS.has(path[0] ?? '')) return null;
  let below = pathBelowStep(path);
  while (below[0] === 'foreach' && below[1] === 'steps') {
    below = below.slice(3);
  }
  return below;
}

function isLiteralField(path: string[]): boolean {
  const below = pathBelowInnermostStep(path);
  if (!below) return LITERAL_ROOT_FIELDS.has(path[0] ?? '');
  const [field = '', second = '', third = ''] = below;
  if (field === 'foreach') return LITERAL_FOREACH_FIELDS.has(second);
  if (field === 'outputs') return LITERAL_OUTPUT_FIELDS.has(third);
  return LITERAL_STEP_FIELDS.has(field);
}

// The names that a list item declares: `- KEY: value`, or `- KEY=value`.
function entryNames(entry: unknown): string[] {
  if (isMap(entry)) {
    return entry.items.flatMap(({ key }) =>
      isScalar(key) ? [String(key.value)] : []
    );
  }
  return isScalar(entry) ? [String(entry.value).split('=')[0] ?? ''] : [];
}

// Names of the entries of root `consts` before the entry at `index`.
function constNamesBefore(text: string, index: number): Set<string> {
  const consts = parseDocument(text).get('consts');
  return new Set(
    isSeq(consts) ? consts.items.slice(0, index).flatMap(entryNames) : []
  );
}

// Names of the step env entries before `entry`, the list index or map key that
// the field has below the env at `envPath`.
function envNamesBefore(
  text: string,
  envPath: string[],
  entry: string
): Set<string> {
  const env = parseDocument(text).getIn(envPath);
  if (isSeq(env)) {
    return new Set(env.items.slice(0, Number(entry)).flatMap(entryNames));
  }
  if (!isMap(env)) return new Set();
  const names = env.items.flatMap(({ key }) =>
    isScalar(key) ? [String(key.value)] : []
  );
  return new Set(names.slice(0, names.indexOf(entry)));
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
  if (isLiteralField(path) || (root === 'params' && key !== 'eval')) {
    return null;
  }
  const items = candidatesAt(text, offset, state);
  if (root === 'consts') {
    const earlier = constNamesBefore(text, Number(entry));
    return items.filter(
      (item) => item.group === 'consts' && earlier.has(item.label)
    );
  }
  const below = pathBelowInnermostStep(path);
  if (below?.[0] === 'env') {
    const envPath = path.slice(0, path.length - below.length + 1);
    return envEntryCandidates(
      items,
      envNamesBefore(text, envPath, below[1] ?? '')
    );
  }
  return isCommandField(below?.[0]) ? items : withoutBuildOutputs(items);
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
// an open "${", or, when `atOffset` allows it, a lone "$" or the empty text at
// the offset.
function replacedRange(
  text: string,
  offset: number,
  atOffset: boolean
): { from: number; to: number } | null {
  const open = findOpenReference(text, offset);
  if (open && (atOffset || text[open.from + 1] === '{')) {
    // The editor closes a brace that the user types: replace it too.
    return { from: open.from, to: text[offset] === '}' ? offset + 1 : offset };
  }
  return atOffset ? { from: offset, to: offset } : null;
}

// Monaco also reports a list that opens while a word is typed as Invoke. Only a
// request with no word before the caret comes from the author, and offering in
// a word would let Enter replace what they typed.
function isExplicit(
  monaco: Monaco,
  model: import('monaco-editor').editor.ITextModel,
  position: import('monaco-editor').Position,
  context: import('monaco-editor').languages.CompletionContext
): boolean {
  return (
    context.triggerKind === monaco.languages.CompletionTriggerKind.Invoke &&
    model.getWordUntilPosition(position).word === ''
  );
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
        isExplicit(monaco, model, position, context)
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
        { explicit: isExplicit(monaco, model, position, context) }
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
