// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { isMap, isScalar, isSeq, parseDocument } from 'yaml';
import { getYamlPathAtOffset } from '@/hooks/useYamlCursorPath';
import { translateStatic } from '@/i18n/translateStatic';
import type { ReferenceCandidate } from '../contracts';
import {
  envEntryCandidates,
  forShellField,
  SHELL_RISK_LABEL,
  SHELL_RISK_NOTE,
  withoutBuildOutputs,
} from './candidates';
import { publishEdit } from './publishOutput';
import {
  candidatesAt,
  editorLocale,
  isCommandField,
  isRunField,
  isRunFieldPath,
  pathBelowBodies,
  pathBelowStep,
  type ReferenceEditorState,
} from './editorState';
import { registerReferenceHover } from './monacoHover';
import { findOpenReference } from './tokens';

type Monaco = typeof import('monaco-editor');

// A lone "$" is shell text as often as the start of a reference: "${" opens.
const TRIGGER_CHARACTERS = ['{', '.'];

const PUBLISH_NOTE = 'Published when chosen';

let fieldProviders = 0;

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

function isLiteralStepField(below: string[]): boolean {
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

// Names that the step env at `envPath` declares from `entry` on: `entry` is the
// list index or map key of the field below that env.
function envNamesFrom(
  text: string,
  envPath: string[],
  entry: string
): Set<string> {
  const env = parseDocument(text).getIn(envPath);
  if (isSeq(env)) {
    return new Set(env.items.slice(Number(entry)).flatMap(entryNames));
  }
  if (!isMap(env)) return new Set();
  const names = env.items.flatMap(({ key }) =>
    isScalar(key) ? [String(key.value)] : []
  );
  return new Set(names.slice(Math.max(0, names.indexOf(entry))));
}

// The candidates that resolve in a step field. `path` is its path in `text`,
// and `below` the part of it below the innermost step.
function narrowToStepField(
  text: string,
  path: string[],
  below: string[],
  items: ReferenceCandidate[]
): ReferenceCandidate[] {
  if (below[0] === 'env') {
    const envPath = path.slice(0, path.length - below.length + 1);
    return envEntryCandidates(
      items,
      envNamesFrom(text, envPath, below[1] ?? '')
    );
  }
  return isCommandField(below[0]) ? items : withoutBuildOutputs(items);
}

// Candidates that resolve in the field at `path`, by the field evaluation
// matrix of spec 003. Null for a field that keeps references as literal text.
function fieldCandidates(
  text: string,
  offset: number,
  path: string[],
  state: ReferenceEditorState
): ReferenceCandidate[] | null {
  const [root = '', entry, key] = path;
  const below = STEP_ROOTS.has(root)
    ? pathBelowBodies(pathBelowStep(path))
    : null;
  const literal = below
    ? isLiteralStepField(below)
    : LITERAL_ROOT_FIELDS.has(root) || (root === 'params' && key !== 'eval');
  if (literal) {
    return null;
  }
  const items = candidatesAt(text, offset, state);
  if (root === 'consts') {
    const earlier = constNamesBefore(text, Number(entry));
    return items.filter(
      (item) => item.group === 'consts' && earlier.has(item.label)
    );
  }
  return below ? narrowToStepField(text, path, below, items) : items;
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
  const inRunField = isRunFieldPath(cursor.path);
  return {
    items: inRunField ? forShellField(items) : items,
    ...range,
    inRunField,
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

type TextModel = import('monaco-editor').editor.ITextModel;

function rangeOf(model: TextModel, from: number, to: number) {
  const start = model.getPositionAt(from);
  const end = model.getPositionAt(to);
  return {
    startLineNumber: start.lineNumber,
    startColumn: start.column,
    endLineNumber: end.lineNumber,
    endColumn: end.column,
  };
}

// The edit that a pick of `item` also makes in the document of the provider, as
// a part of the same undo step.
function publishTextEdits(
  model: TextModel,
  item: ReferenceCandidate
): import('monaco-editor').editor.ISingleEditOperation[] | undefined {
  if (!item.publish) return undefined;
  const result = publishEdit(model.getValue(), item.publish);
  if (!result.ok) return undefined;
  return result.edits.map((edit) => ({
    range: rangeOf(model, edit.from, edit.to),
    text: edit.insert,
  }));
}

function toCompletionList(
  monaco: Monaco,
  model: TextModel,
  result: ReferenceCompletions | null,
  extras: (item: ReferenceCandidate) => {
    command?: import('monaco-editor').languages.Command;
  } = () => ({})
): import('monaco-editor').languages.CompletionList {
  if (!result) {
    return { suggestions: [] };
  }
  const locale = editorLocale();
  const range = rangeOf(model, result.from, result.to);
  const shellNote = translateStatic(
    locale,
    '${{name}} is shell syntax in run. Use ${env.{name}} for Dagu environment references.',
    { name: 'NAME' }
  );
  return {
    suggestions: result.items.map((item, index) => ({
      // A description shows on every row, and a detail on the focused one, so
      // neither waits for the documentation to open.
      label: item.shellRisk
        ? {
            label: item.token,
            description: translateStatic(locale, SHELL_RISK_LABEL),
          }
        : item.token,
      detail: item.shellRisk
        ? translateStatic(locale, SHELL_RISK_NOTE)
        : item.publish
          ? `${item.label} - ${translateStatic(locale, PUBLISH_NOTE)}`
          : item.detail
            ? `${item.label} - ${item.detail}`
            : item.label,
      kind: monaco.languages.CompletionItemKind.Variable,
      insertText: item.token,
      filterText: item.token,
      sortText: String(index).padStart(5, '0'),
      range,
      ...(result.inRunField &&
      (item.group === 'env' || item.group === 'stepEnv')
        ? { documentation: shellNote }
        : {}),
      ...extras(item),
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
  // A completion item can only run code through a command.
  const commandId = `dagu.reference.picked.${++fieldProviders}`;
  const command = monaco.editor.registerCommand(
    commandId,
    (_accessor, token: string) =>
      getCandidates()
        .find((candidate) => candidate.token === token)
        ?.onPicked?.()
  );
  const completion = monaco.languages.registerCompletionItemProvider(language, {
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
        range && { items: getCandidates(), ...range, inRunField: true },
        (item) =>
          item.onPicked
            ? {
                command: {
                  id: commandId,
                  title: '',
                  arguments: [item.token],
                },
              }
            : {}
      );
    },
  });
  return {
    dispose() {
      command.dispose();
      completion.dispose();
    },
  };
}

/**
 * Register reference completion for an editor that holds the YAML of one step,
 * whose top level is the step entry. `candidatesAtPath` takes the path below
 * that entry. Dispose the result to remove it.
 */
export function registerStepYamlCompletion(
  monaco: Monaco,
  editor: { getModel(): unknown },
  candidatesAtPath: (path: string[]) => ReferenceCandidate[]
): { dispose(): void } {
  return monaco.languages.registerCompletionItemProvider('yaml', {
    triggerCharacters: TRIGGER_CHARACTERS,
    provideCompletionItems(model, position, context) {
      if (model !== editor.getModel()) {
        return { suggestions: [] };
      }
      const text = model.getValue();
      const offset = model.getOffsetAt(position);
      const { path, isInValue } = getYamlPathAtOffset(text, offset);
      const range = replacedRange(
        text,
        offset,
        isExplicit(monaco, model, position, context) && isInValue
      );
      const below = pathBelowBodies(path);
      if (!range || isLiteralStepField(below)) {
        return { suggestions: [] };
      }
      const inRunField = isRunField(below[0]);
      const items = narrowToStepField(
        text,
        path,
        below,
        candidatesAtPath(path)
      );
      return toCompletionList(monaco, model, {
        items: inRunField ? forShellField(items) : items,
        ...range,
        inRunField,
      });
    },
  });
}

/**
 * Register the reference completion and hover providers for the YAML of one
 * document, the model at `modelUri`. A provider is global to its language, and
 * other YAML models have other owners. Dispose the result to remove both.
 */
export function registerReferenceCompletion(
  monaco: Monaco,
  getState: () => ReferenceEditorState,
  modelUri: string
): { dispose(): void } {
  const uri = monaco.Uri.parse(modelUri).toString();
  const unpublished = new WeakMap<
    import('monaco-editor').languages.CompletionItem,
    () => import('monaco-editor').editor.ISingleEditOperation[] | undefined
  >();
  const completion = monaco.languages.registerCompletionItemProvider('yaml', {
    triggerCharacters: TRIGGER_CHARACTERS,
    provideCompletionItems(model, position, context) {
      if (model.uri.toString() !== uri) {
        return { suggestions: [] };
      }
      const result = computeReferenceCompletions(
        model.getValue(),
        model.getOffsetAt(position),
        getState(),
        { explicit: isExplicit(monaco, model, position, context) }
      );
      const list = toCompletionList(monaco, model, result);
      result?.items.forEach((item, index) => {
        const suggestion = list.suggestions[index];
        if (item.publish && suggestion) {
          unpublished.set(suggestion, () => publishTextEdits(model, item));
        }
      });
      return list;
    },
    // Each edit parses the document, and a long chain of steps offers many, so
    // an item computes its edit only when the editor is about to need it.
    resolveCompletionItem(suggestion) {
      const edits = unpublished.get(suggestion)?.();
      return edits ? { ...suggestion, additionalTextEdits: edits } : suggestion;
    },
  });
  const hover = registerReferenceHover(monaco, getState, uri);
  return {
    dispose() {
      completion.dispose();
      hover.dispose();
    },
  };
}
