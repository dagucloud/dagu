// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import { translateStatic } from '@/i18n/staticMessages';
import { buildReferenceCandidates, REFERENCE_GROUP_LABELS } from './candidates';
import {
  editorLocale,
  findOwningStep,
  isInRunField,
  type ReferenceEditorState,
} from './editorState';
import { referenceTokenAt } from './tokens';

type Monaco = typeof import('monaco-editor');

export interface ReferenceHover {
  /** Range of the token under the offset. */
  from: number;
  to: number;
  /** Markdown lines. */
  lines: string[];
}

/** Hover details for the reference token under the offset, or null. */
export function computeReferenceHover(
  text: string,
  offset: number,
  state: ReferenceEditorState
): ReferenceHover | null {
  const token = referenceTokenAt(text, offset);
  if (!token) {
    return null;
  }
  const locale = editorLocale();
  const t = (source: string, values?: Record<string, string>) =>
    translateStatic(locale, source, values);
  const result = (lines: string[]): ReferenceHover => ({
    from: token.from,
    to: token.to,
    lines,
  });

  if (token.kind === 'legacy') {
    return result([t('Legacy reference form')]);
  }
  if (token.kind === 'variable') {
    if (!isInRunField(text, offset)) {
      return null;
    }
    return result([
      t(
        '${{name}} is shell syntax here. Use ${env.{name}} for a Dagu reference.',
        {
          name: token.path,
        }
      ),
    ]);
  }
  if (token.kind !== 'canonical') {
    return null;
  }
  const step = findOwningStep(text, offset, state);
  const candidates = buildReferenceCandidates(state.scope, step?.name ?? null);
  const candidate = candidates.find((c) => c.token === token.text);
  if (candidate) {
    const lines = [
      candidate.detail
        ? `${candidate.label} - ${candidate.detail}`
        : candidate.label,
      t(REFERENCE_GROUP_LABELS[candidate.group]),
    ];
    if (candidate.known === 'runtime') {
      lines.push(t('Known after run'));
    }
    return result(lines);
  }
  if (token.namespace === 'steps') {
    return result([t('Not available in this step')]);
  }
  return null;
}

/** Register the reference hover provider for YAML models. Dispose the result to remove it. */
export function registerReferenceHover(
  monaco: Monaco,
  getState: () => ReferenceEditorState
): { dispose(): void } {
  const hover = monaco.languages.registerHoverProvider('yaml', {
    provideHover(model, position) {
      const result = computeReferenceHover(
        model.getValue(),
        model.getOffsetAt(position),
        getState()
      );
      if (!result) {
        return null;
      }
      const start = model.getPositionAt(result.from);
      const end = model.getPositionAt(result.to);
      return {
        range: {
          startLineNumber: start.lineNumber,
          startColumn: start.column,
          endLineNumber: end.lineNumber,
          endColumn: end.column,
        },
        contents: result.lines.map((value) => ({ value })),
      };
    },
  });
  return { dispose: () => hover.dispose() };
}
