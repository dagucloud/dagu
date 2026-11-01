// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { getYamlPathAtOffset } from '@/hooks/useYamlCursorPath';
import { translateStatic } from '@/i18n/staticMessages';
import type { ReferenceCandidate } from '../contracts';
import { buildReferenceCandidates } from './candidates';
import {
  editorLocale,
  findOwningStep,
  isRunFieldPath,
  type ReferenceEditorState,
} from './editorState';
import { registerReferenceHover } from './monacoHover';
import { findOpenReference } from './tokens';

type Monaco = typeof import('monaco-editor');

export interface ReferenceCompletions {
  items: ReferenceCandidate[];
  /** Start of the text that a chosen item replaces. */
  from: number;
  /** End of the text that a chosen item replaces. */
  to: number;
  inRunField: boolean;
}

/**
 * Candidates for the offset, or null when no completion applies.
 * An open "${" always applies. A cursor in a YAML value applies only for an explicit request.
 */
export function computeReferenceCompletions(
  text: string,
  offset: number,
  state: ReferenceEditorState,
  opts: { explicit: boolean }
): ReferenceCompletions | null {
  const open = findOpenReference(text, offset);
  const cursor = getYamlPathAtOffset(text, offset);
  let from: number;
  let to: number;
  if (open) {
    from = open.from;
    // The editor closes a brace that the user types: replace it too.
    to = text[offset] === '}' ? offset + 1 : offset;
  } else if (opts.explicit && cursor.isInValue) {
    from = offset;
    to = offset;
  } else {
    return null;
  }
  const step = findOwningStep(text, offset, state);
  return {
    items: buildReferenceCandidates(state.scope, step?.name ?? null),
    from,
    to,
    inRunField: isRunFieldPath(cursor.path),
  };
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
    triggerCharacters: ['$', '{', '.'],
    provideCompletionItems(model, position, context) {
      const text = model.getValue();
      const offset = model.getOffsetAt(position);
      const explicit =
        context.triggerKind === monaco.languages.CompletionTriggerKind.Invoke;
      const result = computeReferenceCompletions(text, offset, getState(), {
        explicit,
      });
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
      const locale = editorLocale();
      const shellNote = translateStatic(
        locale,
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
