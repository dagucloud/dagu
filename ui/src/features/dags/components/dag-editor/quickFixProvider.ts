// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor, languages, IDisposable, Uri } from 'monaco-editor';
import type {
  DagDocModel,
  StepNameMap,
  TextEdit,
  ValidationIssue,
} from '@/features/visual-editor/contracts';
import {
  mergeQuickFixes,
  planQuickFixes,
  quickFixesForMarker,
  type QuickFix,
} from '@/features/visual-editor/doc/quickFixes';
import { editorLocale } from '@/features/visual-editor/references/editorState';
import { translateStatic } from '@/i18n/staticMessages';
import { ISSUE_MARKER_OWNER } from './issueMarkers';

type Monaco = typeof import('monaco-editor');

/**
 * What an editor tells the quick fix provider: the model and issues that
 * drew its markers, and a callback after an action changed the text.
 */
export interface QuickFixSource {
  /** The issues of the text of the editor, or null when it has none now. */
  getPlan(): {
    model: DagDocModel;
    issues: readonly ValidationIssue[];
    names?: StepNameMap;
  } | null;
  /** Called with the new text after a code action edited the model. */
  onApplied?(text: string): void;
}

const APPLIED_COMMAND = 'dagu.quickFixApplied';
const FIX_ALL_TITLE = 'Fix all quick fixes in the file';

// One provider serves every editor, by the URI of its model. A model without
// a source (the run definition, the schema docs) gets no action.
const sources = new Map<string, QuickFixSource>();
let registered: IDisposable[] = [];

function toRange(model: editor.ITextModel, from: number, to: number) {
  const start = model.getPositionAt(from);
  const end = model.getPositionAt(to);
  return {
    startLineNumber: start.lineNumber,
    startColumn: start.column,
    endLineNumber: end.lineNumber,
    endColumn: end.column,
  };
}

function editOf(
  model: editor.ITextModel,
  edits: readonly TextEdit[]
): languages.WorkspaceEdit {
  const versionId = model.getVersionId();
  return {
    edits: edits.map((edit) => ({
      resource: model.uri,
      versionId,
      textEdit: {
        range: toRange(model, edit.from, edit.to),
        text: edit.insert,
      },
    })),
  };
}

/**
 * The code actions for the markers of a range: one quick fix for each marker
 * that has a fix, and one action for every fix of the file when there are
 * several. Returns nothing when the model is not the text of the plan.
 */
export function buildQuickFixActions(
  model: editor.ITextModel,
  source: QuickFixSource,
  markers: readonly editor.IMarkerData[]
): languages.CodeAction[] {
  const plan = source.getPlan();
  if (!plan || plan.model.text !== model.getValue()) return [];
  const locale = editorLocale();
  const command = {
    id: APPLIED_COMMAND,
    title: '',
    arguments: [model.uri.toString()],
  };

  const byFix = new Map<QuickFix, editor.IMarkerData[]>();
  for (const marker of markers) {
    const fix = quickFixesForMarker(
      plan.model,
      plan.issues,
      marker,
      plan.names
    );
    if (fix) byFix.set(fix, [...(byFix.get(fix) ?? []), marker]);
  }
  const actions: languages.CodeAction[] = [];
  for (const [fix, own] of byFix) {
    actions.push({
      title: translateStatic(locale, fix.message, fix.params),
      kind: 'quickfix',
      diagnostics: own,
      isPreferred: true,
      edit: editOf(model, fix.edits),
      command,
    });
  }
  if (actions.length === 0) return actions;

  const all = [...new Set(planQuickFixes(plan.model, plan.issues).values())];
  if (all.length > 1) {
    actions.push({
      title: translateStatic(locale, FIX_ALL_TITLE),
      kind: 'quickfix',
      diagnostics: [...byFix.values()].flat(),
      edit: editOf(model, mergeQuickFixes(all)),
      command,
    });
  }
  return actions;
}

function ensureProvider(monaco: Monaco): void {
  if (registered.length > 0) return;
  registered = [
    monaco.languages.registerCodeActionProvider('yaml', {
      provideCodeActions(model, _range, context) {
        const source = sources.get(model.uri.toString());
        if (!source) return null;
        const own = context.markers.filter((marker) => {
          const owner = (marker as { owner?: string }).owner;
          return owner === undefined || owner === ISSUE_MARKER_OWNER;
        });
        const actions = buildQuickFixActions(model, source, own);
        return actions.length === 0
          ? null
          : { actions, dispose: () => undefined };
      },
    }),
    monaco.editor.registerCommand(APPLIED_COMMAND, (_accessor, uri: string) => {
      const model = monaco.editor
        .getModels()
        .find((candidate) => candidate.uri.toString() === uri);
      if (model) sources.get(uri)?.onApplied?.(model.getValue());
    }),
  ];
}

/**
 * Offers the quick fixes of `source` as code actions of the models at
 * `modelUri`. One provider serves all the sources, and it goes away with the
 * last one. Dispose the result when the editor unmounts.
 */
export function registerQuickFixSource(
  monaco: Monaco,
  modelUri: string | Uri,
  source: QuickFixSource
): IDisposable {
  const key = (
    typeof modelUri === 'string' ? monaco.Uri.parse(modelUri) : modelUri
  ).toString();
  sources.set(key, source);
  ensureProvider(monaco);
  return {
    dispose() {
      if (sources.get(key) !== source) return;
      sources.delete(key);
      if (sources.size === 0) {
        for (const registration of registered) registration.dispose();
        registered = [];
      }
    },
  };
}
