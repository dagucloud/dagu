// Copyright (C) 2026 Yota Hamada
// SPDX-License-Identifier: GPL-3.0-or-later

import type { editor, IDisposable, languages } from 'monaco-editor';
import type {
  ApiDAGDetails,
  DagDocModel,
  StepNameMap,
} from '@/features/visual-editor/contracts';
import {
  parseDagDocLenient,
  resolveStepNames,
} from '@/features/visual-editor/doc/parse';

type Monaco = typeof import('monaco-editor');

/** A step of the text: the line of its first row and its server name. */
export interface StepLensPoint {
  line: number;
  name: string;
}

/** What the lenses of a whole-file editor need of their host. */
export interface StepLensSource {
  /**
   * The steps of `text`, in order. The lenses ask for the steps of the text of
   * the model at the time of the query: Monaco can query before the host has
   * read an edit, and it drops the query that follows when the editor loses
   * the focus. A list that the host kept from an older text would put the
   * lenses on the wrong rows.
   */
  steps(text: string): readonly StepLensPoint[];
  /** The text of the lens. */
  label: string;
  /** Opens the step in the full-screen step view. */
  open(name: string): void;
}

/**
 * Reads the steps of a text for `StepLensSource.steps`. `checked` is the
 * model of the text that the host parsed last, and `names` its step names:
 * that text costs no parse. Another text is parsed, and its steps are named
 * after `details`. The reader keeps the steps of the last text it read.
 */
export function stepLensReader(
  checked: DagDocModel,
  names: StepNameMap,
  details?: ApiDAGDetails
): (text: string) => readonly StepLensPoint[] {
  let last: { text: string; steps: StepLensPoint[] } | null = null;
  return (text) => {
    if (last?.text === text) return last.steps;
    const model = text === checked.text ? checked : parseDagDocLenient(text);
    const byKey =
      model === checked ? names.byKey : resolveStepNames(model, details).byKey;
    const steps = model.steps.flatMap((step) => {
      // A step with no name runs under its id.
      const name = byKey[step.key] ?? step.explicitName ?? step.id;
      return name ? [{ line: step.range.startLine, name }] : [];
    });
    last = { text, steps };
    return steps;
  };
}

/** The registration of the lenses of one editor. */
export interface StepLensRegistration extends IDisposable {
  /** Draws the lenses again: the names or the label changed. */
  refresh(): void;
}

/**
 * Puts one lens, "Open step", above the first row of each step of the model
 * of `codeEditor`. The lens opens the step in the full-screen step view.
 * Dispose the result when the editor unmounts.
 */
export function registerStepLenses(
  monaco: Monaco,
  codeEditor: editor.IStandaloneCodeEditor,
  read: () => StepLensSource | null
): StepLensRegistration {
  const changed = new monaco.Emitter<languages.CodeLensProvider>();
  // The handler of the command receives the name from the lens.
  const commandId = codeEditor.addCommand(0, (_accessor, name: unknown) => {
    if (typeof name === 'string') read()?.open(name);
  });
  const provider = monaco.languages.registerCodeLensProvider('yaml', {
    onDidChange: changed.event,
    provideCodeLenses(model) {
      const source = read();
      if (!source || !commandId || model !== codeEditor.getModel()) {
        return { lenses: [], dispose() {} };
      }
      const lines = model.getLineCount();
      return {
        lenses: source
          .steps(model.getValue())
          .filter((step) => step.line >= 1 && step.line <= lines)
          .map((step) => ({
            range: {
              startLineNumber: step.line,
              startColumn: 1,
              endLineNumber: step.line,
              endColumn: 1,
            },
            command: {
              id: commandId,
              title: source.label,
              arguments: [step.name],
            },
          })),
        dispose() {},
      };
    },
  });
  return {
    refresh: () => changed.fire({} as languages.CodeLensProvider),
    dispose() {
      provider.dispose();
      changed.dispose();
    },
  };
}
